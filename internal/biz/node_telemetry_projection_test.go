package biz

import (
	"fmt"
	"testing"
	"time"
)

func projectionSnapshot(start, end time.Time, containers ...RuntimeAttempt) *TelemetrySnapshot {
	return &TelemetrySnapshot{CaptureEpoch: 1, CaptureStart: start, CaptureEnd: end, Sources: []TelemetrySource{{Name: "cri", ObservedAt: end, Complete: true}}, Containers: containers}
}

func projectionReport(t *testing.T, state *NodeTelemetryState, history []RuntimeAttempt, now time.Time, snapshot *TelemetrySnapshot, updates ...RuntimeAttempt) []RuntimeAttempt {
	t.Helper()
	sequence := state.LastSequence + 1
	report := NodeReport{Epoch: state.Epoch, Sequence: sequence, LogID: state.Identity.LogID, PayloadDigest: fmt.Sprintf("projection-%d", sequence), Snapshot: snapshot, Attempts: updates}
	merged, err := MergeNodeReport(state, history, report, now)
	if err != nil {
		t.Fatal(err)
	}
	return merged
}

func TestCurrentRuntimeAttemptsIgnoresCoveredHistory(t *testing.T) {
	now := time.Now().UTC()
	history := make([]RuntimeAttempt, 37)
	for index := range history {
		history[index] = RuntimeAttempt{ContainerID: fmt.Sprintf("old-%02d", index), State: 4, ObservedAt: now.Add(-time.Hour)}
	}
	snapshot := projectionSnapshot(now.Add(-time.Second), now)
	if current := CurrentRuntimeAttempts(NodeTelemetryState{Snapshot: snapshot}, history); len(current) != 0 {
		t.Fatalf("complete current absence inherited %d historical attempts", len(current))
	}
	if len(history) != 37 || history[0].ContainerID != "old-00" || history[0].State != 4 {
		t.Fatal("projection modified retained diagnostic history")
	}
}

func TestCurrentRuntimeAttemptsRetainsUpdatesUntilNextCapture(t *testing.T) {
	for _, runtimeState := range []uint32{1, 2, 4} {
		t.Run(fmt.Sprintf("state_%d", runtimeState), func(t *testing.T) {
			now := time.Now().UTC()
			state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 1, Snapshot: projectionSnapshot(now.Add(-2*time.Second), now.Add(-time.Second)), RuntimeUpdates: map[string]RuntimeAttempt{}}
			update := RuntimeAttempt{ContainerID: "new-container", PodUID: "pod", ContainerName: "app", State: runtimeState, ObservedAt: now}
			history := projectionReport(t, &state, nil, now, nil, update)
			current := CurrentRuntimeAttempts(state, history)
			if len(current) != 1 || current[0].State != runtimeState || state.CurrentRevision != 2 {
				t.Fatalf("empty snapshot hid a newer runtime update: current=%+v revision=%d", current, state.CurrentRevision)
			}
			// A capture that started with the update may have listed containers
			// before it occurred. CaptureEnd alone cannot supersede the update.
			history = projectionReport(t, &state, history, now.Add(time.Second), projectionSnapshot(now, now.Add(time.Second)))
			if current = CurrentRuntimeAttempts(state, history); len(current) != 1 || current[0].State != runtimeState || state.CurrentRevision != 2 {
				t.Fatalf("overlapping capture erased an uncovered update: current=%+v revision=%d", current, state.CurrentRevision)
			}
			history = projectionReport(t, &state, history, now.Add(3*time.Second), projectionSnapshot(now.Add(2*time.Second), now.Add(3*time.Second)))
			if current = CurrentRuntimeAttempts(state, history); len(current) != 0 || len(history) != 1 || state.CurrentRevision != 3 {
				t.Fatalf("subsequent complete capture did not converge without deleting history: current=%+v history=%+v revision=%d", current, history, state.CurrentRevision)
			}
		})
	}
}

