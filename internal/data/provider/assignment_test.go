package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
		AssignedIDs: "GPU-a86cb9fe-53ca-e474-5375-b662a54e8878,NVIDIA,4096,10:", AssignedTime: "18446744073709551615", "volcano.sh/bind-time": "1792173153", "volcano.sh/bind-phase": "success",
	}}}
	p.Spec.NodeName = "ani-03"
	c := container{Name: "inference"}
	c.Resources.Limits = map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "4096"}
	p.Spec.Containers = []container{c}
	return p
}

func assignedPodClaims(t *testing.T, pod pod, now time.Time) ([]biz.Claim, error) {
	return assignedPodClaimsUsing(t, &Provider{}, pod, now)
}

func assignedPodClaimsUsing(t *testing.T, reader *Provider, pod pod, now time.Time) ([]biz.Claim, error) {
	return assignedPodClaimsAtFactor(t, reader, pod, now, 1)
}

func assignedPodClaimsAtFactor(t *testing.T, reader *Provider, pod pod, now time.Time, factor uint32, environment ...string) ([]biz.Claim, error) {
	t.Helper()
	if environment == nil {
		environment = []string{"NVIDIA_VISIBLE_DEVICES=" + liveGPUUUID, "CUDA_DEVICE_MEMORY_LIMIT_0=4096m", "CUDA_DEVICE_SM_LIMIT=10"}
	}
	info, err := json.Marshal(map[string]any{"runtimeSpec": map[string]any{"process": map[string]any{"env": environment}}})
	if err != nil {
		t.Fatal(err)
	}
	status := &cri.ContainerStatusResponse{
		Status: &cri.ContainerStatus{Id: "live-container", Metadata: &cri.ContainerMetadata{Name: "inference"}, Labels: map[string]string{"io.kubernetes.pod.uid": pod.Metadata.UID}, State: cri.ContainerState_CONTAINER_RUNNING},
		Info:   map[string]string{"info": string(info)},
	}
	snapshot := nodeSnapshot{
		ObservedAt: now, RuntimeOK: true, ResourcesOK: true,
		Containers: []*cri.Container{{Id: "live-container", Metadata: &cri.ContainerMetadata{Name: "inference"}, Labels: map[string]string{"io.kubernetes.pod.uid": pod.Metadata.UID}}},
		Statuses:   map[string]*cri.ContainerStatusResponse{"live-container": status},
		Resources:  &pr.ListPodResourcesResponse{PodResources: []*pr.PodResources{{Namespace: "acc-live", Name: "acc-plan-iris", Containers: []*pr.ContainerResources{{Name: "inference", Devices: []*pr.ContainerDevices{{ResourceName: "volcano.sh/vgpu-number", DeviceIds: []string{"NVIDIA-0"}}}}}}}},
	}
	group := biz.SupplyGroup{Baseline: &biz.ProviderBaseline{MemoryBlockMiB: factor, BaselineDigest: strings.Repeat("a", 64)}}
	if factor == 10 {
		group.Baseline.BaselineDigest = strings.Repeat("b", 64)
	}
	return reader.podClaims(context.Background(), "cluster", group, pod, NodeSource{UID: "node-uid", Name: "ani-03"}, snapshot, []RegisteredDevice{{UUID: liveGPUUUID}}, now)
}

