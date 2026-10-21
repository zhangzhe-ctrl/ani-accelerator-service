package provider

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	nodepb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/node/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type restartExitRecovery struct {
	requestPod biz.PodRef
	attempt    biz.RuntimeAttempt
	knownIDs   []string
	calls      int
	complete   bool
	persist    func()
}

func (recovery *restartExitRecovery) RecoverNodeExit(_ context.Context, _, _ string, pod biz.PodRef, known []string) ([]biz.RuntimeAttempt, bool, error) {
	recovery.calls++
	recovery.knownIDs = append([]string(nil), known...)
	if pod != recovery.requestPod || !reflect.DeepEqual(known, []string{recovery.attempt.ContainerID}) || !recovery.complete {
		return nil, false, nil
	}
	if recovery.persist != nil {
		persist := recovery.persist
		recovery.persist = nil
		persist()
	}
	return []biz.RuntimeAttempt{recovery.attempt}, true, nil
}

func TestProviderRestartReleasesOnlyOldUIDWithPositiveJournal(t *testing.T) {
	providerControlledSources(t, "v1.16.0", "restart_release")
}

func providerRestartReleaseContract(t *testing.T, database *data.Postgres, adapter *Provider, cluster biz.Cluster, group biz.SupplyGroup, oldPod biz.PodRef, replacementUID string, state *replayState, telemetry *nodeTelemetryFixture) {
	t.Helper()
	ctx := context.Background()
	state.setFault("replacement_pod")
	oldAttempt := telemetry.state.Snapshot.Containers[0]
	oldAttempt.PodUID, oldAttempt.PodNamespace, oldAttempt.PodName, oldAttempt.ContainerName = oldPod.UID, oldPod.Namespace, oldPod.Name, oldPod.ContainerName
	oldAttempt.SandboxID = strings.Repeat("d", 64)
	newAttempt := oldAttempt
	newAttempt.PodUID, newAttempt.ContainerID, newAttempt.SandboxID = replacementUID, strings.Repeat("e", 64), strings.Repeat("f", 64)
	newAttempt.State = 2
	writeReport := func(snapshot *biz.TelemetrySnapshot, attempts []biz.RuntimeAttempt) {
		t.Helper()
		report := biz.NodeReport{Epoch: telemetry.state.Epoch, Sequence: telemetry.state.LastSequence + 1, LogID: telemetry.identity.LogID, OriginPodUID: telemetry.identity.PodUID, OriginInstanceID: telemetry.identity.InstanceID, OriginBootID: telemetry.identity.BootID, Snapshot: snapshot, Attempts: attempts}
		report.PayloadDigest, _ = biz.Hash(report)
		var err error
		telemetry.state, err = database.SaveNodeReport(ctx, cluster.ConnectionRef, telemetry.source.UID, report, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
	}
	initial := *telemetry.state.Snapshot
	writeFacts := func(oldRunning bool) {
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
		unknown := oldAttempt
		unknown.State, unknown.ObservedAt = 4, now
		unknown.Issues = []string{"CRI_STATUS_UNAVAILABLE"}
		if oldRunning {
			unknown.State = 2
			snapshot.Containers = append(snapshot.Containers, unknown)
		}
		sandbox := biz.RuntimeAttempt{ContainerID: oldAttempt.SandboxID, SandboxID: oldAttempt.SandboxID, PodUID: oldPod.UID, PodNamespace: oldPod.Namespace, PodName: oldPod.Name, State: 4, ObservedAt: now, Issues: []string{"RUNTIME_METADATA_UNKNOWN"}}
		writeReport(&snapshot, []biz.RuntimeAttempt{unknown, sandbox})
	}
	now := time.Now().UTC()
	exited := oldAttempt
	exited.State, exited.ObservedAt, exited.CreatedAt, exited.FinishedAt = 3, now, now.Add(-time.Minute), now.Add(-time.Second)
	provenance := &nodepb.JournalProvenance{MachineId: strings.Repeat("a", 32), BootId: strings.Repeat("b", 32), Unit: "containerd.service", Comm: "containerd", Uid: "0", Exe: "/usr/bin/containerd", Transport: "stdout", SandboxId: oldAttempt.SandboxID, SandboxCursor: "sandbox-cursor", CreateCursor: "create-cursor", Cursor: "exit-cursor", SandboxMonotonic: 1, CreateMonotonic: 2, ExitMonotonic: 3, CreatedAt: timestamppb.New(exited.CreatedAt), ExitedAt: timestamppb.New(exited.FinishedAt)}
	exited.JournalProvenance, _ = json.Marshal(provenance)
	recovery := &restartExitRecovery{requestPod: oldPod, attempt: exited, complete: true}
	adapter.ConfigureNodeExitRecovery(recovery)
	for _, test := range []struct {
		name       string
		oldRunning bool
		change     func()
	}{
		{name: "old_current_running_survives_historical_exit", oldRunning: true},
		{name: "incomplete_journal", change: func() { recovery.complete = false }},
		{name: "unexplained_sandbox", change: func() { recovery.attempt.SandboxID = strings.Repeat("1", 64) }},
		{name: "sandbox_id_without_positive_chain", change: func() {
			recovery.attempt.JournalProvenance = json.RawMessage(`{"sandbox_id":"` + oldAttempt.SandboxID + `"}`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			recovery.attempt, recovery.complete = exited, true
			if test.change != nil {
				test.change()
			}
			writeFacts(test.oldRunning)
			batch, err := adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
			if err != nil || len(batch.Releases) != 0 {
				t.Fatalf("insufficient old-UID evidence released allocation: %v %+v", err, batch.Releases)
			}
		})
	}
	recovery.attempt, recovery.complete = exited, true
	writeFacts(false)
	recovery.persist = func() {
		persisted := exited
		persisted.ObservedAt = time.Now().UTC()
		writeReport(nil, []biz.RuntimeAttempt{persisted})
	}
	batch, err := adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
	if err != nil || len(batch.Releases) != 1 || recovery.calls == 0 || batch.Releases[0].Pod != oldPod {
		t.Fatalf("same-name replacement prevented positive old-UID recovery: calls=%d error=%v releases=%+v", recovery.calls, err, batch.Releases)
	}
	if err = database.Transact(ctx, func(transaction biz.Transaction) error { return transaction.SaveObservation(ctx, batch) }); err == nil || err.Error() != "NODE_REVISION_CONFLICT" {
		t.Fatalf("journal ingestion did not invalidate pre-recovery revision: %v", err)
	}
	batch, err = adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Transact(ctx, func(transaction biz.Transaction) error { return transaction.SaveObservation(ctx, batch) }); err != nil {
		t.Fatal(err)
	}
	if err = database.View(ctx, func(reader biz.Reader) error {
		bindings, readError := reader.Bindings(ctx, biz.BindingFilter{ClusterID: cluster.ClusterID}, "", 100)
		if readError != nil {
			return readError
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
			t.Fatalf("restart did not preserve UID-scoped occupancy: old_released=%t replacement_active=%t", oldReleased, replacementActive)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	views, err := database.NodeTelemetry(ctx, cluster.ConnectionRef)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range views[0].Attempts {
		if attempt.ContainerID == oldAttempt.SandboxID && (attempt.State != 4 || attempt.ContainerName != "") {
			t.Fatal("positive sandbox explanation rewrote historical unknown event")
		}
	}
}
