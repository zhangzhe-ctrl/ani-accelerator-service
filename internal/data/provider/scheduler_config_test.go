package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/schedulerconfig"
)

// The external Kubernetes HTTP source is the only substitute. Baseline
// production and the real provider reader use the same public projection.
func TestSchedulerConfigBaselineAndReaderUseOneDigest(t *testing.T) {
	for _, version := range []string{"v1.12.0", "v1.15.2", "v1.16.0"} {
		t.Run(version, func(t *testing.T) {
			document := "actions: enqueue,allocate\ntiers:\n- plugins:\n  - name: deviceshare\n    arguments:\n      deviceshare.VGPUEnable: true\n  - name: nodeorder\n    arguments:\n      podaffinity.weight: 1\n"
			data := map[string]string{schedulerconfig.DataKey: document, "protected": "encoding=v1"}
			binary := map[string]string{"protected": "AQ=="}
			baseline, err := schedulerconfig.ConfigMapDigest(version, data, binary)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/namespaces/system/configmaps/scheduler" {
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "binaryData": binary, "metadata": map[string]string{"resourceVersion": "99"}})
			}))
			defer server.Close()
			reader := &kubeReader{config: Connection{APIServer: server.URL}, client: server.Client()}
			read := func() string {
				t.Helper()
				got, err := reader.schedulerConfigDigest(context.Background(), "system", "scheduler", version)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			if read() != baseline {
				t.Fatal("baseline producer/reader differ")
			}
			oldWhole, err := biz.Hash(struct {
				Data       map[string]string `json:"data"`
				BinaryData map[string]string `json:"binaryData"`
			}{data, binary})
			if err != nil || oldWhole == baseline {
				t.Fatal("old whole-ConfigMap digest was accepted")
			}
			for _, value := range []string{"0", "-1", "9"} {
				data[schedulerconfig.DataKey] = strings.Replace(document, "podaffinity.weight: 1", "podaffinity.weight: "+value, 1)
				if read() != baseline {
					t.Fatal("safe scoring change caused config drift")
				}
			}
			data[schedulerconfig.DataKey] = strings.Replace(document, "VGPUEnable: true", "VGPUEnable: false", 1)
			if read() == baseline {
				t.Fatal("GPU mode drift was ignored")
			}
			data[schedulerconfig.DataKey] = document
			binary["protected"] = "Ag=="
			if read() == baseline {
				t.Fatal("binary data drift was ignored")
			}
			data[schedulerconfig.DataKey] = "tiers: ["
			if _, err := reader.schedulerConfigDigest(context.Background(), "system", "scheduler", version); err == nil {
				t.Fatal("invalid scheduler config was accepted")
			}
		})
	}
}
