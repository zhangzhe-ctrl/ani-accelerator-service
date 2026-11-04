package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
)

func TestProviderRestartReconcilesOnlyOldUIDFromCurrentState(t *testing.T) {
	providerControlledSources(t, "v1.16.0", "restart_release")
}

func providerRestartReleaseContract(t *testing.T, database *data.Postgres, adapter *Provider, cluster biz.Cluster, group biz.SupplyGroup, oldPod biz.PodRef, replacementUID string, state *replayState, telemetry *nodeTelemetryFixture, capacity func() *biz.CapacityView) {
	t.Helper()
	ctx := context.Background()
	state.setFault("replacement_pod")
	oldAttempt := telemetry.state.Snapshot.Containers[0]
	oldAttempt.PodUID, oldAttempt.PodNamespace, oldAttempt.PodName, oldAttempt.ContainerName = oldPod.UID, oldPod.Namespace, oldPod.Name, oldPod.ContainerName
	oldAttempt.SandboxID = strings.Repeat("d", 64)
	newAttempt := oldAttempt
	newAttempt.PodUID, newAttempt.ContainerID, newAttempt.SandboxID = replacementUID, strings.Repeat("e", 64), strings.Repeat("f", 64)
	newAttempt.State = 2
	replacementEnvironment := append([]biz.GPUEnvironmentEntry{{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"}}, oldAttempt.GPUEnvironment...)
	newAttempt.GPUEnvironment = append([]biz.GPUEnvironmentEntry(nil), replacementEnvironment...)

	writeReport := func(snapshot *biz.TelemetrySnapshot, attempts []biz.RuntimeAttempt, gap *biz.TelemetryGap) {
		t.Helper()
		report := biz.NodeReport{Epoch: telemetry.state.Epoch, Sequence: telemetry.state.LastSequence + 1, LogID: telemetry.identity.LogID, OriginPodUID: telemetry.identity.PodUID, OriginInstanceID: telemetry.identity.InstanceID, OriginBootID: telemetry.identity.BootID, Snapshot: snapshot, Attempts: attempts, Gap: gap}
		report.PayloadDigest, _ = biz.Hash(report)
		var err error
		telemetry.state, err = database.SaveNodeReport(ctx, cluster.ConnectionRef, telemetry.source.UID, report, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
	}
	initial := *telemetry.state.Snapshot
	writeCurrent := func(oldState uint32) {
		t.Helper()
		now := time.Now().UTC()
		snapshot := initial
		snapshot.CaptureStart, snapshot.CaptureEnd = now, now
		snapshot.Sources = append([]biz.TelemetrySource(nil), initial.Sources...)
		for index := range snapshot.Sources {
			snapshot.Sources[index].ObservedAt = now
		}
		newAttempt.ObservedAt = now
		snapshot.Containers = []biz.RuntimeAttempt{newAttempt}
		if oldState != 0 {
			current := oldAttempt
			current.State, current.ObservedAt = oldState, now
			current.FinishedAt = time.Time{}
			snapshot.Containers = append(snapshot.Containers, current)
		}
		writeReport(&snapshot, nil, nil)
	}
	collect := func(scope *biz.ReleaseRecheckScope) biz.ObservationBatch {
		t.Helper()
		var batch biz.ObservationBatch
		var err error
		if scope == nil {
			batch, err = adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
		} else {
			batch, err = adapter.CollectRelease(ctx, cluster, []biz.SupplyGroup{group}, scope)
		}
		if err != nil {
			t.Fatal(err)
		}
		return batch
	}
	save := func(batch biz.ObservationBatch) {
		t.Helper()
		if err := database.Transact(ctx, func(transaction biz.Transaction) error { return transaction.SaveObservation(ctx, batch) }); err != nil {
			t.Fatal(err)
		}
	}

	// These incomplete attempts are durable diagnostic history. None has a
	// FinishedAt or journal record, and a later full snapshot supersedes them.
	historicalIDs := map[string]bool{}
	var history []biz.RuntimeAttempt
	observed := time.Now().UTC()
	for index := 0; index < 37; index++ {
		attempt := oldAttempt
		attempt.ContainerID = fmt.Sprintf("%064x", index+1)
		if index == 0 {
			attempt.ContainerID = oldAttempt.ContainerID
		}
		attempt.Attempt, attempt.State, attempt.ObservedAt = uint32(index), 4, observed
		attempt.FinishedAt = time.Time{}
		if index == 1 {
			attempt.PodUID, attempt.ContainerName = "", ""
		}
		history = append(history, attempt)
		historicalIDs[attempt.ContainerID] = true
	}
	writeReport(nil, history, nil)
	for _, test := range []struct {
		name     string
		oldState uint32
		change   func()
	}{
		{name: "old_current_created", oldState: 1},
		{name: "old_current_running", oldState: 2},
		{name: "old_current_unknown", oldState: 4},
		{name: "replacement_not_running", change: func() { newAttempt.State = 1 }},
		{name: "replacement_effective_gpu_unknown", change: func() {
			newAttempt.GPUEnvironment = append(newAttempt.GPUEnvironment, biz.GPUEnvironmentEntry{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			newAttempt.State = 2
			newAttempt.GPUEnvironment = append([]biz.GPUEnvironmentEntry(nil), replacementEnvironment...)
			if test.change != nil {
				test.change()
			}
			writeCurrent(test.oldState)
			if batch := collect(nil); len(batch.Releases) != 0 {
				t.Fatalf("unresolved current old/new UID state released allocation: %+v", batch.Releases)
			}
		})
	}
	newAttempt.State = 2
	newAttempt.GPUEnvironment = append([]biz.GPUEnvironmentEntry(nil), replacementEnvironment...)
	gap := biz.TelemetryGap{Sources: []string{"cri_events"}, Reason: "EVENT_HISTORY_UNAVAILABLE", Start: time.Now().Add(-time.Hour).UTC(), DiscoveredAt: time.Now().UTC()}
	writeReport(nil, nil, &gap)
	writeCurrent(0)
	batch := collect(nil)
	if len(batch.Releases) != 1 || batch.Releases[0].Pod != oldPod || batch.Releases[0].Retirement != biz.RetirementCurrentState || batch.Releases[0].RuntimeExited {
		t.Fatalf("same-name replacement or incomplete history blocked current reconciliation: %+v", batch.Releases)
	}

	// A newer running event must invalidate both the in-flight transaction and
	// another collection against the still older empty-old-UID snapshot.
	newRunning := oldAttempt
	newRunning.State, newRunning.ObservedAt = 2, time.Now().UTC()
	writeReport(nil, []biz.RuntimeAttempt{newRunning}, nil)
	if err := database.Transact(ctx, func(transaction biz.Transaction) error { return transaction.SaveObservation(ctx, batch) }); err == nil || err.Error() != "NODE_REVISION_CONFLICT" {
		t.Fatalf("new running event did not invalidate old reconciliation: %v", err)
	}
	if current := collect(nil); len(current.Releases) != 0 {
		t.Fatalf("old snapshot hid a newer running event: %+v", current.Releases)
	}
	writeCurrent(0)
	batch = collect(nil)
	if len(batch.Releases) != 1 || batch.Releases[0].Pod != oldPod {
		t.Fatalf("next complete snapshot did not recover reconciliation: %+v", batch.Releases)
	}
	save(batch)

	var owner biz.UsageRef
	if err := database.View(ctx, func(reader biz.Reader) error {
		bindings, err := reader.Bindings(ctx, biz.BindingFilter{ClusterID: cluster.ClusterID}, "", 100)
		if err != nil {
			return err
		}
		oldReleased, replacementActive := false, false
		for _, binding := range bindings {
			if binding.Pod != nil && binding.Pod.UID == oldPod.UID {
				oldReleased = binding.State == biz.ReleaseConfirmed
			}
			if binding.Pod != nil && binding.Pod.UID == replacementUID {
				replacementActive = binding.State == biz.Active
			}
		}
		if !oldReleased || !replacementActive {
			t.Fatalf("restart lost UID-scoped occupancy: old_released=%t replacement_active=%t", oldReleased, replacementActive)
		}
		claims, _, err := reader.PodEvidence(ctx, oldPod)
		if err != nil {
			return err
		}
		if len(claims) == 0 || claims[0].OwnerRef == nil {
			t.Fatal("old pod lost authenticated owner")
		}
		owner = *claims[0].OwnerRef
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view := capacity()
	if !view.Observation.Complete || view.KnownCommittedUnits != 1 || view.KnownFreeUnits != 7 {
		t.Fatalf("old UID reconciliation lost or freed the running replacement: %+v", view)
	}
	views, err := database.NodeTelemetry(ctx, cluster.ConnectionRef)
	if err != nil || len(views) != 1 {
		t.Fatalf("cannot inspect retained node history: views=%d error=%v", len(views), err)
	}
	retained := 0
	for _, attempt := range views[0].Attempts {
		if historicalIDs[attempt.ContainerID] {
			retained++
			if !attempt.FinishedAt.IsZero() || len(attempt.JournalProvenance) != 0 {
				t.Fatal("reconciliation fabricated historical exit provenance")
			}
		}
	}
	if retained != 37 || views[0].State.Gaps["cri_events"].Reason != gap.Reason {
		t.Fatal("current reconciliation erased diagnostic attempts or event gaps")
	}

	scope := &biz.ReleaseRecheckScope{Owner: owner, Pods: []biz.PodRef{oldPod}}
	for _, currentState := range []uint32{1, 2, 4} {
		writeCurrent(currentState)
		if current := collect(scope); len(current.Releases) != 0 {
			t.Fatalf("previous retirement replaced current old-UID state %d: %+v", currentState, current.Releases)
		}
	}
	writeCurrent(0)
	batch = collect(scope)
	if len(batch.Releases) != 1 || batch.Releases[0].Pod != oldPod || batch.Releases[0].CheckedNodeRevision != telemetry.state.CurrentRevision {
		t.Fatalf("scoped refresh did not converge on the latest current snapshot: %+v", batch.Releases)
	}
	save(batch)
}
