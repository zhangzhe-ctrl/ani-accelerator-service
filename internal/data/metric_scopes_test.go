package data

import (
	"context"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"testing"
	"time"
)

func TestPostgresMetricHistoryUIDReplacementNilAndSnapshot(t *testing.T) {
	f := pgSetup(t)
	ctx := context.Background()
	var mutableHistory, mutableIndex bool
	if err := f.db.pool.QueryRow(ctx, testSQL(t, "metric_history_privileges.sql")).Scan(&mutableHistory, &mutableIndex); err != nil || mutableHistory || mutableIndex {
		t.Fatalf("runtime can mutate monitoring history/activation: %v %v %v", mutableHistory, mutableIndex, err)
	}
	projection := f.projection(1)
	// PostgreSQL timestamptz and persisted owner operation timestamps use
	// microseconds; avoid an unrelated nanosecond rounding mismatch in the fixture.
	projection.SourceOperationCreatedAt = time.Now().UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	projection.PayloadDigest, _ = biz.ProjectionDigest(projection)
	if _, err := f.u.SyncUsage(biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"}), "monitoring", projection); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	device := biz.NewID()
	record := biz.DeviceRecord{Device: biz.PhysicalDevice{DeviceID: device, ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID, Node: &f.group.Nodes[0], VendorDeviceID: "GPU-AbCd", ModelKey: f.group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest}
	batchFor := func(uid string, at time.Time, ref *biz.UsageRef) biz.ObservationBatch {
		batch := biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Devices: []biz.DeviceRecord{record}}
		for _, source := range []string{"scheduler", "allocation", "runtime"} {
			batch.Claims = append(batch.Claims, biz.Claim{Pod: biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "metric-pod", UID: uid, ContainerName: projection.ContainerName}, DeviceID: device, NodeUID: f.group.Nodes[0].UID, Source: source, ObservedAt: at, ReceivedAt: at, State: biz.Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: f.group.Baseline.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "test:" + source, OwnerRef: ref, ExecutionDigest: projection.ExecutionDigest})
		}
		return batch
	}
	store := func(b biz.ObservationBatch) {
		t.Helper()
		if err := f.db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, b) }); err != nil {
			t.Fatal(err)
		}
	}
	uidA, uidB := biz.NewID(), biz.NewID()
	store(batchFor(uidA, now, projection.Ref))
	store(batchFor(uidB, now, projection.Ref))
	actor := adminActor
	a := &biz.TenantContext{TenantID: f.tenant, RequestID: "metrics", Actor: &actor}
	tenantCtx := biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Action: "ListTenantGpuMetricScopes", TenantID: f.tenant, Actor: actor})
	filter := biz.MetricScopeFilter{ClusterID: f.cluster.ClusterID, Start: now.Add(-time.Hour).Format(time.RFC3339Nano), End: time.Now().UTC().Format(time.RFC3339Nano), Page: &biz.Page{Size: 1}}
	first, err := f.u.ListTenantGpuMetricScopes(tenantCtx, a, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextToken == "" || first.Items[0].GPUUUID != "abcd" || first.Items[0].OwnershipState != "VERIFIED" || first.Items[0].EvidenceRef == "" {
		t.Fatalf("bad first page: %+v", first)
	}
	filter.SnapshotID = first.SnapshotID
	filter.Page.Token = first.NextToken
	second, err := f.u.ListTenantGpuMetricScopes(tenantCtx, a, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ScopeID == first.Items[0].ScopeID {
		t.Fatal("snapshot pagination lost/duplicated scope")
	}
	store(batchFor(uidA, now.Add(time.Millisecond), nil))
	if _, err = f.u.ListTenantGpuMetricScopes(tenantCtx, a, filter); err == nil || err.Error() != "METRIC_SNAPSHOT_CHANGED" {
		t.Fatalf("changed snapshot accepted: %v", err)
	}
	filter.Page = nil
	filter.SnapshotID = ""
	filter.End = time.Now().UTC().Format(time.RFC3339Nano)
	history, err := f.u.ListTenantGpuMetricScopes(tenantCtx, a, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 2 {
		t.Fatalf("trusted history erased on nil upsert: %+v", history)
	}
	ended := false
	for _, s := range history.Items {
		if s.PodUID == uidA && s.ValidTo != "" {
			ended = true
		}
	}
	if !ended {
		t.Fatal("old trusted owner remained current after nil")
	}
	wrong := filter
	wrong.Ref = &biz.UsageRef{TenantID: biz.NewID(), OwnerService: projection.Ref.OwnerService, ResourceID: projection.Ref.ResourceID, CreateOperationID: projection.Ref.CreateOperationID}
	if _, err = f.u.ListTenantGpuMetricScopes(tenantCtx, a, wrong); err == nil {
		t.Fatal("cross-tenant ref accepted")
	}
	other := f.projection(1)
	other.Ref.TenantID = biz.NewID()
	other.PayloadDigest, _ = biz.ProjectionDigest(other)
	if _, err = f.u.SyncUsage(biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"}), "monitoring-other", other); err != nil {
		t.Fatal(err)
	}
	legacy := batchFor(biz.NewID(), now.Add(2*time.Millisecond), other.Ref)
	for i := range legacy.Claims {
		legacy.Claims[i].Pod.Name = "legacy-singleton"
	}
	store(legacy)
	legacyFilter := filter
	legacyFilter.Ref = other.Ref
	legacyFilter.End = time.Now().UTC().Format(time.RFC3339Nano)
	legacyScopes, err := f.u.ListAdminGpuMetricScopes(adminContext("ListAdminGpuMetricScopes"), &biz.AdminRead{RequestID: "legacy", Actor: &actor}, legacyFilter)
	if err != nil || len(legacyScopes.Items) != 1 || legacyScopes.Items[0].CandidateComplete || legacyScopes.Items[0].OwnershipState != "OWNERSHIP_UNKNOWN" {
		t.Fatalf("pre-index singleton was treated as proven complete: %+v %v", legacyScopes, err)
	}
	store(batchFor(biz.NewID(), now.Add(2*time.Millisecond), other.Ref))
	filter.End = time.Now().UTC().Format(time.RFC3339Nano)
	conflicted, err := f.u.ListTenantGpuMetricScopes(tenantCtx, a, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicted.Items) != 2 {
		t.Fatalf("tenant output leaked or lost own history: %+v", conflicted)
	}
	for _, s := range conflicted.Items {
		if s.Ref.TenantID != f.tenant || s.OwnershipState != "OWNERSHIP_AMBIGUOUS" || s.CandidateComplete {
			t.Fatalf("conflict checked after tenant filtering: %+v", s)
		}
	}
	t.Log("A02/A03/A04/A05/A06: real PG append-only history, same lifecycle UID replacement, nil current ref retention, bound snapshot pagination, global source conflict and cross-tenant denial")
}
