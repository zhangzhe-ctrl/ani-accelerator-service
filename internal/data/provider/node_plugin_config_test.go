package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/schedulerconfig"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

func TestNodePluginConfigMapsKeepIndependentGroups(t *testing.T) {
	f := newNodePluginFixture(t)
	adapter := f.provider(t, f.references)
	for _, group := range f.groups {
		inspection, err := adapter.Inspect(context.Background(), f.cluster, group)
		if err != nil {
			t.Fatalf("%v group must retain its own frozen plugin configuration: %v", group.Mode, err)
		}
		if len(inspection.Nodes) != 1 || inspection.Nodes[0].ConfigDigest != group.Baseline.NodeConfigDigest || inspection.Nodes[0].SplitCount != group.Baseline.SplitCount {
			t.Fatalf("wrong per-node configuration evidence: %+v", inspection)
		}
	}
	discovery, err := adapter.DiscoverInitialization(context.Background())
	if err != nil || len(discovery.Nodes) != 2 {
		t.Fatalf("independent nodes were not discovered: %+v, %v", discovery, err)
	}
	for _, observed := range discovery.Nodes {
		var expected biz.SupplyGroup
		for _, group := range f.groups {
			if group.Nodes[0].UID == observed.Node.UID {
				expected = group
			}
		}
		if len(observed.Baselines) != 1 || observed.Baselines[0].BaselineRef != expected.Baseline.BaselineID+":"+expected.Baseline.BaselineDigest {
			t.Fatalf("node inherited another node's plugin configuration: %+v", observed)
		}
	}
}

func TestNodePluginConfigMapsRejectSplitEvidence(t *testing.T) {
	f := newNodePluginFixture(t)
	f.fault = "split_evidence"
	adapter := f.provider(t, f.references)
	if _, err := adapter.Inspect(context.Background(), f.cluster, f.groups[1]); err == nil {
		t.Fatal("inspection combined configuration and unit evidence from different plugin Pods")
	}
	discovery, err := adapter.DiscoverInitialization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, observed := range discovery.Nodes {
		if observed.Node.UID == f.groups[1].Nodes[0].UID && len(observed.Baselines) != 0 {
			t.Fatalf("discovery combined configuration and unit evidence from different plugin Pods: %+v", observed)
		}
	}
}

func TestNodePluginConfigMapsRejectMissingFile(t *testing.T) {
	f := newNodePluginFixture(t)
	f.fault = "missing_config_file"
	// Even a frozen digest of this real ConfigMap cannot prove config.json is
	// consumed when that key does not exist in the mounted source.
	f.groups[1].Baseline.NodeConfigDigest, _ = biz.Hash(map[string]any{"data": map[string]string{"other.json": "{}"}, "binaryData": nil})
	f.groups[1].Baseline.BaselineDigest, _ = biz.BaselineDigest(f.groups[1].Baseline)
	adapter := f.provider(t, f.references)
	if _, err := adapter.Inspect(context.Background(), f.cluster, f.groups[1]); err == nil {
		t.Fatal("mounted ConfigMap without config.json was accepted")
	}
	f.assertSharedUnavailable(t, adapter)
}

func TestNodePluginConfigMapsRejectUnboundEvidence(t *testing.T) {
	for _, fault := range []string{"wrong_node_uid", "unknown_node", "wrong_mount", "missing_mount", "wrong_config_key", "wrong_container", "shadowed_file", "wrong_plugin_node", "config_drift", "config_unknown", "different_group_configs"} {
		t.Run(fault, func(t *testing.T) {
			f := newNodePluginFixture(t)
			f.fault = fault
			references := []map[string]string{{"node_uid": f.references[0]["node_uid"], "node_name": "shared-node", "config_map": "plugin-shared"}}
			if fault == "wrong_node_uid" || fault == "unknown_node" {
				references[0]["node_uid"] = biz.NewID()
			}
			if fault == "unknown_node" {
				references[0]["node_name"] = "foreign-node"
			}
			adapter := f.provider(t, references)
			group := f.groups[1]
			if fault == "different_group_configs" {
				group.Nodes = append(group.Nodes, f.groups[0].Nodes[0])
			}
			if _, err := adapter.Inspect(context.Background(), f.cluster, group); err == nil {
				t.Fatal("inspection accepted unbound plugin configuration evidence")
			}
			if fault == "different_group_configs" {
				return
			}
			if fault == "wrong_node_uid" || fault == "unknown_node" {
				if _, err := adapter.DiscoverInitialization(context.Background()); err == nil {
					t.Fatal("discovery accepted an unknown managed Node identity")
				}
				return
			}
			f.assertSharedUnavailable(t, adapter)
		})
	}
}

