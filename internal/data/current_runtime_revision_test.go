package data

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data/accdb"
)

func TestPostgresCurrentRuntimeRevisionTracksSnapshotCoveredIncrement(t *testing.T) {
	base := openPGTest(t)
	trace := &attemptReadTrace{}
	config := base.pool.Config()
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	database := &Postgres{pool: pool}
	ctx := context.Background()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: "current-runtime:" + biz.NewID(), NodeUID: biz.NewID(), NodeName: "current-runtime-node", PodUID: biz.NewID(), LogID: biz.NewID()}
	session, err := database.OpenNodeSession(ctx, identity, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	emptySnapshot := func(at time.Time) *biz.TelemetrySnapshot {
		return &biz.TelemetrySnapshot{
			CaptureEpoch: session.Epoch,
			CaptureStart: at,
			CaptureEnd:   at,
			Sources: []biz.TelemetrySource{
				{Name: "cri", Complete: true, ObservedAt: at},
				{Name: "podresources", Complete: true, ObservedAt: at},
			},
		}
	}
	var sequence uint64
	save := func(snapshot *biz.TelemetrySnapshot, attempts []biz.RuntimeAttempt, at time.Time) biz.NodeTelemetryView {
		t.Helper()
		sequence++
		report := biz.NodeReport{Epoch: session.Epoch, Sequence: sequence, LogID: identity.LogID, PayloadDigest: fmt.Sprintf("current-runtime-%d", sequence), Snapshot: snapshot, Attempts: attempts}
		trace.reset()
		if _, err := database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, at); err != nil {
			t.Fatal(err)
		}
		// These reports contain only new IDs or an empty snapshot. Comparing
		// current state must not fetch unrelated historical attempt rows.
		rows := trace.returned()
		var returned int64
		for _, count := range rows {
			returned += count
		}
		if len(rows) > 1 || returned != 0 {
			t.Fatalf("report fetched historical attempts for current revision: rows=%v", rows)
		}
		views, err := database.NodeTelemetry(ctx, identity.ConnectionRef)
		if err != nil || len(views) != 1 {
			t.Fatalf("read persisted node state: views=%+v error=%v", views, err)
		}
		return views[0]
	}
	var history []biz.RuntimeAttempt
	for index := 0; index < 37; index++ {
		history = append(history, biz.RuntimeAttempt{ContainerID: fmt.Sprintf("history-%02d", index), PodUID: biz.NewID(), ContainerName: "app", State: 4, ObservedAt: now.Add(-time.Minute)})
	}
	first := save(emptySnapshot(now), history, now)
	if current := biz.CurrentRuntimeAttempts(first.State, first.Attempts); len(current) != 0 {
		t.Fatalf("initial empty snapshot retained old runtime history: %+v", current)
	}

	// A did not appear in S1. Its increment must survive in persisted state
	// so that the next empty snapshot can compare against A before removing it.
	attempt := biz.RuntimeAttempt{ContainerID: "increment-after-snapshot", PodUID: biz.NewID(), PodNamespace: "tenant", PodName: "workload", ContainerName: "app", State: 2, ObservedAt: now.Add(time.Second), StartedAt: now.Add(time.Second)}
	running := save(nil, []biz.RuntimeAttempt{attempt}, now.Add(time.Second))
	current := biz.CurrentRuntimeAttempts(running.State, running.Attempts)
	if len(current) != 1 || current[0].ContainerID != attempt.ContainerID || current[0].State != 2 || running.State.CurrentRevision <= first.State.CurrentRevision {
		t.Fatalf("new running increment did not change persisted current state: current=%+v revisions=%d -> %d", current, first.State.CurrentRevision, running.State.CurrentRevision)
	}
	second := save(emptySnapshot(now.Add(2*time.Second)), nil, now.Add(2*time.Second))
	current = biz.CurrentRuntimeAttempts(second.State, second.Attempts)
	if len(current) != 0 || second.State.CurrentRevision <= running.State.CurrentRevision {
		t.Fatalf("covering empty snapshot failed to retire the increment and advance revision: current=%+v revisions=%d -> %d", current, running.State.CurrentRevision, second.State.CurrentRevision)
	}
	if len(second.Attempts) != len(history)+1 {
		t.Fatalf("current reconciliation removed diagnostic history: %+v", second.Attempts)
	}
	for _, stored := range second.Attempts {
		if stored.ContainerID == attempt.ContainerID && (stored.State != 2 || !stored.FinishedAt.IsZero()) {
			t.Fatalf("current absence fabricated a historical exit: %+v", stored)
		}
	}
}

