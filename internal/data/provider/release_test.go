package provider

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type releaseReader struct {
	biz.Reader
	bindings []biz.Binding
	claims   []biz.Claim
}

func (r releaseReader) Bindings(context.Context, biz.BindingFilter, string, int) ([]biz.Binding, error) {
	return r.bindings, nil
}

func (r releaseReader) PodEvidence(context.Context, biz.PodRef) ([]biz.Claim, []biz.ReleaseProof, error) {
	return r.claims, nil, nil
}

type releaseRepository struct {
	biz.Repository
	reader releaseReader
}

func (r releaseRepository) View(_ context.Context, read func(biz.Reader) error) error {
	return read(r.reader)
}

func TestReleaseProofsReconcileCurrentAbsenceForEveryClaimSource(t *testing.T) {
	for _, source := range []string{"scheduler", "allocation", "runtime"} {
		t.Run(source, func(t *testing.T) {
			reader, snapshot, node, now := releaseFixture(source)
			snapshot.Containers, snapshot.Statuses = nil, nil
			before := append([]biz.Claim(nil), reader.claims...)
			provider := &Provider{repo: releaseRepository{reader: reader}}
			proofs, err := provider.releaseProofs(context.Background(), "cluster", node, snapshot, true, now)
			if err != nil || len(proofs) != 1 {
				t.Fatalf("current absence did not retire historical %s claim: proofs=%+v error=%v", source, proofs, err)
			}
			proof := proofs[0]
			if proof.Retirement != biz.RetirementCurrentState || proof.RuntimeExited || proof.AdmissionRejected || !proof.AllocationAbsent || !proof.NodeOnline || proof.CheckedNodeRevision != snapshot.NodeRevision || proof.Pod != *reader.bindings[0].Pod || proof.NodeUID != node.UID || proof.DeviceID != reader.bindings[0].PhysicalDeviceID || proof.OwnerRef != reader.claims[0].OwnerRef || proof.ExecutionDigest != reader.claims[0].ExecutionDigest {
				t.Fatalf("release lost the exact allocation scope: %+v", proof)
			}
			binding, err := biz.MergeBinding(reader.bindings[0].BindingID, reader.claims, &proof, nil)
			if err != nil || binding.State != biz.ReleaseConfirmed || !binding.Observation.Complete {
				t.Fatalf("current reconciliation left unknown occupancy: binding=%+v error=%v", binding, err)
			}
			if !reflect.DeepEqual(before, reader.claims) {
				t.Fatal("release rewrote historical allocation claims")
			}
		})
	}
}

func TestReleaseProofsRequireCompleteCurrentSourcesAndNoLiveOccupancy(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*releaseReader, *nodeSnapshot, *NodeSource, *bool)
	}{
		{name: "status_pod_uid_conflict", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) {
			s.Statuses["exited-container"].Status.Labels["io.kubernetes.pod.uid"] = "replacement-pod"
		}},
		{name: "status_container_conflict", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) {
			s.Statuses["exited-container"].Status.Metadata.Name = "sidecar"
		}},
		{name: "missing_status", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) {
			delete(s.Statuses, "exited-container")
		}},
		{name: "created_state", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) {
			s.Statuses["exited-container"].Status.State = cri.ContainerState_CONTAINER_CREATED
		}},
		{name: "unknown_state", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) {
			s.Statuses["exited-container"].Status.State = cri.ContainerState_CONTAINER_UNKNOWN
		}},
		{name: "another_attempt_running", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) {
			s.Containers = append(s.Containers, &cri.Container{Id: "running-container", Metadata: &cri.ContainerMetadata{Name: "workspace"}, Labels: map[string]string{"io.kubernetes.pod.uid": "pod-uid"}})
			s.Statuses["running-container"] = &cri.ContainerStatusResponse{Status: &cri.ContainerStatus{Id: "running-container", Metadata: &cri.ContainerMetadata{Name: "workspace"}, Labels: map[string]string{"io.kubernetes.pod.uid": "pod-uid"}, State: cri.ContainerState_CONTAINER_RUNNING}}
		}},
		{name: "unmapped_current_container", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) {
			s.UnknownPodContainers = map[string][]string{"pod-uid": {"unmapped-current-container"}}
		}},
		{name: "gpu_allocation_present", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) {
			s.Resources.PodResources[0].Containers[0].Devices = []*pr.ContainerDevices{{ResourceName: "volcano.sh/vgpu-number", DeviceIds: []string{"NVIDIA-0"}}}
		}},
		{name: "podresources_unreadable", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) { s.ResourcesOK = false }},
		{name: "podresources_result_missing", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) { s.Resources = nil }},
		{name: "runtime_unreadable", change: func(_ *releaseReader, s *nodeSnapshot, _ *NodeSource, _ *bool) { s.RuntimeOK = false }},
		{name: "node_offline", change: func(_ *releaseReader, _ *nodeSnapshot, _ *NodeSource, online *bool) { *online = false }},
		{name: "different_node_uid", change: func(_ *releaseReader, _ *nodeSnapshot, n *NodeSource, _ *bool) { n.UID = "replacement-node" }},
		{name: "historical_pod_scope_conflict", change: func(r *releaseReader, _ *nodeSnapshot, _ *NodeSource, _ *bool) { r.claims[0].Pod.UID = "another-pod" }},
		{name: "no_historical_claim", change: func(r *releaseReader, _ *nodeSnapshot, _ *NodeSource, _ *bool) { r.claims = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, snapshot, node, now := releaseFixture("scheduler")
			online := true
			test.change(&reader, &snapshot, &node, &online)
			provider := &Provider{repo: releaseRepository{reader: reader}}
			proofs, err := provider.releaseProofs(context.Background(), "cluster", node, snapshot, online, now)
			if err != nil || len(proofs) != 0 {
				t.Fatalf("unresolved current state released occupancy: proofs=%+v error=%v", proofs, err)
			}
		})
	}
}