func (f *nodePluginFixture) assertSharedUnavailable(t *testing.T, adapter *Provider) {
	t.Helper()
	discovery, err := adapter.DiscoverInitialization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, observed := range discovery.Nodes {
		if observed.Node.UID != f.groups[1].Nodes[0].UID {
			continue
		}
		if len(observed.Baselines) != 0 || observed.Selectable || len(observed.Sources) != 3 {
			t.Fatalf("unbound configuration produced usable supply: %+v", observed)
		}
		for _, source := range observed.Sources {
			if !source.Complete {
				t.Fatalf("configuration failure corrupted independent Agent source health: %+v", source)
			}
		}
	}
}

func TestNodePluginConfigMapsStrictManagedConfiguration(t *testing.T) {
	uid := biz.NewID()
	for _, test := range []struct {
		name       string
		references []map[string]string
		legacy     string
		valid      bool
	}{
		{name: "default_unchanged", valid: true},
		{name: "managed_reference", references: []map[string]string{{"node_uid": uid, "node_name": "node3", "config_map": "plugin-shared"}}, valid: true},
		{name: "malformed_reference", references: []map[string]string{{"node_uid": uid, "node_name": "node3", "config_map": "system/plugin-shared"}}},
		{name: "missing_identity", references: []map[string]string{{"node_name": "node3", "config_map": "plugin-shared"}}},
		{name: "duplicate_uid", references: []map[string]string{{"node_uid": uid, "node_name": "node3", "config_map": "plugin-shared"}, {"node_uid": uid, "node_name": "node4", "config_map": "plugin-shared"}}},
		{name: "duplicate_name", references: []map[string]string{{"node_uid": uid, "node_name": "node3", "config_map": "plugin-shared"}, {"node_uid": biz.NewID(), "node_name": "node3", "config_map": "plugin-shared"}}},
		{name: "unknown_field", references: []map[string]string{{"node_uid": uid, "node_name": "node3", "config_map": "plugin-shared", "unexpected": "value"}}},
		{name: "cri_socket_forbidden", references: []map[string]string{{"node_uid": uid, "node_name": "node3", "config_map": "plugin-shared", "cri_socket": "/run/containerd/containerd.sock"}}},
		{name: "podresources_socket_forbidden", references: []map[string]string{{"node_uid": uid, "node_name": "node3", "config_map": "plugin-shared", "podresources_socket": "/run/kubelet/pod-resources/kubelet.sock"}}},
		{name: "legacy_nodes_forbidden", legacy: "nodes"},
		{name: "legacy_local_node_forbidden", legacy: "local_node_uid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection := map[string]any{"ref": "node-config:test", "api_server": "https://cluster.example:6443", "server_name": "cluster.example", "ca_file": "/run/ca.pem", "plugin_namespace": "system", "plugin_selector": "app=plugin", "plugin_container": "plugin", "plugin_config_map": "plugin-whole", "scheduler_namespace": "system", "scheduler_selector": "app=scheduler", "scheduler_container": "scheduler", "scheduler_config_map": "scheduler", "node_plugin_config_maps": test.references}
			if test.legacy == "nodes" {
				connection["nodes"] = []map[string]string{{"uid": uid, "name": "node3", "cri_socket": "/legacy"}}
			} else if test.legacy != "" {
				connection[test.legacy] = uid
			}
			payload, _ := json.Marshal(map[string]any{"connections": []any{connection}})
			path := filepath.Join(t.TempDir(), "provider.json")
			testkit.Write(t, path, payload)
			_, err := LoadConfig(path)
			if (err == nil) != test.valid {
				t.Fatalf("unexpected managed configuration result: %v", err)
			}
			if test.legacy != "" && err.Error() != "PROVIDER_LOCAL_COLLECTION_REMOVED" {
				t.Fatalf("legacy collector rejection changed: %v", err)
			}
		})
	}
}

type nodePluginFixture struct {
	mu         sync.Mutex
	fault      string
	connection Connection
	references []map[string]string
	cluster    biz.Cluster
	groups     []biz.SupplyGroup
	database   *data.Postgres
}

