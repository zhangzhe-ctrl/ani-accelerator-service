package data

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresNodeRevisionAllowsRefreshAndRejectsChangedFacts(t *testing.T) {
	fixture := pgSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	identities := []biz.NodeIdentity{}
	states := []biz.NodeTelemetryState{}
	for index := 0; index < 2; index++ {
		identity := biz.NodeIdentity{ConnectionRef: fixture.cluster.ConnectionRef, NodeUID: biz.NewID(), NodeName: "revision-node", PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "agent", ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute)}
		state, err := fixture.db.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, identity)
		states = append(states, state)
	}
	report := func(index int, attempts []biz.RuntimeAttempt) {
		t.Helper()
		observed := time.Now().UTC()
		state := states[index]
		snapshot := &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: observed, CaptureEnd: observed, Sources: []biz.TelemetrySource{{Name: "cri", ObservedAt: observed, Complete: true}, {Name: "podresources", ObservedAt: observed, Complete: true}}, Containers: attempts}
		digest, _ := biz.Hash(snapshot)
		identity := identities[index]
		next, err := fixture.db.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, biz.NodeReport{Epoch: state.Epoch, Sequence: state.LastSequence + 1, LogID: identity.LogID, PayloadDigest: digest, Snapshot: snapshot}, observed)
		if err != nil {
			t.Fatal(err)
		}
		states[index] = next
	}
	batch := func() biz.ObservationBatch {
		result := biz.ObservationBatch{ConnectionRef: fixture.cluster.ConnectionRef, ClusterID: fixture.cluster.ClusterID, NodeRevisions: map[string]uint64{}}
		for index, identity := range identities {
			result.NodeRevisions[identity.NodeUID] = states[index].CurrentRevision
		}
		return result
	}
	store := func(value biz.ObservationBatch) error {
		return fixture.db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, value) })
	}
	for index := range identities {
		report(index, nil)
	}
	for iteration := 0; iteration < 3; iteration++ {
		prepared := batch()
		for index := range identities {
			report(index, nil)
			if states[index].CurrentRevision != prepared.NodeRevisions[identities[index].NodeUID] {
				t.Fatal("ordinary snapshot refresh changed domain revision")
			}
		}
		if err := store(prepared); err != nil {
			t.Fatalf("continuous unchanged reports starved atomic consumption: %v", err)
		}
	}
	stale := batch()
	running := biz.RuntimeAttempt{ContainerID: "new-running", PodUID: biz.NewID(), ContainerName: "inference", State: 2, ObservedAt: time.Now().UTC()}
	report(0, []biz.RuntimeAttempt{running})
	if err := store(stale); err == nil || err.Error() != "NODE_REVISION_CONFLICT" {
		t.Fatalf("new Running did not fence old batch: %v", err)
	}
	unknownBatch := batch()
	running.State = 4
	running.ObservedAt = time.Now().UTC()
	running.Issues = []string{"CONTROLLED_RUNTIME_UNKNOWN"}
	report(0, []biz.RuntimeAttempt{running})
	if err := store(unknownBatch); err == nil || err.Error() != "NODE_REVISION_CONFLICT" {
		t.Fatalf("new UNKNOWN did not fence old batch: %v", err)
	}
	running.State = 3
	running.Issues = nil
	running.FinishedAt = time.Now().UTC()
	running.ObservedAt = running.FinishedAt
	report(0, []biz.RuntimeAttempt{running})
	clean := batch()
	for index := range identities {
		if index == 0 {
			running.ObservedAt = time.Now().UTC()
			report(index, []biz.RuntimeAttempt{running})
		} else {
			report(index, nil)
		}
	}
	if err := store(clean); err != nil {
		t.Fatalf("legitimate clean state could not converge while snapshots continued: %v", err)
	}
	views, err := fixture.db.NodeTelemetry(ctx, fixture.cluster.ConnectionRef)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.State.ConsumedRevision != view.State.CurrentRevision {
			t.Fatal("successful transaction did not consume complete node revision")
		}
	}
}