func TestPostgresLegacyRuntimeBaselineKeepsUnmentionedSamplingWindowUpdate(t *testing.T) {
	base := openPGTest(t)
	trace := &attemptReadTrace{}
	config := base.pool.Config()
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	database := &Postgres{pool: pool}
	ctx := context.Background()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: "legacy-runtime:" + biz.NewID(), NodeUID: biz.NewID(), NodeName: "legacy-runtime-node", PodUID: biz.NewID(), LogID: biz.NewID()}
	session, err := database.OpenNodeSession(ctx, identity, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	emptySnapshot := func(start, end time.Time) *biz.TelemetrySnapshot {
		return &biz.TelemetrySnapshot{CaptureEpoch: session.Epoch, CaptureStart: start, CaptureEnd: end, Sources: []biz.TelemetrySource{{Name: "cri", Complete: true, ObservedAt: end}, {Name: "podresources", Complete: true, ObservedAt: end}}}
	}
	var sequence uint64
	save := func(snapshot *biz.TelemetrySnapshot, attempts []biz.RuntimeAttempt, at time.Time) biz.NodeTelemetryState {
		t.Helper()
		sequence++
		report := biz.NodeReport{Epoch: session.Epoch, Sequence: sequence, LogID: identity.LogID, PayloadDigest: fmt.Sprintf("legacy-runtime-%d", sequence), Snapshot: snapshot, Attempts: attempts}
		state, err := database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, at)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	read := func() biz.NodeTelemetryView {
		t.Helper()
		views, err := database.NodeTelemetry(ctx, identity.ConnectionRef)
		if err != nil || len(views) != 1 {
			t.Fatalf("read persisted legacy node: views=%+v error=%v", views, err)
		}
		return views[0]
	}
	assertAttemptReads := func(want int64) {
		t.Helper()
		rows := trace.returned()
		var returned int64
		for _, count := range rows {
			returned += count
		}
		if len(rows) > 1 || returned != want {
			t.Fatalf("unexpected attempt reads while initializing or refreshing baseline: rows=%v want=%d", rows, want)
		}
	}
	var history []biz.RuntimeAttempt
	for index := 0; index < 37; index++ {
		history = append(history, biz.RuntimeAttempt{ContainerID: fmt.Sprintf("legacy-history-%02d", index), PodUID: biz.NewID(), ContainerName: "app", State: 4, ObservedAt: now.Add(-time.Hour)})
	}
	save(emptySnapshot(now, now), history, now)
	attempt := biz.RuntimeAttempt{ContainerID: "legacy-window-increment", PodUID: biz.NewID(), PodNamespace: "tenant", PodName: "workload", ContainerName: "app", State: 2, ObservedAt: now.Add(2 * time.Second), StartedAt: now.Add(2 * time.Second)}
	running := save(nil, []biz.RuntimeAttempt{attempt}, attempt.ObservedAt)
	// Simulate an older persisted state: S1 and A are durable, but the state
	// predates the map that carries increments outside the snapshot itself.
	if _, err = database.nodeTransaction(ctx, identity.ConnectionRef, identity.NodeUID, func(_ *accdb.Queries, state *biz.NodeTelemetryState) error {
		state.RuntimeUpdates = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy := read()
	if legacy.State.RuntimeUpdates != nil || legacy.State.CurrentRevision != running.CurrentRevision || len(legacy.Attempts) != len(history)+1 {
		t.Fatal("legacy fixture lost its current revision, nil baseline or durable history")
	}

	// S2 did not mention A, but started before A. Its first baseline must
	// recover A once from storage and preserve it throughout this window.
	trace.reset()
	save(emptySnapshot(now.Add(time.Second), now.Add(3*time.Second)), nil, now.Add(3*time.Second))
	assertAttemptReads(int64(len(history) + 1))
	initialized := read()
	current := biz.CurrentRuntimeAttempts(initialized.State, nil)
	if initialized.State.RuntimeUpdates == nil || initialized.State.CurrentRevision != running.CurrentRevision+1 || len(current) != 1 || current[0].ContainerID != attempt.ContainerID || current[0].State != 2 {
		t.Fatalf("legacy baseline erased unmentioned occupancy or failed its revision fence: state=%+v current=%+v", initialized.State, current)
	}

	// S3 starts after A, so it can cover A. The legacy scan must not recur.
	trace.reset()
	save(emptySnapshot(now.Add(4*time.Second), now.Add(5*time.Second)), nil, now.Add(5*time.Second))
	assertAttemptReads(0)
	covered := read()
	if covered.State.RuntimeUpdates == nil || len(biz.CurrentRuntimeAttempts(covered.State, nil)) != 0 || covered.State.CurrentRevision != initialized.State.CurrentRevision+1 {
		t.Fatalf("later complete capture did not retire the covered legacy increment: %+v", covered.State)
	}
	if len(covered.Attempts) != len(history)+1 {
		t.Fatal("legacy initialization or current reconciliation deleted diagnostic history")
	}
}
