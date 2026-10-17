package contract_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kratos "github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/middleware"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/server"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/service"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// This test binary supplies external provider observations. Production main always
// assembles the production provider and owner-signature verifier.
type controlledProvider struct{}

func (controlledProvider) Inspect(_ context.Context, _ biz.Cluster, g biz.SupplyGroup) (biz.GroupInspection, error) {
	i := biz.GroupInspection{BaselineDigest: g.Baseline.BaselineDigest, QueueName: g.QueueName, ObservedAt: time.Now()}
	for _, n := range g.Nodes {
		i.Nodes = append(i.Nodes, biz.InspectedNode{Node: n, ModelKey: g.ModelKey, Factor: g.Baseline.MemoryBlockMiB, SplitCount: g.Baseline.SplitCount, ConfigDigest: g.Baseline.NodeConfigDigest, Labels: map[string]string{biz.GroupLabel: g.GroupID, biz.ModelLabel: g.ModelKey, biz.BaselineLabel: g.Baseline.BaselineID}, DeviceBudgetsMiB: []int64{24576}})
	}
	return i, nil
}
func (controlledProvider) Collect(_ context.Context, c biz.Cluster, _ []biz.SupplyGroup) (biz.ObservationBatch, error) {
	return biz.ObservationBatch{ClusterID: c.ClusterID}, nil
}

type interruptKey struct{}
type interruptRepository struct {
	biz.Repository
	marker string
}

