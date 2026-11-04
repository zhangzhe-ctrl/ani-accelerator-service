package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestReadPhysicalMemoryUsesScopedCanonicalSamples(t *testing.T) {
	clusterID := biz.NewID()
	nodeUID := biz.NewID()
	tokenFile := filepath.Join(t.TempDir(), "prometheus-token")
	if err := os.WriteFile(tokenFile, []byte("prometheus-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/prometheus/api/v1/query" || request.URL.Query().Get("query") != "nvidia_smi_memory_total_bytes{ani_cluster_id=\""+clusterID+"\",ani_gpu_source=\"nvml\"}" || request.Header.Get("Authorization") != "Bearer prometheus-credential" {
			t.Errorf("incorrect scoped query or credential: %s", request.URL)
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		var samples []any
		for _, sample := range []struct {
			cluster string
			source  string
			uuid    string
			value   string
		}{
			{clusterID, "nvml", gpuUUID, "1.7179869184e+10"},
			{clusterID, "nvml", strings.ToUpper(gpuUUID), "25769803776"},
			{clusterID, "nvml", gpuUUID, "NaN"},
			{clusterID, "nvml", gpuUUID, "18446744073709551616"},
			{clusterID, "nvml", gpuUUID2, "1.5"},
			{clusterID, "hami", gpuUUID2, "99999999999"},
			{biz.NewID(), "nvml", gpuUUID2, "99999999999"},
		} {
			samples = append(samples, map[string]any{"metric": map[string]string{"ani_cluster_id": sample.cluster, "ani_node_uid": nodeUID, "ani_gpu_source": sample.source, "uuid": sample.uuid}, "value": []any{1793786400, sample.value}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": samples}})
	}))
	defer server.Close()
	reader, err := newPrometheusInventoryReader(PrometheusInventoryConfig{URL: server.URL + "/prometheus", BearerTokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	samples, err := reader.ReadPhysicalMemory(context.Background(), clusterID)
	if err != nil || len(samples) != 1 || samples[0].NodeUID != nodeUID || samples[0].CanonicalUUID != biz.NormalizeGPUUUID(gpuUUID) || samples[0].Bytes != 24576<<20 {
		t.Fatalf("scoped canonical metadata = %+v, error = %v", samples, err)
	}
}

func TestCollectPhysicalMemoryFailureOnlyClearsDisplayBytes(t *testing.T) {
	fixture := newNodePluginFixture(t)
	fixture.adopted = true
	adapter := fixture.provider(t, fixture.references)
	failed, calls := false, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls++
		if failed {
			http.Error(w, "metadata unavailable", http.StatusServiceUnavailable)
			return
		}
		var samples []any
		for index, group := range fixture.groups {
			samples = append(samples, map[string]any{"metric": map[string]string{"ani_cluster_id": fixture.cluster.ClusterID, "ani_node_uid": group.Nodes[0].UID, "ani_gpu_source": "nvml", "uuid": []string{gpuUUID, gpuUUID2}[index]}, "value": []any{1793786400, "1024"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": samples}})
	}))
	defer server.Close()
	inventory, err := newPrometheusInventoryReader(PrometheusInventoryConfig{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	adapter.connections[fixture.cluster.ConnectionRef].inventory = inventory
	first, err := adapter.Collect(context.Background(), fixture.cluster, fixture.groups)
	if err != nil || calls != 1 || len(first.Devices) != 2 {
		t.Fatalf("metadata collection = %+v, calls = %d, error = %v", first, calls, err)
	}
	for _, record := range first.Devices {
		if record.Device.PhysicalMemoryBytes != 1024 || !record.Device.Observation.Complete || record.SafeMemoryBudgetMiB != 24570 {
			t.Fatalf("physical bytes limited registered budget: %+v", record)
		}
	}
	failed = true
	second, err := adapter.Collect(context.Background(), fixture.cluster, fixture.groups)
	if err != nil || calls != 2 || len(second.BindingIssues) != 0 || len(second.Devices) != 2 {
		t.Fatalf("metadata error changed allocation completeness: %+v, calls = %d, error = %v", second, calls, err)
	}
	for _, record := range second.Devices {
		if record.Device.PhysicalMemoryBytes != 0 || !record.Device.Observation.Complete || record.SafeMemoryBudgetMiB != 24570 {
			t.Fatalf("metadata failure reused old bytes or blocked capacity: %+v", record)
		}
	}
}
