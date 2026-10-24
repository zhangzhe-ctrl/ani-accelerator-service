package data

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresNodeTelemetryCommitReplayRestartAndFencing(t *testing.T) {
	database := openPGTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: "node-test:" + biz.NewID(), NodeUID: biz.NewID(), NodeName: "node-test", PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "agent-test", ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute)}
	state, err := database.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	report := biz.NodeReport{Epoch: state.Epoch, Sequence: 1, LogID: identity.LogID, PayloadDigest: "record-one", OriginPodUID: identity.PodUID, OriginInstanceID: identity.InstanceID, OriginBootID: identity.BootID, Snapshot: &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now, CaptureEnd: now, Sources: []biz.TelemetrySource{{Name: "cri", Complete: true, ObservedAt: now}}, Containers: []biz.RuntimeAttempt{{ContainerID: "container-one", PodUID: biz.NewID(), ContainerName: "app", State: 2, ObservedAt: now}}}}
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil {
		t.Fatal(err)
	}
	revision := state.CurrentRevision
	reopened := openPGTest(t)
	state, err = reopened.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSequence != 1 || state.LastPayloadDigest != "record-one" {
		t.Fatal("restart lost committed receipt")
	}
	if state.CurrentRevision != revision+1 {
		t.Fatal("new session did not invalidate the prior snapshot revision")
	}
	revision = state.CurrentRevision
	if _, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now); err == nil {
		t.Fatal("old stream not fenced")
	}
	report.Epoch = state.Epoch
	state, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentRevision != revision {
		t.Fatal("ACK loss replay advanced fact revision")
	}
	views, err := reopened.NodeTelemetry(ctx, identity.ConnectionRef)
	if err != nil || len(views) != 1 || len(views[0].Attempts) != 1 {
		t.Fatalf("committed facts missing or duplicated: %v %v", views, err)
	}
	bad := report
	bad.PayloadDigest = "record-different"
	if _, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, bad, now); err == nil {
		t.Fatal("accepted changed same-sequence payload")
	}
	bad = report
	bad.Sequence = 3
	if _, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, bad, now); err == nil {
		t.Fatal("accepted sequence gap")
	}
	if err = reopened.RevokeNodeSession(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now); err == nil {
		t.Fatal("revoked identity still committed")
	}
}