func (r interruptRepository) Transact(ctx context.Context, fn func(biz.Transaction) error) error {
	return r.Repository.Transact(ctx, func(tx biz.Transaction) error {
		if err := fn(tx); err != nil {
			return err
		}
		if ctx.Value(interruptKey{}) == true {
			if err := os.WriteFile(r.marker, []byte("before"), 0600); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
}

func TestContractServerProcess(t *testing.T) {
	if os.Getenv("ACC_CONTRACT_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	b, err := os.ReadFile(os.Getenv("ACC_CONTRACT_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg conf.Bootstrap
	if err = protojson.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	db, err := data.OpenPostgres(context.Background(), cfg.Database.DsnFile)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := os.ReadFile(cfg.Security.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := biz.NewCursorCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	u, err := biz.NewUsecase(interruptRepository{db, os.Getenv("ACC_CONTRACT_MARKER")}, controlledProvider{}, cursor, []string{os.Getenv("ACC_CONTRACT_CONNECTION")}, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	fault := func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			q, ok := request.(*pb.SyncGpuUsageRequest)
			if ok && q.RequestId == "interrupt-before-commit" {
				ctx = context.WithValue(ctx, interruptKey{}, true)
			}
			result, err := next(ctx, request)
			if ok && err == nil && q.RequestId == "interrupt-after-commit" {
				testkit.Write(t, os.Getenv("ACC_CONTRACT_MARKER"), []byte("after"))
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return result, err
		}
	}
	srv, err := server.NewSecureGRPCServer(cfg.Server.Grpc, cfg.Security, fault)
	if err != nil {
		t.Fatal(err)
	}
	handler := service.NewAccelerator(u)
	pb.RegisterAcceleratorAdminServiceServer(srv, handler)
	pb.RegisterAcceleratorCatalogServiceServer(srv, handler)
	pb.RegisterAcceleratorUsageServiceServer(srv, handler)
	app := kratos.New(kratos.Name("acc-contract-test"), kratos.Logger(slog.New(slog.NewTextHandler(io.Discard, nil))), kratos.Server(srv), kratos.StopTimeout(2*time.Second))
	if err = app.Run(); err != nil {
		t.Fatal(err)
	}
}

type child struct {
	cmd     *exec.Cmd
	done    chan error
	stopped bool
	log     string
}

func startChild(t *testing.T, r *testkit.Runtime, connection, marker string) *child {
	t.Helper()
	path := r.WriteConfig(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(r.PKI.Root, "child-"+strings.ReplaceAll(r.Config.Server.Grpc.Addr, ":", "-")+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestContractServerProcess$", "-test.v")
	cmd.Env = append(os.Environ(), "ACC_CONTRACT_CHILD=1", "ACC_CONTRACT_CONFIG="+path, "ACC_CONTRACT_CONNECTION="+connection, "ACC_CONTRACT_MARKER="+marker)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, done: make(chan error, 1), log: logPath}
	go func() { c.done <- cmd.Wait(); logFile.Close() }()
	t.Cleanup(func() {
		if !c.stopped {
			c.kill(t)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", r.Config.Server.Grpc.Addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return c
		}
		select {
		case err := <-c.done:
			c.stopped = true
			b, _ := os.ReadFile(c.log)
			t.Fatalf("child exited %v: %s", err, b)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child did not listen")
	return nil
}
func (c *child) kill(t *testing.T) {
	t.Helper()
	if c.stopped {
		return
	}
	_ = c.cmd.Process.Kill()
	<-c.done
	c.stopped = true
}
func address(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	l.Close()
	return a
}
func conn(t *testing.T, r *testkit.Runtime, cert, key string) *grpc.ClientConn {
	t.Helper()
	c, err := grpc.NewClient(r.Config.Server.Grpc.Addr, grpc.WithTransportCredentials(credentials.NewTLS(testkit.ClientTLS(t, r.PKI.CAFile, cert, key))), grpc.WithDefaultCallOptions(grpc.WaitForReady(true)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func methodPath(method string) string {
	family := "AcceleratorUsageService"
	if server.AdminActions[method] {
		family = "AcceleratorAdminService"
	} else if method == "ListProfiles" || method == "GetProfile" || method == "ResolveGpuRequest" || method == "GetCapacity" || method == "CheckGpuFit" {
		family = "AcceleratorCatalogService"
	}
	return "/accelerator.v1." + family + "/" + method
}
func callContext(method, tenant string) context.Context {
	pairs := []string{"x-ani-action", method, "x-ani-actor-type", "user", "x-ani-actor-id", "contract-user"}
	if tenant != "" {
		pairs = append(pairs, "x-ani-tenant-id", tenant)
	}
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(pairs...))
}
func adminRead() *pb.AdminRead {
	return &pb.AdminRead{RequestId: "contract", Actor: &pb.Actor{Type: "user", Id: "contract-user"}}
}
func mutation() *pb.AdminMutation {
	return &pb.AdminMutation{Context: adminRead(), IdempotencyKey: biz.NewID()}
}
func tenantContext(tenant string) *pb.TenantContext {
	return &pb.TenantContext{RequestId: "contract", TenantId: tenant, Actor: adminRead().Actor}
}
func copyAs[T any](t *testing.T, v any) *T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func replaceDelegatedActor(message protoreflect.Message, actor string) {
	if message.Descriptor().FullName() == "accelerator.v1.Actor" {
		message.Set(message.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString(actor))
		return
	}
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Message() != nil && !field.IsList() && !field.IsMap() {
			replaceDelegatedActor(value.Message(), actor)
		}
		return true
	})
}

func TestAll22RPCsMTLSAndRecovery(t *testing.T) {
	r := testkit.NewRuntime(t, address(t), address(t))
	connection := "contract:" + biz.NewID()
	tenant := biz.NewID()
	otherTenant := biz.NewID()
	marker := filepath.Join(r.PKI.Root, "interrupt.marker")
	adminActions := []string{}
	for a := range server.AdminActions {
		adminActions = append(adminActions, a)
	}
	tenantActions := []string{}
	for a := range server.TenantActions {
		tenantActions = append(tenantActions, a)
	}
	r.Config.Security.Grants = []*conf.DelegationGrant{{ActorType: "user", ActorId: "contract-user", Actions: adminActions, Platform: true, ClusterIds: []string{"*"}}}
	process := startChild(t, r, connection, marker)
	gov := conn(t, r, r.GovCert, r.GovKey)
	owner := conn(t, r, r.OwnerCert, r.OwnerKey)
	covered := map[string]bool{}
	rpc := func(method, tenantID string, in, out proto.Message) {
		t.Helper()
		client, wrong := gov, owner
		if method == "ObserveRelease" {
			client, wrong = owner, gov
		}
		ctx, cancel := context.WithTimeout(callContext(method, tenantID), 5*time.Second)
		defer cancel()
		if err := client.Invoke(ctx, methodPath(method), in, out); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		badOut := proto.Clone(out)
		if err := wrong.Invoke(ctx, methodPath(method), in, badOut); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s wrong service accepted: %v", method, err)
		}
		if server.AdminActions[method] || server.TenantActions[method] {
			// The certificate remains the real authorized Gov URI. Both typed actor
			// and metadata change together, so the failure is missing delegation,
			// not a business-layer actor mismatch or wrong service identity.
			denied := proto.Clone(in)
			replaceDelegatedActor(denied.ProtoReflect(), "ungranted-actor")
			pairs := []string{"x-ani-action", method, "x-ani-actor-type", "user", "x-ani-actor-id", "ungranted-actor"}
			if tenantID != "" {
				pairs = append(pairs, "x-ani-tenant-id", tenantID)
			}
			deniedCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(pairs...))
			err := gov.Invoke(deniedCtx, methodPath(method), denied, proto.Clone(out))
			if status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != "DELEGATION_DENIED" {
				t.Fatalf("%s missing grant not rejected at delegation boundary: %v", method, err)
			}
			t.Logf("AUTH-03 %s: valid Gov mTLS URI, matching typed/metadata actor without grant rejected DELEGATION_DENIED", method)
		}
		covered[method] = true
	}
	cluster := new(pb.Cluster)
	register := &pb.RegisterClusterRequest{Mutation: mutation(), DisplayName: "contract", ConnectionRef: connection}
	rpc("RegisterCluster", "", register, cluster)
	if !biz.UUID(cluster.ClusterId) || cluster.ConnectionRef != connection {
		t.Fatal(cluster)
	}
	found := false
	page := &pb.Page{Size: 200}
	seenTokens := map[string]bool{}
	for !found {
		clusters := new(pb.ListClustersResponse)
		rpc("ListClusters", "", &pb.ListClustersRequest{Context: adminRead(), Page: page}, clusters)
		for _, c := range clusters.Items {
			if c.ClusterId == cluster.ClusterId {
				found = true
				break
			}
		}
		if found || clusters.NextToken == "" {
			break
		}
		if seenTokens[clusters.NextToken] {
			t.Fatal("ListClusters repeated a page token")
		}
		seenTokens[clusters.NextToken] = true
		page.Token = clusters.NextToken
	}
	if !found {
		t.Fatal("registered cluster absent")
	}
	pool := new(pb.Pool)
	rpc("CreatePool", "", &pb.CreatePoolRequest{Mutation: mutation(), ClusterId: cluster.ClusterId, DisplayName: "contract"}, pool)
	pools := new(pb.ListPoolsResponse)
	rpc("ListPools", "", &pb.ListPoolsRequest{Context: adminRead(), ClusterId: cluster.ClusterId}, pools)
	if len(pools.Items) != 1 || pools.Items[0].PoolId != pool.PoolId {
		t.Fatal(pools)
	}
	b := &biz.ProviderBaseline{BaselineID: biz.NewID(), AdapterID: "volcano-hami-mib-v1", VolcanoVersion: "v1.12.0", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "controlled-test", MemoryBlockMiB: 1, MemoryEncoding: "MIB", SplitCount: 4, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: strings.Repeat("c", 64), SchedulerConfigDigest: strings.Repeat("d", 64), RecipeVersion: "volcano-hami-mib-v1", RuntimeClassName: "nvidia"}
	b.BaselineDigest, _ = biz.BaselineDigest(b)
	group := new(pb.SupplyGroup)
	adopt := &pb.AdoptSupplyGroupRequest{Mutation: mutation(), PoolId: pool.PoolId, DisplayName: "contract", Mode: pb.SupplyMode_SHARED_FIXED, Nodes: []*pb.NodeRef{{Uid: "node-" + biz.NewID(), Name: "contract-node"}}, ModelKey: "nvidia-test", Baseline: copyAs[pb.ProviderBaseline](t, b), QueueName: "gpu"}
	rpc("AdoptSupplyGroup", "", adopt, group)
	if group.Admission != pb.AdmissionState_ADMISSION_CLOSED {
		t.Fatal(group)
	}
	groups := new(pb.ListSupplyGroupsResponse)
	rpc("ListSupplyGroups", "", &pb.ListSupplyGroupsRequest{Context: adminRead(), PoolId: pool.PoolId}, groups)
	if len(groups.Items) != 1 || groups.Items[0].GroupId != group.GroupId {
		t.Fatal(groups)
	}
	profile := new(pb.GpuProfile)
	publish := &pb.CreateProfileRequest{Mutation: mutation(), DisplayName: "6 GiB", Spec: &pb.ProfileSpec{GroupId: group.GroupId, Mode: pb.SupplyMode_SHARED_FIXED, ModelKey: group.ModelKey, MemoryMibPerDevice: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}, ExpectedGroupVersion: 1}
	rpc("CreateProfile", "", publish, profile)
	if profile.State != pb.ProfileState_PROFILE_ACTIVE || profile.Revision != 1 || profile.ProfileVersion != 1 {
		t.Fatal(profile)
	}
	profiles := new(pb.ListProfilesResponse)
	rpc("AdminListProfiles", "", &pb.AdminListProfilesRequest{Context: adminRead(), ClusterId: cluster.ClusterId}, profiles)
	if len(profiles.Items) != 1 || profiles.Items[0].ProfileId != profile.ProfileId {
		t.Fatal(profiles)
	}
	rpc("SetAdmission", "", &pb.SetAdmissionRequest{Mutation: mutation(), GroupId: group.GroupId, ExpectedVersion: 2, Desired: pb.AdmissionState_ADMISSION_OPEN, Reason: "isolated test"}, group)
	if group.Admission != pb.AdmissionState_ADMISSION_OPEN || group.Version != 3 {
		t.Fatal(group)
	}
	// Restart with exact known cluster tenant grants; no wildcard tenant scope.
	process.kill(t)
	r.Config.Security.Grants = append(r.Config.Security.Grants, &conf.DelegationGrant{ActorType: "user", ActorId: "contract-user", Actions: tenantActions, TenantIds: []string{tenant, otherTenant}, ClusterIds: []string{cluster.ClusterId}})
	process = startChild(t, r, connection, marker)
	tc := tenantContext(tenant)
	rpc("ListProfiles", tenant, &pb.ListProfilesRequest{Context: tc, ClusterId: cluster.ClusterId}, profiles)
	if len(profiles.Items) != 1 {
		t.Fatal(profiles)
	}
	gotProfile := new(pb.GpuProfile)
	rpc("GetProfile", tenant, &pb.GetProfileRequest{Context: tc, ProfileId: profile.ProfileId, ProfileVersion: 1}, gotProfile)
	if !proto.Equal(profile, gotProfile) {
		t.Fatal("profile changed across restart")
	}
	request := &pb.GpuRequest{ClusterId: cluster.ClusterId, PoolId: pool.PoolId, ProfileId: profile.ProfileId, ProfileVersion: 1, Replicas: 2, DevicesPerReplica: 1, ContainerName: "inference"}
	plan := new(pb.ResolvedGpuRequest)
	rpc("ResolveGpuRequest", tenant, &pb.ResolveGpuRequestRequest{Context: tc, Gpu: request}, plan)
	if plan.Execution.Resources["volcano.sh/vgpu-memory"] != "6144" || len(plan.QuotaItems) != 1 || plan.QuotaItems[0].Units != 12288 {
		t.Fatal(plan)
	}
	db, err := data.OpenPostgres(context.Background(), r.Config.Database.DsnFile)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ref := &biz.UsageRef{TenantID: tenant, OwnerService: "ani-inference", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	now := time.Now().UTC()
	device := biz.NewID()
	pod := biz.PodRef{ClusterID: cluster.ClusterId, Namespace: "contract", Name: "inference", UID: "pod-" + biz.NewID(), ContainerName: "inference"}
	batch := biz.ObservationBatch{ClusterID: cluster.ClusterId, Devices: []biz.DeviceRecord{{Device: biz.PhysicalDevice{DeviceID: device, ClusterID: cluster.ClusterId, GroupID: group.GroupId, Node: copyAs[biz.NodeRef](t, group.Nodes[0]), VendorDeviceID: "GPU-01234567-89ab-cdef-0123-456789abcdef", ModelKey: group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: b.BaselineDigest}}}
	for _, source := range []string{"scheduler", "allocation", "runtime"} {
		batch.Claims = append(batch.Claims, biz.Claim{Pod: pod, DeviceID: device, NodeUID: group.Nodes[0].Uid, Source: source, Cursor: "1", ObservedAt: now, ReceivedAt: now, State: biz.Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: b.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "controlled:" + source, OwnerRef: ref, ExecutionDigest: plan.Execution.ExecutionDigest})
	}
	for _, source := range []string{"kubernetes", "podresources", "cri"} {
		batch.Watermarks = append(batch.Watermarks, biz.SourceWatermark{ClusterID: cluster.ClusterId, NodeUID: group.Nodes[0].Uid, Source: source, ObservedAt: now, ReceivedAt: now, Complete: true})
	}
	store := func(b biz.ObservationBatch) {
		t.Helper()
		if err := db.Transact(context.Background(), func(tx biz.Transaction) error { return tx.SaveObservation(context.Background(), b) }); err != nil {
			t.Fatal(err)
		}
	}
	store(batch)
	devices := new(pb.ListDevicesResponse)
	rpc("ListDevices", "", &pb.ListDevicesRequest{Context: adminRead(), ClusterId: cluster.ClusterId}, devices)
	if len(devices.Items) != 1 || devices.Items[0].DeviceId != device {
		t.Fatal(devices)
	}
	bindings := new(pb.ListBindingsResponse)
	rpc("AdminListBindings", "", &pb.AdminListBindingsRequest{Context: adminRead(), ClusterId: cluster.ClusterId}, bindings)
	if len(bindings.Items) != 1 || bindings.Items[0].VerifiedUsageRef != nil || bindings.Items[0].PhysicalDeviceId != device {
		t.Fatal(bindings)
	}
	capacity := new(pb.CapacityView)
	rpc("AdminGetCapacity", "", &pb.AdminGetCapacityRequest{Context: adminRead(), ProfileId: profile.ProfileId, ProfileVersion: 1, IncludeDevices: true}, capacity)
	if len(capacity.Devices) != 1 || capacity.KnownFreeUnits != 3 || capacity.KnownCommittedUnits != 1 {
		t.Fatal(capacity)
	}
	rpc("GetCapacity", tenant, &pb.GetCapacityRequest{Context: tc, ProfileId: profile.ProfileId, ProfileVersion: 1}, capacity)
	if len(capacity.Devices) != 0 || capacity.KnownFreeUnits != 3 {
		t.Fatal(capacity)
	}
	fit := new(pb.GpuFit)
	rpc("CheckGpuFit", tenant, &pb.CheckGpuFitRequest{Context: tc, Gpu: request}, fit)
	if fit.Status != pb.FitStatus_FIT_ESTIMATE || !fit.EstimateKnown || fit.EstimatedReplicas != 3 {
		t.Fatal(fit)
	}
	release := &pb.ObserveReleaseRequest{RequestId: "contract", Ref: copyAs[pb.GpuUsageRef](t, ref), DeleteOperationId: biz.NewID(), Pods: []*pb.PodRef{copyAs[pb.PodRef](t, pod)}}
	observation := new(pb.ReleaseObservation)
	rpc("ObserveRelease", tenant, release, observation)
	if observation.Finding != pb.ReleaseFinding_ALLOCATION_STILL_PRESENT {
		t.Fatal(observation)
	}
	projection := &biz.UsageProjection{Ref: ref, Revision: 2, State: biz.Ended, ProfileID: profile.ProfileId, ProfileVersion: profile.ProfileVersion, GroupID: group.GroupId, BaselineID: b.BaselineID, ClusterID: cluster.ClusterId, ContainerName: "inference", ExecutionDigest: plan.Execution.ExecutionDigest, EndReason: biz.OwnerReleased, SourceFactRef: "quota-operation:" + ref.CreateOperationID + ":gpu-fully-released", SourceOperationCreatedAt: "2026-09-23T00:00:00Z"}
	projection.PayloadDigest, _ = biz.ProjectionDigest(projection)
	syncRequest := &pb.SyncGpuUsageRequest{RequestId: "contract", Projection: copyAs[pb.GpuUsageProjection](t, projection)}
	syncResult := new(pb.SyncGpuUsageResponse)
	rpc("SyncGpuUsage", "", syncRequest, syncResult)
	if syncResult.State != pb.UsageState_ENDED || syncResult.AppliedRevision != 2 {
		t.Fatal(syncResult)
	}
	usage := new(pb.GpuUsageProjection)
	get := &pb.GetGpuUsageRequest{Context: tc, Ref: release.Ref}
	rpc("GetGpuUsage", tenant, get, usage)
	if usage.State != pb.UsageState_ENDED || usage.SourceFactRef != "" {
		t.Fatal(usage)
	}
	usages := new(pb.ListGpuUsagesResponse)
	rpc("ListGpuUsages", tenant, &pb.ListGpuUsagesRequest{Context: tc}, usages)
	if len(usages.Items) != 1 || usages.Items[0].Ref.ResourceId != ref.ResourceID {
		t.Fatal(usages)
	}
	rpc("ListBindings", tenant, &pb.ListBindingsRequest{Context: tc, Ref: release.Ref}, bindings)
	if len(bindings.Items) != 1 || bindings.Items[0].PhysicalDeviceId != "" || bindings.Items[0].Pod.Uid != "" || bindings.Items[0].EvidenceRef != "" || bindings.Items[0].State != pb.AllocationState_ACTIVE {
		t.Fatal(bindings)
	}
	if len(covered) != 22 {
		t.Fatalf("only %d RPCs covered: %v", len(covered), covered)
	}
	// Cross-tenant forged payload, empty scope, untrusted identities and no cert.
	ctx, cancel := context.WithTimeout(callContext("GetGpuUsage", otherTenant), time.Second)
	defer cancel()
	if err = gov.Invoke(ctx, methodPath("GetGpuUsage"), get, new(pb.GpuUsageProjection)); status.Code(err) != codes.PermissionDenied {
		t.Fatal("forged tenant accepted", err)
	}
	for _, creds := range []struct{ cert, key string }{{"", ""}} {
		noCert := conn(t, r, creds.cert, creds.key)
		ctx, cancel := context.WithTimeout(callContext("ListClusters", ""), 500*time.Millisecond)
		err = noCert.Invoke(ctx, methodPath("ListClusters"), &pb.ListClustersRequest{Context: adminRead()}, new(pb.ListClustersResponse))
		cancel()
		if err == nil {
			t.Fatal("no certificate accepted")
		}
	}
	wrongCert, wrongKey := r.PKI.Issue(t, "unrelated", "spiffe://ani.internal/service/unrelated", false)
	wrong := conn(t, r, wrongCert, wrongKey)
	ctx, cancel = context.WithTimeout(callContext("ListClusters", ""), time.Second)
	err = wrong.Invoke(ctx, methodPath("ListClusters"), &pb.ListClustersRequest{Context: adminRead()}, new(pb.ListClustersResponse))
	cancel()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("same CA wrong service accepted", err)
	}
	foreign := testkit.NewPKI(t)
	foreignCert, foreignKey := foreign.Issue(t, "untrusted", "spiffe://ani.internal/service/ani-governance", false)
	untrusted := conn(t, r, foreignCert, foreignKey)
	ctx, cancel = context.WithTimeout(callContext("ListClusters", ""), 500*time.Millisecond)
	err = untrusted.Invoke(ctx, methodPath("ListClusters"), &pb.ListClustersRequest{Context: adminRead()}, new(pb.ListClustersResponse))
	cancel()
	if err == nil {
		t.Fatal("untrusted CA accepted")
	}
	emptyScope := proto.Clone(release).(*pb.ObserveReleaseRequest)
	emptyScope.Pods = nil
	ctx, cancel = context.WithTimeout(callContext("ObserveRelease", tenant), time.Second)
	err = owner.Invoke(ctx, methodPath("ObserveRelease"), emptyScope, new(pb.ReleaseObservation))
	cancel()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal("empty release scope accepted", err)
	}
	// Two real server processes race against the same PG receiver state.
	r2 := *r
	r2.Config = proto.Clone(r.Config).(*conf.Bootstrap)
	r2.Config.Server.Grpc.Addr = address(t)
	r2.Config.Server.Admin.Addr = address(t)
	second := startChild(t, &r2, connection, marker)
	gov2 := conn(t, &r2, r.GovCert, r.GovKey)
	var wg sync.WaitGroup
	errorsCh := make(chan error, 12)
	for i := range 12 {
		wg.Go(func() {
			client := gov
			if i%2 == 1 {
				client = gov2
			}
			ctx, cancel := context.WithTimeout(callContext("SyncGpuUsage", ""), 3*time.Second)
			defer cancel()
			res := new(pb.SyncGpuUsageResponse)
			e := client.Invoke(ctx, methodPath("SyncGpuUsage"), syncRequest, res)
			if e == nil && (res.State != pb.UsageState_ENDED || !res.Replayed) {
				e = io.ErrUnexpectedEOF
			}
			errorsCh <- e
		})
	}
	wg.Wait()
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal(e)
		}
	}
	second.kill(t)
	// Positive scoped release retires old evidence, even after ENDED.
	store(biz.ObservationBatch{ClusterID: cluster.ClusterId, Releases: []biz.ReleaseProof{{Pod: pod, DeviceID: device, NodeUID: group.Nodes[0].Uid, ObservedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), EvidenceRef: "controlled:released", RuntimeExited: true, AllocationAbsent: true, NodeOnline: true, OwnerRef: ref, ExecutionDigest: plan.Execution.ExecutionDigest}}})
	rpc("ObserveRelease", tenant, release, observation)
	if observation.Finding != pb.ReleaseFinding_ALLOCATION_RELEASED {
		t.Fatal(observation)
	}
	// Kill after ACK, then read/replay the same committed facts from a new process.
	process.kill(t)
	process = startChild(t, r, connection, marker)
	rpc("GetGpuUsage", tenant, get, usage)
	if usage.State != pb.UsageState_ENDED {
		t.Fatal("restart lost usage")
	}
	rpc("SyncGpuUsage", "", syncRequest, syncResult)
	if !syncResult.Replayed {
		t.Fatal("restart lost receiver dedupe")
	}
	for _, point := range []string{"before", "after"} {
		copyProjection := copyAs[biz.UsageProjection](t, projection)
		copyProjection.Ref.ResourceID = biz.NewID()
		copyProjection.Ref.CreateOperationID = biz.NewID()
		copyProjection.SourceFactRef = "quota-operation:" + copyProjection.Ref.CreateOperationID + ":gpu-fully-released"
		copyProjection.PayloadDigest, _ = biz.ProjectionDigest(copyProjection)
		request := &pb.SyncGpuUsageRequest{RequestId: "interrupt-" + point + "-commit", Projection: copyAs[pb.GpuUsageProjection](t, copyProjection)}
		_ = os.Remove(marker)
		lost := make(chan error, 1)
		ctx, cancel := context.WithTimeout(callContext("SyncGpuUsage", ""), 10*time.Second)
		go func() { lost <- gov.Invoke(ctx, methodPath("SyncGpuUsage"), request, new(pb.SyncGpuUsageResponse)) }()
		deadline := time.Now().Add(5 * time.Second)
		seen := false
		for time.Now().Before(deadline) {
			if b, e := os.ReadFile(marker); e == nil && string(b) == point {
				seen = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !seen {
			cancel()
			t.Fatal("commit interruption marker absent", point)
		}
		process.kill(t)
		cancel()
		if <-lost == nil {
			t.Fatal("interrupted call unexpectedly ACKed")
		}
		err = db.View(context.Background(), func(reader biz.Reader) error {
			p, e := reader.Usage(context.Background(), *copyProjection.Ref)
			if point == "before" {
				if !biz.IsNotFound(e) {
					t.Fatal("uncommitted write survived process death", p, e)
				}
				return nil
			}
			if e != nil {
				return e
			}
			if p.State != biz.Ended {
				t.Fatal("committed terminal state lost")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		process = startChild(t, r, connection, marker)
		request.RequestId = "response-loss-replay"
		rpc("SyncGpuUsage", "", request, syncResult)
		if syncResult.Replayed != (point == "after") {
			t.Fatal("incorrect commit recovery replay", point, syncResult)
		}
		t.Log("RUN-03: killed real process at", point, "commit; recovery/replay matched durable PG state")
	}
	process.kill(t)
	t.Logf("RUN-01: all %d RPCs positive + wrong-service negative via real mTLS processes; PG multiprocess replay and kill/restart passed", len(covered))
}
