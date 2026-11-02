package data

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresRejectedAdmissionReleaseAndLateRuntime(t *testing.T) {
	f := pgSetup(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second)
	device := biz.NewID()
	ref := biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "rejected", UID: biz.NewID(), ContainerName: "inference"}
	claim := biz.Claim{Pod: ref, DeviceID: device, NodeUID: f.group.Nodes[0].UID, Source: "scheduler", Cursor: "1", ObservedAt: now, ReceivedAt: now, State: biz.Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: f.group.Baseline.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "scheduler:rejected"}
	batch := biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Claims: []biz.Claim{claim}, Devices: []biz.DeviceRecord{{
		Device:               biz.PhysicalDevice{DeviceID: device, ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID, Node: &f.group.Nodes[0], VendorDeviceID: "GPU-rejected", ModelKey: f.group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}},
		ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest,
	}}}
	for _, source := range []string{"kubernetes", "podresources", "cri"} {
		batch.Watermarks = append(batch.Watermarks, biz.SourceWatermark{ClusterID: f.cluster.ClusterID, NodeUID: f.group.Nodes[0].UID, Source: source, ObservedAt: now, ReceivedAt: now, Complete: true})
	}
	store := func(batch biz.ObservationBatch) {
		t.Helper()
		if err := f.db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
			t.Fatal(err)
		}
	}
	check := func(complete bool, committed, free int64) {
		t.Helper()
		capacity, err := f.u.AdminGetCapacity(adminContext("AdminGetCapacity"), adminMutation().Context, f.profile.ProfileID, 1, true)
		if err != nil || capacity.Observation.Complete != complete || capacity.KnownCommittedUnits != committed || capacity.KnownFreeUnits != free {
			t.Fatalf("capacity=%+v error=%v; want complete=%v committed=%d free=%d", capacity, err, complete, committed, free)
		}
	}
	store(batch)
	check(false, 1, 0)
	proof := biz.ReleaseProof{Pod: ref, DeviceID: device, NodeUID: claim.NodeUID, ObservedAt: now.Add(time.Millisecond), ReceivedAt: now.Add(time.Millisecond), EvidenceRef: "admission-rejected:exact-pod", AdmissionRejected: true, AllocationAbsent: true, NodeOnline: true}
	store(biz.ObservationBatch{ClusterID: batch.ClusterID, Releases: []biz.ReleaseProof{proof}})
	check(true, 0, 4)
	// A subsequent collection re-reads the persisted proof instead of reviving
	// the historical scheduler claim after the rejected Pod has disappeared.
	store(biz.ObservationBatch{ClusterID: batch.ClusterID})
	check(true, 0, 4)
	// Never-started evidence cannot erase an allocation that arrives late,
	// even if it describes a timestamp before that admission proof.
	claim.Source = "runtime"
	claim.EvidenceRef = "cri:late-runtime"
	store(biz.ObservationBatch{ClusterID: batch.ClusterID, Claims: []biz.Claim{claim}})
	check(false, 1, 0)
}