func TestReleaseProofsAcceptCurrentExitedWithoutHistoricalFinishedAt(t *testing.T) {
	reader, snapshot, node, now := releaseFixture("runtime")
	snapshot.Statuses["exited-container"].Status.FinishedAt = 0
	provider := &Provider{repo: releaseRepository{reader: reader}}
	proofs, err := provider.releaseProofs(context.Background(), "cluster", node, snapshot, true, now)
	if err != nil || len(proofs) != 1 || proofs[0].Retirement != biz.RetirementCurrentState || proofs[0].RuntimeExited {
		t.Fatalf("current EXITED unnecessarily required historical exit provenance: proofs=%+v error=%v", proofs, err)
	}
}

func releaseFixture(source string) (releaseReader, nodeSnapshot, NodeSource, time.Time) {
	now := time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)
	pod := biz.PodRef{ClusterID: "cluster", Namespace: "tenant", Name: "instance-0", UID: "pod-uid", ContainerName: "workspace"}
	ref := &biz.UsageRef{TenantID: "tenant", OwnerService: "ani-resource-service", ResourceID: "instance", CreateOperationID: "create"}
	claim := biz.Claim{Pod: pod, DeviceID: "device", NodeUID: "node-uid", Source: source, ObservedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-time.Minute), State: biz.Active, MemoryMiB: 1024, CoreLimitPercent: 10, BaselineDigest: strings.Repeat("a", 64), MemoryBlockMiB: 1, EvidenceRef: "scheduler:allocation", OwnerRef: ref, ExecutionDigest: strings.Repeat("b", 64)}
	reader := releaseReader{bindings: []biz.Binding{{BindingID: "binding", Pod: &pod, PhysicalDeviceID: "device", State: biz.AllocationUnknown}}, claims: []biz.Claim{claim}}
	snapshot := nodeSnapshot{
		ObservedAt: now, ResourcesOK: true, RuntimeOK: true, NodeRevision: 7,
		Resources:  &pr.ListPodResourcesResponse{PodResources: []*pr.PodResources{{Namespace: "tenant", Name: "instance-0", Containers: []*pr.ContainerResources{{Name: "workspace"}}}}},
		Containers: []*cri.Container{{Id: "exited-container", Metadata: &cri.ContainerMetadata{Name: "workspace"}, Labels: map[string]string{"io.kubernetes.pod.uid": "pod-uid"}}},
		Statuses:   map[string]*cri.ContainerStatusResponse{"exited-container": {Status: &cri.ContainerStatus{Id: "exited-container", Metadata: &cri.ContainerMetadata{Name: "workspace"}, Labels: map[string]string{"io.kubernetes.pod.uid": "pod-uid"}, State: cri.ContainerState_CONTAINER_EXITED, FinishedAt: now.Add(-time.Second).UnixNano()}}},
	}
	snapshot.SourceObservedAt = map[string]time.Time{"cri": now, "podresources": now}
	return reader, snapshot, NodeSource{UID: "node-uid", Name: "ani-03"}, now
}
