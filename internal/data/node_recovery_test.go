package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresNodeRecoveryScopeSurvivesEpochAndDoesNotReleaseRunning(testContext *testing.T) {
	database := openPGTest(testContext)
	ctx := context.Background()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: "node-recovery:" + biz.NewID(), NodeUID: biz.NewID(), NodeName: "node-recovery", PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "node-agent", ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute)}
	state, err := database.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		testContext.Fatal(err)
	}
	pod := biz.PodRef{ClusterID: biz.NewID(), UID: biz.NewID(), Namespace: "tenant", Name: "workload", ContainerName: "workspace"}
	containerID := strings.Repeat("a", 64)
	running := biz.RuntimeAttempt{ContainerID: containerID, PodUID: pod.UID, PodNamespace: pod.Namespace, PodName: pod.Name, ContainerName: pod.ContainerName, State: 2, ObservedAt: now}
	report := biz.NodeReport{Epoch: state.Epoch, Sequence: 1, LogID: identity.LogID, PayloadDigest: "running-record", Attempts: []biz.RuntimeAttempt{running}}
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil {
		testContext.Fatal(err)
	}
	requestID := biz.NewID()
	if err = database.RegisterNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch, requestID, pod, []string{containerID}); err != nil {
		testContext.Fatal(err)
	}
	views, err := database.NodeTelemetry(ctx, identity.ConnectionRef)
	if err != nil {
		testContext.Fatal(err)
	}
	if views[0].State.CurrentRevision != state.CurrentRevision {
		testContext.Fatal("request bookkeeping advanced fact revision")
	}
	reopened := openPGTest(testContext)
	newState, err := reopened.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		testContext.Fatal(err)
	}
	recoveredPod, known, err := reopened.ReadNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, requestID)
	if err != nil || recoveredPod != pod || len(known) != 1 || known[0] != containerID {
		testContext.Fatal("epoch change lost immutable issued scope", err)
	}
	if err = database.RegisterNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch, biz.NewID(), pod, []string{containerID}); err == nil {
		testContext.Fatal("old epoch issued a recovery command")
	}
	exited := running
	exited.State = 3
	exited.ObservedAt = now.Add(time.Millisecond)
	exited.FinishedAt = now.Add(-time.Second)
	exited.JournalProvenance = []byte(`{"machine_id":"bounded-original-journal"}`)
	report = biz.NodeReport{Epoch: newState.Epoch, Sequence: 2, LogID: identity.LogID, PayloadDigest: "late-journal-record", JournalRequestID: requestID, Attempts: []biz.RuntimeAttempt{exited}}
	newState, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil {
		testContext.Fatal(err)
	}
	views, err = reopened.NodeTelemetry(ctx, identity.ConnectionRef)
	if err != nil || len(views[0].Attempts) != 1 || views[0].Attempts[0].State != 4 {
		testContext.Fatal("old positive exit overrode current Running", err)
	}
	if _, _, err = reopened.ReadNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, requestID); err == nil {
		testContext.Fatal("committed result did not retire issued scope")
	}
	replayed, err := reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil || replayed.CurrentRevision != newState.CurrentRevision {
		testContext.Fatal("last receipt replay required an already completed scope or remerged facts", err)
	}
	report.Sequence = 3
	report.PayloadDigest = "unsolicited-journal-record"
	report.JournalRequestID = biz.NewID()
	if _, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now); err == nil {
		testContext.Fatal("unsolicited result entered persistent facts")
	}
}

func TestPostgresNodeRecoveryScopeIsBoundedAndImmutable(testContext *testing.T) {
	database := openPGTest(testContext)
	ctx := context.Background()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: "node-recovery-limit:" + biz.NewID(), NodeUID: biz.NewID(), NodeName: "node-recovery", PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "node-agent", ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute)}
	state, err := database.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		testContext.Fatal(err)
	}
	firstID := biz.NewID()
	firstPod := biz.PodRef{ClusterID: biz.NewID(), UID: biz.NewID(), Namespace: "tenant", Name: "workload", ContainerName: "workspace"}
	if err = database.RegisterNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch, firstID, firstPod, nil); err != nil {
		testContext.Fatal(err)
	}
	changed := firstPod
	changed.UID = biz.NewID()
	if err = database.RegisterNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch, firstID, changed, nil); err == nil {
		testContext.Fatal("request ID changed authorization scope")
	}
	for index := 1; index < 16; index++ {
		pod := firstPod
		pod.UID = biz.NewID()
		if err = database.RegisterNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch, biz.NewID(), pod, nil); err != nil {
			testContext.Fatal(err)
		}
	}
	if err = database.RegisterNodeRecovery(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch, biz.NewID(), changed, nil); err == nil {
		testContext.Fatal("persisted request ledger exceeded its bound")
	}
}
