package data

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresNodeTelemetryCommitReplayRestartAndFencing(t *testing.T) {
	database := openPGTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: "node-test:" + biz.NewID(), NodeUID: biz.NewID(), NodeName: "node-test", PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "agent-test", ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute)}
	state, err := database.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	report := biz.NodeReport{Epoch: state.Epoch, Sequence: 1, LogID: identity.LogID, PayloadDigest: "record-one", OriginPodUID: identity.PodUID, OriginInstanceID: identity.InstanceID, OriginBootID: identity.BootID, Snapshot: &biz.TelemetrySnapshot{CaptureEpoch: state.Epoch, CaptureStart: now, CaptureEnd: now, Sources: []biz.TelemetrySource{{Name: "cri", Complete: true, ObservedAt: now}}, Containers: []biz.RuntimeAttempt{{ContainerID: "container-one", PodUID: biz.NewID(), ContainerName: "app", State: 2, ObservedAt: now}}}}
	state, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil {
		t.Fatal(err)
	}
	revision := state.CurrentRevision
	reopened := openPGTest(t)
	state, err = reopened.OpenNodeSession(ctx, identity, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSequence != 1 || state.LastPayloadDigest != "record-one" {
		t.Fatal("restart lost committed receipt")
	}
	if _, err = database.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now); err == nil {
		t.Fatal("old stream not fenced")
	}
	report.Epoch = state.Epoch
	state, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentRevision != revision {
		t.Fatal("ACK loss replay advanced fact revision")
	}
	views, err := reopened.NodeTelemetry(ctx, identity.ConnectionRef)
	if err != nil || len(views) != 1 || len(views[0].Attempts) != 1 {
		t.Fatalf("committed facts missing or duplicated: %v %v", views, err)
	}
	bad := report
	bad.PayloadDigest = "record-different"
	if _, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, bad, now); err == nil {
		t.Fatal("accepted changed same-sequence payload")
	}
	bad = report
	bad.Sequence = 3
	if _, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, bad, now); err == nil {
		t.Fatal("accepted sequence gap")
	}
	if err = reopened.RevokeNodeSession(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.SaveNodeReport(ctx, identity.ConnectionRef, identity.NodeUID, report, now); err == nil {
		t.Fatal("revoked identity still committed")
	}
}
