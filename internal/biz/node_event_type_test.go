package biz

import (
	"testing"
	"time"
)

func TestNodeSnapshotPreservesRuntimeEventTypeWithoutRevisionChurn(testContext *testing.T) {
	now := time.Now().UTC()
	started := uint32(2)
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 1}
	attempt := RuntimeAttempt{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: 2, ObservedAt: now, EventType: &started}
	report := NodeReport{Epoch: 1, Sequence: 1, LogID: "log", PayloadDigest: "event", Attempts: []RuntimeAttempt{attempt}}
	attempts, err := MergeNodeReport(&state, nil, report, now)
	if err != nil {
		testContext.Fatal(err)
	}
	revision := state.CurrentRevision
	attempt.EventType = nil
	attempt.ObservedAt = now.Add(time.Second)
	report.Sequence = 2
	report.PayloadDigest = "snapshot"
	report.Attempts = nil
	report.Snapshot = &TelemetrySnapshot{CaptureEpoch: 1, CaptureStart: now.Add(time.Second), CaptureEnd: now.Add(time.Second), Containers: []RuntimeAttempt{attempt}}
	attempts, err = MergeNodeReport(&state, attempts, report, now.Add(time.Second))
	if err != nil {
		testContext.Fatal(err)
	}
	if attempts[0].EventType == nil || *attempts[0].EventType != started || state.CurrentRevision != revision {
		testContext.Fatal("ordinary snapshot erased event type or invalidated unchanged fact revision")
	}
	deleted := uint32(4)
	attempt.EventType = &deleted
	report.Sequence = 3
	report.PayloadDigest = "new-event"
	report.Snapshot = nil
	report.Attempts = []RuntimeAttempt{attempt}
	attempts, err = MergeNodeReport(&state, attempts, report, now.Add(time.Second))
	if err != nil {
		testContext.Fatal(err)
	}
	if *attempts[0].EventType != deleted || state.CurrentRevision != revision+1 {
		testContext.Fatal("new original event type was overwritten or ignored")
	}
}
