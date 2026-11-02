package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

// The kubelet rejected this Pod before creating a container. There is a
// scheduler claim, but consequently no container exit to recover from CRI.
func TestReleaseRejectedAdmissionWithoutContainer(t *testing.T) {
	reader, snapshot, node, now := releaseFixture("scheduler")
	snapshot.Containers, snapshot.Statuses = nil, nil
	snapshot.NodeRevision = 7
	var rejected pod
	if err := json.Unmarshal([]byte(`{"metadata":{"uid":"pod-uid","name":"instance-0","namespace":"tenant","resourceVersion":"10","creationTimestamp":"2026-10-17T02:58:00Z"},"spec":{"nodeName":"ani-03"},"status":{"phase":"Failed","reason":"UnexpectedAdmissionError"}}`), &rejected); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*releaseReader, *nodeSnapshot, *[]pod)
		want   bool
	}{
		{"rejected_before_runtime", func(*releaseReader, *nodeSnapshot, *[]pod) {}, true},
		{"deleted_without_rejection", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { *pods = nil }, false},
		{"different_pod_uid", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Metadata.UID = "replacement" }, false},
		{"different_node", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Spec.NodeName = "other-node" }, false},
		{"not_terminal", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Status.Phase = "Pending" }, false},
		{"container_failed_after_start", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Status.Reason = "Error" }, false},
		{"missing_creation_time", func(_ *releaseReader, _ *nodeSnapshot, pods *[]pod) { (*pods)[0].Metadata.CreationTimestamp = "" }, false},
		{"open_runtime_gap", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) {
			s.SourceGaps = map[string]biz.TelemetryGap{"cri_events": {Start: now.Add(-time.Hour)}}
		}, false},
		{"old_runtime_gap", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) {
			s.SourceGaps = map[string]biz.TelemetryGap{"cri_events": {End: now.Add(-time.Hour)}}
		}, true},
		{"previous_runtime_claim", func(r *releaseReader, _ *nodeSnapshot, _ *[]pod) { r.claims[0].Source = "runtime" }, false},
		{"previous_allocation_claim", func(r *releaseReader, _ *nodeSnapshot, _ *[]pod) { r.claims[0].Source = "allocation" }, false},
		{"runtime_unavailable", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) { s.RuntimeOK = false }, false},
		{"allocation_unavailable", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) { s.ResourcesOK = false }, false},
		{"uncovered_runtime_gap", func(_ *releaseReader, s *nodeSnapshot, _ *[]pod) {
			s.SourceGaps = map[string]biz.TelemetryGap{"cri_events": {Start: now.Add(-time.Minute), End: now.Add(-time.Second)}}
		}, false},
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
			if proofs[0].RuntimeExited || !proofs[0].AllocationAbsent || proofs[0].CheckedNodeRevision != 7 {
				t.Fatalf("admission rejection must not fabricate a container exit: %+v", proofs[0])
			}
			binding, err := biz.MergeBinding(r.bindings[0].BindingID, r.claims, &proofs[0], nil)
			if err != nil || binding.State != biz.ReleaseConfirmed {
				t.Fatalf("rejected admission remained allocated: %+v, %v", binding, err)
			}
		})
	}
}