func TestNodeSnapshotWindowPreservesConcurrentOccupancy(t *testing.T) {
	for _, runtimeState := range []uint32{1, 2, 4} {
		t.Run(fmt.Sprintf("state_%d", runtimeState), func(t *testing.T) {
			now := time.Now().UTC()
			state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), Snapshot: projectionSnapshot(now.Add(-2*time.Second), now.Add(-time.Second)), RuntimeUpdates: map[string]RuntimeAttempt{}}
			update := RuntimeAttempt{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: runtimeState, ObservedAt: now}
			history := projectionReport(t, &state, nil, now, nil, update)
			exited := update
			exited.State, exited.ObservedAt, exited.FinishedAt = 3, now.Add(time.Second), now.Add(500*time.Millisecond)
			history = projectionReport(t, &state, history, exited.ObservedAt, projectionSnapshot(now.Add(-time.Second), exited.ObservedAt, exited))
			current := CurrentRuntimeAttempts(state, history)
			if len(current) != 1 || current[0].State != runtimeState || !current[0].ObservedAt.Equal(update.ObservedAt) {
				t.Fatalf("EXITED sampled in the same window erased a runtime update: %+v", current)
			}
			exited.ObservedAt = now.Add(3 * time.Second)
			history = projectionReport(t, &state, history, exited.ObservedAt, projectionSnapshot(now.Add(2*time.Second), exited.ObservedAt, exited))
			if current = CurrentRuntimeAttempts(state, history); len(current) != 1 || current[0].State != 3 {
				t.Fatalf("later complete EXITED observation did not cover the update: %+v", current)
			}
		})
	}
}

func TestNodeCurrentMembershipChangesRevisionWithoutRefreshChurn(t *testing.T) {
	now := time.Now().UTC()
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 1}
	running := RuntimeAttempt{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: 2, ObservedAt: now}
	history := projectionReport(t, &state, nil, now, projectionSnapshot(now, now, running))
	revision := state.CurrentRevision
	running.ObservedAt = now.Add(time.Second)
	history = projectionReport(t, &state, history, running.ObservedAt, projectionSnapshot(running.ObservedAt, running.ObservedAt, running))
	if state.CurrentRevision != revision {
		t.Fatal("refreshing only observation and receipt times changed the revision")
	}
	history = projectionReport(t, &state, history, now.Add(3*time.Second), projectionSnapshot(now.Add(2*time.Second), now.Add(3*time.Second)))
	if state.CurrentRevision != revision+1 || len(history) != 1 || history[0].State != 2 || len(CurrentRuntimeAttempts(state, history)) != 0 {
		t.Fatalf("current disappearance did not invalidate the revision while retaining history: revision=%d history=%+v", state.CurrentRevision, history)
	}
	history = projectionReport(t, &state, history, now.Add(5*time.Second), projectionSnapshot(now.Add(4*time.Second), now.Add(5*time.Second)))
	if state.CurrentRevision != revision+1 {
		t.Fatal("unchanged complete absence invalidated the revision")
	}
	// A late report older than the current capture can improve the history
	// without changing the current state used by an observation transaction.
	oldUnknown := RuntimeAttempt{ContainerID: "old-container", State: 4, ObservedAt: now.Add(-time.Hour)}
	history = projectionReport(t, &state, history, now.Add(6*time.Second), nil, oldUnknown)
	if state.CurrentRevision != revision+1 || len(history) != 2 || len(CurrentRuntimeAttempts(state, history)) != 0 {
		t.Fatal("covered history changed current runtime membership or revision")
	}
}

func TestCompleteNodeSnapshotExitDoesNotRequireHistoricalFinishedAt(t *testing.T) {
	now := time.Now().UTC()
	running := RuntimeAttempt{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: 2, ObservedAt: now.Add(-2 * time.Second)}
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), Snapshot: projectionSnapshot(running.ObservedAt, running.ObservedAt, running), RuntimeUpdates: map[string]RuntimeAttempt{}}
	exited := running
	exited.State, exited.ObservedAt = 3, now
	history := projectionReport(t, &state, []RuntimeAttempt{running}, now, projectionSnapshot(now.Add(-time.Second), now, exited))
	current := CurrentRuntimeAttempts(state, history)
	if len(current) != 1 || current[0].State != 3 || !current[0].FinishedAt.IsZero() {
		t.Fatalf("complete current EXITED was downgraded by missing historical exit time: %+v", current)
	}
	revision := state.CurrentRevision
	// A later positive event must still defeat that complete EXITED snapshot.
	running.ObservedAt = now.Add(time.Second)
	history = projectionReport(t, &state, history, running.ObservedAt, nil, running)
	if current = CurrentRuntimeAttempts(state, history); len(current) != 1 || current[0].State != 2 || state.CurrentRevision != revision+1 {
		t.Fatalf("confirmed exit concealed newer Running: current=%+v revision=%d", current, state.CurrentRevision)
	}
}

