package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type currentMetricRepository struct {
	biz.Repository
	reader *currentMetricReader
}

func (r currentMetricRepository) View(ctx context.Context, read func(biz.Reader) error) error {
	return read(r.reader)
}

type currentMetricReader struct {
	biz.Reader
	read func(context.Context, []string, time.Time, time.Time) ([]biz.CurrentBindingMetric, error)
}

func (r *currentMetricReader) CurrentBindingMetrics(ctx context.Context, connections []string, after, before time.Time) ([]biz.CurrentBindingMetric, error) {
	return r.read(ctx, connections, after, before)
}

func TestCurrentBindingCollectorExportsVerifiedIdentity(t *testing.T) {
	metric := biz.CurrentBindingMetric{
		Pod:  biz.PodRef{ClusterID: "10000000-0000-4000-8000-000000000001", Namespace: "tenant", Name: "worker", UID: "pod-uid", ContainerName: "main"},
		Node: biz.NodeRef{UID: "node-uid", Name: "node1"}, GPUUUID: "GPU-ABCDEF01-2345-6789-ABCD-EF0123456789",
		UsageRef: biz.UsageRef{TenantID: "20000000-0000-4000-8000-000000000001", OwnerService: "ani-inference", ResourceID: "30000000-0000-4000-8000-000000000001", CreateOperationID: "40000000-0000-4000-8000-000000000001"},
	}
	calls := 0
	reader := &currentMetricReader{read: func(ctx context.Context, connections []string, after, before time.Time) ([]biz.CurrentBindingMetric, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second || len(connections) != 1 || connections[0] != "managed" || before.Sub(after) != 125*time.Second {
			return nil, errors.New("collector lost its query scope, freshness or timeout")
		}
		return []biz.CurrentBindingMetric{metric}, nil
	}}
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCurrentGpuBindingCollector(currentMetricRepository{reader: reader}, []string{"managed"}, 120*time.Second))
	families, err := registry.Gather()
	if err != nil || calls != 1 || len(families) != 1 || len(families[0].Metric) != 1 {
		t.Fatalf("gather = %v, calls=%d, error=%v", families, calls, err)
	}
	got := families[0].Metric[0]
	labels := map[string]string{}
	for _, label := range got.Label {
		labels[label.GetName()] = label.GetValue()
	}
	want := map[string]string{
		"ani_cluster_id": metric.Pod.ClusterID, "ani_node_id": metric.Node.Name, "ani_node_uid": metric.Node.UID,
		"podnamespace": metric.Pod.Namespace, "podname": metric.Pod.Name, "ctrname": metric.Pod.ContainerName,
		"pod_uid": metric.Pod.UID, "deviceuuid": "abcdef01-2345-6789-abcd-ef0123456789",
		"ani_tenant_id": metric.UsageRef.TenantID, "owner_service": metric.UsageRef.OwnerService,
		"resource_id": metric.UsageRef.ResourceID, "create_operation_id": metric.UsageRef.CreateOperationID,
		"resource_kind": "inference",
	}
	if len(labels) != 13 || got.GetGauge().GetValue() != 1 {
		t.Fatalf("unexpected current identity metric: %v", got)
	}
	for name, value := range want {
		if labels[name] != value {
			t.Errorf("%s = %q, want %q", name, labels[name], value)
		}
	}
}

func TestCurrentBindingCollectorFailsTheScrapeOnReadFailure(t *testing.T) {
	for _, fault := range []string{"query", "deadline"} {
		t.Run(fault, func(t *testing.T) {
			reader := &currentMetricReader{read: func(ctx context.Context, _ []string, _, _ time.Time) ([]biz.CurrentBindingMetric, error) {
				if fault == "deadline" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return nil, errors.New("database unavailable")
			}}
			registry := prometheus.NewRegistry()
			registry.MustRegister(NewCurrentGpuBindingCollector(currentMetricRepository{reader: reader}, []string{"managed"}, time.Minute))
			response := httptest.NewRecorder()
			promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "ani_acc_gpu_binding_info") {
				t.Fatalf("failed query became successful empty scrape: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