func TestTerminalPodClaimsKeepCurrentOccupancyWithoutSchedulingIntent(t *testing.T) {
	for _, test := range []struct {
		name       string
		deleting   bool
		allocation bool
		current    bool
		state      cri.ContainerState
		source     string
	}{
		{name: "failed_still_allocated", allocation: true, source: "allocation"},
		{name: "deleting_still_allocated", deleting: true, allocation: true, source: "allocation"},
		{name: "failed_absent"},
		{name: "deleting_absent", deleting: true},
		{name: "failed_exited", current: true, state: cri.ContainerState_CONTAINER_EXITED},
		{name: "failed_created", current: true, state: cri.ContainerState_CONTAINER_CREATED, source: "runtime"},
		{name: "failed_unknown", current: true, state: cri.ContainerState_CONTAINER_UNKNOWN, source: "runtime"},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1792173213, 0).UTC()
			pod := liveAssignedPod()
			pod.Status.Phase = "Failed"
			if test.deleting {
				pod.Status.Phase = "Pending"
				pod.Metadata.DeletionTimestamp = now.Add(-time.Second).Format(time.RFC3339Nano)
			}
			snapshot := nodeSnapshot{ObservedAt: now, RuntimeOK: true, ResourcesOK: true, Resources: &pr.ListPodResourcesResponse{}}
			snapshot.SourceObservedAt = map[string]time.Time{"cri": now.Add(-time.Second), "podresources": now.Add(-2 * time.Second)}
			if test.allocation {
				snapshot.Resources.PodResources = []*pr.PodResources{{Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name, Containers: []*pr.ContainerResources{{Name: "inference", Devices: []*pr.ContainerDevices{{ResourceName: "volcano.sh/vgpu-number", DeviceIds: []string{"NVIDIA-0"}}}}}}}
			}
			if test.current {
				labels := map[string]string{"io.kubernetes.pod.uid": pod.Metadata.UID}
				snapshot.Containers = []*cri.Container{{Id: "current-container", Metadata: &cri.ContainerMetadata{Name: "inference"}, Labels: labels}}
				snapshot.Statuses = map[string]*cri.ContainerStatusResponse{"current-container": {Status: &cri.ContainerStatus{Id: "current-container", Metadata: &cri.ContainerMetadata{Name: "inference"}, Labels: labels, State: test.state}}}
			}
			group := biz.SupplyGroup{Baseline: &biz.ProviderBaseline{MemoryBlockMiB: 1, BaselineDigest: strings.Repeat("a", 64)}}
			claims, err := (&Provider{}).podClaims(context.Background(), "cluster", group, pod, NodeSource{UID: "node-uid", Name: "ani-03"}, snapshot, []RegisteredDevice{{UUID: liveGPUUUID}}, now)
			if err != nil {
				t.Fatal(err)
			}
			if test.source == "" {
				if len(claims) != 0 {
					t.Fatalf("terminal Pod without current occupancy retained its scheduler reservation: %+v", claims)
				}
				return
			}
			if len(claims) != 1 {
				t.Fatalf("current occupancy should produce one unresolved source, without scheduler intent: %+v", claims)
			}
			claim := claims[0]
			if claim.Source != test.source || claim.State != biz.AllocationUnknown || claim.Pod.UID != pod.Metadata.UID || claim.Pod.ContainerName != "inference" || claim.NodeUID != "node-uid" || claim.DeviceID != PhysicalID("cluster", "node-uid", liveGPUUUID) || claim.MemoryMiB != 4096 {
				t.Fatalf("current occupancy lost source, identity or quantity: %+v", claim)
			}
			if test.source == "allocation" && !claim.ObservedAt.Equal(snapshot.SourceObservedAt["podresources"]) {
				t.Fatalf("allocation used an old scheduler time instead of current source time: %+v", claim)
			}
			binding, err := biz.MergeBinding("binding", claims, nil, nil)
			if err != nil || binding.State != biz.AllocationUnknown || binding.Observation.Complete {
				t.Fatalf("unresolved current occupancy became available: binding=%+v error=%v", binding, err)
			}
		})
	}
}

func TestPodClaimsRuntimeEnvironmentDuplicatesDoNotDuplicateBinding(t *testing.T) {
	now := time.Unix(1792173213, 0).UTC()
	for _, selected := range []string{liveGPUUUID, gpuUUID} {
		t.Run(selected, func(t *testing.T) {
			claims, err := assignedPodClaimsAtFactor(t, &Provider{}, liveAssignedPod(), now, 1,
				"NVIDIA_VISIBLE_DEVICES=all", "NVIDIA_VISIBLE_DEVICES="+selected,
				"CUDA_DEVICE_MEMORY_LIMIT_0=4096m", "CUDA_DEVICE_SM_LIMIT=10")
			if selected != liveGPUUUID {
				if err == nil || len(claims) != 0 {
					t.Fatalf("effective runtime GPU disagreed with scheduler but was accepted: claims=%+v error=%v", claims, err)
				}
				return
			}
			if err != nil || len(claims) != 3 {
				t.Fatalf("effective GPU did not retain three independent sources: claims=%+v error=%v", claims, err)
			}
			for index, source := range []string{"scheduler", "runtime", "allocation"} {
				if claims[index].Source != source || claims[index].DeviceID != claims[0].DeviceID || claims[index].MemoryMiB != 4096 {
					t.Fatalf("runtime normalization changed source, device or units: %+v", claims)
				}
			}
			binding, err := biz.MergeBinding("binding", claims, nil, nil)
			if err != nil || binding.State != biz.Active || !binding.Observation.Complete || binding.MemoryMiB != 4096 {
				t.Fatalf("three agreeing sources did not merge into one allocation: binding=%+v error=%v", binding, err)
			}
		})
	}
}

