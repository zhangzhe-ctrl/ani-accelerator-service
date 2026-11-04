package provider

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestCurrentSnapshotRetiresBindingAfterManyUnprovenHistoricalAttempts(t *testing.T) {
	reader, _, node, now := releaseFixture("allocation")
	view := biz.NodeTelemetryView{
		State: biz.NodeTelemetryState{
			Identity:        biz.NodeIdentity{NodeUID: node.UID, NodeName: node.Name},
			Epoch:           1,
			CurrentRevision: 7,
			LeaseExpiresAt:  now.Add(time.Minute),
			Snapshot: &biz.TelemetrySnapshot{
				CaptureEpoch: 1,
				CaptureStart: now.Add(-time.Second),
				CaptureEnd:   now,
				Sources: []biz.TelemetrySource{
					{Name: "cri", Complete: true, ObservedAt: now},
					{Name: "podresources", Complete: true, ObservedAt: now},
				},
			},
			Gaps: map[string]biz.TelemetryGap{"cri_events": {Start: now.Add(-2 * time.Minute)}},
		},
	}
	for index := 0; index < 37; index++ {
		view.Attempts = append(view.Attempts, biz.RuntimeAttempt{
			ContainerID:   fmt.Sprintf("%064x", index+1),
			PodUID:        "pod-uid",
			PodNamespace:  "tenant",
			PodName:       "instance-0",
			ContainerName: "workspace",
			State:         4,
			ObservedAt:    now.Add(-time.Minute),
		})
	}
	// One old record was never identified. A complete current list supersedes
	// that old observation; it must not mark this node's current CRI unhealthy.
	view.Attempts[0].PodUID, view.Attempts[0].ContainerName = "", ""
	snapshot := telemetryNodeSnapshot(view, now, time.Minute)
	if !snapshot.RuntimeOK || !snapshot.ResourcesOK || len(snapshot.Containers) != 0 {
		t.Fatalf("old incomplete history contaminated the complete current snapshot: %+v", snapshot)
	}
	provider := &Provider{repo: releaseRepository{reader: reader}}
	proofs, err := provider.releaseProofs(context.Background(), "cluster", node, snapshot, true, now)
	if err != nil || len(proofs) != 1 || proofs[0].Retirement != biz.RetirementCurrentState || proofs[0].RuntimeExited {
		t.Fatalf("37 historical attempts still required exit recovery: proofs=%+v error=%v", proofs, err)
	}
	binding, err := biz.MergeBinding(reader.bindings[0].BindingID, reader.claims, &proofs[0], nil)
	if err != nil || binding.State != biz.ReleaseConfirmed {
		t.Fatalf("current absence did not restore the binding: binding=%+v error=%v", binding, err)
	}
	if len(view.Attempts) != 37 || view.Attempts[0].PodUID != "" || view.Attempts[0].State != 4 {
		t.Fatal("reconciliation fabricated or deleted diagnostic history")
	}
}

func TestCurrentReconciliationTimeDoesNotCoverNewerClaim(t *testing.T) {
	reader, snapshot, node, now := releaseFixture("runtime")
	snapshot.Containers, snapshot.Statuses = nil, nil
	snapshot.SourceObservedAt["cri"] = now.Add(-2 * time.Second)
	snapshot.SourceObservedAt["podresources"] = now.Add(-time.Second)
	reader.claims[0].ObservedAt = now.Add(-500 * time.Millisecond)
	provider := &Provider{repo: releaseRepository{reader: reader}}
	proofs, err := provider.releaseProofs(context.Background(), "cluster", node, snapshot, true, now)
	if err != nil || len(proofs) != 1 {
		t.Fatalf("complete snapshot did not produce its bounded reconciliation: proofs=%+v error=%v", proofs, err)
	}
	if proofs[0].ObservedAt.After(snapshot.SourceObservedAt["cri"]) {
		t.Fatalf("reconciliation advanced past its oldest current source: %+v", proofs[0])
	}
	binding, err := biz.MergeBinding(reader.bindings[0].BindingID, reader.claims, &proofs[0], nil)
	if err != nil || binding.State == biz.ReleaseConfirmed {
		t.Fatalf("older current sources erased a newer claim: binding=%+v error=%v", binding, err)
	}
}
