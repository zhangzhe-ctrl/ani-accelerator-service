package provider

import (
	"fmt"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func currentTelemetryView(now time.Time) biz.NodeTelemetryView {
	return biz.NodeTelemetryView{State: biz.NodeTelemetryState{Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 7, RuntimeUpdates: map[string]biz.RuntimeAttempt{}, Snapshot: &biz.TelemetrySnapshot{CaptureEpoch: 1, CaptureStart: now.Add(-time.Second), CaptureEnd: now, Sources: []biz.TelemetrySource{{Name: "cri", ObservedAt: now, Complete: true}, {Name: "podresources", ObservedAt: now, Complete: true}}}}}
}

func TestTelemetryCurrentSnapshotExcludesCoveredHistoryAndGaps(t *testing.T) {
	now := time.Now().UTC()
	view := currentTelemetryView(now)
	for index := 0; index < 37; index++ {
		view.Attempts = append(view.Attempts, biz.RuntimeAttempt{ContainerID: fmt.Sprintf("historical-%02d", index), State: 4, ObservedAt: now.Add(-time.Hour)})
	}
	view.State.Gaps = map[string]biz.TelemetryGap{"cri_events": {Sources: []string{"cri_events"}, Start: now.Add(-time.Hour), Reason: "OLD_EVENT_GAP"}}
	snapshot := telemetryNodeSnapshot(view, now, time.Minute)
	if !snapshot.RuntimeOK || !snapshot.ResourcesOK || len(snapshot.Containers) != 0 || len(snapshot.UnknownPodContainers) != 0 {
		t.Fatalf("historical unknowns or identity gaps poisoned complete current sources: %+v", snapshot)
	}
	if !snapshot.CaptureStart.Equal(view.State.Snapshot.CaptureStart) || snapshot.NodeRevision != view.State.CurrentRevision {
		t.Fatal("projection lost the capture boundary or transaction revision")
	}
	statuses, complete := runtimeFor(snapshot, "old-pod", "app")
	if !complete || len(statuses) != 0 || len(view.Attempts) != 37 {
		t.Fatal("current absence still requires historical exits or removed retained history")
	}
}

func TestTelemetryCurrentSnapshotIncludesUncoveredRuntimeUpdates(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		state uint32
		want  cri.ContainerState
	}{{"created", 1, cri.ContainerState_CONTAINER_CREATED}, {"running", 2, cri.ContainerState_CONTAINER_RUNNING}, {"unknown", 4, cri.ContainerState_CONTAINER_UNKNOWN}} {
		t.Run(scenario.name, func(t *testing.T) {
			now := time.Now().UTC()
			view := currentTelemetryView(now)
			update := biz.RuntimeAttempt{ContainerID: "new-container", PodUID: "pod", PodNamespace: "tenant", PodName: "workload", ContainerName: "app", State: scenario.state, ObservedAt: now.Add(time.Millisecond)}
			view.Attempts = []biz.RuntimeAttempt{update}
			view.State.RuntimeUpdates[update.ContainerID] = update
			snapshot := telemetryNodeSnapshot(view, now.Add(time.Second), time.Minute)
			statuses, complete := runtimeFor(snapshot, "pod", "app")
			if !complete || len(statuses) != 1 || statuses[0].Status.State != scenario.want || snapshot.HistoricalStatuses[update.ContainerID] {
				t.Fatalf("new runtime update disappeared behind an empty snapshot: complete=%t statuses=%+v", complete, statuses)
			}
			if !runtimeObservation(snapshot, statuses[0]).Equal(update.ObservedAt) {
				t.Fatal("current update was relabeled with the older snapshot time")
			}
		})
	}
}

func TestTelemetryCurrentIdentityAndSourceFailuresRemainUnknown(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*biz.NodeTelemetryView, time.Time)
	}{
		{"current_identity_missing", func(view *biz.NodeTelemetryView, now time.Time) {
			view.State.Snapshot.Containers = []biz.RuntimeAttempt{{ContainerID: "current-container", ContainerName: "app", State: 2, ObservedAt: now}}
		}},
		{"source_incomplete", func(view *biz.NodeTelemetryView, _ time.Time) { view.State.Snapshot.Sources[0].Complete = false }},
		{"source_stale", func(view *biz.NodeTelemetryView, now time.Time) { view.State.Snapshot.Sources[0].ObservedAt = now.Add(-2 * time.Minute) }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			now := time.Now().UTC()
			view := currentTelemetryView(now)
			scenario.change(&view, now)
			snapshot := telemetryNodeSnapshot(view, now, time.Minute)
			if snapshot.RuntimeOK {
				t.Fatal("incomplete current runtime was treated as complete absence")
			}
			if _, complete := runtimeFor(snapshot, "pod", "app"); complete {
				t.Fatal("failed current source could release a binding as absent")
			}
		})
	}
}

func TestTelemetryUnmappedUpdateRetainsItsObservationTime(t *testing.T) {
	now := time.Now().UTC()
	view := currentTelemetryView(now)
	update := biz.RuntimeAttempt{ContainerID: "unknown-container", PodUID: "pod", State: 4, ObservedAt: now.Add(time.Second)}
	view.State.RuntimeUpdates[update.ContainerID] = update
	snapshot := telemetryNodeSnapshot(view, now.Add(2*time.Second), time.Minute)
	if !snapshot.FactObservedAt[update.ContainerID].Equal(update.ObservedAt) || len(snapshot.UnknownPodContainers[update.PodUID]) != 1 {
		t.Fatal("unmapped current update lost its own time or identity uncertainty")
	}
	if _, complete := runtimeFor(snapshot, update.PodUID, "app"); complete {
		t.Fatal("unmapped current update was treated as a complete absence")
	}
}