func TestPostgresNodeSessionDropsObsoleteCachePreservesACKAndHistory(t *testing.T) {
	fixture := pgSetup(t)
	database, ctx := fixture.db, context.Background()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: fixture.cluster.ConnectionRef, NodeUID: biz.NewID(), NodeName: "cache-node", PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "agent", ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute)}
	state, err := database.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	oldSnapshot := &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now, CaptureEnd: now, Sources: []biz.TelemetrySource{{Name: "nvml", Complete: true, ObservedAt: now}, {Name: "podresources", Complete: true, ObservedAt: now}, {Name: "cri", Complete: true, ObservedAt: now}}, Devices: []biz.TelemetryDevice{{UUID: "GPU-cache", Model: "test", PhysicalMemoryBytes: 16 << 30}}, Containers: []biz.RuntimeAttempt{{ContainerID: "durable-container", PodUID: biz.NewID(), ContainerName: "train", State: 3, ObservedAt: now, FinishedAt: now}}}
	// Preserve the real source shape: virtual resource IDs are allocation facts,
	// not separate database writes. Their obsolete cache must leave the state.
	for index := 0; index < 3866; index++ {
		oldSnapshot.Allocations = append(oldSnapshot.Allocations, biz.TelemetryAllocation{Namespace: "training", PodName: "train", ContainerName: "train", ResourceName: "volcano.sh/vgpu-memory", DeviceIDs: []string{biz.NewID()}})
	}
	report := biz.NodeReport{Epoch: state.Epoch, Sequence: 1, LogID: identity.LogID, PayloadDigest: "snapshot-one", Snapshot: oldSnapshot, Gap: &biz.TelemetryGap{Sources: []string{"cri"}, Reason: "CURSOR_GAP", Start: now.Add(-time.Second), End: now}}
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil {
		t.Fatal(err)
	}
	recoveryID := biz.NewID()
	pod := biz.PodRef{ClusterID: fixture.cluster.ClusterID, Namespace: "training", Name: "train", UID: oldSnapshot.Containers[0].PodUID, ContainerName: "train"}
	if err = database.RegisterNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch, recoveryID, pod, []string{strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	prepared := biz.ObservationBatch{ConnectionRef: identity.ConnectionRef, ClusterID: fixture.cluster.ClusterID, NodeRevisions: map[string]uint64{identity.NodeUID: state.CurrentRevision}}
	if err = database.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, prepared) }); err != nil {
		t.Fatal(err)
	}
	read := func() biz.NodeTelemetryView {
		t.Helper()
		views, err := database.NodeTelemetry(ctx, identity.ConnectionRef)
		if err != nil || len(views) != 1 {
			t.Fatalf("read durable telemetry: count=%d error=%v", len(views), err)
		}
		return views[0]
	}
	before := read()
	state, err = database.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot != nil {
		t.Fatal("new epoch retained an obsolete snapshot cache")
	}
	if state.Epoch != before.State.Epoch+1 || state.CurrentRevision != before.State.CurrentRevision+1 || state.ConsumedRevision != before.State.ConsumedRevision || state.CurrentRevision == state.ConsumedRevision {
		t.Fatal("cache invalidation did not fence the consumed revision exactly once")
	}
	if state.Identity.LogID != identity.LogID || state.LastSequence != before.State.LastSequence || state.LastPayloadDigest != before.State.LastPayloadDigest || !reflect.DeepEqual(state.Gaps, before.State.Gaps) || !reflect.DeepEqual(state.RecoveryRequests, before.State.RecoveryRequests) || !reflect.DeepEqual(read().Attempts, before.Attempts) {
		t.Fatal("new session changed durable receipt, history or recovery scope")
	}
	if err = database.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, prepared) }); err == nil || err.Error() != "NODE_REVISION_CONFLICT" {
		t.Fatalf("old observation survived session cache invalidation: %v", err)
	}
	// A lost ACK is replayed under the new stream without losing its original
	// immutable payload identity or resurrecting its previous-epoch snapshot.
	report.Epoch = state.Epoch
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil || state.Snapshot != nil || state.LastSequence != 1 {
		t.Fatalf("ACK replay restored obsolete cache: sequence=%d snapshot=%v error=%v", state.LastSequence, state.Snapshot != nil, err)
	}
	report.Sequence, report.PayloadDigest = 2, "old-epoch-next-record"
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil || state.Snapshot != nil || state.LastSequence != 2 || !reflect.DeepEqual(read().Attempts, before.Attempts) {
		t.Fatalf("old FIFO report lost history or restored cache: sequence=%d snapshot=%v error=%v", state.LastSequence, state.Snapshot != nil, err)
	}
	oldRevision := state.CurrentRevision
	state, err = database.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil || state.CurrentRevision != oldRevision || state.Snapshot != nil {
		t.Fatalf("empty-cache reconnect manufactured a fact change: %v", err)
	}
	fresh := *oldSnapshot
	fresh.CaptureEpoch = state.Epoch
	fresh.CaptureStart, fresh.CaptureEnd = now.Add(time.Millisecond), now.Add(time.Millisecond)
	report.Epoch, report.Sequence, report.PayloadDigest, report.Snapshot = state.Epoch, 3, "current-epoch-snapshot", &fresh
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now.Add(time.Millisecond))
	if err != nil || state.Snapshot == nil || state.Snapshot.CaptureEpoch != state.Epoch || state.LastSequence != 3 {
		t.Fatalf("current epoch snapshot could not restore source evidence: %v", err)
	}
}
