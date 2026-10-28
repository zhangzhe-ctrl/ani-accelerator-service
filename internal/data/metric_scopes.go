package data

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type storedMetricScope struct {
	Scope  biz.MetricScope `json:"scope"`
	Active bool            `json:"active"`
}

// This projection runs inside the existing binding transaction. It never
// changes lifecycle, release, capacity, quota or current binding semantics.
func (s *pgStore) captureMetricScope(ctx context.Context, b *biz.Binding, usage *biz.UsageProjection) error {
	d, err := decodeOne[biz.DeviceRecord](s.q.SaveObservationData1(ctx, b.PhysicalDeviceID))
	if err != nil {
		return err
	}
	if b.Pod == nil || d.Device.Node == nil {
		return biz.Precondition("METRIC_SELECTOR_INCOMPLETE")
	}
	scope := biz.MetricScope{Ref: b.VerifiedUsageRef, ClusterID: b.Pod.ClusterID, Node: d.Device.Node, Namespace: b.Pod.Namespace, PodName: b.Pod.Name, ContainerName: b.Pod.ContainerName, GPUUUID: biz.NormalizeGPUUUID(d.Device.VendorDeviceID), PodUID: b.Pod.UID, DeviceID: b.PhysicalDeviceID, BindingID: b.BindingID, OwnershipState: "VERIFIED", CandidateComplete: true}
	if b.VerifiedUsageRef != nil {
		switch b.VerifiedUsageRef.OwnerService {
		case "ani-resource-service":
			scope.ResourceKind = "container_instance"
		case "ani-inference":
			scope.ResourceKind = "inference"
		case "ani-modeldev-service":
			scope.ResourceKind = "modeldev_execution"
		}
	}
	if b.VerifiedUsageRef == nil || b.Observation == nil || !b.Observation.Complete {
		scope.OwnershipState = "OWNERSHIP_UNKNOWN"
		scope.CandidateComplete = false
		scope.Reasons = []string{"CURRENT_EVIDENCE_INCOMPLETE"}
	}
	index, err := s.q.GpuMetricIndex(ctx)
	if err != nil {
		return cleanError(err)
	}
	// A current singleton cannot prove absence of pre-index cache candidates.
	// Only a trusted original lifecycle established after monitoring began can
	// supply complete candidates without a separate trusted cache-exclusion proof.
	if usage != nil && b.VerifiedUsageRef != nil {
		created, err := time.Parse(time.RFC3339Nano, usage.SourceOperationCreatedAt)
		if err != nil || created.Before(index.StartedAt) {
			scope.OwnershipState = "OWNERSHIP_UNKNOWN"
			scope.CandidateComplete = false
			scope.Reasons = append(scope.Reasons, "LIFECYCLE_PRECEDES_INDEX")
		}
	}
	// The signature deliberately excludes poll/evidence timestamps: identical
	// ownership is not a new historical event on each scrape.
	value := storedMetricScope{Scope: scope, Active: b.State != biz.ReleaseConfirmed}
	signature, err := biz.Hash(value)
	if err != nil {
		return err
	}
	value.Scope.ScopeID = biz.NewID()
	value.Scope.EvidenceRef = b.EvidenceRef
	if b.Observation != nil {
		value.Scope.EvidenceAt = b.Observation.ObservedAt
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sourceKey := strings.Join([]string{scope.ClusterID, scope.Node.Name, scope.Namespace, scope.PodName, scope.ContainerName, scope.GPUUUID, scope.VdeviceID}, "\x1f")
	return cleanError(s.q.CaptureGpuMetricScope(ctx, value.Scope.ScopeID, b.BindingID, sourceKey, signature, payload))
}

func (s *pgStore) MetricScopeEvents(ctx context.Context, connections []string, f biz.MetricScopeFilter) ([]biz.MetricScopeEvent, uint64, time.Time, error) {
	index, err := s.q.GpuMetricIndex(ctx)
	if err != nil {
		return nil, 0, time.Time{}, cleanError(err)
	}
	owner, resource, create := "", "", ""
	if f.Ref != nil {
		owner = f.Ref.OwnerService
		resource = f.Ref.ResourceID
		create = f.Ref.CreateOperationID
	}
	rows, err := s.q.GpuMetricEvents(ctx, connections, f.ClusterID, f.TenantID, owner, resource, create)
	if err != nil {
		return nil, 0, time.Time{}, cleanError(err)
	}
	if len(rows) > 8192 {
		return nil, 0, time.Time{}, biz.Invalid("METRIC_SCOPE_BUDGET_EXCEEDED")
	}
	out := make([]biz.MetricScopeEvent, 0, len(rows))
	for _, row := range rows {
		var v storedMetricScope
		if json.Unmarshal(row.Payload, &v) != nil {
			return nil, 0, time.Time{}, biz.Precondition("STORED_METRIC_SCOPE_INVALID")
		}
		out = append(out, biz.MetricScopeEvent{Revision: row.Revision, RecordedAt: row.RecordedAt, SourceKey: row.SourceKey, Scope: v.Scope, Active: v.Active})
	}
	return out, index.Revision, index.StartedAt, nil
}
