package biz

import (
	"testing"
	"time"
)

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
