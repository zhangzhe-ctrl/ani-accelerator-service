package provider

import (
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

const liveGPUUUID = "GPU-a86cb9fe-53ca-e474-5375-b662a54e8878"

func liveAssignedPod() pod {
	p := pod{Metadata: metadata{UID: "live-pod-uid", Name: "acc-plan-iris", Namespace: "acc-live", ResourceVersion: "100", Annotations: map[string]string{
		AssignedIDs: "GPU-a86cb9fe-53ca-e474-5375-b662a54e8878,NVIDIA,4,10:", AssignedTime: "18446744073709551615", "volcano.sh/bind-time": "1792173153", "volcano.sh/bind-phase": "success",
	}}}
	p.Spec.NodeName = "ani-03"
	c := container{Name: "inference"}
	c.Resources.Limits = map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "4"}
	p.Spec.Containers = []container{c}
	return p
}

func assignedPodClaims(t *testing.T, pod pod, now time.Time) ([]biz.Claim, error) {
	t.Helper()
	status := &cri.ContainerStatusResponse{
		Status: &cri.ContainerStatus{Id: "live-container", Metadata: &cri.ContainerMetadata{Name: "inference"}, Labels: map[string]string{"io.kubernetes.pod.uid": pod.Metadata.UID}, State: cri.ContainerState_CONTAINER_RUNNING},
		Info:   map[string]string{"info": `{"runtimeSpec":{"process":{"env":["NVIDIA_VISIBLE_DEVICES=` + liveGPUUUID + `","CUDA_DEVICE_MEMORY_LIMIT_0=4096m","CUDA_DEVICE_SM_LIMIT=10"]}}}`},
	}
	snapshot := nodeSnapshot{
		ObservedAt: now, RuntimeOK: true, ResourcesOK: true,
		Containers: []*cri.Container{{Id: "live-container", Metadata: &cri.ContainerMetadata{Name: "inference"}, Labels: map[string]string{"io.kubernetes.pod.uid": pod.Metadata.UID}}},
		Statuses:   map[string]*cri.ContainerStatusResponse{"live-container": status},
		Resources:  &pr.ListPodResourcesResponse{PodResources: []*pr.PodResources{{Namespace: "acc-live", Name: "acc-plan-iris", Containers: []*pr.ContainerResources{{Name: "inference", Devices: []*pr.ContainerDevices{{ResourceName: "volcano.sh/vgpu-number", DeviceIds: []string{"NVIDIA-0"}}}}}}}},
	}
	group := biz.SupplyGroup{Baseline: &biz.ProviderBaseline{MemoryBlockMiB: 1024, BaselineDigest: strings.Repeat("a", 64)}}
	return (&Provider{}).podClaims("cluster", group, pod, NodeSource{UID: "node-uid", Name: "ani-03"}, snapshot, []RegisteredDevice{{UUID: liveGPUUUID}}, now)
}

func TestPodClaimsConsumedAssignmentUsesHistoricalBindTime(t *testing.T) {
	now := time.Unix(1792173213, 0).UTC()
	claims, err := assignedPodClaims(t, liveAssignedPod(), now)
	if err != nil || len(claims) != 3 {
		t.Fatalf("claims = %+v, error = %v", claims, err)
	}
	if claims[0].Source != "scheduler" || !claims[0].ObservedAt.Equal(time.Unix(1792173153, 0).UTC()) || claims[0].ObservedAt.Equal(now) {
		t.Fatalf("scheduler history was lost: %+v", claims[0])
	}
	for i, source := range []string{"scheduler", "runtime", "allocation"} {
		if claims[i].Source != source || claims[i].MemoryMiB != 4096 || claims[i].CoreLimitPercent != 10 {
			t.Fatalf("claim %d = %+v", i, claims[i])
		}
	}
	for _, test := range []struct {
		name   string
		change func(map[string]string)
	}{
		{name: "failed_marker", change: func(a map[string]string) { a["volcano.sh/bind-phase"] = "failed" }},
		{name: "allocating_marker", change: func(a map[string]string) { a["volcano.sh/bind-phase"] = "allocating" }},
		{name: "missing_phase", change: func(a map[string]string) { delete(a, "volcano.sh/bind-phase") }},
		{name: "missing_bind_time", change: func(a map[string]string) { delete(a, "volcano.sh/bind-time") }},
		{name: "invalid_bind_time", change: func(a map[string]string) { a["volcano.sh/bind-time"] = "0" }},
		{name: "nanosecond_bind_time", change: func(a map[string]string) { a["volcano.sh/bind-time"] = "1792173153000000000" }},
		{name: "unknown_assigned_time", change: func(a map[string]string) { a[AssignedTime] = "unknown" }},
		{name: "different_sentinel", change: func(a map[string]string) { a[AssignedTime] = "18446744073709551614" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := liveAssignedPod()
			test.change(p.Metadata.Annotations)
			if claims, err := assignedPodClaims(t, p, now); err == nil || len(claims) != 0 {
				t.Fatalf("unknown assignment time accepted: %+v, %v", claims, err)
			}
		})
	}
}

func TestSchedulerClaimStableAcrossPodStatusRevisions(t *testing.T) {
	now := time.Unix(1792173213, 0).UTC()
	p := liveAssignedPod()
	p.Metadata.Annotations[AssignedTime] = "1792173153"
	first, err := assignedPodClaims(t, p, now)
	if err != nil {
		t.Fatal(err)
	}
	p.Metadata.ResourceVersion = "101"
	p.Metadata.Annotations["unrelated-status-annotation"] = "new"
	p.Status.Phase = "Running"
	second, err := assignedPodClaims(t, p, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	a, b := first[0], second[0]
	a.ReceivedAt, b.ReceivedAt = time.Time{}, time.Time{}
	firstDigest, _ := biz.Hash(a)
	secondDigest, _ := biz.Hash(b)
	if firstDigest != secondDigest {
		t.Fatalf("unchanged scheduler fact changed with Pod status: first=%+v second=%+v", a, b)
	}
	p.Metadata.Annotations[AssignedIDs] = strings.Replace(p.Metadata.Annotations[AssignedIDs], ",4,10:", ",5,10:", 1)
	changed, err := assignedPodClaims(t, p, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if changed[0].Cursor == first[0].Cursor || changed[0].EvidenceRef == first[0].EvidenceRef {
		t.Fatal("different allocation reused the same scheduler revision")
	}
}