func TestNodeCurrentProjectionTracksUnmentionedUpdates(t *testing.T) {
	now := time.Now().UTC()
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute)}
	projectionReport(t, &state, nil, now, projectionSnapshot(now, now))
	running := RuntimeAttempt{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: 2, ObservedAt: now.Add(time.Second)}
	history := projectionReport(t, &state, nil, running.ObservedAt, nil, running)
	revision := state.CurrentRevision
	if len(CurrentRuntimeAttempts(state, nil)) != 1 {
		t.Fatal("current increment depends on loading its historical row again")
	}
	// Production persistence loads only the IDs named in a report. This empty
	// snapshot therefore reaches MergeNodeReport with no historical rows.
	projectionReport(t, &state, nil, now.Add(3*time.Second), projectionSnapshot(now.Add(2*time.Second), now.Add(3*time.Second)))
	if state.CurrentRevision != revision+1 || len(CurrentRuntimeAttempts(state, history)) != 0 || len(state.RuntimeUpdates) != 0 {
		t.Fatalf("empty report missed the unmentioned current member: revision=%d updates=%+v", state.CurrentRevision, state.RuntimeUpdates)
	}
	if len(history) != 1 || history[0].State != 2 {
		t.Fatal("reconciliation deleted diagnostic history")
	}
}

func TestNodeCurrentProjectionInitializesLegacyBaselineOnce(t *testing.T) {
	now := time.Now().UTC()
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute), CurrentRevision: 7, Snapshot: projectionSnapshot(now.Add(-2*time.Second), now.Add(-time.Second))}
	projectionReport(t, &state, nil, now, projectionSnapshot(now, now))
	if state.RuntimeUpdates == nil || state.CurrentRevision != 8 {
		t.Fatal("first complete current baseline did not fence the legacy state")
	}
	projectionReport(t, &state, nil, now.Add(time.Second), projectionSnapshot(now.Add(time.Second), now.Add(time.Second)))
	if state.CurrentRevision != 8 {
		t.Fatal("initialized baseline caused timestamp-only revision churn")
	}
}

func TestNodeHistoricalGapsDoNotInvalidateCurrentProjection(t *testing.T) {
	now := time.Now().UTC()
	state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute)}
	projectionReport(t, &state, nil, now, projectionSnapshot(now, now))
	revision := state.CurrentRevision
	for index := 0; index < 3; index++ {
		observed := now.Add(time.Duration(index+1) * time.Second)
		report := NodeReport{Epoch: 1, Sequence: state.LastSequence + 1, LogID: "log", PayloadDigest: fmt.Sprintf("gap-%d", index), Gap: &TelemetryGap{Sources: []string{"cri_events"}, Reason: "EVENT_STREAM_INTERRUPTED", Start: now.Add(-time.Hour), DiscoveredAt: observed}}
		if _, err := MergeNodeReport(&state, nil, report, observed); err != nil {
			t.Fatal(err)
		}
		if state.CurrentRevision != revision || !state.Gaps["cri_events"].DiscoveredAt.Equal(observed) {
			t.Fatal("diagnostic gap churn invalidated current state or lost the diagnostic")
		}
	}
}

func TestNodeSnapshotWindowKeepsLateRuntimeUpdate(t *testing.T) {
	for _, runtimeState := range []uint32{1, 2, 4} {
		t.Run(fmt.Sprintf("state_%d", runtimeState), func(t *testing.T) {
			now := time.Now().UTC()
			state := NodeTelemetryState{Identity: NodeIdentity{LogID: "log"}, Epoch: 1, LeaseExpiresAt: now.Add(time.Minute)}
			exited := RuntimeAttempt{ContainerID: "container", PodUID: "pod", ContainerName: "app", State: 3, ObservedAt: now.Add(2 * time.Second), FinishedAt: now}
			history := projectionReport(t, &state, nil, exited.ObservedAt, projectionSnapshot(now, exited.ObservedAt, exited))
			revision := state.CurrentRevision
			update := exited
			update.State, update.ObservedAt, update.FinishedAt = runtimeState, now.Add(time.Second), time.Time{}
			history = projectionReport(t, &state, history, now.Add(3*time.Second), nil, update)
			current := CurrentRuntimeAttempts(state, nil)
			if len(current) != 1 || current[0].State != runtimeState || state.CurrentRevision != revision+1 {
				t.Fatalf("CaptureEnd hid a late update from inside its sampling window: current=%+v revision=%d", current, state.CurrentRevision)
			}
			exited.ObservedAt = now.Add(5 * time.Second)
			projectionReport(t, &state, history, exited.ObservedAt, projectionSnapshot(now.Add(4*time.Second), exited.ObservedAt, exited))
			if current = CurrentRuntimeAttempts(state, nil); len(current) != 1 || current[0].State != 3 {
				t.Fatal("later complete capture did not resolve the sampling-window conflict")
			}
		})
	}
}
