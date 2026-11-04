package server

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

// CurrentGpuBindingCollector exposes only the current allocation identities.
// Prometheus owns sample history; gathering never changes the ACC ledger.
type CurrentGpuBindingCollector struct {
	repo        biz.Repository
	connections []string
	freshness   time.Duration
	desc        *prometheus.Desc
}

func NewCurrentGpuBindingCollector(repo biz.Repository, connections []string, freshness time.Duration) *CurrentGpuBindingCollector {
	return &CurrentGpuBindingCollector{
		repo: repo, connections: append([]string(nil), connections...), freshness: freshness,
		desc: prometheus.NewDesc(
			"ani_acc_gpu_binding_info",
			"Current GPU allocation with its verified business identity.",
			[]string{
				"ani_cluster_id", "ani_node_id", "ani_node_uid",
				"podnamespace", "podname", "ctrname", "pod_uid", "deviceuuid",
				"ani_tenant_id", "owner_service", "resource_id", "create_operation_id", "resource_kind",
			},
			nil,
		),
	}
}

func (c *CurrentGpuBindingCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *CurrentGpuBindingCollector) Collect(ch chan<- prometheus.Metric) {
	if c.repo == nil || c.freshness <= 0 {
		ch <- prometheus.NewInvalidMetric(c.desc, fmt.Errorf("current GPU binding reader is unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	now := time.Now().UTC()
	var bindings []biz.CurrentBindingMetric
	if err := c.repo.View(ctx, func(reader biz.Reader) error {
		var err error
		bindings, err = reader.CurrentBindingMetrics(ctx, c.connections, now.Add(-c.freshness), now.Add(5*time.Second))
		return err
	}); err != nil {
		// Do not turn a failed query into a successful empty mapping scrape.
		ch <- prometheus.NewInvalidMetric(c.desc, fmt.Errorf("read current GPU bindings: %w", err))
		return
	}

	metrics := make([]prometheus.Metric, 0, len(bindings))
	for _, binding := range bindings {
		kind := currentGpuResourceKind(binding.UsageRef.OwnerService)
		gpuUUID := biz.NormalizeGPUUUID(binding.GPUUUID)
		if biz.ValidatePod(binding.Pod) != nil || biz.ValidateRef(&binding.UsageRef) != nil ||
			!biz.Ref(binding.Node.UID) || !biz.Ref(binding.Node.Name) || gpuUUID == "" || kind == "" {
			ch <- prometheus.NewInvalidMetric(c.desc, fmt.Errorf("invalid current GPU binding identity"))
			return
		}
		metric, err := prometheus.NewConstMetric(c.desc, prometheus.GaugeValue, 1,
			binding.Pod.ClusterID, binding.Node.Name, binding.Node.UID,
			binding.Pod.Namespace, binding.Pod.Name, binding.Pod.ContainerName, binding.Pod.UID, gpuUUID,
			binding.UsageRef.TenantID, binding.UsageRef.OwnerService,
			binding.UsageRef.ResourceID, binding.UsageRef.CreateOperationID, kind,
		)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(c.desc, err)
			return
		}
		metrics = append(metrics, metric)
	}
	// Publish only after the complete result has been mapped successfully.
	for _, metric := range metrics {
		ch <- metric
	}
}

func currentGpuResourceKind(owner string) string {
	switch owner {
	case "ani-resource-service":
		return "container"
	case "ani-inference":
		return "inference"
	case "ani-modeldev-service":
		return "modeldev_execution"
	default:
		return ""
	}
}
