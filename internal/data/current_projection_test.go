package data

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func currentProjectionBatch(f *pgFixture) biz.ObservationBatch {
	now := time.Now().UTC()
	device := biz.DeviceRecord{
		Device: biz.PhysicalDevice{DeviceID: biz.NewID(), ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID,
			Node: &f.group.Nodes[0], VendorDeviceID: "GPU-abcdef01-2345-6789-abcd-ef0123456789", ModelKey: f.group.ModelKey, Health: "HEALTHY",
			Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}},
		ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest,
	}
	return biz.ObservationBatch{
		ClusterID: f.cluster.ClusterID, ObservedAt: now, ReceivedAt: now, SnapshotRef: "current-pods",
		PodListComplete: true, NodeListComplete: true, Devices: []biz.DeviceRecord{device},
		LiveNodeUIDs: []string{f.group.Nodes[0].UID}, InventoryCompleteNodeUIDs: []string{f.group.Nodes[0].UID},
		LivePodUIDs: []string{"pod-a", "pod-b"},
		Bindings: []biz.ObservedBinding{
			{Pod: biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "worker-a", UID: "pod-a", ContainerName: "inference"},
				NodeUID: f.group.Nodes[0].UID, DeviceID: device.Device.DeviceID, MemoryMiB: 6144, CoreLimitPercent: 25, MemoryBlockMiB: 1, BaselineDigest: f.group.Baseline.BaselineDigest},
			{Pod: biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "worker-b", UID: "pod-b", ContainerName: "inference"},
				NodeUID: f.group.Nodes[0].UID, DeviceID: device.Device.DeviceID, MemoryMiB: 6144, CoreLimitPercent: 25, MemoryBlockMiB: 1, BaselineDigest: f.group.Baseline.BaselineDigest},
		},
	}
}

