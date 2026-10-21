package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestInitializationLabelsAreConditionalAndKeepPartialSuccess(t *testing.T) {
	for _, mode := range []string{"normal", "resource_version_retry", "wrong_uid", "foreign_label", "external_group_change"} {
		t.Run(mode, func(t *testing.T) {
			cluster := biz.Cluster{ClusterID: biz.NewID(), ConnectionRef: "local"}
			target := biz.NodeRef{UID: biz.NewID(), Name: "ani-03"}
			group := biz.SupplyGroup{GroupID: biz.NewID(), ClusterID: cluster.ClusterID, ModelKey: "tesla-t4", Baseline: &biz.ProviderBaseline{BaselineID: biz.NewID()}, Nodes: []biz.NodeRef{target}}
			labels := map[string]string{"example.com/existing": "preserved"}
			uid := target.UID
			if mode == "wrong_uid" {
				uid = biz.NewID()
			}
			if mode == "foreign_label" {
				labels[biz.GroupLabel] = biz.NewID()
			}
			resourceVersion := "10"
			patches := 0
			patched := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/api/v1/nodes/ani-03" {
					t.Errorf("unexpected path %s", request.URL.Path)
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				if request.Method == http.MethodGet {
					_ = json.NewEncoder(writer).Encode(map[string]any{"metadata": map[string]any{"uid": uid, "name": target.Name, "resourceVersion": resourceVersion, "labels": labels}, "spec": map[string]any{"unschedulable": true}})
					return
				}
				if request.Method != http.MethodPatch || request.Header.Get("Content-Type") != "application/json-patch+json" {
					t.Error("unexpected mutation")
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				patches++
				var operations []initializationLabelPatch
				if json.NewDecoder(request.Body).Decode(&operations) != nil || len(operations) != 5 {
					t.Error("expected two preconditions and exactly three label additions")
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				if operations[0].Operation != "test" || operations[0].Path != "/metadata/uid" || operations[0].Value != target.UID || operations[1].Operation != "test" || operations[1].Path != "/metadata/resourceVersion" || operations[1].Value != resourceVersion {
					t.Error("node identity/resourceVersion preconditions absent")
				}
				if mode == "resource_version_retry" && patches == 1 {
					resourceVersion = "11"
					writer.WriteHeader(http.StatusConflict)
					return
				}
				allowed := map[string]string{"/metadata/labels/accelerator.ani.io~1supply-group": biz.GroupLabel, "/metadata/labels/accelerator.ani.io~1model-key": biz.ModelLabel, "/metadata/labels/accelerator.ani.io~1baseline-id": biz.BaselineLabel}
				for _, operation := range operations[2:] {
					key, ok := allowed[operation.Path]
					if !ok || operation.Operation != "add" {
						t.Errorf("forbidden node mutation %s", operation.Path)
						continue
					}
					labels[key] = operation.Value.(string)
				}
				patched = true
				resourceVersion = "12"
				writer.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			provider := &Provider{connections: map[string]*kubeReader{"local": {config: Connection{APIServer: server.URL}, client: server.Client()}}}
			guard := func(context.Context) error {
				if mode == "external_group_change" && patched {
					return biz.Conflict("VERSION_CONFLICT")
				}
				return nil
			}
			err := provider.EnsureInitializationLabels(context.Background(), cluster, group, guard)
			if mode == "wrong_uid" || mode == "foreign_label" {
				if err == nil || patches != 0 {
					t.Fatal("identity/label conflict was overwritten")
				}
				return
			}
			if mode == "external_group_change" {
				if err == nil || !patched || labels[biz.GroupLabel] != group.GroupID {
					t.Fatal("partial success was hidden or rolled back")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if mode == "resource_version_retry" {
				expected = 2
			}
			if patches != expected || labels["example.com/existing"] != "preserved" {
				t.Fatal("unexpected patch count or unrelated label mutation")
			}
			if err = provider.EnsureInitializationLabels(context.Background(), cluster, group, guard); err != nil || patches != expected {
				t.Fatal("same-label replay did not recover idempotently")
			}
		})
	}
}