// Only external Kubernetes, NVML and node socket boundaries are constructed.
// Inspection and discovery read production PostgreSQL-backed node telemetry.
func newNodePluginFixture(t *testing.T) *nodePluginFixture {
	t.Helper()
	if os.Getenv("ACC_TEST_DSN_FILE") == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	database, err := data.OpenPostgres(context.Background(), os.Getenv("ACC_TEST_DSN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	f := &nodePluginFixture{database: database, cluster: biz.Cluster{ClusterID: biz.NewID(), ConnectionRef: "node-config:" + biz.NewID()}}
	configs := []map[string]string{{"config.json": "{\"deviceSplitCount\":1}"}, {"config.json": "{\"deviceSplitCount\":2}"}}
	schedulerData := map[string]string{schedulerconfig.DataKey: "actions: enqueue,allocate\ntiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.VGPUEnable: true\n"}
	schedulerDigest, err := schedulerconfig.ConfigMapDigest("v1.15.2", schedulerData, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index, mode := range []biz.SupplyMode{biz.Whole, biz.Shared} {
		digest, _ := biz.Hash(map[string]any{"data": configs[index], "binaryData": nil})
		baseline := &biz.ProviderBaseline{BaselineID: biz.NewID(), AdapterID: "volcano-hami-mib-v1", VolcanoVersion: "v1.15.2", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "controlled-node-config", MemoryBlockMiB: 10, MemoryEncoding: "MIB", SplitCount: uint32(index + 1), MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: digest, SchedulerConfigDigest: schedulerDigest, RecipeVersion: "volcano-hami-mib-v1", RuntimeClassName: "nvidia"}
		baseline.BaselineDigest, _ = biz.BaselineDigest(baseline)
		f.groups = append(f.groups, biz.SupplyGroup{GroupID: biz.NewID(), ClusterID: f.cluster.ClusterID, Mode: mode, ModelKey: "nvidia-test", Nodes: []biz.NodeRef{{UID: biz.NewID(), Name: []string{"whole-node", "shared-node"}[index]}}, Baseline: baseline, QueueName: "gpu"})
	}
	f.references = []map[string]string{{"node_uid": f.groups[1].Nodes[0].UID, "node_name": "shared-node", "config_map": "plugin-shared"}}
	pki := testkit.NewPKI(t)
	cert, key := pki.Issue(t, "node-config-source", "", true)
	clientCert, clientKey := pki.Issue(t, "node-config-reader", "node-config-reader.test", false)
	api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		fault := f.fault
		f.mu.Unlock()
		var response any
		switch r.URL.Path {
		case "/api/v1/nodes":
			items := []any{}
			for index, group := range f.groups {
				node := group.Nodes[0]
				uuid := []string{gpuUUID, gpuUUID2}[index]
				now := time.Now().UTC()
				items = append(items, map[string]any{"metadata": map[string]any{"uid": node.UID, "name": node.Name, "annotations": map[string]string{NodeRegister: fmt.Sprintf("%s,%d,2457,NVIDIA-Test,true,hami-core:", uuid, group.Baseline.SplitCount), NodeHandshake: "Reported " + now.Format("2006-01-02 15:04:05.999999999 -0700 MST")}}, "status": map[string]any{"allocatable": map[string]string{"volcano.sh/vgpu-number": "2", "volcano.sh/vgpu-memory": "2457", "volcano.sh/vgpu-cores": "100"}, "conditions": []any{map[string]any{"type": "Ready", "status": "True", "lastHeartbeatTime": now.Format(time.RFC3339Nano)}}}})
			}
			response = map[string]any{"items": items}
		case "/api/v1/namespaces/system/configmaps/plugin-whole", "/api/v1/namespaces/system/configmaps/plugin-shared":
			index := 0
			if strings.HasSuffix(r.URL.Path, "plugin-shared") {
				index = 1
			}
			if fault == "config_unknown" && index == 1 {
				http.Error(w, "controlled source unavailable", http.StatusServiceUnavailable)
				return
			}
			values := configs[index]
			if fault == "config_drift" && index == 1 {
				values = map[string]string{"config.json": "{\"deviceSplitCount\":3}"}
			}
			if fault == "missing_config_file" && index == 1 {
				values = map[string]string{"other.json": "{}"}
			}
			response = map[string]any{"data": values, "binaryData": nil}
		case "/api/v1/namespaces/system/configmaps/scheduler":
			response = map[string]any{"data": schedulerData, "binaryData": nil}
		case "/api/v1/namespaces/system/pods":
			items := []any{}
			if r.URL.Query().Get("labelSelector") == "app=plugin" {
				for index, group := range f.groups {
					cm := []string{"plugin-whole", "plugin-shared"}[index]
					if (fault == "wrong_mount" || fault == "split_evidence") && index == 1 {
						cm = "plugin-whole"
					}
					items = append(items, map[string]any{"metadata": map[string]any{"name": "plugin-" + group.Nodes[0].Name}, "spec": map[string]any{"nodeName": group.Nodes[0].Name, "volumes": []any{map[string]any{"name": "node-config", "configMap": map[string]any{"name": cm}}}, "containers": []any{map[string]any{"name": "plugin", "args": []string{"--gpu-memory-factor=10", fmt.Sprintf("--device-split-count=%d", group.Baseline.SplitCount)}, "volumeMounts": []any{map[string]any{"name": "node-config", "mountPath": "/config", "readOnly": true}}}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "plugin", "ready": true, "imageID": group.Baseline.DevicePluginImageDigest}}}})
					if index == 1 {
						spec := items[len(items)-1].(map[string]any)["spec"].(map[string]any)
						main := spec["containers"].([]any)[0].(map[string]any)
						switch fault {
						case "missing_mount":
							delete(main, "volumeMounts")
						case "wrong_config_key":
							spec["volumes"].([]any)[0].(map[string]any)["configMap"].(map[string]any)["items"] = []any{map[string]any{"key": "config.json", "path": "other.json"}}
						case "wrong_container":
							spec["containers"] = append(spec["containers"].([]any), map[string]any{"name": "sidecar", "volumeMounts": main["volumeMounts"]})
							delete(main, "volumeMounts")
						case "shadowed_file":
							main["volumeMounts"] = append(main["volumeMounts"].([]any), map[string]any{"name": "foreign-config", "mountPath": "/config/config.json", "subPath": "config.json"})
							spec["volumes"] = append(spec["volumes"].([]any), map[string]any{"name": "foreign-config", "configMap": map[string]any{"name": "plugin-whole"}})
						case "wrong_plugin_node":
							spec["nodeName"] = "foreign-node"
						}
					}
					if fault == "split_evidence" && index == 1 {
						payload, _ := json.Marshal(items[len(items)-1])
						var second map[string]any
						_ = json.Unmarshal(payload, &second)
						spec := second["spec"].(map[string]any)
						spec["volumes"].([]any)[0].(map[string]any)["configMap"].(map[string]any)["name"] = "plugin-shared"
						spec["containers"].([]any)[0].(map[string]any)["args"] = []string{"--gpu-memory-factor=256", "--device-split-count=2"}
						items = append(items, second)
					}
				}
			} else {
				items = append(items, map[string]any{"metadata": map[string]any{"name": "scheduler"}, "spec": map[string]any{"containers": []any{map[string]any{"name": "scheduler"}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "scheduler", "ready": true, "imageID": f.groups[0].Baseline.VolcanoImageDigest}}}})
			}
			response = map[string]any{"items": items}
		case "/apis/scheduling.volcano.sh/v1beta1/queues/gpu":
			response = map[string]any{"metadata": map[string]any{"name": "gpu"}, "status": map[string]any{"state": "Open"}}
		case "/apis/node.k8s.io/v1/runtimeclasses/nvidia":
			response = map[string]any{"metadata": map[string]any{"name": "nvidia", "uid": "10000000-0000-4000-8000-000000000099", "resourceVersion": "1"}, "handler": "nvidia"}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	api.TLS, err = tlsKeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	api.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	api.TLS.ClientCAs = x509.NewCertPool()
	api.TLS.ClientCAs.AddCert(pki.CACert)
	api.StartTLS()
	t.Cleanup(api.Close)
	f.connection = Connection{Ref: f.cluster.ConnectionRef, APIServer: api.URL, ServerName: "accelerator.test", CAFile: pki.CAFile, CertFile: clientCert, KeyFile: clientKey, PluginNamespace: "system", PluginSelector: "app=plugin", PluginContainer: "plugin", PluginConfigMap: "plugin-whole", SchedulerNamespace: "system", SchedulerSelector: "app=scheduler", SchedulerContainer: "scheduler", SchedulerConfigMap: "scheduler"}
	for index, group := range f.groups {
		f.connection.InstallationBaselines = append(f.connection.InstallationBaselines, InstallationBaseline{Mode: group.Mode, Baseline: group.Baseline})
		state := &replayState{absent: true, exited: true}
		resources := unixServer(t, "node-config-pr", func(s *grpc.Server) { pr.RegisterPodResourcesListerServer(s, &replayResources{state: state}) })
		runtime := unixServer(t, "node-config-cri", func(s *grpc.Server) { cri.RegisterRuntimeServiceServer(s, &replayRuntime{state: state}) })
		fixture := newNodeTelemetryFixture(t, database, f.cluster.ConnectionRef, NodeSource{UID: group.Nodes[0].UID, Name: group.Nodes[0].Name, PodResourcesSocket: resources, CRISocket: runtime}, fixedTelemetryInventory{{UUID: []string{gpuUUID, gpuUUID2}[index], Model: "NVIDIA-Test", Bytes: 24576 << 20}})
		fixture.refresh(t)
	}
	return f
}

func (f *nodePluginFixture) provider(t *testing.T, references []map[string]string) *Provider {
	t.Helper()
	// JSON keeps this behavior regression executable against the prior adapter,
	// where the newly managed optional field was not yet represented in Go.
	payload, _ := json.Marshal(f.connection)
	var configured map[string]any
	_ = json.Unmarshal(payload, &configured)
	configured["node_plugin_config_maps"] = references
	payload, _ = json.Marshal(configured)
	var connection Connection
	if err := json.Unmarshal(payload, &connection); err != nil {
		t.Fatal(err)
	}
	adapter, err := New(Config{Connections: []Connection{connection}}, nil, f.database, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}
