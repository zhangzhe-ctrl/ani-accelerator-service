package data

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresIdenticalBindingReconciliationDoesNotUpdateButAllChangesPersist(t *testing.T) {
	f := pgSetup(t)
	ctx := context.Background()
	projection := f.projection(1)
	now := time.Now().UTC()
	device := biz.NewID()
	pod := biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "binding-noop", UID: biz.NewID(), ContainerName: projection.ContainerName}
	record := biz.DeviceRecord{Device: biz.PhysicalDevice{DeviceID: device, ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID, Node: &f.group.Nodes[0], VendorDeviceID: "GPU-binding-noop", ModelKey: f.group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest}
	batch := biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Devices: []biz.DeviceRecord{record}}
	for _, source := range []string{"scheduler", "allocation", "runtime"} {
		batch.Claims = append(batch.Claims, biz.Claim{Pod: pod, DeviceID: device, NodeUID: f.group.Nodes[0].UID, Source: source, Cursor: "one", ObservedAt: now, ReceivedAt: now, State: biz.Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: f.group.Baseline.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "test:" + source, OwnerRef: projection.Ref, ExecutionDigest: projection.ExecutionDigest})
	}
	store := func(value biz.ObservationBatch) {
		t.Helper()
		if err := f.db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, value) }); err != nil {
			t.Fatal(err)
		}
	}
	read := func() (biz.Binding, string) {
		t.Helper()
		var binding biz.Binding
		if err := f.db.View(ctx, func(reader biz.Reader) error {
			rows, err := reader.DeviceBindings(ctx, device)
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				t.Fatalf("expected original binding: count=%d", len(rows))
			}
			binding = rows[0]
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var version string
		if err := f.db.pool.QueryRow(ctx, testSQL(t, "binding_row_version.sql"), binding.BindingID).Scan(&version); err != nil {
			t.Fatal(err)
		}
		return binding, version
	}
	assertReplayNoWrite := func() {
		t.Helper()
		before, version := read()
		var claimsBefore, claimsAfter []biz.Claim
		var proofBefore, proofAfter []biz.ReleaseProof
		if err := f.db.View(ctx, func(reader biz.Reader) (err error) {
			claimsBefore, proofBefore, err = reader.PodEvidence(ctx, pod)
			return
		}); err != nil {
			t.Fatal(err)
		}
		// The normal missing-key poll must still reconcile persisted evidence.
		store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID})
		after, next := read()
		if !reflect.DeepEqual(before, after) || next != version {
			t.Fatal("identical binding reconciliation manufactured a PostgreSQL row UPDATE")
		}
		if err := f.db.View(ctx, func(reader biz.Reader) (err error) {
			claimsAfter, proofAfter, err = reader.PodEvidence(ctx, pod)
			return
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(claimsBefore, claimsAfter) || !reflect.DeepEqual(proofBefore, proofAfter) {
			t.Fatal("no-op reconciliation changed original facts or evidence times")
		}
	}
	assertChanged := func(previousVersion string) biz.Binding {
		t.Helper()
		binding, version := read()
		if version == previousVersion {
			t.Fatal("changed binding did not persist a PostgreSQL row UPDATE")
		}
		assertReplayNoWrite()
		return binding
	}
	store(batch)
	assertReplayNoWrite()
	before, version := read()
	if _, err := f.u.SyncUsage(biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"}), "binding-noop", projection); err != nil {
		t.Fatal(err)
	}
	linked := assertChanged(version)
	if linked.VerifiedUsageRef == nil || *linked.VerifiedUsageRef != *projection.Ref || before.VerifiedUsageRef != nil {
		t.Fatal("owner association was suppressed")
	}
	_, version = read()
	for index := 1; index < len(batch.Claims); index++ {
		batch.Claims[index].ObservedAt = now.Add(time.Millisecond)
	}
	store(biz.ObservationBatch{ClusterID: batch.ClusterID, Claims: batch.Claims[1:]})
	timed := assertChanged(version)
	if timed.Observation.ObservedAt != now.Add(time.Millisecond).Format(time.RFC3339Nano) || timed.EvidenceRef != linked.EvidenceRef || timed.Observation.ReceivedAt != linked.Observation.ReceivedAt {
		t.Fatal("actual observation time was lost or unrelated evidence changed")
	}
	_, version = read()
	allocation := batch.Claims[1]
	allocation.ObservedAt = now.Add(2 * time.Millisecond)
	allocation.EvidenceRef = "test:allocation-next-evidence"
	store(biz.ObservationBatch{ClusterID: batch.ClusterID, Claims: []biz.Claim{allocation}})
	evidence := assertChanged(version)
	if evidence.EvidenceRef == timed.EvidenceRef || evidence.Observation.ObservedAt != timed.Observation.ObservedAt {
		t.Fatal("actual evidence change was lost or original time changed")
	}
	_, version = read()
	allocation.ObservedAt = now.Add(3 * time.Millisecond)
	allocation.State = biz.AllocationUnknown
	store(biz.ObservationBatch{ClusterID: batch.ClusterID, Claims: []biz.Claim{allocation}})
	unknown := assertChanged(version)
	if unknown.State != biz.AllocationUnknown || unknown.Observation.Complete {
		t.Fatal("actual unknown allocation state was suppressed")
	}
	_, version = read()
	later := now.Add(4 * time.Millisecond)
	proof := biz.ReleaseProof{Pod: pod, DeviceID: device, NodeUID: f.group.Nodes[0].UID, ObservedAt: later, ReceivedAt: later, EvidenceRef: "test:binding-noop-release", RuntimeExited: true, AllocationAbsent: true, NodeOnline: true, OwnerRef: projection.Ref, ExecutionDigest: projection.ExecutionDigest}
	store(biz.ObservationBatch{ClusterID: batch.ClusterID, Releases: []biz.ReleaseProof{proof}})
	released := assertChanged(version)
	if released.State != biz.ReleaseConfirmed || released.EvidenceRef != proof.EvidenceRef || released.Observation.ObservedAt != later.Format(time.RFC3339Nano) {
		t.Fatal("exact positive release evidence was suppressed")
	}
}
