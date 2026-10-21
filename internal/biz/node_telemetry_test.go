package biz

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNodeJournalCoveragePersistsOnlyForIdenticalOrdinaryExit(t *testing.T) {
	now := time.Now().UTC()
	previous := RuntimeAttempt{ContainerID: "container", SandboxID: "sandbox", PodUID: "pod", PodNamespace: "tenant", PodName: "workload", ContainerName: "app", State: 3, ObservedAt: now.Add(-time.Second), FinishedAt: now.Add(-2 * time.Second), JournalObservedAt: now.Add(-time.Second), JournalProvenance: json.RawMessage(`{"positive":"chain"}`)}
	for _, scenario := range []struct {
		name   string
		change func(*RuntimeAttempt)
		keep   bool
	}{
		{name: "same_exit", keep: true},
		{name: "running", change: func(attempt *RuntimeAttempt) { attempt.State = 2 }},
		{name: "unknown", change: func(attempt *RuntimeAttempt) { attempt.State = 4 }},
		{name: "different_pod", change: func(attempt *RuntimeAttempt) { attempt.PodUID = "other" }},
		{name: "different_namespace", change: func(attempt *RuntimeAttempt) { attempt.PodNamespace = "other" }},
		{name: "different_name", change: func(attempt *RuntimeAttempt) { attempt.PodName = "other" }},
		{name: "different_container", change: func(attempt *RuntimeAttempt) { attempt.ContainerName = "other" }},
		{name: "different_sandbox", change: func(attempt *RuntimeAttempt) { attempt.SandboxID = "other" }},
		{name: "different_exit", change: func(attempt *RuntimeAttempt) { attempt.FinishedAt = now.Add(-3 * time.Second) }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 1}
			incoming := previous
			incoming.ObservedAt, incoming.JournalObservedAt, incoming.JournalProvenance = now, time.Time{}, nil
			if scenario.change != nil {
				scenario.change(&incoming)
			}
			report := NodeReport{Epoch: 1, Sequence: 1, LogID: "log", PayloadDigest: "ordinary", Attempts: []RuntimeAttempt{incoming}}
			merged, err := MergeNodeReport(&state, []RuntimeAttempt{previous}, report, now)
			if err != nil {
				t.Fatal(err)
			}
			kept := merged[0].JournalObservedAt.Equal(previous.JournalObservedAt) && string(merged[0].JournalProvenance) == string(previous.JournalProvenance)
			if kept != scenario.keep || (!scenario.keep && !merged[0].JournalObservedAt.IsZero()) {
				t.Fatalf("ordinary state inherited journal coverage: %+v", merged[0])
			}
			if scenario.keep && state.CurrentRevision != 1 {
				t.Fatal("ordinary observation changed coverage meaning")
			}
		})
	}
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 1}
	recovered := previous
	recovered.ObservedAt, recovered.JournalObservedAt = now, now
	_, err := MergeNodeReport(&state, []RuntimeAttempt{previous}, NodeReport{Epoch: 1, Sequence: 1, LogID: "log", PayloadDigest: "new-journal", Attempts: []RuntimeAttempt{recovered}}, now)
	if err != nil || state.CurrentRevision != 2 {
		t.Fatalf("new complete journal coverage did not advance revision: %v revision=%d", err, state.CurrentRevision)
	}
}

