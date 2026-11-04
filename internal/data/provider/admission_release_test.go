package provider

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestReleaseTerminalOrDeletedPodWithoutRuntime(t *testing.T) {
	reader, snapshot, node, now := releaseFixture("scheduler")
	snapshot.Containers, snapshot.Statuses = nil, nil
	rejected := pod{Metadata: metadata{UID: "pod-uid", Name: "instance-0", Namespace: "tenant", ResourceVersion: "10", CreationTimestamp: now.Add(-2 * time.Minute).Format(time.RFC3339Nano)}}
	rejected.Spec.NodeName = node.Name
	rejected.Status.Phase, rejected.Status.Reason = "Failed", "UnexpectedAdmissionError"
	for _, test := range []struct {
		name   string
		change func(*releaseReader, *nodeSnapshot, *[]pod)
		want   bool
	}{
		{"rejected_before_runtime", func(*releaseReader, *nodeSnapshot, *[]pod) {}, true},
		{"pod_gone_without_exit_event", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { *pods = nil }, true},
		{"same_name_new_uid_without_old_allocation", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Metadata.UID = biz.NewID() }, true},
		{"same_uid_wrong_node", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Spec.NodeName = "other-node" }, false},
		{"same_uid_wrong_name", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Metadata.Name = "another-workload" }, false},
		{"pending_reservation", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Status.Phase = "Pending" }, false},
		{"running_restart_gap", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Status.Phase = "Running" }, false},
		{"ordinary_failure", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Status.Reason = "Error" }, true},
		{"succeeded", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Status.Phase = "Succeeded" }, true},
		{"deleted_pending", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) {
			(*pods)[0].Status.Phase = "Pending"
			(*pods)[0].Metadata.DeletionTimestamp = now.Add(-time.Second).Format(time.RFC3339Nano)
		}, true},
		{"no_historical_creation_time", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Metadata.CreationTimestamp = "" }, true},
		{"historical_open_event_gap", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) {
			s.SourceGaps = map[string]biz.TelemetryGap{"cri_events": {Start: now.Add(-time.Hour)}}
		}, true},
		{"historical_closed_event_gap", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) {
			s.SourceGaps = map[string]biz.TelemetryGap{"cri_events": {Start: now.Add(-time.Minute), End: now.Add(-time.Second)}}
		}, true},
		{"previous_runtime_claim", func(r *releaseReader, _ *nodeSnapshot, _ *[]pod) { r.claims[0].Source = "runtime" }, true},
		{"previous_allocation_claim", func(r *releaseReader, _ *nodeSnapshot, _ *[]pod) { r.claims[0].Source = "allocation" }, true},
		{"runtime_unavailable", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) { s.RuntimeOK = false }, false},
		{"allocation_unavailable", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) { s.ResourcesOK = false }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, s, pods := reader, snapshot, []pod{rejected}
			r.claims = append([]biz.Claim(nil), reader.claims...)
			test.change(&r, &s, &pods)
			provider := &Provider{repo: releaseRepository{reader: r}}
			proofs, err := provider.releaseProofsScoped(context.Background(), "connection", "cluster", node, s, true, now, nil, pods)
			if err != nil || (len(proofs) == 1) != test.want {
				t.Fatalf("want release=%v, proofs=%+v error=%v", test.want, proofs, err)
			}
			if !test.want {
				return
			}
			if proofs[0].Retirement != biz.RetirementCurrentState || proofs[0].RuntimeExited || proofs[0].AdmissionRejected || !proofs[0].AllocationAbsent || proofs[0].CheckedNodeRevision != 7 {
				t.Fatalf("current absence must not fabricate an exit or admission rejection: %+v", proofs[0])
			}
			binding, err := biz.MergeBinding(r.bindings[0].BindingID, r.claims, &proofs[0], nil)
			if err != nil || binding.State != biz.ReleaseConfirmed {
				t.Fatalf("complete current absence remained allocated: %+v, %v", binding, err)
			}
		})
	}
}
