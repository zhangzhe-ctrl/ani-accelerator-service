package data

import (
	"context"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/service"
)

func TestPostgresProfileCatalogScopesMatchEachPage(t *testing.T) {
	f := pgSetup(t)
	secondPool, err := f.u.CreatePool(adminContext("CreatePool"), adminMutation(), f.cluster.ClusterID, "second pool")
	if err != nil {
		t.Fatal(err)
	}
	baseline := *f.group.Baseline
	baseline.BaselineID = biz.NewID()
	baseline.BaselineDigest, err = biz.BaselineDigest(&baseline)
	if err != nil {
		t.Fatal(err)
	}
	group, err := f.u.AdoptSupplyGroup(adminContext("AdoptSupplyGroup"), adminMutation(), biz.AdoptInput{PoolID: secondPool.PoolID, DisplayName: "second group", Mode: biz.Shared, Nodes: []biz.NodeRef{{UID: "node-" + biz.NewID(), Name: "second-node"}}, ModelKey: f.group.ModelKey, Baseline: &baseline, QueueName: f.group.QueueName})
	if err != nil {
		t.Fatal(err)
	}
	spec := *f.profile.Spec
	spec.GroupID = group.GroupID
	second, err := f.u.CreateProfile(adminContext("CreateProfile"), adminMutation(), biz.CreateProfileInput{DisplayName: "another profile", Spec: &spec, ExpectedGroupVersion: group.Version})
	if err != nil {
		t.Fatal(err)
	}
	actor := pb.Actor{Type: adminActor.Type, Id: adminActor.ID}
	ctx := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Actor: adminActor, TenantID: f.tenant, Action: "ListProfiles"})
	server := service.NewAccelerator(f.u)
	request := &pb.ListProfilesRequest{Context: &pb.TenantContext{RequestId: "catalog-scope", TenantId: f.tenant, Actor: &actor}, ClusterId: f.cluster.ClusterID, Page: &pb.Page{Size: 1}}
	expectedPools := map[string]string{f.profile.ProfileID: f.pool.PoolID, second.ProfileID: secondPool.PoolID}
	seen := make(map[string]bool)
	for pages := 0; ; pages++ {
		if pages > 2 {
			t.Fatal("catalog pagination did not terminate")
		}
		out, err := server.ListProfiles(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Items) != 1 || len(out.Scopes) != 1 {
			t.Fatalf("items and scopes must match the page: %+v", out)
		}
		profile, scope := out.Items[0], out.Scopes[0]
		if scope.ProfileId != profile.ProfileId || scope.ProfileVersion != profile.ProfileVersion || scope.SpecDigest != profile.SpecDigest || scope.ClusterId != f.cluster.ClusterID || scope.PoolId != expectedPools[profile.ProfileId] || seen[scope.ProfileId] {
			t.Fatalf("wrong or repeated scope: profile=%+v scope=%+v", profile, scope)
		}
		seen[scope.ProfileId] = true
		if out.NextToken == "" {
			break
		}
		request.Page.Token = out.NextToken
	}
	if !seen[f.profile.ProfileID] || !seen[second.ProfileID] || len(seen) != 2 {
		t.Fatalf("incomplete catalog: %+v", seen)
	}
	admin, err := server.AdminListProfiles(adminContext("AdminListProfiles"), &pb.AdminListProfilesRequest{Context: &pb.AdminRead{RequestId: "catalog-scope-admin", Actor: &actor}, ClusterId: f.cluster.ClusterID})
	if err != nil || len(admin.GetScopes()) != len(admin.GetItems()) || len(admin.GetItems()) != 2 {
		t.Fatalf("admin catalog lost matching scope: result=%+v error=%v", admin, err)
	}
	request.Page.Token = ""
	request.Mode = pb.SupplyMode_WHOLE_EXCLUSIVE
	empty, err := server.ListProfiles(ctx, request)
	if err != nil || len(empty.GetItems()) != 0 || len(empty.GetScopes()) != 0 || empty.GetNextToken() != "" {
		t.Fatalf("empty catalog scope: result=%+v error=%v", empty, err)
	}
	wrongTenant := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Actor: adminActor, TenantID: biz.NewID(), Action: "ListProfiles"})
	if _, err := server.ListProfiles(wrongTenant, request); err == nil {
		t.Fatal("cross-tenant catalog accepted")
	}
	unscoped := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Actor: adminActor, TenantID: f.tenant, Action: "ListProfiles"})
	if _, err := server.ListProfiles(unscoped, request); err != nil {
		t.Fatalf("trusted Gov tenant must use provider management scope without user grants: %v", err)
	}
}

func TestPostgresProviderConnectionsDefineManagementScope(t *testing.T) {
	f := pgSetup(t)
	other := pgSetup(t)
	page, err := f.u.ListClusters(adminContext("ListClusters"), &biz.AdminRead{RequestID: "scope", Actor: &adminActor}, nil)
	if err != nil || len(page.Items) != 1 || page.Items[0].ClusterID != f.cluster.ClusterID {
		t.Fatalf("managed connection filter failed: %+v %v", page, err)
	}
	if _, err := f.u.RegisterCluster(adminContext("RegisterCluster"), adminMutation(), "unknown", "unconfigured:"+biz.NewID()); err == nil {
		t.Fatal("unconfigured provider connection accepted")
	}
	if _, err := f.u.CreatePool(adminContext("CreatePool"), adminMutation(), other.cluster.ClusterID, "foreign"); !biz.IsNotFound(err) {
		t.Fatalf("unmanaged cluster accepted: %v", err)
	}
	if _, err := f.u.ListPools(adminContext("ListPools"), &biz.AdminRead{RequestID: "scope", Actor: &adminActor}, other.cluster.ClusterID, nil); !biz.IsNotFound(err) {
		t.Fatalf("unmanaged cluster listed: %v", err)
	}
	tenant := &biz.TenantContext{RequestID: "scope", TenantID: f.tenant, Actor: &adminActor}
	caller := biz.Caller{Service: "ani-governance", Action: "GetProfile", Actor: adminActor, TenantID: f.tenant}
	if _, err := f.u.GetProfile(biz.WithCaller(context.Background(), caller), tenant, other.profile.ProfileID, 1); !biz.IsNotFound(err) {
		t.Fatalf("unmanaged profile read accepted: %v", err)
	}
	caller.Action = "GetCapacity"
	if _, err := f.u.GetCapacity(biz.WithCaller(context.Background(), caller), tenant, other.profile.ProfileID, 1); !biz.IsNotFound(err) {
		t.Fatalf("unmanaged capacity accepted: %v", err)
	}
	caller.Action = "ResolveGpuRequest"
	if _, err := f.u.Resolve(biz.WithCaller(context.Background(), caller), tenant, other.request); !biz.IsNotFound(err) {
		t.Fatalf("unmanaged resolve accepted: %v", err)
	}
}
