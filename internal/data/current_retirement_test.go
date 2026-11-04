package data

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresCurrentRetirementPersistsAndFencesNewOccupancy(t *testing.T) {
	f := pgSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	base := now.Add(-5 * time.Second)
	node, device := f.group.Nodes[0], biz.NewID()
	pod := biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "current-retirement", UID: biz.NewID(), ContainerName: "inference"}
	identity := biz.NodeIdentity{
		ConnectionRef: f.cluster.ConnectionRef, NodeUID: node.UID, NodeName: node.Name,
		PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "agent",
		ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(),
		LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute),
	}
	state, err := f.db.OpenNodeSession(ctx, identity, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	report := func(at time.Time, containers []biz.RuntimeAttempt) {
		t.Helper()
		snapshot := &biz.TelemetrySnapshot{
			CaptureEpoch: state.Epoch, CaptureStart: at, CaptureEnd: at, Containers: containers,
			Sources: []biz.TelemetrySource{{Name: "cri", ObservedAt: at, Complete: true}, {Name: "podresources", ObservedAt: at, Complete: true}},
		}
		digest, err := biz.Hash(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		state, err = f.db.SaveNodeReport(ctx, identity.ConnectionRef, node.UID, biz.NodeReport{
			Epoch: state.Epoch, Sequence: state.LastSequence + 1, LogID: identity.LogID,
			PayloadDigest: digest, Snapshot: snapshot,
		}, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	batch := func() biz.ObservationBatch {
		return biz.ObservationBatch{ConnectionRef: identity.ConnectionRef, ClusterID: f.cluster.ClusterID, NodeRevisions: map[string]uint64{node.UID: state.CurrentRevision}}
	}
	retirement := func(at time.Time) biz.ObservationBatch {
		result := batch()
		result.Releases = []biz.ReleaseProof{{
			Pod: pod, DeviceID: device, NodeUID: node.UID, ObservedAt: at, ReceivedAt: at,
			EvidenceRef: "current-state:retired", Retirement: biz.RetirementCurrentState,
			AllocationAbsent: true, NodeOnline: true, CheckedNodeRevision: state.CurrentRevision,
		}}
		return result
	}
	store := func(value biz.ObservationBatch) error {
		return f.db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, value) })
	}
	read := func(want biz.AllocationState) biz.Binding {
		t.Helper()
		var result biz.Binding
		if err := f.db.View(ctx, func(reader biz.Reader) error {
			bindings, err := reader.DeviceBindings(ctx, device)
			if err != nil {
				return err
			}
			if len(bindings) != 1 || bindings[0].State != want {
				t.Fatalf("want binding state %v, got %+v", want, bindings)
			}
			result = bindings[0]
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	checkCapacity := func(complete bool, committed, free int64) {
		t.Helper()
		capacity, err := f.u.AdminGetCapacity(adminContext("AdminGetCapacity"), adminMutation().Context, f.profile.ProfileID, 1, true)
		if err != nil || capacity.Observation.Complete != complete || capacity.KnownCommittedUnits != committed || capacity.KnownFreeUnits != free {
			t.Fatalf("capacity=%+v error=%v; want complete=%v committed=%d free=%d", capacity, err, complete, committed, free)
		}
	}
	report(base.Add(time.Second), nil)
	initial := batch()
	initial.Devices = []biz.DeviceRecord{{
		Device: biz.PhysicalDevice{DeviceID: device, ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID, Node: &node, VendorDeviceID: "GPU-current-retirement", ModelKey: f.group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: base.Format(time.RFC3339Nano), ReceivedAt: base.Format(time.RFC3339Nano)}},
		ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest,
	}}
	for _, source := range []string{"scheduler", "runtime", "allocation"} {
		claim := biz.Claim{Pod: pod, DeviceID: device, NodeUID: node.UID, Source: source, ObservedAt: base, ReceivedAt: base, State: biz.AllocationUnknown, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: f.group.Baseline.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "old:" + source}
		if source == "scheduler" {
			claim.State = biz.Active
		}
		initial.Claims = append(initial.Claims, claim)
	}
	for _, source := range []string{"kubernetes", "podresources", "cri"} {
		initial.Watermarks = append(initial.Watermarks, biz.SourceWatermark{ClusterID: f.cluster.ClusterID, NodeUID: node.UID, Source: source, ObservedAt: base, ReceivedAt: base, Complete: true, ConsumedNodeRevision: state.CurrentRevision})
	}
	if err := store(initial); err != nil {
		t.Fatal(err)
	}
	original := read(biz.AllocationUnknown)
	checkCapacity(false, 1, 0)

	// A report received after collection must fence the prepared retirement,
	// even before new occupancy claims reach the observation tables.
	stale := retirement(base.Add(time.Second))
	runningAt := base.Add(2 * time.Second)
	running := biz.RuntimeAttempt{ContainerID: "current-running", PodUID: pod.UID, PodNamespace: pod.Namespace, PodName: pod.Name, ContainerName: pod.ContainerName, State: 2, ObservedAt: runningAt}
	report(runningAt, []biz.RuntimeAttempt{running})
	if err := store(stale); err == nil || err.Error() != "NODE_REVISION_CONFLICT" {
		t.Fatalf("new runtime did not fence current-state retirement: %v", err)
	}
	read(biz.AllocationUnknown)
	currentClaims := func(at time.Time) biz.ObservationBatch {
		result := batch()
		for _, claim := range initial.Claims[1:] {
			claim.State, claim.ObservedAt, claim.ReceivedAt = biz.Active, at, at
			result.Claims = append(result.Claims, claim)
		}
		return result
	}
	if err := store(currentClaims(runningAt)); err != nil {
		t.Fatal(err)
	}
	read(biz.Active)

	// A later complete reconciliation can retire the same persisted claims.
	// It is saved with an explicit mode, without manufacturing exit evidence.
	retiredAt := base.Add(3 * time.Second)
	report(retiredAt, nil)
	if err := store(retirement(retiredAt)); err != nil {
		t.Fatal(err)
	}
	released := read(biz.ReleaseConfirmed)
	if released.BindingID != original.BindingID {
		t.Fatal("retirement replaced the binding identity")
	}
	if err := f.db.View(ctx, func(reader biz.Reader) error {
		_, proofs, err := reader.PodEvidence(ctx, pod)
		if err != nil {
			return err
		}
		if len(proofs) != 1 || proofs[0].Retirement != biz.RetirementCurrentState || proofs[0].RuntimeExited || proofs[0].AdmissionRejected {
			t.Fatalf("current retirement did not survive persistence with its own meaning: %+v", proofs)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checkCapacity(true, 0, 4)
	if err := store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID}); err != nil {
		t.Fatal(err)
	}
	read(biz.ReleaseConfirmed)

	// Current-state retirement is not a permanent exemption for this Pod.
	// A newer allocation reuses the binding and is counted again.
	running.ContainerID, running.ObservedAt = "restarted-running", base.Add(4*time.Second)
	report(running.ObservedAt, []biz.RuntimeAttempt{running})
	if err := store(currentClaims(running.ObservedAt)); err != nil {
		t.Fatal(err)
	}
	if active := read(biz.Active); active.BindingID != original.BindingID {
		t.Fatal("new occupancy replaced the binding identity")
	}
	checkCapacity(true, 1, 3)
}
