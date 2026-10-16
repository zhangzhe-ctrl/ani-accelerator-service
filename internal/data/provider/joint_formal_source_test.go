package provider

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/schedulerconfig"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
)

// Supplies only a TLS-authenticated Kubernetes source shape. The production
// reader and production consumption verifier remain untouched. It never issues
// hardware attestation, marks a supply VERIFIED, or writes a profile.
func TestJointFormalProviderSource(t *testing.T) {
	root := os.Getenv("ACC_FORMAL_SOURCE_DIR")
	if root == "" {
		t.Skip("explicit A-layer source process")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("absolute task directory required")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	pki := testkit.NewPKI(t)
	cert, key := pki.Issue(t, "source-server", "", true)
	clientCert, clientKey := pki.Issue(t, "source-client", "spiffe://ani.internal/test/source-reader", false)
	cmData := map[string]string{"config.yaml": "constructed-source-fixture: true", schedulerconfig.DataKey: "actions: enqueue,allocate\ntiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.VGPUEnable: true\n"}
	cm := map[string]any{"data": cmData, "binaryData": nil}
	digest, _ := biz.Hash(cm)
	schedulerDigest, err := schedulerconfig.ConfigMapDigest("v1.12.0", cmData, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseline := &biz.ProviderBaseline{BaselineID: biz.NewID(), AdapterID: "volcano-hami-factor-v1", VolcanoVersion: "v1.12.0", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "controlled-formal-source", MemoryBlockMiB: 1024, FactorEvidenceRef: "formal:factor", MemoryEncoding: "VOLCANO_BLOCKS_TO_MIB", SplitCount: 4, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: digest, SchedulerConfigDigest: schedulerDigest, RecipeVersion: "volcano-hami-v1"}
	baseline.BaselineDigest, _ = biz.BaselineDigest(baseline)
	node := biz.NodeRef{UID: "formal-node-" + biz.NewID(), Name: "formal-source-node"}
	groupFile := filepath.Join(root, "group-id")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		group, _ := os.ReadFile(groupFile)
		var response any
		switch {
		case r.URL.Path == "/api/v1/nodes":
			response = map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"uid": node.UID, "name": node.Name, "resourceVersion": "1", "labels": map[string]string{biz.GroupLabel: strings.TrimSpace(string(group)), biz.ModelLabel: "nvidia-test", biz.BaselineLabel: baseline.BaselineID}, "annotations": map[string]string{NodeRegister: gpuUUID + ",4,24,NVIDIA-Test,true,hami-core:", NodeHandshake: "Reported " + now.Format("2006-01-02 15:04:05.999999999 -0700 MST")}}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True", "lastHeartbeatTime": now.Format(time.RFC3339Nano)}}}}}}
		case r.URL.Path == "/api/v1/pods":
			response = map[string]any{"items": []any{}}
		case strings.Contains(r.URL.Path, "/configmaps/"):
			response = cm
		case r.URL.Path == "/apis/scheduling.volcano.sh/v1beta1/queues/gpu":
			response = map[string]any{"metadata": map[string]any{"name": "gpu"}, "status": map[string]any{"state": "Open"}}
		case r.URL.Path == "/api/v1/namespaces/system/pods":
			name, image, args := "scheduler", baseline.VolcanoImageDigest, []string{}
			if r.URL.Query().Get("labelSelector") == "app=plugin" {
				name, image, args = "plugin", baseline.DevicePluginImageDigest, []string{"--gpu-memory-factor=1024", "--device-split-count=4"}
			}
			response = map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": name}, "spec": map[string]any{"nodeName": node.Name, "containers": []any{map[string]any{"name": name, "args": args}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": name, "ready": true, "imageID": image}}}}}}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
	api := httptest.NewUnstartedServer(handler)
	tc, e := tlsKeyPair(cert, key)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(pki.CACert)
	tc.ClientCAs = roots
	tc.ClientAuth = tls.RequireAndVerifyClientCert
	api.TLS = tc
	api.StartTLS()
	defer api.Close()
	connection := Connection{Ref: "formal-source:" + biz.NewID(), APIServer: api.URL, ServerName: "accelerator.test", CAFile: pki.CAFile, CertFile: clientCert, KeyFile: clientKey, PluginNamespace: "system", PluginSelector: "app=plugin", PluginContainer: "plugin", PluginConfigMap: "plugin", SchedulerNamespace: "system", SchedulerSelector: "app=scheduler", SchedulerContainer: "scheduler", SchedulerConfigMap: "scheduler"}
	configPath := filepath.Join(root, "provider.json")
	payload, e := json.Marshal(Config{Connections: []Connection{connection}})
	if e != nil {
		t.Fatal(e)
	}
	testkit.Write(t, configPath, payload)
	payload, e = json.MarshalIndent(map[string]any{"provider_config_file": configPath, "connection_ref": connection.Ref, "baseline": baseline, "node": node, "model_key": "nvidia-test", "queue_name": "gpu", "group_id_file": groupFile, "scope": "constructed A-layer TLS source only; no hardware attestation"}, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	testkit.Write(t, filepath.Join(root, "source.json"), payload)
	t.Log("formal source ready; production Publish must reach CONSUMPTION_NOT_VERIFIED after writing adopted group ID to group-id")
	for {
		if _, e = os.Stat(filepath.Join(root, "stop")); e == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
}