func TestNodeTelemetryReceiptAndMeaningfulRevision(t *testing.T) {
	now := time.Now().UTC()
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 1}
	report := NodeReport{Epoch: 1, Sequence: 1, LogID: "log", PayloadDigest: "first", Snapshot: &TelemetrySnapshot{CaptureEpoch: 1, CaptureStart: now, CaptureEnd: now, Sources: []TelemetrySource{{Name: "cri", ObservedAt: now, Complete: true}}, Containers: []RuntimeAttempt{{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: 2, ObservedAt: now, StartedAt: now.Add(-time.Minute)}}}}
	attempts, err := MergeNodeReport(&state, nil, report, now)
	if err != nil {
		t.Fatal(err)
	}
	revision := state.CurrentRevision
	if revision != 2 {
		t.Fatalf("initial facts revision=%d", revision)
	}
	for sequence := uint64(2); sequence <= 8; sequence++ {
		sampled := now.Add(time.Duration(sequence) * time.Second)
		copySnapshot := *report.Snapshot
		copySnapshot.CaptureStart, copySnapshot.CaptureEnd = sampled, sampled
		copySnapshot.Sources = []TelemetrySource{{Name: "cri", ObservedAt: sampled, Complete: true}}
		copySnapshot.Containers = append([]RuntimeAttempt(nil), report.Snapshot.Containers...)
		copySnapshot.Containers[0].ObservedAt = sampled
		report.Snapshot = &copySnapshot
		report.Sequence, report.PayloadDigest = sequence, "next"
		attempts, err = MergeNodeReport(&state, attempts, report, sampled)
		if err != nil {
			t.Fatal(err)
		}
		if state.CurrentRevision != revision {
			t.Fatalf("unchanged sampling invalidated revision at %d", sequence)
		}
	}
	changed := report
	changed.Sequence, changed.PayloadDigest = 9, "changed"
	changed.Snapshot = nil
	changed.Attempts = []RuntimeAttempt{{ContainerID: "new-container", PodUID: "pod", ContainerName: "app", State: 4, ObservedAt: now.Add(9 * time.Second)}}
	attempts, err = MergeNodeReport(&state, attempts, changed, now.Add(9*time.Second))
	if err != nil || state.CurrentRevision != revision+1 {
		t.Fatalf("new UNKNOWN must invalidate revision: %d %v", state.CurrentRevision, err)
	}
	if _, err = MergeNodeReport(&state, attempts, changed, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if state.CurrentRevision != revision+1 {
		t.Fatal("ACK replay advanced revision")
	}
	conflict := changed
	conflict.PayloadDigest = "different"
	if _, err = MergeNodeReport(&state, attempts, conflict, now); err == nil {
		t.Fatal("accepted same sequence with different content")
	}
	compacted := report
	compacted.Sequence = 1
	if _, err = MergeNodeReport(&state, attempts, compacted, now); err == nil {
		t.Fatal("accepted compacted sequence")
	}
	outOfOrder := report
	outOfOrder.Sequence = 11
	if _, err = MergeNodeReport(&state, attempts, outOfOrder, now); err == nil {
		t.Fatal("accepted sequence gap")
	}
}

func TestNodeTelemetryOldCaptureCannotRefreshCurrentOrEraseRunning(t *testing.T) {
	now := time.Now().UTC()
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 2, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 3, Snapshot: &TelemetrySnapshot{CaptureEpoch: 2, CaptureEnd: now}}
	attempts := []RuntimeAttempt{{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: 2, ObservedAt: now}}
	report := NodeReport{Epoch: 2, Sequence: 1, LogID: "log", PayloadDigest: "old", Snapshot: &TelemetrySnapshot{CaptureEpoch: 1, CaptureEnd: now.Add(time.Second), Containers: []RuntimeAttempt{{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: 3, ObservedAt: now.Add(-time.Second), FinishedAt: now.Add(-2 * time.Second)}}}}
	merged, err := MergeNodeReport(&state, attempts, report, now)
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot.CaptureEpoch != 2 || !state.Snapshot.CaptureEnd.Equal(now) || merged[0].State != 2 {
		t.Fatal("old capture refreshed current facts or erased Running")
	}
	report.Sequence, report.PayloadDigest = 2, "gap"
	report.Snapshot = nil
	report.Gap = &TelemetryGap{Sources: []string{"cri_events"}, Reason: "EVENT_STREAM_INTERRUPTED", DiscoveredAt: now}
	merged, err = MergeNodeReport(&state, merged, report, now)
	if err != nil {
		t.Fatal(err)
	}
	report.Sequence, report.PayloadDigest = 3, "empty"
	report.Gap = nil
	report.Snapshot = &TelemetrySnapshot{CaptureEpoch: 2, CaptureEnd: now}
	if _, err = MergeNodeReport(&state, merged, report, now); err != nil {
		t.Fatal(err)
	}
	if _, found := state.Gaps["cri_events"]; !found {
		t.Fatal("empty snapshot erased source gap")
	}
}
