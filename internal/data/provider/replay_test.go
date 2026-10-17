package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/schedulerconfig"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type replayState struct {
	mu                                        sync.Mutex
	memory                                    int64
	exited, absent, apiDeleted, runtimeFailed bool
	fault                                     string
}

func (s *replayState) getFault() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.fault }
func (s *replayState) setFault(f string) { s.mu.Lock(); defer s.mu.Unlock(); s.fault = f }

func (s *replayState) snapshot() (int64, bool, bool, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory, s.exited, s.absent, s.apiDeleted, s.runtimeFailed
}

type replayResources struct {
	pr.UnimplementedPodResourcesListerServer
	state *replayState
}

func (r *replayResources) List(context.Context, *pr.ListPodResourcesRequest) (*pr.ListPodResourcesResponse, error) {
	if r.state.getFault() == "podresources" {
		return nil, status.Error(codes.Unavailable, "controlled source outage")
	}
	_, _, absent, _, _ := r.state.snapshot()
	if absent {
		return &pr.ListPodResourcesResponse{}, nil
	}
	return &pr.ListPodResourcesResponse{PodResources: []*pr.PodResources{{Name: "workload", Namespace: "replay", Containers: []*pr.ContainerResources{{Name: "inference", Devices: []*pr.ContainerDevices{{ResourceName: "volcano.sh/vgpu-number", DeviceIds: []string{"NVIDIA-0"}}}}}}}}, nil
}

type replayRuntime struct {
	cri.UnimplementedRuntimeServiceServer
	state  *replayState
	podUID string
}

func (r *replayRuntime) Version(context.Context, *cri.VersionRequest) (*cri.VersionResponse, error) {
	return &cri.VersionResponse{Version: "0.1.0", RuntimeName: "containerd", RuntimeVersion: "2.1.5", RuntimeApiVersion: "v1"}, nil
}
func (r *replayRuntime) ListContainers(context.Context, *cri.ListContainersRequest) (*cri.ListContainersResponse, error) {
	if r.state.getFault() == "pending_owner" {
		return &cri.ListContainersResponse{}, nil
	}
	_, exited, _, _, failed := r.state.snapshot()
	if failed {
		return nil, status.Error(codes.Unavailable, "controlled source outage")
	}
	state := cri.ContainerState_CONTAINER_RUNNING
	if exited {
		state = cri.ContainerState_CONTAINER_EXITED
	}
	return &cri.ListContainersResponse{Containers: []*cri.Container{{Id: "cri-container", Metadata: &cri.ContainerMetadata{Name: "inference"}, State: state, Labels: map[string]string{"io.kubernetes.pod.uid": r.podUID}}}}, nil
}
func (r *replayRuntime) ContainerStatus(context.Context, *cri.ContainerStatusRequest) (*cri.ContainerStatusResponse, error) {
	memory, exited, _, _, _ := r.state.snapshot()
	state := cri.ContainerState_CONTAINER_RUNNING
	finished := int64(0)
	if exited {
		state = cri.ContainerState_CONTAINER_EXITED
		finished = time.Now().UnixNano()
	}
	return &cri.ContainerStatusResponse{Status: &cri.ContainerStatus{Id: "cri-container", Metadata: &cri.ContainerMetadata{Name: "inference"}, State: state, FinishedAt: finished, Labels: map[string]string{"io.kubernetes.pod.uid": r.podUID}}, Info: map[string]string{"info": fmt.Sprintf(`{"runtimeSpec":{"process":{"env":["NVIDIA_VISIBLE_DEVICES=%s","CUDA_DEVICE_MEMORY_LIMIT_0=%dm","CUDA_DEVICE_SM_LIMIT=25"]}}}`, gpuUUID, memory)}}, nil
}

type replayInventory struct{}

func tlsKeyPair(cert, key string) (*tls.Config, error) {
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, nil
}

const gpuUUID2 = "GPU-fedcba98-7654-3210-fedc-ba9876543210"

