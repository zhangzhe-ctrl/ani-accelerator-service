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
	finishedAt                                time.Time
	runtimeID                                 string
	runtimeUnknown                            bool
	runtimeEnvironment                        []string
}

func (s *replayState) getFault() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.fault }
func (s *replayState) setFault(f string) { s.mu.Lock(); defer s.mu.Unlock(); s.fault = f }

func (s *replayState) exitTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finishedAt.IsZero() {
		s.finishedAt = time.Now().UTC()
	}
	return s.finishedAt
}

func (s *replayState) runtimeIdentity() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.runtimeID
	if id == "" {
		id = "cri-container"
	}
	return id, s.runtimeUnknown
}

func (s *replayState) gpuEnvironment(memory int64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeEnvironment != nil {
		return append([]string(nil), s.runtimeEnvironment...)
	}
	return []string{"NVIDIA_VISIBLE_DEVICES=" + gpuUUID, fmt.Sprintf("CUDA_DEVICE_MEMORY_LIMIT_0=%dm", memory), "CUDA_DEVICE_SM_LIMIT=25"}
}

func duplicateRuntimeGPUEnvironment(memory int64) []string {
	return []string{"NVIDIA_VISIBLE_DEVICES=all", "CUDA_DEVICE_SM_LIMIT=25", "NVIDIA_VISIBLE_DEVICES=" + gpuUUID, fmt.Sprintf("CUDA_DEVICE_MEMORY_LIMIT_0=%dm", memory)}
}

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
	id, unknown := r.state.runtimeIdentity()
	if exited {
		state = cri.ContainerState_CONTAINER_EXITED
	} else if unknown {
		state = cri.ContainerState_CONTAINER_UNKNOWN
	}
	return &cri.ListContainersResponse{Containers: []*cri.Container{{Id: id, Metadata: &cri.ContainerMetadata{Name: "inference"}, State: state, Labels: map[string]string{"io.kubernetes.pod.uid": r.podUID}}}}, nil
}
func (r *replayRuntime) ContainerStatus(context.Context, *cri.ContainerStatusRequest) (*cri.ContainerStatusResponse, error) {
	memory, exited, _, _, _ := r.state.snapshot()
	state := cri.ContainerState_CONTAINER_RUNNING
	id, unknown := r.state.runtimeIdentity()
	finished := int64(0)
	if exited {
		state = cri.ContainerState_CONTAINER_EXITED
		finished = r.state.exitTime().UnixNano()
	} else if unknown {
		state = cri.ContainerState_CONTAINER_UNKNOWN
	}
	info, err := json.Marshal(map[string]any{"runtimeSpec": map[string]any{"process": map[string]any{"env": r.state.gpuEnvironment(memory)}}})
	if err != nil {
		return nil, err
	}
	return &cri.ContainerStatusResponse{Status: &cri.ContainerStatus{Id: id, Metadata: &cri.ContainerMetadata{Name: "inference"}, State: state, FinishedAt: finished, Labels: map[string]string{"io.kubernetes.pod.uid": r.podUID}}, Info: map[string]string{"info": string(info)}}, nil
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
const gpuUUID3 = "GPU-abcdef01-2345-6789-abcd-ef0123456789"

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

func TestProviderRuntimeClassSelectorFrozenAndConflictsRejected(t *testing.T) {
	providerControlledSources(t, "v1.15.2", "runtimeclass_selector")
}

func TestProviderControlledSourcesPersistRecoverAndRelease(t *testing.T) {
	for _, version := range []string{"v1.12.0", "v1.15.2", "v1.16.0"} {
		t.Run(version, func(t *testing.T) { providerControlledSources(t, version, "") })
	}
}

func TestProviderLateOwnerPreservesHistoricalSchedulerAndAssociatesRuntime(t *testing.T) {
	providerControlledSources(t, "v1.15.2", "late_owner")
}

func TestProviderHistoricalGroupDoesNotReplaceCurrentSupply(t *testing.T) {
	providerControlledSources(t, "v1.16.0", "historical_group")
}

func providerControlledSources(t *testing.T, schedulerVersion, configurationFault string) {
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
	nodeUID2 := "node-" + biz.NewID()
	podUID := "pod-" + biz.NewID()
	replacementPodUID := biz.NewID()
	if configurationFault == "restart_release" {
		nodeUID, podUID = biz.NewID(), biz.NewID()
	}
	runtimeClassUID := biz.NewID()
	groupID := biz.NewID()
	workloadMiB, workloadBlocks, registeredBlocks, liveFactor := int64(6144), "6144", "24576", "1"
	if configurationFault == "factor10" {
		workloadMiB, workloadBlocks, registeredBlocks, liveFactor = 3840, "384", "2457", "10"
	}
	cmData := map[string]string{"config.yaml": "constructed-source-fixture: true", schedulerconfig.DataKey: "actions: enqueue,allocate\ntiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.VGPUEnable: true\n"}
	cm := map[string]any{"data": cmData, "binaryData": nil}
	configDigest, _ := biz.Hash(cm)
	schedulerDigest, err := schedulerconfig.ConfigMapDigest(schedulerVersion, cmData, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := &biz.ProviderBaseline{BaselineID: biz.NewID(), AdapterID: "volcano-hami-mib-v1", VolcanoVersion: "v1.12.0", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "controlled-replay", MemoryBlockMiB: 1, MemoryEncoding: "MIB", SplitCount: 4, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: configDigest, SchedulerConfigDigest: schedulerDigest, RecipeVersion: "volcano-hami-mib-v1", RuntimeClassName: "nvidia"}
	b.VolcanoVersion = schedulerVersion
	if configurationFault == "factor10" {
		b.MemoryBlockMiB = 10
	}
	b.BaselineDigest, _ = biz.BaselineDigest(b)
	g := biz.SupplyGroup{GroupID: groupID, PoolID: pool.PoolID, ClusterID: cluster.ClusterID, DisplayName: "replay", Mode: biz.Shared, Nodes: []biz.NodeRef{{UID: nodeUID, Name: "replay-node"}}, ModelKey: "nvidia-test", Baseline: b, Admission: biz.Closed, QueueName: "gpu", Version: 1, AdoptedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if configurationFault == "revision_continuity" {
		g.Nodes = append(g.Nodes, biz.NodeRef{UID: nodeUID2, Name: "replay-node-2"})
	}
	if configurationFault == "runtimeclass_selector" {
		g.Admission = biz.Open
	}
	spec := &biz.ProfileSpec{GroupID: groupID, Mode: biz.Shared, ModelKey: g.ModelKey, MemoryMiBPerDevice: workloadMiB, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}
	sd, _ := biz.SpecDigest(spec, b.BaselineID)
	profile := biz.Profile{ProfileID: biz.NewID(), ProfileVersion: 1, DisplayName: "replay", Spec: spec, BaselineID: b.BaselineID, SpecDigest: sd, State: biz.ProfileActive, Revision: 1}
	ownerPub, ownerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ref := &biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-inference", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	executionDigest := strings.Repeat("d", 64)
	podRef := biz.PodRef{ClusterID: cluster.ClusterID, Namespace: "replay", Name: "workload", UID: podUID, ContainerName: "inference"}
	assertion, _ := biz.Canonical(OwnerAssertion{podRef, *ref, executionDigest})
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(ownerKey, append([]byte("acc-owner-pod-v2\n"), assertion...)))
	replacementPodRef := podRef
	replacementPodRef.UID = replacementPodUID
	replacementAssertion, _ := biz.Canonical(OwnerAssertion{replacementPodRef, *ref, executionDigest})
	replacementSignature := base64.StdEncoding.EncodeToString(ed25519.Sign(ownerKey, append([]byte("acc-owner-pod-v2\n"), replacementAssertion...)))
	state := &replayState{memory: workloadMiB}
	if configurationFault == "runtime_env_order" {
		state.runtimeEnvironment = duplicateRuntimeGPUEnvironment(workloadMiB)
	}
	if configurationFault == "restart_release" {
		state.runtimeID = strings.Repeat("c", 64)
	}
	if configurationFault == "late_owner" {
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
		if fault == "replacement_pod" {
			currentSignature = replacementSignature
		}
		if fault == "pending_owner" {
			currentSignature = ""
		}
		assignmentValue := gpuUUID + ",NVIDIA," + workloadBlocks + ",25:"
		if fault == "scheduler_memory" {
			assignmentValue = gpuUUID + ",NVIDIA,7168,25:"
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
			response = map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"uid": nodeUID, "name": "replay-node", "resourceVersion": "1", "labels": map[string]string{biz.GroupLabel: g.GroupID, biz.ModelLabel: g.ModelKey, biz.BaselineLabel: b.BaselineID, "kubernetes.io/hostname": "replay-node"}, "annotations": map[string]string{NodeRegister: gpuUUID + ",4," + registeredBlocks + ",NVIDIA-Test,true,hami-core:" + gpuUUID2 + ",4," + registeredBlocks + ",NVIDIA-Test,true,hami-core:", NodeHandshake: "Reported " + now.Format("2006-01-02 15:04:05.999999999 -0700 MST")}}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True", "lastHeartbeatTime": now.Format(time.RFC3339Nano)}}}}}}
			if fault == "registration" {
				response.(map[string]any)["items"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]string)[NodeRegister] = "invalid-registration"
			}
			status := response.(map[string]any)["items"].([]any)[0].(map[string]any)["status"].(map[string]any)
			if fault != "allocatable" {
				status["allocatable"] = map[string]string{"volcano.sh/vgpu-number": "8", "volcano.sh/vgpu-memory": "49152", "volcano.sh/vgpu-cores": "200"}
			}
			if fault == "allocatable_zero" {
				status["allocatable"].(map[string]string)["volcano.sh/vgpu-cores"] = "0"
			}
			if fault == "allocatable_invalid" {
				status["allocatable"].(map[string]string)["volcano.sh/vgpu-memory"] = "bad"
			}
			if fault == "node_rv" {
				response.(map[string]any)["items"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["resourceVersion"] = "2"
			}
			if configurationFault == "revision_continuity" {
				payload, _ := json.Marshal(response.(map[string]any)["items"].([]any)[0])
				var second map[string]any
				_ = json.Unmarshal(payload, &second)
				meta := second["metadata"].(map[string]any)
				meta["uid"], meta["name"] = nodeUID2, "replay-node-2"
				meta["annotations"].(map[string]any)[NodeRegister] = gpuUUID3 + ",4,24576,NVIDIA-Test,true,hami-core:"
				response.(map[string]any)["items"] = append(response.(map[string]any)["items"].([]any), second)
			}

		case r.URL.Path == "/apis/node.k8s.io/v1/runtimeclasses/nvidia":
			if fault == "runtimeclass_missing" {
				http.NotFound(w, r)
				return
			}
			meta := map[string]any{"name": "nvidia", "uid": runtimeClassUID, "resourceVersion": "1"}
			rc := map[string]any{"metadata": meta, "handler": "nvidia"}
			selector := map[string]string{}
			if configurationFault == "runtimeclass_selector" {
				selector["kubernetes.io/hostname"] = "replay-node"
			}
			scheduling := map[string]any{"nodeSelector": selector}
			rc["scheduling"] = scheduling
			switch fault {
			case "runtimeclass_name":
				meta["name"] = "replacement"
			case "runtimeclass_uid":
				meta["uid"] = ""
			case "runtimeclass_rv":
				delete(meta, "resourceVersion")
			case "runtimeclass_overhead":
				rc["overhead"] = map[string]any{"podFixed": map[string]string{"cpu": "1"}}
			case "runtimeclass_toleration":
				scheduling["tolerations"] = []any{map[string]string{"operator": "Exists"}}
			case "runtimeclass_unknown_selector":
				selector["untrusted.example/placement"] = "any"
			case "runtimeclass_identity_conflict":
				selector[biz.GroupLabel] = biz.NewID()
			case "runtimeclass_bad_selector":
				selector["kubernetes.io/hostname"] = "bad/value"
			case "runtimeclass_wrong_node":
				selector["kubernetes.io/hostname"] = "foreign-node"
			}
			response = rc
		case r.URL.Path == "/api/v1/pods":
			items := []any{}
			items = append(items, map[string]any{"metadata": map[string]any{"uid": "pending-1", "name": "pending", "namespace": "replay"}, "spec": map[string]any{"nodeSelector": map[string]string{biz.GroupLabel: g.GroupID}, "containers": []any{map[string]any{"name": "inference", "resources": map[string]any{"limits": map[string]string{"volcano.sh/vgpu-number": "1"}}}}}, "status": map[string]string{"phase": "Pending"}})
			if !deleted {
				annotations := map[string]string{AssignedIDs: assignmentValue, AssignedTime: strconv.FormatInt(assignmentTime.Unix(), 10), "accelerator.ani.io/execution-digest": executionDigest, "accelerator.ani.io/owner-signature": currentSignature}
				if configurationFault == "factor10" && fault != "missing_factor_annotation" {
					annotations["accelerator.ani.io/memory-block-mib"] = liveFactor
				}
				if fault == "factor_annotation_drift" {
					annotations["accelerator.ani.io/memory-block-mib"] = "1"
				}
				items = append(items, map[string]any{"metadata": map[string]any{"uid": podUID, "name": "workload", "namespace": "replay", "resourceVersion": "1", "labels": map[string]string{"accelerator.ani.io/resource-tenant-id": ref.TenantID, "accelerator.ani.io/owner-service": ref.OwnerService, "accelerator.ani.io/resource-id": ref.ResourceID, "accelerator.ani.io/create-operation-id": ref.CreateOperationID}, "annotations": annotations}, "spec": map[string]any{"nodeName": "replay-node", "containers": []any{map[string]any{"name": "inference", "resources": map[string]any{"limits": map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": workloadBlocks}}}}}})
			}
			response = map[string]any{"items": items}
			if fault == "replacement_pod" {
				for _, item := range items {
					metadata := item.(map[string]any)["metadata"].(map[string]any)
					if metadata["uid"] == podUID {
						metadata["uid"] = replacementPodUID
					}
				}
			}
		case strings.Contains(r.URL.Path, "/configmaps/"):
			response = cm
			if fault == "scheduler_config" && strings.HasSuffix(r.URL.Path, "/scheduler") {
				changed := map[string]string{}
				for key, value := range cmData {
					changed[key] = value
				}
				changed["configuration-test-drift"] = "changed configuration"
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
				args = []string{"--gpu-memory-factor=" + liveFactor, "--device-split-count=4"}
				if fault == "factor" {
					args = []string{"--gpu-memory-factor=256", "--device-split-count=4"}
				}
			}
			response = map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": containerName}, "spec": map[string]any{"nodeName": "replay-node", "containers": []any{map[string]any{"name": containerName, "args": args}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": containerName, "ready": true, "imageID": image}}}}}}
			if configurationFault == "revision_continuity" && containerName == "plugin" {
				payload, _ := json.Marshal(response.(map[string]any)["items"].([]any)[0])
				var second map[string]any
				_ = json.Unmarshal(payload, &second)
				second["spec"].(map[string]any)["nodeName"] = "replay-node-2"
				response.(map[string]any)["items"] = append(response.(map[string]any)["items"].([]any), second)
			}
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
	source := NodeSource{nodeUID, "replay-node", prSocket, criSocket}
	config := Connection{Ref: cluster.ConnectionRef, APIServer: api.URL, ServerName: "accelerator.test", CAFile: pki.CAFile, CertFile: cert, KeyFile: key, PluginNamespace: "system", PluginSelector: "app=plugin", PluginContainer: "plugin", PluginConfigMap: "plugin", SchedulerNamespace: "system", SchedulerSelector: "app=scheduler", SchedulerContainer: "scheduler", SchedulerConfigMap: "scheduler"}
	if configurationFault == "discovery_contract" {
		config.InstallationBaselines = []InstallationBaseline{{Mode: g.Mode, Baseline: g.Baseline}}
	}
	adapter, err := New(Config{Connections: []Connection{config}}, ownerPub, db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	telemetry := newNodeTelemetryFixture(t, db, cluster.ConnectionRef, source, replayInventory{})
	telemetries := []*nodeTelemetryFixture{telemetry}
	if configurationFault == "revision_continuity" {
		idle := &replayState{absent: true, fault: "pending_owner"}
		idleResources := unixServer(t, "idle-pr", func(server *grpc.Server) { pr.RegisterPodResourcesListerServer(server, &replayResources{state: idle}) })
		idleRuntime := unixServer(t, "idle-cri", func(server *grpc.Server) { cri.RegisterRuntimeServiceServer(server, &replayRuntime{state: idle}) })
		telemetries = append(telemetries, newNodeTelemetryFixture(t, db, cluster.ConnectionRef, NodeSource{nodeUID2, "replay-node-2", idleResources, idleRuntime}, fixedTelemetryInventory{{UUID: gpuUUID3, Model: "NVIDIA-Test", Bytes: 24576 << 20}}))
	}
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
	for _, fixture := range telemetries {
		fixture.refresh(t)
	}
	inspection, err := adapter.Inspect(ctx, cluster, g)
	if err != nil || len(inspection.Nodes) != len(g.Nodes) {
		t.Fatal(inspection, err)
	}
	rebuilt := g
	rebuilt.Nodes = []biz.NodeRef{{UID: "replacement-node-uid", Name: g.Nodes[0].Name}}
	if _, err := adapter.Inspect(ctx, cluster, rebuilt); err == nil {
		t.Fatal("node name inherited old UID identity")
	}
	collectBatch := func(groups []biz.SupplyGroup) (biz.ObservationBatch, error) {
		for _, fixture := range telemetries {
			fixture.refresh(t)
		}
		return adapter.Collect(ctx, cluster, groups)
	}
	collect := func() {
		t.Helper()
		batch, err := collectBatch([]biz.SupplyGroup{g})
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
			t.Fatal(err)
		}
	}
	collect()
	if configurationFault == "factor10" {
		providerFactor10Contract(t, db, adapter, cluster, g, profile, podRef, state, telemetry)
		return
	}
	if configurationFault == "discovery_contract" {
		providerDiscoveryContract(t, db, adapter, cluster, g, state, telemetry)
		return
	}
	if configurationFault == "revision_continuity" {
		providerRevisionContinuity(t, db, adapter, cluster, g, ref, podRef, state, telemetries)
		return
	}
	if configurationFault == "historical_group" {
		// Maintenance retains the old group's immutable node/baseline history.
		// Current node labels identify the successor regardless of catalog order.
		historical := g
		historical.GroupID = biz.NewID()
		oldBaseline := *b
		oldBaseline.BaselineID = biz.NewID()
		oldBaseline.SplitCount = 1
		oldBaseline.BaselineDigest, _ = biz.BaselineDigest(&oldBaseline)
		historical.Baseline = &oldBaseline
		historical.Mode = biz.Whole
		for _, groups := range [][]biz.SupplyGroup{{g, historical}, {historical, g}} {
			batch, err := collectBatch(groups)
			if err != nil || len(batch.Devices) != 2 {
				t.Fatalf("current supply collection failed: %+v error=%v", batch, err)
			}
			for _, device := range batch.Devices {
				if device.Device.GroupID != g.GroupID || device.BaselineDigest != b.BaselineDigest || !device.Device.Observation.Complete {
					t.Fatalf("historical group replaced label-selected supply: %+v", device)
				}
			}
			if err := db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
				t.Fatal(err)
			}
		}
		batch, err := collectBatch([]biz.SupplyGroup{historical})
		if err != nil || len(batch.Devices) != 2 {
			t.Fatalf("unmatched assignment must invalidate cached supply: %+v error=%v", batch, err)
		}
		for _, device := range batch.Devices {
			if device.Device.Observation.Complete || device.Device.GroupID != g.GroupID {
				t.Fatalf("unmatched labels exposed a historical supply as current: %+v", device)
			}
		}
		state.mu.Lock()
		state.exited, state.absent, state.apiDeleted = true, true, true
		state.mu.Unlock()
		batch, err = collectBatch([]biz.SupplyGroup{historical})
		if err != nil || len(batch.Releases) != 1 {
			t.Fatalf("historical membership selection blocked real physical release: %+v error=%v", batch, err)
		}
		return
	}
	if configurationFault == "late_owner" {
		var first biz.Claim
		if err := db.View(ctx, func(r biz.Reader) error {
			claims, _, err := r.PodEvidence(ctx, podRef)
			if err != nil {
				return err
			}
			if len(claims) != 1 || claims[0].Source != "scheduler" || claims[0].OwnerRef != nil || claims[0].ExecutionDigest != "" {
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
		projection := biz.UsageProjection{Ref: ref, Revision: 1, State: biz.Declared, ProfileID: profile.ProfileID, ProfileVersion: profile.ProfileVersion, GroupID: g.GroupID, BaselineID: b.BaselineID, ClusterID: cluster.ClusterID, ContainerName: "inference", ExecutionDigest: executionDigest, SourceOperationCreatedAt: createdAt.Format(time.RFC3339Nano), SourceFactRef: "quota-operation:" + ref.CreateOperationID + ":accepted"}
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
				} else if claim.OwnerRef == nil || *claim.OwnerRef != *ref || claim.ExecutionDigest != executionDigest {
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
		changed, err := collectBatch([]biz.SupplyGroup{g})
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
	u, err := biz.NewUsecase(db, adapter, codec, []string{cluster.ConnectionRef}, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if configurationFault == "runtimeclass_selector" {
		actor := biz.Actor{Type: "user", ID: "runtimeclass-test"}
		scoped := biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Actor: actor, Action: "ResolveGpuRequest", TenantID: ref.TenantID})
		request := &biz.GpuRequest{ClusterID: cluster.ClusterID, PoolID: pool.PoolID, ProfileID: profile.ProfileID, ProfileVersion: 1, Replicas: 1, DevicesPerReplica: 1, ContainerName: "node"}
		tc := &biz.TenantContext{RequestID: "runtimeclass", TenantID: ref.TenantID, Actor: &actor}
		resolved, err := u.Resolve(scoped, tc, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(resolved.Execution.NodeSelector) != 4 || resolved.Execution.NodeSelector["kubernetes.io/hostname"] != "replay-node" {
			t.Fatalf("actual RuntimeClass placement missing from frozen execution: %+v", resolved.Execution)
		}
		before, _ := biz.Canonical(resolved)
		for _, fault := range []string{"runtimeclass_missing", "runtimeclass_name", "runtimeclass_uid", "runtimeclass_rv", "runtimeclass_overhead", "runtimeclass_toleration", "runtimeclass_unknown_selector", "runtimeclass_identity_conflict", "runtimeclass_bad_selector", "runtimeclass_wrong_node"} {
			state.setFault(fault)
			if _, err := u.Resolve(scoped, tc, request); err == nil {
				t.Fatalf("accepted unsupported/drifted RuntimeClass %s", fault)
			}
			after, _ := biz.Canonical(resolved)
			if string(before) != string(after) {
				t.Fatal("previous frozen execution changed")
			}
		}
		return
	}
	actor := biz.Actor{Type: "user", ID: "replay"}
	admin := biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Actor: actor, Action: "AdminGetCapacity"})
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
	if configurationFault == "restart_release" {
		providerRestartReleaseContract(t, db, adapter, cluster, g, podRef, replacementPodUID, state, telemetry, capacity)
		return
	}
	if configurationFault == "runtime_env_order" {
		providerRuntimeEnvironmentOrderContract(t, db, adapter, cluster, g, podRef, capacity)
		return
	}
	owner := biz.WithCaller(ctx, biz.Caller{Service: "ani-inference", Action: "ObserveRelease", TenantID: ref.TenantID})
	if configurationFault == "session_cache" {
		providerSessionCacheContract(t, db, adapter, cluster, g, u, owner, ref, podRef, state, telemetry, capacity)
		return
	}
	if configurationFault != "" {
		configurationObservation(t, configurationFault, adapter, telemetry, state, cluster, g, db, u, owner, ref, podRef, capacity)
		return
	}
	faults := []string{"stale", "offline", "factor"}
	if schedulerVersion == "v1.15.2" || schedulerVersion == "v1.16.0" {
		faults = append(faults, "allocatable", "allocatable_zero", "allocatable_invalid")
	}
	for _, fault := range faults {
		state.setFault(fault)
		telemetry.refresh(t)
		if _, err := adapter.Inspect(ctx, cluster, g); err == nil {
			t.Fatal("inspection accepted", fault)
		}
		collect()
		if capacity().Observation.Complete {
			t.Fatal("bad source complete", fault)
		}
		observation, err := u.ObserveRelease(owner, "replay", ref, biz.NewID(), cluster.ClusterID, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), []biz.PodRef{podRef})
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
	conflicted, err := u.ObserveRelease(owner, "replay", ref, biz.NewID(), cluster.ClusterID, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), []biz.PodRef{podRef})
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
	adapter, err = New(Config{Connections: []Connection{config}}, ownerPub, db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.runtimeFailed = false
	state.apiDeleted = true
	state.mu.Unlock()
	collect()
	release, err := u.ObserveRelease(owner, "replay", ref, biz.NewID(), cluster.ClusterID, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), []biz.PodRef{podRef})
	if err != nil || release.Finding == biz.AllocationReleased {
		t.Fatal("API deletion falsely released", release, err)
	}
	state.mu.Lock()
	state.exited = true
	state.absent = true
	state.mu.Unlock()
	collect()
	release, err = u.ObserveRelease(owner, "replay", ref, biz.NewID(), cluster.ClusterID, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), []biz.PodRef{podRef})
	if err != nil || release.Finding != biz.AllocationReleased {
		_ = db.View(ctx, func(reader biz.Reader) error {
			claims, proofs, evidenceError := reader.PodEvidence(ctx, podRef)
			watermarks, watermarkError := reader.SourceHealth(ctx, cluster.ClusterID)
			bindings, bindingError := reader.Bindings(ctx, biz.BindingFilter{ClusterID: cluster.ClusterID, Pod: &podRef}, "", 201)
			t.Logf("release failure evidence claims=%+v proofs=%+v watermarks=%+v bindings=%+v errors=%v/%v/%v", claims, proofs, watermarks, bindings, evidenceError, watermarkError, bindingError)
			return nil
		})
		t.Fatal(release, err)
	}
	if capacity().KnownFreeUnits != 8 {
		t.Fatal("positive release failed to restore capacity")
	}
	t.Log("OBS-01..06 CAP-01..04 REL-01..04: external HTTP/TLS + Unix readers, durable node telemetry and real PG domain collection/release; no actual GPU sample")
}
