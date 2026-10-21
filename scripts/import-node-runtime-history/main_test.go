package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
)

func TestLegacyHistoryRealPostgresReplayConflictAndOldExit(testContext *testing.T) {
	dsnFile := os.Getenv("ACC_TEST_DSN_FILE")
	if dsnFile == "" {
		testContext.Skip("requires isolated PostgreSQL ACC_TEST_DSN_FILE")
	}
	ctx := context.Background()
	repository, err := data.OpenPostgres(ctx, dsnFile)
	if err != nil {
		testContext.Fatal(err)
	}
	testContext.Cleanup(repository.Close)
	now := time.Now().UTC()
	cluster := biz.Cluster{ClusterID: biz.NewID(), ConnectionRef: "history-test:" + biz.NewID(), Version: 1}
	pool := biz.Pool{PoolID: biz.NewID(), ClusterID: cluster.ClusterID}
	node := biz.NodeRef{UID: biz.NewID(), Name: "history-node"}
	group := biz.SupplyGroup{GroupID: biz.NewID(), PoolID: pool.PoolID, ClusterID: cluster.ClusterID, Mode: biz.Shared, Admission: biz.Closed, Version: 1, Nodes: []biz.NodeRef{node}, Baseline: &biz.ProviderBaseline{BaselineDigest: strings.Repeat("f", 64)}}
	if err := repository.Transact(ctx, func(transaction biz.Transaction) error {
		if err := transaction.InsertCluster(ctx, cluster); err != nil {
			return err
		}
		if err := transaction.InsertPool(ctx, pool); err != nil {
			return err
		}
		return transaction.InsertGroup(ctx, group)
	}); err != nil {
		testContext.Fatal(err)
	}
	identity := biz.NodeIdentity{ConnectionRef: cluster.ConnectionRef, NodeUID: node.UID, NodeName: node.Name, PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "agent-history", ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute)}
	state, err := repository.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		testContext.Fatal(err)
	}
	podUID := biz.NewID()
	current := []biz.RuntimeAttempt{
		{ContainerID: strings.Repeat("b", 64), PodUID: podUID, PodNamespace: "tenant", PodName: "workload", ContainerName: "app", State: 2, ObservedAt: now},
		{ContainerID: strings.Repeat("c", 64), PodUID: podUID, PodNamespace: "tenant", PodName: "workload", ContainerName: "app", State: 4, ObservedAt: now},
	}
	report := biz.NodeReport{Epoch: state.Epoch, LogID: identity.LogID, Sequence: 1, PayloadDigest: "actual-agent-receipt", Snapshot: &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now, CaptureEnd: now, Sources: []biz.TelemetrySource{{Name: "cri", Complete: true, ObservedAt: now}}, Containers: current}}
	if _, err := repository.SaveNodeReport(ctx, cluster.ConnectionRef, node.UID, report, now); err != nil {
		testContext.Fatal(err)
	}
	before := readNode(testContext, repository, cluster.ConnectionRef)
	legacy := legacyFile{NodeUID: node.UID, NodeName: node.Name, Socket: "/host/containerd/containerd.sock", States: map[string]legacyState{}}
	for _, prefix := range []string{"a", "b", "c"} {
		id := strings.Repeat(prefix, 64)
		uid := podUID
		if prefix == "a" {
			uid = strings.Repeat("a", 32)
		}
		legacy.States[id] = legacyState{ID: id, PodUID: uid, Namespace: "tenant", PodName: "workload", ContainerName: "app", State: 2, ObservedAt: now.Add(-time.Minute).UnixNano(), FinishedAt: now.Add(-2 * time.Minute).UnixNano()}
	}
	encoded, _ := json.Marshal(legacy)
	file := filepath.Join(testContext.TempDir(), "legacy.json")
	if err := os.WriteFile(file, encoded, 0o600); err != nil {
		testContext.Fatal(err)
	}
	hash := sha256.Sum256(encoded)
	digest := hex.EncodeToString(hash[:])
	scope, err := readLegacy(file, digest, cluster.ConnectionRef, cluster.ClusterID, node.UID, node.Name, legacy.Socket, now)
	if err != nil {
		testContext.Fatal(err)
	}
	if _, err := readLegacy(file, strings.Repeat("0", 64), cluster.ConnectionRef, cluster.ClusterID, node.UID, node.Name, legacy.Socket, now); err == nil {
		testContext.Fatal("changed original file digest accepted")
	}
	if _, err := readLegacy(file, digest, cluster.ConnectionRef, cluster.ClusterID, biz.NewID(), node.Name, legacy.Socket, now); err == nil {
		testContext.Fatal("different source Node UID accepted")
	}
	preview, err := importLegacy(ctx, repository, dsnFile, scope, false)
	if err != nil || preview.Inserted != 1 || preview.Existing != 2 || preview.Applied {
		testContext.Fatalf("preview: %+v %v", preview, err)
	}
	if !reflect.DeepEqual(before, readNode(testContext, repository, cluster.ConnectionRef)) {
		testContext.Fatal("preview persisted changes")
	}
	receipt, err := importLegacy(ctx, repository, dsnFile, scope, true)
	if err != nil || receipt.Inserted != 1 || receipt.Existing != 2 || !receipt.Applied || receipt.SourceSHA256 != digest || receipt.RevisionAfter != before.State.CurrentRevision+1 {
		testContext.Fatalf("import: %+v %v", receipt, err)
	}
	after := readNode(testContext, repository, cluster.ConnectionRef)
	if len(after.Attempts) != 3 || after.State.LastSequence != before.State.LastSequence || after.State.LastPayloadDigest != before.State.LastPayloadDigest || after.State.Epoch != before.State.Epoch || !reflect.DeepEqual(after.State.Identity, before.State.Identity) || !reflect.DeepEqual(after.State.Snapshot, before.State.Snapshot) || !reflect.DeepEqual(after.State.Gaps, before.State.Gaps) || !after.State.LeaseExpiresAt.Equal(before.State.LeaseExpiresAt) || after.State.ConsumedRevision != before.State.ConsumedRevision {
		testContext.Fatal("history import changed authentication receipt, source freshness, gap or current snapshot")
	}
	if after.Attempts[0].ContainerID != strings.Repeat("a", 64) || after.Attempts[0].PodUID != strings.Repeat("a", 32) || !after.Attempts[0].ObservedAt.Equal(now.Add(-time.Minute)) || after.Attempts[0].Issues[0] != "LEGACY_LOCAL_EVENT_HISTORY_SHA256:"+digest || after.Attempts[1].State != 2 || after.Attempts[2].State != 4 {
		testContext.Fatal("original history time/provenance lost or old EXITED overwrote current Running/UNKNOWN")
	}
	replay, err := importLegacy(ctx, repository, dsnFile, scope, true)
	if err != nil || replay.Inserted != 0 || replay.Existing != 3 || replay.RevisionAfter != receipt.RevisionAfter {
		testContext.Fatalf("replay: %+v %v", replay, err)
	}
	conflict := scope
	conflict.Attempts = append([]biz.RuntimeAttempt{{ContainerID: strings.Repeat("0", 64), PodUID: podUID, PodNamespace: "tenant", PodName: "workload", ContainerName: "app", State: 4, ObservedAt: now}}, scope.Attempts...)
	conflict.Attempts[1].PodUID = biz.NewID()
	if _, err := importLegacy(ctx, repository, dsnFile, conflict, true); err == nil {
		testContext.Fatal("conflicting container identity accepted")
	}
	if !reflect.DeepEqual(after, readNode(testContext, repository, cluster.ConnectionRef)) {
		testContext.Fatal("conflicting import did not roll back inserted prefix atomically")
	}
	if _, err := repository.RenewNodeSession(ctx, identity, state.Epoch, now.Add(-time.Minute)); err != nil {
		testContext.Fatal(err)
	}
	if _, err := importLegacy(ctx, repository, dsnFile, scope, true); err == nil || err.Error() != "REAL_AUTHENTICATED_NODE_FACT_REQUIRED" {
		testContext.Fatalf("expired real node lease admitted offline import: %v", err)
	}
	group.Admission = biz.Open
	group.Version++
	if err := repository.Transact(ctx, func(transaction biz.Transaction) error { return transaction.UpdateGroup(ctx, group, 1) }); err != nil {
		testContext.Fatal(err)
	}
	if _, err := importLegacy(ctx, repository, dsnFile, scope, true); err == nil {
		testContext.Fatal("OPEN group admitted offline import")
	}
}

func readNode(testContext *testing.T, repository *data.Postgres, connection string) biz.NodeTelemetryView {
	testContext.Helper()
	views, err := repository.NodeTelemetry(context.Background(), connection)
	if err != nil || len(views) != 1 {
		testContext.Fatalf("node read: %v %v", views, err)
	}
	return views[0]
}
