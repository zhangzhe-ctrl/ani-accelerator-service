package contract_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// This one-shot fixture exists only in the test binary. Gov first commits and
// synchronizes its original ref; the helper reads that persisted plan and writes
// controlled inventory through the actual transactional repository.
func TestJointObservationFixture(t *testing.T) {
	path := os.Getenv("ACC_JOINT_OBSERVATION_FILE")
	if path == "" {
		t.Skip("one-shot isolated software observation fixture")
	}
	var in struct {
		Ref      biz.UsageRef `json:"ref"`
		State    string       `json:"state"`
		DeviceID string       `json:"device_id"`
		Pod      *biz.PodRef  `json:"pod"`
	}
	readJointJSON(t, path, &in)
	if err := biz.ValidateRef(&in.Ref); err != nil {
		t.Fatal(err)
	}
	db, err := data.OpenPostgres(context.Background(), os.Getenv("ACC_TEST_DSN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var usage *biz.UsageProjection
	var group *biz.SupplyGroup
	if err = db.View(context.Background(), func(r biz.Reader) error {
		var e error
		usage, e = r.Usage(context.Background(), in.Ref)
		if e != nil {
			return e
		}
		group, e = r.Group(context.Background(), usage.Plan.Profile.Spec.GroupID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(group.Nodes) == 0 {
		t.Fatal("stored group has no node")
	}
	if in.State != "ACTIVE" && in.State != "RELEASED" {
		t.Fatal("state must be ACTIVE or RELEASED")
	}
	if in.State == "RELEASED" && (in.DeviceID == "" || in.Pod == nil) {
		t.Fatal("release requires the original actual fixture scope")
	}
	if in.DeviceID == "" {
		in.DeviceID = biz.NewID()
	}
	if in.Pod == nil {
		in.Pod = &biz.PodRef{ClusterID: group.ClusterID, Namespace: "joint-contract", Name: "owner-instance", UID: "pod-" + biz.NewID(), ContainerName: usage.Plan.Request.ContainerName}
	}
	if in.Pod.ClusterID != group.ClusterID || in.Pod.ContainerName != usage.Plan.Request.ContainerName {
		t.Fatal("fixture scope differs from frozen plan")
	}
	now := time.Now().UTC()
	batch := biz.ObservationBatch{ClusterID: group.ClusterID}
	if in.State == "ACTIVE" {
		batch.Devices = []biz.DeviceRecord{{Device: biz.PhysicalDevice{DeviceID: in.DeviceID, ClusterID: group.ClusterID, GroupID: group.GroupID, Node: &group.Nodes[0], VendorDeviceID: "GPU-joint-" + in.DeviceID, ModelKey: group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576 / int64(group.Baseline.MemoryBlockMiB), MemoryBlockMiB: group.Baseline.MemoryBlockMiB, SafeMemoryBudgetMiB: 24576, BaselineDigest: group.Baseline.BaselineDigest}}
		for _, source := range []string{"scheduler", "allocation", "runtime"} {
			batch.Claims = append(batch.Claims, biz.Claim{Pod: *in.Pod, DeviceID: in.DeviceID, NodeUID: group.Nodes[0].UID, Source: source, Cursor: "joint-active", ObservedAt: now, ReceivedAt: now, State: biz.Active, MemoryMiB: usage.Plan.Profile.Spec.SharedMemoryMiB, CoreLimitPercent: usage.Plan.Profile.Spec.CoreLimitPercent, BaselineDigest: group.Baseline.BaselineDigest, MemoryBlockMiB: group.Baseline.MemoryBlockMiB, EvidenceRef: "isolated-joint:" + source, OwnerRef: &in.Ref, PlanDigest: usage.Plan.ResolutionDigest})
		}
		for _, source := range []string{"kubernetes", "podresources", "cri"} {
			batch.Watermarks = append(batch.Watermarks, biz.SourceWatermark{ClusterID: group.ClusterID, NodeUID: group.Nodes[0].UID, Source: source, ObservedAt: now, ReceivedAt: now, Complete: true})
		}
	} else {
		batch.Releases = []biz.ReleaseProof{{Pod: *in.Pod, DeviceID: in.DeviceID, NodeUID: group.Nodes[0].UID, ObservedAt: now, ReceivedAt: now, EvidenceRef: "isolated-joint:scope-release", RuntimeExited: true, AllocationAbsent: true, NodeOnline: true, OwnerRef: &in.Ref, PlanDigest: usage.Plan.ResolutionDigest}}
	}
	if err = db.Transact(context.Background(), func(tx biz.Transaction) error { return tx.SaveObservation(context.Background(), batch) }); err != nil {
		t.Fatal(err)
	}
	if err = db.View(context.Background(), func(r biz.Reader) error {
		bindings, e := r.Bindings(context.Background(), biz.BindingFilter{Usage: &in.Ref}, "", 200)
		if e != nil {
			return e
		}
		for _, binding := range bindings {
			if binding.PhysicalDeviceID == in.DeviceID && binding.Pod != nil && *binding.Pod == *in.Pod {
				return nil
			}
		}
		t.Fatal("nonempty sensitive binding did not persist for original tenant/ref")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeJointJSON(t, path+".result.json", in)
	t.Log("PASS: controlled software observation persisted and queried; no hardware or owner-cleanup claim")
}

// Seeds B through the same authenticated management RPCs exposed to Gov. The
// caller must provide an already running test-only ContractServerProcess.
func TestJointCatalogSeed(t *testing.T) {
	output := os.Getenv("ACC_JOINT_SEED_OUTPUT")
	if output == "" {
		t.Skip("explicit isolated B-layer catalog seed")
	}
	b, err := os.ReadFile(os.Getenv("ACC_CONTRACT_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg conf.Bootstrap
	if err = protojson.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	c, err := grpc.NewClient(cfg.Server.Grpc.Addr, grpc.WithTransportCredentials(credentials.NewTLS(testkit.ClientTLS(t, cfg.Security.CaFile, os.Getenv("ACC_JOINT_GOV_CERT"), os.Getenv("ACC_JOINT_GOV_KEY")))))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	actor := os.Getenv("ACC_JOINT_ACTOR_ID")
	if actor == "" {
		actor = "contract-user"
	}
	admin := &pb.AdminRead{RequestId: "joint-seed", Actor: &pb.Actor{Type: "user", Id: actor}}
	mutation := func() *pb.AdminMutation { return &pb.AdminMutation{Context: admin, IdempotencyKey: biz.NewID()} }
	requests := map[string]proto.Message{}
	rpc := func(method string, in, out proto.Message) {
		t.Helper()
		requests[method] = proto.Clone(in)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-ani-action", method, "x-ani-actor-type", "user", "x-ani-actor-id", actor))
		if e := c.Invoke(ctx, methodPath(method), in, out); e != nil {
			t.Fatal(method, e)
		}
	}
	cluster := new(pb.Cluster)
	rpc("RegisterCluster", &pb.RegisterClusterRequest{Mutation: mutation(), DisplayName: "joint-software-only", ConnectionRef: os.Getenv("ACC_CONTRACT_CONNECTION")}, cluster)
	pool := new(pb.Pool)
	rpc("CreatePool", &pb.CreatePoolRequest{Mutation: mutation(), ClusterId: cluster.ClusterId, DisplayName: "joint"}, pool)
	baseline := &biz.ProviderBaseline{BaselineID: biz.NewID(), AdapterID: "volcano-hami-factor-v1", VolcanoVersion: "v1.12.0", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "isolated-joint", MemoryBlockMiB: 1024, FactorEvidenceRef: "joint:factor", MemoryEncoding: "VOLCANO_BLOCKS_TO_MIB", SplitCount: 4, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: strings.Repeat("c", 64), SchedulerConfigDigest: strings.Repeat("d", 64), RecipeVersion: "volcano-hami-v1"}
	baseline.BaselineDigest, _ = biz.BaselineDigest(baseline)
	group := new(pb.SupplyGroup)
	rpc("AdoptSupplyGroup", &pb.AdoptSupplyGroupRequest{Mutation: mutation(), PoolId: pool.PoolId, DisplayName: "joint", Mode: pb.SupplyMode_SHARED_FIXED, Nodes: []*pb.NodeRef{{Uid: "node-" + biz.NewID(), Name: "joint-node"}}, ModelKey: "nvidia-test", Baseline: copyAs[pb.ProviderBaseline](t, baseline), QueueName: "gpu"}, group)
	profile := new(pb.GpuProfile)
	rpc("PublishProfile", &pb.PublishProfileRequest{Mutation: mutation(), DisplayName: "joint-6gib", Spec: &pb.ProfileSpec{GroupId: group.GroupId, Mode: pb.SupplyMode_SHARED_FIXED, ModelKey: group.ModelKey, SharedMemoryMib: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}, ExpectedGroupVersion: 1, VerificationRef: "isolated-test:consumption"}, profile)
	rpc("SetAdmission", &pb.SetAdmissionRequest{Mutation: mutation(), GroupId: group.GroupId, ExpectedVersion: 2, Desired: pb.AdmissionState_ADMISSION_OPEN, Reason: "isolated B-layer only"}, group)
	request := &pb.GpuRequest{ClusterId: cluster.ClusterId, PoolId: pool.PoolId, ProfileId: profile.ProfileId, ProfileVersion: 1, Replicas: 2, DevicesPerReplica: 1, ContainerName: "inference"}
	writeJointJSON(t, output, map[string]any{"requests": requests, "cluster": cluster, "pool": pool, "group": group, "profile": profile, "request": request, "scope": "isolated B-layer; no hardware evidence"})
}
func readJointJSON(t *testing.T, path string, out any) {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, out); e != nil {
		t.Fatal(e)
	}
}
func writeJointJSON(t *testing.T, path string, in any) {
	t.Helper()
	b, e := json.MarshalIndent(in, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	testkit.Write(t, path, b)
}