func saveProjectionBatch(t *testing.T, f *pgFixture, batch biz.ObservationBatch) {
	t.Helper()
	ctx := context.Background()
	if err := f.db.View(ctx, func(r biz.Reader) error {
		state, err := r.CollectionState(ctx, batch.ClusterID)
		if err == nil {
			batch.BaseObservationRevision = state.Revision
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
		t.Fatal(err)
	}
}

func projectionBindings(t *testing.T, f *pgFixture, device string) map[string]biz.Binding {
	t.Helper()
	result := map[string]biz.Binding{}
	ctx := context.Background()
	if err := f.db.View(ctx, func(r biz.Reader) error {
		bindings, err := r.Bindings(ctx, biz.BindingFilter{ClusterID: f.cluster.ClusterID, DeviceID: device}, "", 100)
		for _, binding := range bindings {
			result[binding.Pod.UID] = binding
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPostgresCurrentSetReplacementPreservesUnknownAndStableIDs(t *testing.T) {
	f := pgSetup(t)
	batch := currentProjectionBatch(f)
	device := batch.Devices[0].Device.DeviceID
	saveProjectionBatch(t, f, batch)
	original := projectionBindings(t, f, device)
	currentB := batch.Bindings[1]
	batch.LivePodUIDs, batch.IncompletePodUIDs, batch.Bindings = []string{"pod-b"}, []string{"pod-b"}, nil
	batch.ObservedAt, batch.ReceivedAt, batch.SnapshotRef = batch.ObservedAt.Add(time.Millisecond), batch.ReceivedAt.Add(time.Millisecond), "damaged-b"
	saveProjectionBatch(t, f, batch)
	got := projectionBindings(t, f, device)
	if got["pod-a"].State != biz.ReleaseConfirmed || got["pod-b"].State != biz.AllocationUnknown ||
		got["pod-b"].MemoryMiB != original["pod-b"].MemoryMiB || got["pod-b"].EvidenceRef != original["pod-b"].EvidenceRef ||
		got["pod-b"].Observation.ObservedAt != original["pod-b"].Observation.ObservedAt {
		t.Fatalf("independent absent and malformed Pods did not converge: %+v", got)
	}
	// A valid current set recovers the same identity, then a source failure
	// preserves it; only a complete empty list ends it.
	batch.IncompletePodUIDs, batch.Bindings = nil, []biz.ObservedBinding{currentB}
	saveProjectionBatch(t, f, batch)
	beforeFailure := projectionBindings(t, f, device)["pod-b"]
	saveProjectionBatch(t, f, biz.ObservationBatch{ClusterID: batch.ClusterID, ReceivedAt: batch.ReceivedAt, BindingIssues: []string{"SOURCE_UNKNOWN"}})
	if after := projectionBindings(t, f, device)["pod-b"]; !reflect.DeepEqual(beforeFailure, after) {
		t.Fatalf("failed list changed the current binding: before=%+v after=%+v", beforeFailure, after)
	}
	batch.LivePodUIDs, batch.Bindings = nil, nil
	saveProjectionBatch(t, f, batch)
	if got := projectionBindings(t, f, device)["pod-b"]; got.State != biz.ReleaseConfirmed {
		t.Fatalf("complete empty set did not end occupancy: %+v", got)
	}
	batch.LivePodUIDs, batch.Bindings = []string{"pod-b"}, []biz.ObservedBinding{currentB}
	saveProjectionBatch(t, f, batch)
	if got := projectionBindings(t, f, device)["pod-b"]; got.State != biz.Active || got.BindingID != original["pod-b"].BindingID {
		t.Fatalf("current observation changed stable identity: %+v", got)
	}
}

func TestPostgresLateUsageLinksEndedBindingWithoutRewritingReplays(t *testing.T) {
	f := pgSetup(t)
	usage := f.projection(1)
	batch := currentProjectionBatch(f)
	batch.Bindings, batch.LivePodUIDs = batch.Bindings[:1], batch.LivePodUIDs[:1]
	batch.Bindings[0].OwnerRef, batch.Bindings[0].ExecutionDigest = usage.Ref, usage.ExecutionDigest
	device := batch.Devices[0].Device.DeviceID
	saveProjectionBatch(t, f, batch)
	batch.Bindings, batch.LivePodUIDs = nil, nil
	saveProjectionBatch(t, f, batch)
	ended := projectionBindings(t, f, device)["pod-a"]
	if ended.State != biz.ReleaseConfirmed || ended.VerifiedUsageRef != nil {
		t.Fatalf("bad pending ended fixture: %+v", ended)
	}
	ctx := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"})
	if _, err := f.u.SyncUsage(ctx, "late", usage); err != nil {
		t.Fatal(err)
	}
	linked := projectionBindings(t, f, device)["pod-a"]
	ended.VerifiedUsageRef = usage.Ref
	if !reflect.DeepEqual(ended, linked) {
		t.Fatalf("late Usage changed allocation instead of only linking: want=%+v got=%+v", ended, linked)
	}
	rowVersion := func() string {
		t.Helper()
		var version string
		if err := f.db.pool.QueryRow(ctx, testSQL(t, "current_binding_row_version.sql"), linked.BindingID).Scan(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}
	before := rowVersion()
	if _, err := f.u.SyncUsage(ctx, "replay", usage); err != nil {
		t.Fatal(err)
	}
	if after := rowVersion(); after != before {
		t.Fatalf("Usage replay rewrote an already-linked historical binding: %s -> %s", before, after)
	}
}

func TestPostgresCurrentMetricsExcludeUnknownEndedUnlinkedAndRetired(t *testing.T) {
	f := pgSetup(t)
	usage := f.projection(1)
	batch := currentProjectionBatch(f)
	batch.Bindings, batch.LivePodUIDs = batch.Bindings[:1], batch.LivePodUIDs[:1]
	batch.Bindings[0].OwnerRef, batch.Bindings[0].ExecutionDigest = usage.Ref, usage.ExecutionDigest
	assigned := batch.Bindings[0]
	ctx := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"})
	count := func(connections []string, after time.Time) int {
		t.Helper()
		n := 0
		if err := f.db.View(ctx, func(r biz.Reader) error {
			rows, err := r.CurrentBindingMetrics(ctx, connections, after, after.Add(time.Hour))
			n = len(rows)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	connections := []string{f.cluster.ConnectionRef}
	after := batch.ObservedAt.Add(-time.Minute)
	saveProjectionBatch(t, f, batch)
	if count(connections, after) != 0 {
		t.Fatal("unlinked candidate leaked business identity")
	}
	if _, err := f.u.SyncUsage(ctx, "metrics", usage); err != nil {
		t.Fatal(err)
	}
	if count(connections, after) != 1 || count(nil, after) != 0 || count([]string{"other"}, after) != 0 || count(connections, batch.ObservedAt.Add(time.Second)) != 0 {
		t.Fatal("current metric query lost scope or freshness filtering")
	}
	batch.Bindings, batch.IncompletePodUIDs = nil, []string{"pod-a"}
	saveProjectionBatch(t, f, batch)
	if count(connections, after) != 0 {
		t.Fatal("unknown allocation exported current info")
	}
	batch.IncompletePodUIDs, batch.LivePodUIDs = nil, nil
	saveProjectionBatch(t, f, batch)
	if count(connections, after) != 0 {
		t.Fatal("ended allocation exported current info")
	}
	batch.Bindings, batch.LivePodUIDs = []biz.ObservedBinding{assigned}, []string{"pod-a"}
	saveProjectionBatch(t, f, batch)
	saveProjectionBatch(t, f, biz.ObservationBatch{ClusterID: batch.ClusterID, ReceivedAt: batch.ReceivedAt, NodeListComplete: true})
	if count(connections, after) != 0 {
		t.Fatal("retired inventory exported current info")
	}
}
