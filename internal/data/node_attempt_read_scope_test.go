package data

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

// CommandTag counts rows actually returned by PostgreSQL, independently of the
// query spelling or execution plan. The tracer never records SQL or arguments.
type attemptReadTrace struct {
	sync.Mutex
	rows []int64
}
type attemptReadTraceKey struct{}

func (trace *attemptReadTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, attemptReadTraceKey{}, strings.Contains(data.SQL, "FROM accelerator.node_runtime_attempts"))
}
func (trace *attemptReadTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if ctx.Value(attemptReadTraceKey{}) == true && data.Err == nil {
		trace.Lock()
		defer trace.Unlock()
		trace.rows = append(trace.rows, data.CommandTag.RowsAffected())
	}
}
func (trace *attemptReadTrace) reset() {
	trace.Lock()
	defer trace.Unlock()
	trace.rows = nil
}
func (trace *attemptReadTrace) returned() []int64 {
	trace.Lock()
	defer trace.Unlock()
	return append([]int64(nil), trace.rows...)
}

func TestPostgresNodeReportReadsOnlyAffectedAttemptHistory(t *testing.T) {
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
	identity := biz.NodeIdentity{ConnectionRef: "attempt-read:" + biz.NewID(), NodeUID: biz.NewID(), NodeName: "read-node", PodUID: biz.NewID(), LogID: biz.NewID()}
	state, err := database.OpenNodeSession(ctx, identity, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var history, current []biz.RuntimeAttempt
	for index := range 999 {
		history = append(history, biz.RuntimeAttempt{ContainerID: fmt.Sprintf("history-%04d", index), PodUID: biz.NewID(), ContainerName: "app", State: 3, ObservedAt: now.Add(-time.Hour), FinishedAt: now.Add(-time.Hour)})
	}
	for index := range 338 {
		attempt := biz.RuntimeAttempt{ContainerID: fmt.Sprintf("current-%04d", index), PodUID: biz.NewID(), PodNamespace: "tenant", PodName: "workload", SandboxID: "sandbox", ContainerName: "app", State: 2, ObservedAt: now, StartedAt: now.Add(-time.Second)}
		if index%3 == 0 {
			attempt.State, attempt.FinishedAt = 3, now
			attempt.JournalObservedAt, attempt.JournalProvenance = now, []byte(`{"cursor":"retained-journal"}`)
		}
		current = append(current, attempt)
	}
	seed := biz.NodeReport{Epoch: state.Epoch, Sequence: 1, LogID: identity.LogID, PayloadDigest: "initial-history", Attempts: history,
		Snapshot: &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now, CaptureEnd: now, Sources: []biz.TelemetrySource{{Name: "cri", Complete: true, ObservedAt: now}}, Containers: current}}
	if _, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, seed, now); err != nil {
		t.Fatal(err)
	}
	read := func() biz.NodeTelemetryView {
		t.Helper()
		views, err := database.NodeTelemetry(ctx, identity.ConnectionRef)
		if err != nil || len(views) != 1 {
			t.Fatalf("read full durable history: %v", err)
		}
		return views[0]
	}
	compare := func(report biz.NodeReport, at time.Time, expectedReads int64) biz.NodeTelemetryView {
		t.Helper()
		before := read()
		// Keep the existing full-history business merge as the independent oracle.
		raw, err := json.Marshal(before)
		if err != nil {
			t.Fatal(err)
		}
		var expected biz.NodeTelemetryView
		if err = json.Unmarshal(raw, &expected); err != nil {
			t.Fatal(err)
		}
		expected.Attempts, err = biz.MergeNodeReport(&expected.State, expected.Attempts, report, at)
		if err != nil {
			t.Fatal(err)
		}
		trace.reset()
		if _, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, at); err != nil {
			t.Fatal(err)
		}
		rows := trace.returned()
		var total int64
		for _, count := range rows {
			total += count
		}
		if len(rows) > 1 || total != expectedReads {
			t.Fatalf("report fetched %d attempt rows in %d reads; only %d existing affected container IDs are needed", total, len(rows), expectedReads)
		}
		after := read()
		want, _ := json.Marshal(expected)
		got, _ := json.Marshal(after)
		if string(want) != string(got) {
			t.Fatal("targeted persistence changed the full-history merge, revision, receipt, ordering or snapshot")
		}
		return after
	}
	before := read()
	versions := attemptVersions(t, database, identity)
	// Time-only observations are real writes. Reverse snapshot order and repeat
	// one ID across report/snapshot without changing their original merge order.
	updated := make([]biz.RuntimeAttempt, 0, len(current))
	for index := len(current) - 1; index >= 0; index-- {
		attempt := current[index]
		attempt.ObservedAt = now.Add(time.Second)
		attempt.JournalObservedAt, attempt.JournalProvenance = time.Time{}, nil
		updated = append(updated, attempt)
	}
	report := biz.NodeReport{Epoch: state.Epoch, Sequence: 2, LogID: identity.LogID, PayloadDigest: "time-only", Attempts: []biz.RuntimeAttempt{current[0]},
		Snapshot: &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now.Add(time.Second), CaptureEnd: now.Add(time.Second), Sources: []biz.TelemetrySource{{Name: "cri", Complete: true, ObservedAt: now.Add(time.Second)}}, Containers: updated}}
	after := compare(report, now.Add(time.Second), 338)
	if after.State.CurrentRevision != before.State.CurrentRevision || len(after.Attempts) != 1337 {
		t.Fatal("time-only observation altered meaning or dropped historical attempts")
	}
	written := attemptVersions(t, database, identity)
	for _, attempt := range history {
		if written[attempt.ContainerID] != versions[attempt.ContainerID] {
			t.Fatal("unrelated historical row was rewritten")
		}
	}
	for _, attempt := range current {
		if written[attempt.ContainerID] == versions[attempt.ContainerID] {
			t.Fatal("time-only attempt update was omitted")
		}
	}
	compare(report, now.Add(time.Second), 338)
	if !reflect.DeepEqual(written, attemptVersions(t, database, identity)) {
		t.Fatal("ACK replay rewrote facts")
	}
	// A gap-only record has no affected attempt IDs; it still changes its durable
	// gap, revision and receipt while retaining all 1337 history rows.
	gap := biz.NodeReport{Epoch: state.Epoch, Sequence: 3, LogID: identity.LogID, PayloadDigest: "gap-only", Gap: &biz.TelemetryGap{Sources: []string{"cri"}, Reason: "CURSOR_GAP", Start: now, End: now.Add(time.Second)}}
	compare(gap, now.Add(2*time.Second), 0)
	// Retain per-ID input order, identity-conflict handling and the Running→Exit
	// ordering fence, alongside a genuinely new container in the same batch.
	badExit := current[1]
	badExit.State, badExit.ObservedAt, badExit.FinishedAt = 3, now.Add(3*time.Second), now
	conflict := current[2]
	conflict.PodUID, conflict.ObservedAt = biz.NewID(), now.Add(3*time.Second)
	newAttempt := biz.RuntimeAttempt{ContainerID: "new-container", PodUID: biz.NewID(), ContainerName: "app", State: 2, ObservedAt: now.Add(3 * time.Second)}
	older := current[1]
	older.ObservedAt = now.Add(-time.Second)
	changed := biz.NodeReport{Epoch: state.Epoch, Sequence: 4, LogID: identity.LogID, PayloadDigest: "new-conflict-terminal", Attempts: []biz.RuntimeAttempt{badExit, newAttempt, conflict, older}}
	after = compare(changed, now.Add(3*time.Second), 2)
	if len(after.Attempts) != 1338 {
		t.Fatal("new batch lost historical or new container")
	}
	// Reject invalid later input atomically: no valid prefix or durable receipt
	// survives. The existing bulk-write rollback regression covers SQL failure.
	before = read()
	versions = attemptVersions(t, database, identity)
	invalid := biz.NodeReport{Epoch: state.Epoch, Sequence: 5, LogID: identity.LogID, PayloadDigest: "must-rollback", Attempts: []biz.RuntimeAttempt{{ContainerID: "valid-prefix", ObservedAt: now}, {ContainerID: "", ObservedAt: now}}}
	if _, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, invalid, now.Add(4*time.Second)); err == nil || !reflect.DeepEqual(versions, attemptVersions(t, database, identity)) {
		t.Fatal("invalid report retained a prefix or attempt mutation")
	}
	got, _ := json.Marshal(read())
	want, _ := json.Marshal(before)
	if string(got) != string(want) {
		t.Fatal("failed merge advanced history, snapshot, revision or durable ACK")
	}
}
