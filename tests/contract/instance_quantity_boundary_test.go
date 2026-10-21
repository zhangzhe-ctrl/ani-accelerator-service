package contract_test

// This explicitly started test process substitutes only the external GPU
// catalog/Resolve and usage destination. It uses the production mTLS/context
// guard and real BuildResolution. It has no PostgreSQL connection and
// cannot write actual supply, real usage, binding or release facts.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/server"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/schedulerconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type instanceBoundaryConfig struct {
	Root              string `json:"root"`
	Address           string `json:"address"`
	CAFile            string `json:"ca_file"`
	CertFile          string `json:"cert_file"`
	KeyFile           string `json:"key_file"`
	GovernanceDNSName string `json:"governance_dns_name"`
	ClusterID         string `json:"cluster_id"`
	TenantID          string `json:"tenant_id"`
	ActorType         string `json:"actor_type"`
	ActorID           string `json:"actor_id"`
	QueueName         string `json:"queue_name"`
	PlatformActorType string `json:"platform_actor_type"`
	PlatformActorID   string `json:"platform_actor_id"`
}

const instanceBoundaryClusterID = "b90dc57f-81ad-44ed-8088-6d731b63adea"
const instanceBoundaryTenantID = "92b09b38-ce59-4a4f-9293-f2b33148d799"

type instanceQuantityBoundary struct {
	pb.UnimplementedAcceleratorAdminServiceServer
	pb.UnimplementedAcceleratorCatalogServiceServer
	pb.UnimplementedAcceleratorUsageServiceServer
	config   instanceBoundaryConfig
	pool     *biz.Pool
	profiles []*biz.Profile
	groups   map[string]*biz.SupplyGroup
	mu       sync.Mutex
	usage    map[biz.UsageRef]*biz.UsageProjection
}

func newInstanceQuantityBoundary(c instanceBoundaryConfig) (*instanceQuantityBoundary, error) {
	if c.ClusterID != instanceBoundaryClusterID || c.TenantID != instanceBoundaryTenantID || !biz.Name(c.QueueName) || !biz.Ref(c.ActorType) || !biz.Ref(c.ActorID) || !biz.Ref(c.PlatformActorType) || !biz.Ref(c.PlatformActorID) {
		return nil, fmt.Errorf("explicit task cluster/tenant/actors/queue required")
	}
	b := &instanceQuantityBoundary{config: c, pool: &biz.Pool{PoolID: biz.NewID(), ClusterID: c.ClusterID, DisplayName: "CF03 external candidate fixture"}, groups: map[string]*biz.SupplyGroup{}, usage: map[biz.UsageRef]*biz.UsageProjection{}}
	schedulerDigest, err := schedulerconfig.ConfigMapDigest("v1.15.2", map[string]string{schedulerconfig.DataKey: "actions: enqueue,allocate\ntiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.VGPUEnable: true\n"}, nil)
	if err != nil {
		return nil, err
	}
	for _, mode := range []biz.SupplyMode{biz.Whole, biz.Shared} {
		split, maximum, cores, memory, isolation := uint32(1), uint32(2), uint32(100), int64(24576), "WHOLE_DEVICE_EXCLUSIVE"
		if mode == biz.Shared {
			split, maximum, cores, memory, isolation = 4, 1, 25, 6144, "SOFTWARE_COOPERATIVE"
		}
		baseline := &biz.ProviderBaseline{BaselineID: biz.NewID(), AdapterID: "volcano-hami-mib-v1", VolcanoVersion: "v1.15.2", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "CF03-external-boundary", MemoryBlockMiB: 1, MemoryEncoding: "MIB", SplitCount: split, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: strings.Repeat("c", 64), SchedulerConfigDigest: schedulerDigest, RecipeVersion: "volcano-hami-mib-v1", RuntimeClassName: "nvidia"}
		baseline.BaselineDigest, err = biz.BaselineDigest(baseline)
		if err != nil {
			return nil, err
		}
		group := &biz.SupplyGroup{GroupID: biz.NewID(), PoolID: b.pool.PoolID, ClusterID: c.ClusterID, DisplayName: fmt.Sprintf("CF03 external mode %d", mode), Mode: mode, ModelKey: "nvidia-test", Baseline: baseline, Admission: biz.Open, QueueName: c.QueueName, Version: 1}
		spec := &biz.ProfileSpec{GroupID: group.GroupID, Mode: mode, ModelKey: group.ModelKey, MemoryMiBPerDevice: memory, CoreLimitPercent: cores, MaxDevicesPerReplica: maximum, IsolationClass: isolation}
		specDigest, err := biz.SpecDigest(spec, baseline.BaselineID)
		if err != nil {
			return nil, err
		}
		profile := &biz.Profile{ProfileID: biz.NewID(), ProfileVersion: 1, DisplayName: group.DisplayName, Spec: spec, BaselineID: baseline.BaselineID, SpecDigest: specDigest, State: biz.ProfileActive, Revision: 1}
		b.groups[group.GroupID], b.profiles = group, append(b.profiles, profile)
	}
	return b, nil
}