func TestSchedulerClaimKeepsHistoricalFactorAcrossLiveFactorChange(t *testing.T) {
	now := time.Unix(1792173213, 0).UTC()
	pod := liveAssignedPod()
	previous, err := assignedPodClaims(t, pod, now)
	if err != nil {
		t.Fatal(err)
	}
	reader := &Provider{repo: releaseRepository{reader: releaseReader{claims: previous[:1]}}}
	current, err := assignedPodClaimsAtFactor(t, reader, pod, now.Add(time.Second), 10)
	if err != nil || len(current) != 3 {
		t.Fatalf("historical factor reuse failed: %+v error=%v", current, err)
	}
	if current[0].MemoryMiB != 4096 || current[0].MemoryBlockMiB != 1 || current[0].BaselineDigest != previous[0].BaselineDigest || current[0].Cursor != previous[0].Cursor {
		t.Fatalf("historical allocation was reinterpreted with the new factor: %+v", current[0])
	}
	if current[1].MemoryMiB != 4096 || current[2].MemoryMiB != 4096 {
		t.Fatalf("runtime MiB or historical allocation was multiplied again: %+v", current)
	}
	pod.Metadata.Annotations[AssignedIDs] = strings.Replace(pod.Metadata.Annotations[AssignedIDs], ",4096,10:", ",5120,10:", 1)
	changed, err := assignedPodClaimsAtFactor(t, reader, pod, now.Add(2*time.Second), 10)
	if err != nil || changed[0].MemoryMiB != 51200 || changed[0].Cursor == previous[0].Cursor {
		t.Fatalf("changed raw assignment was hidden by historical reuse: %+v error=%v", changed, err)
	}
}

func signAssignedOwner(t *testing.T, pod *pod, ref biz.UsageRef, key ed25519.PrivateKey, digest string) {
	t.Helper()
	pod.Metadata.Labels = map[string]string{"accelerator.ani.io/resource-tenant-id": ref.TenantID, "accelerator.ani.io/owner-service": ref.OwnerService, "accelerator.ani.io/resource-id": ref.ResourceID, "accelerator.ani.io/create-operation-id": ref.CreateOperationID}
	pod.Metadata.Annotations["accelerator.ani.io/execution-digest"] = digest
	podRef := biz.PodRef{ClusterID: "cluster", Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name, UID: pod.Metadata.UID, ContainerName: "inference"}
	payload, err := biz.Canonical(OwnerAssertion{podRef, ref, digest})
	if err != nil {
		t.Fatal(err)
	}
	pod.Metadata.Annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("acc-owner-pod-v2\n"), payload...)))
}

func TestSchedulerHistoryDoesNotHideConflictingOrInvalidOwner(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		name := "two_verified_owners_conflict"
		if invalid {
			name = "invalid_late_signature_is_unassociated"
		}
		t.Run(name, func(t *testing.T) {
			public, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			reader := &Provider{resourceOwnerKey: public}
			pod := liveAssignedPod()
			now := time.Now().UTC()
			firstOwner := biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-resource-service", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
			if !invalid {
				signAssignedOwner(t, &pod, firstOwner, key, strings.Repeat("b", 64))
			}
			first, err := assignedPodClaimsUsing(t, reader, pod, now)
			if err != nil {
				t.Fatal(err)
			}
			reader.repo = releaseRepository{reader: releaseReader{claims: first[:1]}}
			lateOwner := biz.UsageRef{TenantID: firstOwner.TenantID, OwnerService: "ani-resource-service", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
			digest := strings.Repeat("c", 64)
			signAssignedOwner(t, &pod, lateOwner, key, digest)
			if invalid {
				pod.Metadata.Annotations["accelerator.ani.io/owner-signature"] = "invalid"
			}
			current, err := assignedPodClaimsUsing(t, reader, pod, now.Add(time.Second))
			if err != nil || len(current) != 3 || current[0].Cursor != first[0].Cursor || current[0].EvidenceRef != first[0].EvidenceRef {
				t.Fatalf("historical scheduler revision changed: claims=%+v error=%v", current, err)
			}
			for _, claim := range current[1:] {
				if invalid && claim.OwnerRef != nil || !invalid && (claim.OwnerRef == nil || *claim.OwnerRef != lateOwner || claim.ExecutionDigest != digest) {
					t.Fatalf("current owner signature boundary was bypassed: %+v", claim)
				}
			}
			usage := &biz.UsageProjection{Ref: &lateOwner, ExecutionDigest: digest, ClusterID: "cluster", ContainerName: "inference"}
			binding, err := biz.MergeBinding("binding", current, nil, usage)
			if err != nil || binding.VerifiedUsageRef != nil || !invalid && !strings.Contains(strings.Join(binding.Observation.Issues, ","), "OWNERSHIP_CONFLICT") {
				t.Fatalf("conflicting/invalid owner acquired attribution: binding=%+v error=%v", binding, err)
			}
		})
	}
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
	p.Metadata.Annotations[AssignedIDs] = strings.Replace(p.Metadata.Annotations[AssignedIDs], ",4096,10:", ",5120,10:", 1)
	changed, err := assignedPodClaims(t, p, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if changed[0].Cursor == first[0].Cursor || changed[0].EvidenceRef == first[0].EvidenceRef {
		t.Fatal("different allocation reused the same scheduler revision")
	}
}