func (replayInventory) Read(context.Context) ([]Inventory, error) {
	return []Inventory{{gpuUUID, "NVIDIA-Test", 24576 << 20}, {gpuUUID2, "NVIDIA-Test", 24576 << 20}}, nil
}
func unixServer(t *testing.T, name string, register func(*grpc.Server)) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, name+".sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	register(s)
	go s.Serve(l)
	t.Cleanup(s.Stop)
	return path
}

func TestProviderControlledSourcesPersistRecoverAndRelease(t *testing.T) {
	for _, version := range []string{"v1.12.0", "v1.15.2"} {
		t.Run(version, func(t *testing.T) { providerControlledSources(t, version, "") })
	}
}

func TestProviderLateOwnerPreservesHistoricalSchedulerAndAssociatesRuntime(t *testing.T) {
	providerControlledSources(t, "v1.15.2", "late_owner")
}

func providerControlledSources(t *testing.T, schedulerVersion, qualificationFault string) {
	path := os.Getenv("ACC_TEST_DSN_FILE")
	if path == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	db, err := data.OpenPostgres(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cluster := biz.Cluster{ClusterID: biz.NewID(), DisplayName: "provider replay", ConnectionRef: "replay:" + biz.NewID(), Version: 1}
	pool := biz.Pool{PoolID: biz.NewID(), ClusterID: cluster.ClusterID, DisplayName: "replay"}
	nodeUID := "node-" + biz.NewID()
	podUID := "pod-" + biz.NewID()
	groupID := biz.NewID()
	cmData := map[string]string{"config.yaml": "constructed-source-fixture: true", schedulerconfig.DataKey: "actions: enqueue,allocate\ntiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.VGPUEnable: true\n"}
	cm := map[string]any{"data": cmData, "binaryData": nil}
	configDigest, _ := biz.Hash(cm)
	schedulerDigest, err := schedulerconfig.ConfigMapDigest(schedulerVersion, cmData, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := &biz.ProviderBaseline{BaselineID: biz.NewID(), AdapterID: "volcano-hami-factor-v1", VolcanoVersion: "v1.12.0", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "controlled-replay", MemoryBlockMiB: 1024, FactorEvidenceRef: "replay:factor", MemoryEncoding: "VOLCANO_BLOCKS_TO_MIB", SplitCount: 4, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: configDigest, SchedulerConfigDigest: schedulerDigest, RecipeVersion: "volcano-hami-v1"}
	b.VolcanoVersion = schedulerVersion
	b.BaselineDigest, _ = biz.BaselineDigest(b)
	g := biz.SupplyGroup{GroupID: groupID, PoolID: pool.PoolID, ClusterID: cluster.ClusterID, DisplayName: "replay", Mode: biz.Shared, Nodes: []biz.NodeRef{{UID: nodeUID, Name: "replay-node"}}, ModelKey: "nvidia-test", Baseline: b, Admission: biz.Closed, QueueName: "gpu", Version: 1, AdoptedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	spec := &biz.ProfileSpec{GroupID: groupID, Mode: biz.Shared, ModelKey: g.ModelKey, SharedMemoryMiB: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}
	sd, _ := biz.SpecDigest(spec, b.BaselineID)
	profile := biz.Profile{ProfileID: biz.NewID(), ProfileVersion: 1, DisplayName: "replay", Spec: spec, BaselineID: b.BaselineID, SpecDigest: sd, Published: true}
	ownerPub, ownerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ref := &biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-inference", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	plan, err := biz.BuildPlan(&biz.GpuRequest{ClusterID: cluster.ClusterID, PoolID: pool.PoolID, ProfileID: profile.ProfileID, ProfileVersion: 1, Replicas: 1, DevicesPerReplica: 1, ContainerName: "inference"}, &profile, &g, 16)
	if err != nil {
		t.Fatal(err)
	}
	podRef := biz.PodRef{ClusterID: cluster.ClusterID, Namespace: "replay", Name: "workload", UID: podUID, ContainerName: "inference"}
	assertion, _ := biz.Canonical(OwnerAssertion{podRef, *ref, plan.ResolutionDigest})
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(ownerKey, append([]byte("acc-owner-pod-v1\n"), assertion...)))
	state := &replayState{memory: 6144}
	if qualificationFault == "late_owner" {
		state.setFault("pending_owner")
	}
	assignmentTime := time.Now().UTC()
	pki := testkit.NewPKI(t)
	cert, key := pki.Issue(t, "kubernetes", "", true)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _, _, deleted, _ := state.snapshot()
		now := time.Now().UTC()
		fault := state.getFault()
		currentSignature := signature
		if fault == "pending_owner" {
			currentSignature = ""
		}
		assignmentValue := gpuUUID + ",NVIDIA,6,25:"
		if fault == "scheduler_memory" {
			assignmentValue = gpuUUID + ",NVIDIA,7,25:"
		}
		if fault == "stale" {
			now = now.Add(-time.Hour)
		}
		if fault == "offline" {
			http.Error(w, "controlled outage", http.StatusServiceUnavailable)
			return
		}
		var response any
		switch {
		case r.URL.Path == "/api/v1/nodes":
			response = map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"uid": nodeUID, "name": "replay-node", "resourceVersion": "1", "labels": map[string]string{biz.GroupLabel: g.GroupID, biz.ModelLabel: g.ModelKey, biz.BaselineLabel: b.BaselineID}, "annotations": map[string]string{NodeRegister: gpuUUID + ",4,24,NVIDIA-Test,true,hami-core:" + gpuUUID2 + ",4,24,NVIDIA-Test,true,hami-core:", NodeHandshake: "Reported " + now.Format("2006-01-02 15:04:05.999999999 -0700 MST")}}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True", "lastHeartbeatTime": now.Format(time.RFC3339Nano)}}}}}}
			if fault == "registration" {
				response.(map[string]any)["items"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]string)[NodeRegister] = "invalid-registration"
			}
			status := response.(map[string]any)["items"].([]any)[0].(map[string]any)["status"].(map[string]any)
			if fault != "allocatable" {
				status["allocatable"] = map[string]string{"volcano.sh/vgpu-number": "8", "volcano.sh/vgpu-memory": "48", "volcano.sh/vgpu-cores": "200"}
			}
			if fault == "allocatable_zero" {
				status["allocatable"].(map[string]string)["volcano.sh/vgpu-cores"] = "0"
			}
			if fault == "allocatable_invalid" {
				status["allocatable"].(map[string]string)["volcano.sh/vgpu-memory"] = "bad"
			}

		case r.URL.Path == "/api/v1/pods":
			items := []any{}
			items = append(items, map[string]any{"metadata": map[string]any{"uid": "pending-1", "name": "pending", "namespace": "replay"}, "spec": map[string]any{"nodeSelector": map[string]string{biz.GroupLabel: g.GroupID}, "containers": []any{map[string]any{"name": "inference", "resources": map[string]any{"limits": map[string]string{"volcano.sh/vgpu-number": "1"}}}}}, "status": map[string]string{"phase": "Pending"}})
			if !deleted {
				items = append(items, map[string]any{"metadata": map[string]any{"uid": podUID, "name": "workload", "namespace": "replay", "resourceVersion": "1", "labels": map[string]string{"accelerator.ani.io/resource-tenant-id": ref.TenantID, "accelerator.ani.io/owner-service": ref.OwnerService, "accelerator.ani.io/resource-id": ref.ResourceID, "accelerator.ani.io/create-operation-id": ref.CreateOperationID}, "annotations": map[string]string{AssignedIDs: assignmentValue, AssignedTime: strconv.FormatInt(assignmentTime.Unix(), 10), "accelerator.ani.io/plan-digest": plan.ResolutionDigest, "accelerator.ani.io/owner-signature": currentSignature}}, "spec": map[string]any{"nodeName": "replay-node", "containers": []any{map[string]any{"name": "inference", "resources": map[string]any{"limits": map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "6"}}}}}})
			}
			response = map[string]any{"items": items}
		case strings.Contains(r.URL.Path, "/configmaps/"):
			response = cm
			if fault == "scheduler_config" && strings.HasSuffix(r.URL.Path, "/scheduler") {
				changed := map[string]string{}
				for key, value := range cmData {
					changed[key] = value
				}
				changed["qualification-test-drift"] = "changed after qualification"
				response = map[string]any{"data": changed, "binaryData": nil}
			}
		case r.URL.Path == "/apis/scheduling.volcano.sh/v1beta1/queues/gpu":
			response = map[string]any{"metadata": map[string]any{"name": "gpu"}, "status": map[string]any{"state": "Open"}}
		case r.URL.Path == "/api/v1/namespaces/system/pods":
			image := b.VolcanoImageDigest
			if fault == "scheduler_image" {
				image = "sha256:" + strings.Repeat("c", 64)
			}
			containerName := "scheduler"
			args := []string{}
			if r.URL.Query().Get("labelSelector") == "app=plugin" {
				image = b.DevicePluginImageDigest
				containerName = "plugin"
				args = []string{"--gpu-memory-factor=1024", "--device-split-count=4"}
				if fault == "factor" {
					args = []string{"--gpu-memory-factor=256", "--device-split-count=4"}
				}
			}
			response = map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": containerName}, "spec": map[string]any{"nodeName": "replay-node", "containers": []any{map[string]any{"name": containerName, "args": args}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": containerName, "ready": true, "imageID": image}}}}}}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(response)
	})
	api := httptest.NewUnstartedServer(handler)
	serverCert, err := tlsKeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	api.TLS = serverCert
	api.StartTLS()
	defer api.Close()
	prSocket := unixServer(t, "pr", func(s *grpc.Server) { pr.RegisterPodResourcesListerServer(s, &replayResources{state: state}) })
	criSocket := unixServer(t, "cri", func(s *grpc.Server) {
		cri.RegisterRuntimeServiceServer(s, &replayRuntime{state: state, podUID: podUID})
	})
	attPub, attKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pubFile := filepath.Join(dir, "hardware.pub")
	testkit.Write(t, pubFile, []byte(base64.StdEncoding.EncodeToString(attPub)))
	evidence, err := NewEvidenceStore(dir, pubFile)
	if err != nil {
		t.Fatal(err)
	}
	a := Attestation{SchemaVersion: 1, Kind: "gpu-hardware-consumption-v1", EvidenceRef: "replay:consumption", ClusterID: cluster.ClusterID, GroupID: g.GroupID, BaselineDigest: b.BaselineDigest, SpecDigest: sd, NodeUIDs: []string{nodeUID}, Devices: []HardwareDevice{{nodeUID, gpuUUID, g.ModelKey, 24576 << 20, 24576}, {nodeUID, gpuUUID2, g.ModelKey, 24576 << 20, 24576}}, VerifiedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), ExperimentManifestDigest: strings.Repeat("e", 64)}
	canonical, _ := biz.Canonical(a)
	signed := SignedAttestation{Payload: a, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(attKey, append([]byte("acc-hardware-attestation-v1\n"), canonical...)))}
	bytes, _ := json.Marshal(signed)
	testkit.Write(t, filepath.Join(dir, EvidenceFilename(a.EvidenceRef)), bytes)
	b.Verification, err = evidence.Verify(ctx, g, spec, a.EvidenceRef)
	if err != nil {
		t.Fatal(err)
	}
	config := Connection{Ref: cluster.ConnectionRef, APIServer: api.URL, ServerName: "accelerator.test", CAFile: pki.CAFile, CertFile: cert, KeyFile: key, PluginNamespace: "system", PluginSelector: "app=plugin", PluginContainer: "plugin", PluginConfigMap: "plugin", SchedulerNamespace: "system", SchedulerSelector: "app=scheduler", SchedulerContainer: "scheduler", SchedulerConfigMap: "scheduler", LocalNodeUID: nodeUID, Nodes: []NodeSource{{nodeUID, "replay-node", prSocket, criSocket}}}
	adapter, err := New(Config{Connections: []Connection{config}}, evidence, ownerPub, db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	adapter.inventory = replayInventory{}
	err = db.Transact(ctx, func(tx biz.Transaction) error {
		if e := tx.InsertCluster(ctx, cluster); e != nil {
			return e
		}
		if e := tx.InsertPool(ctx, pool); e != nil {
			return e
		}
		if e := tx.InsertGroup(ctx, g); e != nil {
			return e
		}
		return tx.InsertProfile(ctx, profile, cluster.ClusterID)
	})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := adapter.Inspect(ctx, cluster, g)
	if err != nil || len(inspection.Nodes) != 1 {
		t.Fatal(inspection, err)
	}
	rebuilt := g
	rebuilt.Nodes = []biz.NodeRef{{UID: "replacement-node-uid", Name: g.Nodes[0].Name}}
	if _, err := adapter.Inspect(ctx, cluster, rebuilt); err == nil {
		t.Fatal("node name inherited old UID qualification")
	}
	collect := func() {
		t.Helper()
		batch, err := adapter.Collect(ctx, cluster, []biz.SupplyGroup{g})
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
			t.Fatal(err)
		}
	}
	collect()
	if qualificationFault == "late_owner" {
		var first biz.Claim
		if err := db.View(ctx, func(r biz.Reader) error {
			claims, _, err := r.PodEvidence(ctx, podRef)
			if err != nil {
				return err
			}
			if len(claims) != 1 || claims[0].Source != "scheduler" || claims[0].OwnerRef != nil || claims[0].PlanDigest != "" {
				t.Fatalf("expected pre-signature scheduler-only observation: %+v", claims)
			}
			first = claims[0]
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		state.setFault("")
		collect()
		collect() // Reception time changes do not create a new scheduler revision.
		// Owner operation times originate in PostgreSQL at microsecond precision.
		createdAt := time.Now().UTC().Truncate(time.Microsecond)
		projection := biz.UsageProjection{Ref: ref, Revision: 1, State: biz.Declared, Plan: plan, SourceOperationCreatedAt: createdAt.Format(time.RFC3339Nano), SourceFactRef: "quota-operation:" + ref.CreateOperationID + ":accepted"}
		projection.PayloadDigest, _ = biz.ProjectionDigest(&projection)
		if err := db.Transact(ctx, func(tx biz.Transaction) error { _, err := tx.SaveUsage(ctx, projection); return err }); err != nil {
			t.Fatal(err)
		}
		if err := db.View(ctx, func(r biz.Reader) error {
			claims, _, err := r.PodEvidence(ctx, podRef)
			if err != nil {
				return err
			}
			if len(claims) != 3 {
				t.Fatalf("late signature blocked current runtime/allocation: %+v", claims)
			}
			for _, claim := range claims {
				if claim.Source == "scheduler" {
					old, next := first, claim
					old.ReceivedAt, next.ReceivedAt = time.Time{}, time.Time{}
					a, _ := biz.Hash(old)
					b, _ := biz.Hash(next)
					if a != b || claim.OwnerRef != nil {
						t.Fatal("late owner rewrote the original scheduler fact")
					}
				} else if claim.OwnerRef == nil || *claim.OwnerRef != *ref || claim.PlanDigest != plan.ResolutionDigest {
					t.Fatalf("fresh actual allocation lost verified owner: %+v", claim)
				}
			}
			bindings, err := r.Bindings(ctx, biz.BindingFilter{ClusterID: cluster.ClusterID, Pod: &podRef}, "", 201)
			if err != nil || len(bindings) != 1 || bindings[0].State != biz.Active || bindings[0].VerifiedUsageRef == nil || *bindings[0].VerifiedUsageRef != *ref || bindings[0].MemoryMiB != 6144 {
				t.Fatalf("runtime ownership did not reconcile one actual binding: %+v error=%v", bindings, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		state.setFault("scheduler_memory")
		changed, err := adapter.Collect(ctx, cluster, []biz.SupplyGroup{g})
		if err != nil {
			t.Fatal(err)
		}
		err = db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, changed) })
		if err == nil || err.Error() != "SOURCE_REVISION_CONFLICT" {
			t.Fatalf("different actual allocation reused its historical revision: %v", err)
		}
		return
	}
	codec, _ := biz.NewCursorCodec([]byte(strings.Repeat("k", 32)))
	u, err := biz.NewUsecase(db, adapter, evidence, codec, []string{cluster.ConnectionRef}, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	actor := biz.Actor{Type: "user", ID: "replay"}
	admin := biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Actor: actor, Action: "AdminGetCapacity", ClusterIDs: []string{"*"}})
	capacity := func() *biz.CapacityView {
		t.Helper()
		v, err := u.AdminGetCapacity(admin, &biz.AdminRead{RequestID: "replay", Actor: &actor}, profile.ProfileID, 1, true)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := capacity()
	if !v.Observation.Complete || v.KnownCommittedUnits != 1 || v.KnownFreeUnits != 7 || v.UnplacedRequestedUnits != 1 {
		t.Fatal(v)
	}
	owner := biz.WithCaller(ctx, biz.Caller{Service: "ani-inference", Action: "ObserveRelease", TenantID: ref.TenantID})
	if qualificationFault != "" {
		qualificationObservation(t, qualificationFault, adapter, evidence, dir, a, state, cluster, g, db, u, owner, ref, podRef, capacity)
		return
	}
	faults := []string{"stale", "offline", "factor"}
	if schedulerVersion == "v1.15.2" {
		faults = append(faults, "allocatable", "allocatable_zero", "allocatable_invalid")
	}
	for _, fault := range faults {
		state.setFault(fault)
		if _, err := adapter.Inspect(ctx, cluster, g); err == nil {
			t.Fatal("inspection accepted", fault)
		}
		collect()
		if capacity().Observation.Complete {
			t.Fatal("bad source complete", fault)
		}
		observation, err := u.ObserveRelease(owner, "replay", ref, biz.NewID(), []biz.PodRef{podRef})
		wantFinding := biz.ReleaseUnknown
		if strings.HasPrefix(fault, "allocatable") {
			// Admission registration is invalid, but current independent runtime
			// and allocation probes still positively observe this live binding.
			wantFinding = biz.StillPresent
		}
		if err != nil || observation.Finding != wantFinding {
			t.Fatal("source/admission failure changed observed allocation", fault, observation, err)
		}
		state.setFault("")
		collect()
	}
	state.mu.Lock()
	state.memory = 8192
	state.mu.Unlock()
	collect()
	v = capacity()
	if v.Observation.Complete || v.KnownFreeUnits != 4 {
		t.Fatalf("conflict was not device-local: %+v", v)
	}
	conflicted, err := u.ObserveRelease(owner, "replay", ref, biz.NewID(), []biz.PodRef{podRef})
	if err != nil || conflicted.Finding != biz.ReleaseUnknown {
		t.Fatal("conflicting evidence was not unknown", conflicted, err)
	}
	state.mu.Lock()
	state.memory = 6144
	state.runtimeFailed = true
	state.mu.Unlock()
	collect()
	if capacity().Observation.Complete {
		t.Fatal("source outage considered complete")
	}
	// Reconstruct adapter; persisted facts survive source/API object absence.
	adapter, err = New(Config{Connections: []Connection{config}}, evidence, ownerPub, db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	adapter.inventory = replayInventory{}
	state.mu.Lock()
	state.runtimeFailed = false
	state.apiDeleted = true
	state.mu.Unlock()
	collect()
	release, err := u.ObserveRelease(owner, "replay", ref, biz.NewID(), []biz.PodRef{podRef})
	if err != nil || release.Finding == biz.AllocationReleased {
		t.Fatal("API deletion falsely released", release, err)
	}
	state.mu.Lock()
	state.exited = true
	state.absent = true
	state.mu.Unlock()
	collect()
	release, err = u.ObserveRelease(owner, "replay", ref, biz.NewID(), []biz.PodRef{podRef})
	if err != nil || release.Finding != biz.AllocationReleased {
		t.Fatal(release, err)
	}
	if capacity().KnownFreeUnits != 8 {
		t.Fatal("positive release failed to restore capacity")
	}
	t.Log("OBS-01..06 CAP-01..04 REL-01..04: production HTTP/TLS + Unix gRPC readers, controlled source replay + real PG; no actual GPU sample")
}