func boundaryCopy[O any](value any) (*O, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	out := new(O)
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *instanceQuantityBoundary) tenant(ctx context.Context, in *pb.TenantContext, action string) error {
	input, err := boundaryCopy[biz.TenantContext](in)
	if err != nil {
		return err
	}
	if err := biz.AuthorizeTenant(ctx, input, action); err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if in.GetTenantId() != b.config.TenantID {
		return status.Error(codes.PermissionDenied, "BOUNDARY_TENANT_DENIED")
	}
	return nil
}
func (b *instanceQuantityBoundary) admin(ctx context.Context, in *pb.AdminRead, action string) error {
	input, err := boundaryCopy[biz.AdminRead](in)
	if err != nil {
		return err
	}
	if err := biz.AuthorizeAdmin(ctx, input, action); err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return nil
}
func boundaryPage(p *pb.Page) error {
	if p.GetToken() != "" || (p.GetSize() != 0 && p.GetSize() < 2) || p.GetSize() > 200 {
		return status.Error(codes.InvalidArgument, "BOUNDARY_REQUIRES_SINGLE_CATALOG_PAGE")
	}
	return nil
}
func (b *instanceQuantityBoundary) catalog(mode pb.SupplyMode) (*pb.ListProfilesResponse, error) {
	out := new(pb.ListProfilesResponse)
	for _, profile := range b.profiles {
		if mode != 0 && int32(mode) != int32(profile.Spec.Mode) {
			continue
		}
		item, err := boundaryCopy[pb.GpuProfile](profile)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, item)
		out.Scopes = append(out.Scopes, &pb.ProfileScope{ProfileId: profile.ProfileID, ProfileVersion: profile.ProfileVersion, SpecDigest: profile.SpecDigest, ClusterId: b.config.ClusterID, PoolId: b.pool.PoolID})
	}
	return out, nil
}
func (b *instanceQuantityBoundary) ListProfiles(ctx context.Context, in *pb.ListProfilesRequest) (*pb.ListProfilesResponse, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if err := b.tenant(ctx, in.Context, "ListProfiles"); err != nil {
		return nil, err
	}
	if in.ClusterId != b.config.ClusterID {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	if in.Mode != 0 && in.Mode != pb.SupplyMode_WHOLE_EXCLUSIVE && in.Mode != pb.SupplyMode_SHARED_FIXED {
		return nil, status.Error(codes.InvalidArgument, "mode unsupported")
	}
	if err := boundaryPage(in.Page); err != nil {
		return nil, err
	}
	return b.catalog(in.Mode)
}
func (b *instanceQuantityBoundary) AdminListProfiles(ctx context.Context, in *pb.AdminListProfilesRequest) (*pb.ListProfilesResponse, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if err := b.admin(ctx, in.Context, "AdminListProfiles"); err != nil {
		return nil, err
	}
	if in.ClusterId != b.config.ClusterID {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	if err := boundaryPage(in.Page); err != nil {
		return nil, err
	}
	return b.catalog(0)
}
func (b *instanceQuantityBoundary) GetProfile(ctx context.Context, in *pb.GetProfileRequest) (*pb.GpuProfile, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if err := b.tenant(ctx, in.Context, "GetProfile"); err != nil {
		return nil, err
	}
	for _, profile := range b.profiles {
		if profile.ProfileID == in.ProfileId && profile.ProfileVersion == in.ProfileVersion {
			return boundaryCopy[pb.GpuProfile](profile)
		}
	}
	return nil, status.Error(codes.NotFound, "profile not found")
}
func (b *instanceQuantityBoundary) ListPools(ctx context.Context, in *pb.ListPoolsRequest) (*pb.ListPoolsResponse, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if err := b.admin(ctx, in.Context, "ListPools"); err != nil {
		return nil, err
	}
	if in.ClusterId != b.config.ClusterID {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	if err := boundaryPage(in.Page); err != nil {
		return nil, err
	}
	pool, err := boundaryCopy[pb.Pool](b.pool)
	return &pb.ListPoolsResponse{Items: []*pb.Pool{pool}}, err
}
func (b *instanceQuantityBoundary) ListSupplyGroups(ctx context.Context, in *pb.ListSupplyGroupsRequest) (*pb.ListSupplyGroupsResponse, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if err := b.admin(ctx, in.Context, "ListSupplyGroups"); err != nil {
		return nil, err
	}
	if in.PoolId != b.pool.PoolID {
		return nil, status.Error(codes.NotFound, "pool not found")
	}
	if err := boundaryPage(in.Page); err != nil {
		return nil, err
	}
	out := new(pb.ListSupplyGroupsResponse)
	for _, profile := range b.profiles {
		group, err := boundaryCopy[pb.SupplyGroup](b.groups[profile.Spec.GroupID])
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, group)
	}
	return out, nil
}
func (b *instanceQuantityBoundary) build(request *biz.GpuRequest) (*biz.ResolvedGpuRequest, error) {
	if request == nil || request.Replicas != 1 || request.ContainerName != "workspace" {
		return nil, status.Error(codes.InvalidArgument, "instance requires one workspace Pod")
	}
	for _, profile := range b.profiles {
		if profile.ProfileID == request.ProfileID {
			group := b.groups[profile.Spec.GroupID]
			plan, err := biz.BuildResolution(request, profile, group, 16, nil)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			return plan, nil
		}
	}
	return nil, status.Error(codes.NotFound, "profile not found")
}
func (b *instanceQuantityBoundary) ResolveGpuRequest(ctx context.Context, in *pb.ResolveGpuRequestRequest) (*pb.ResolvedGpuRequest, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if err := b.tenant(ctx, in.Context, "ResolveGpuRequest"); err != nil {
		return nil, err
	}
	request, err := boundaryCopy[biz.GpuRequest](in.Gpu)
	if err != nil {
		return nil, err
	}
	plan, err := b.build(request)
	if err != nil {
		return nil, err
	}
	return boundaryCopy[pb.ResolvedGpuRequest](plan)
}
func (b *instanceQuantityBoundary) SyncGpuUsage(ctx context.Context, in *pb.SyncGpuUsageRequest) (*pb.SyncGpuUsageResponse, error) {
	caller, ok := biz.CurrentCaller(ctx)
	if !ok || caller.Service != "ani-governance" {
		return nil, status.Error(codes.PermissionDenied, "governance required")
	}
	if in == nil || !biz.Ref(in.RequestId) {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	projection, err := boundaryCopy[biz.UsageProjection](in.Projection)
	if err != nil {
		return nil, err
	}
	if projection.Ref == nil || projection.Ref.TenantID != b.config.TenantID || projection.Ref.OwnerService != "ani-resource-service" {
		return nil, status.Error(codes.PermissionDenied, "boundary scope denied")
	}
	if err := biz.ValidateProjection(projection); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if projection.ClusterID != b.config.ClusterID || projection.ContainerName != "workspace" {
		return nil, status.Error(codes.PermissionDenied, "projection scope denied")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	prior := b.usage[*projection.Ref]
	if prior != nil && prior.Revision == projection.Revision && prior.PayloadDigest != projection.PayloadDigest {
		return nil, status.Error(codes.AlreadyExists, "projection conflict")
	}
	if prior != nil && prior.Revision >= projection.Revision {
		return &pb.SyncGpuUsageResponse{Ref: in.Projection.Ref, AppliedRevision: prior.Revision, State: pb.UsageState(prior.State), Replayed: true}, nil
	}
	if prior != nil && (prior.ExecutionDigest != projection.ExecutionDigest || prior.SourceOperationCreatedAt != projection.SourceOperationCreatedAt) {
		return nil, status.Error(codes.AlreadyExists, "original projection context changed")
	}
	file, err := os.OpenFile(filepath.Join(b.config.Root, "usage-projections.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	err = json.NewEncoder(file).Encode(projection)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	b.usage[*projection.Ref] = projection
	return &pb.SyncGpuUsageResponse{Ref: in.Projection.Ref, AppliedRevision: projection.Revision, State: in.Projection.State}, nil
}

func TestInstanceQuantityBoundaryRuntime(t *testing.T) {
	path := os.Getenv("ACC_INSTANCE_BOUNDARY_CONFIG")
	if path == "" {
		t.Skip("explicit CF03 external GPU boundary only")
	}
	var c instanceBoundaryConfig
	readJointJSON(t, path, &c)
	if !filepath.IsAbs(c.Root) || c.Address == "" || c.GovernanceDNSName != "ani-governance" {
		t.Fatal("absolute task root/address/exact governance DNS required")
	}
	if _, err := os.Stat(filepath.Join(c.Root, "ready.json")); !os.IsNotExist(err) {
		t.Fatal("boundary root already used; review before starting a new run")
	}
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := newInstanceQuantityBoundary(c)
	if err != nil {
		t.Fatal(err)
	}
	security := &conf.Security{CaFile: c.CAFile, CertFile: c.CertFile, KeyFile: c.KeyFile, GovernanceDnsName: c.GovernanceDNSName, InferenceDnsName: "ani-inference-service", ResourceDnsName: "ani-resource-service", ModeldevDnsName: "ani-modeldev-service"}
	tlsConfig, err := server.LoadServerTLS(security)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", c.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)), grpc.UnaryInterceptor(server.Authorize(security)))
	pb.RegisterAcceleratorAdminServiceServer(srv, b)
	pb.RegisterAcceleratorCatalogServiceServer(srv, b)
	pb.RegisterAcceleratorUsageServiceServer(srv, b)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	defer srv.Stop()
	requests := map[string]*biz.GpuRequest{}
	plans := map[string]*biz.ResolvedGpuRequest{}
	for _, profile := range b.profiles {
		for n := uint32(1); n <= profile.Spec.MaxDevicesPerReplica; n++ {
			name := fmt.Sprintf("Whole%d", n)
			if profile.Spec.Mode == biz.Shared {
				name = "Shared1"
			}
			req := &biz.GpuRequest{ClusterID: c.ClusterID, PoolID: b.pool.PoolID, ProfileID: profile.ProfileID, ProfileVersion: 1, Replicas: 1, DevicesPerReplica: n, ContainerName: "workspace"}
			plan, err := b.build(req)
			if err != nil {
				t.Fatal(err)
			}
			requests[name], plans[name] = req, plan
		}
	}
	writeJointJSON(t, filepath.Join(c.Root, "public.json"), map[string]any{"cluster_id": c.ClusterID, "tenant_id": c.TenantID, "pool": b.pool, "profiles": b.profiles, "requests": requests, "plans": plans, "scope": "external candidate/Resolve/usage boundary only; no real ACC database, binding or hardware claim"})
	writeJointJSON(t, filepath.Join(c.Root, "ready.json"), map[string]any{"address": listener.Addr().String(), "public_file": filepath.Join(c.Root, "public.json"), "usage_file": filepath.Join(c.Root, "usage-projections.jsonl"), "stop_file": filepath.Join(c.Root, "stop"), "cluster_id": c.ClusterID, "tenant_id": c.TenantID, "scope": "CF03 external GPU dependency only"})
	t.Log("CF03 external GPU dependency ready; real BuildResolution, managed mTLS, no ACC database")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(c.Root, "stop")); err == nil {
				return
			}
		}
	}
}

func TestInstanceQuantityBoundaryBuilderAndScope(t *testing.T) {
	c := instanceBoundaryConfig{Root: t.TempDir(), ClusterID: instanceBoundaryClusterID, TenantID: instanceBoundaryTenantID, ActorType: "user", ActorID: "2", PlatformActorType: "user", PlatformActorID: "1", QueueName: "default"}
	b, err := newInstanceQuantityBoundary(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range b.profiles {
		for n := uint32(1); n <= profile.Spec.MaxDevicesPerReplica; n++ {
			req := &biz.GpuRequest{ClusterID: c.ClusterID, PoolID: b.pool.PoolID, ProfileID: profile.ProfileID, ProfileVersion: 1, Replicas: 1, DevicesPerReplica: n, ContainerName: "workspace"}
			plan, err := b.build(req)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Execution.Resources["volcano.sh/vgpu-number"] != strconv.FormatUint(uint64(n), 10) {
				t.Fatal("boundary changed device topology")
			}
		}
		req := &biz.GpuRequest{ClusterID: c.ClusterID, PoolID: b.pool.PoolID, ProfileID: profile.ProfileID, ProfileVersion: 1, Replicas: 1, DevicesPerReplica: profile.Spec.MaxDevicesPerReplica + 1, ContainerName: "workspace"}
		if _, err := b.build(req); err == nil {
			t.Fatal("out-of-range request accepted")
		}
		req.DevicesPerReplica = 1
		req.ClusterID = biz.NewID()
		if _, err := b.build(req); err == nil {
			t.Fatal("wrong cluster accepted")
		}
	}
	ctx := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Action: "ListProfiles", TenantID: c.TenantID, Actor: biz.Actor{Type: c.ActorType, ID: c.ActorID}})
	in := &pb.ListProfilesRequest{Context: &pb.TenantContext{RequestId: "scope-test", TenantId: c.TenantID, Actor: &pb.Actor{Type: c.ActorType, Id: c.ActorID}}, ClusterId: c.ClusterID}
	out, err := b.ListProfiles(ctx, in)
	if err != nil || len(out.GetItems()) != 2 || len(out.GetScopes()) != 2 {
		t.Fatal("scope catalog", err)
	}
	in.Context.TenantId = biz.NewID()
	if _, err := b.ListProfiles(ctx, in); err == nil {
		t.Fatal("cross-tenant catalog accepted")
	}
	c.ClusterID = biz.NewID()
	if _, err := newInstanceQuantityBoundary(c); err == nil {
		t.Fatal("unapproved target cluster accepted")
	}
}
