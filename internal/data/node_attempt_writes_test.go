package data

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data/accdb"
)

func TestPostgresNodeTelemetrySkipsUnchangedAttemptWrites(testContext *testing.T) {
	database := openPGTest(testContext)
	ctx := context.Background()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: "node-writes:" + biz.NewID(), NodeUID: biz.NewID(), NodeName: "node-writes", PodUID: biz.NewID(), LogID: biz.NewID()}
	state, err := database.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		testContext.Fatal(err)
	}
	history := make([]biz.RuntimeAttempt, 587)
	for index := range history {
		history[index] = biz.RuntimeAttempt{ContainerID: fmt.Sprintf("history-%03d", index), PodUID: biz.NewID(), ContainerName: "app", State: 3, ObservedAt: now.Add(-time.Hour), FinishedAt: now.Add(-time.Hour)}
	}
	live := biz.RuntimeAttempt{ContainerID: "current-attempt", PodUID: biz.NewID(), ContainerName: "app", State: 1, ObservedAt: now}
	report := biz.NodeReport{Epoch: state.Epoch, Sequence: 1, LogID: identity.LogID, PayloadDigest: "initial", Attempts: history, Snapshot: &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now, CaptureEnd: now, Containers: []biz.RuntimeAttempt{live}}}
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil {
		testContext.Fatal(err)
	}
	revision := state.CurrentRevision
	before := attemptVersions(testContext, database, identity)
	report.Sequence, report.PayloadDigest, report.Attempts = 2, "unchanged", nil
	report.Snapshot = &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now.Add(time.Millisecond), CaptureEnd: now.Add(time.Millisecond), Containers: []biz.RuntimeAttempt{live}}
	started := time.Now()
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now.Add(time.Millisecond))
	if err != nil {
		testContext.Fatal(err)
	}
	testContext.Logf("unchanged snapshot persisted receipt in %s with 587 historical attempts", time.Since(started))
	if state.LastSequence != 2 || state.LastPayloadDigest != "unchanged" || state.CurrentRevision != revision {
		testContext.Fatal("unchanged report lost its durable receipt or changed semantic revision")
	}
	if !reflect.DeepEqual(before, attemptVersions(testContext, database, identity)) {
		testContext.Fatal("unchanged current/history attempt rows were rewritten")
	}
	if _, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now.Add(time.Millisecond)); err != nil {
		testContext.Fatal(err)
	}
	if !reflect.DeepEqual(before, attemptVersions(testContext, database, identity)) {
		testContext.Fatal("ACK replay rewrote attempt rows")
	}
	live.State, live.ObservedAt, live.StartedAt = 2, now.Add(time.Second), now.Add(time.Second)
	report.Sequence, report.PayloadDigest = 3, "new-running"
	report.Snapshot = &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now.Add(time.Second), CaptureEnd: now.Add(time.Second), Containers: []biz.RuntimeAttempt{live}}
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now.Add(time.Second))
	if err != nil || state.LastSequence != 3 || state.CurrentRevision != revision+1 {
		testContext.Fatalf("new Running was not committed with its receipt: %v", err)
	}
	after := attemptVersions(testContext, database, identity)
	if after[live.ContainerID] == before[live.ContainerID] {
		testContext.Fatal("changed Running attempt was not written")
	}
	for _, attempt := range history {
		if after[attempt.ContainerID] != before[attempt.ContainerID] {
			testContext.Fatal("new Running rewrote unrelated historical attempt")
		}
	}
	updated := append([]biz.RuntimeAttempt(nil), history[:200]...)
	for index := range updated {
		updated[index].ObservedAt = now.Add(2 * time.Second)
	}
	report.Sequence, report.PayloadDigest, report.Attempts = 4, "multiple-observations", updated
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now.Add(2*time.Second))
	if err != nil || state.LastSequence != 4 || state.CurrentRevision != revision+1 {
		testContext.Fatalf("multiple changed observations lost receipt or changed meaning: %v", err)
	}
	batch := attemptVersions(testContext, database, identity)
	for index, attempt := range history {
		if (batch[attempt.ContainerID] != after[attempt.ContainerID]) != (index < len(updated)) {
			testContext.Fatal("multiple changed observations were dropped or rewrote unrelated history")
		}
	}
	if batch[live.ContainerID] != after[live.ContainerID] {
		testContext.Fatal("unrelated current Running attempt changed")
	}
	invalid, _ := json.Marshal([]map[string]string{{"ContainerID": "rollback-prefix"}, {"ContainerID": ""}})
	_, err = database.nodeTransaction(ctx, identity.ConnectionRef, identity.NodeUID, func(queries *accdb.Queries, pending *biz.NodeTelemetryState) error {
		pending.LastSequence, pending.LastPayloadDigest = 5, "must-rollback"
		return queries.SaveNodeRuntimeAttempts(ctx, identity.ConnectionRef, identity.NodeUID, invalid)
	})
	if err == nil || !reflect.DeepEqual(batch, attemptVersions(testContext, database, identity)) {
		testContext.Fatal("invalid bulk row did not roll back its valid prefix atomically")
	}
	views, err := database.NodeTelemetry(ctx, identity.ConnectionRef)
	if err != nil || views[0].State.LastSequence != 4 || views[0].State.LastPayloadDigest != "multiple-observations" {
		testContext.Fatal("failed bulk advanced durable receipt")
	}
}

func attemptVersions(testContext *testing.T, database *Postgres, identity biz.NodeIdentity) map[string]string {
	testContext.Helper()
	rows, err := database.pool.Query(context.Background(), testSQL(testContext, "node_attempt_versions.sql"), identity.ConnectionRef, identity.NodeUID)
	if err != nil {
		testContext.Fatal(err)
	}
	defer rows.Close()
	versions := map[string]string{}
	for rows.Next() {
		var id, version string
		if err := rows.Scan(&id, &version); err != nil {
			testContext.Fatal(err)
		}
		versions[id] = version
	}
	if rows.Err() != nil {
		testContext.Fatal(rows.Err())
	}
	return versions
}
