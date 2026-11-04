package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

const liveGPUUUID = "GPU-a86cb9fe-53ca-e474-5375-b662a54e8878"
const assignmentClusterID = "10000000-0000-4000-8000-000000000001"

func liveAssignedPod() pod {
	workload := pod{Metadata: metadata{UID: "live-pod-uid", Name: "acc-plan-iris", Namespace: "acc-live", ResourceVersion: "100", Annotations: map[string]string{
		AssignedIDs: liveGPUUUID + ",NVIDIA,4096,10:",
	}}}
	workload.Spec.NodeName = "ani-03"
	main := container{Name: "inference"}
	main.Resources.Limits = map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "4096"}
	workload.Spec.Containers = []container{main}
	return workload
}

func assignedBindings(reader *Provider, workload pod, factor uint32) ([]biz.ObservedBinding, error) {
	group := biz.SupplyGroup{Baseline: &biz.ProviderBaseline{MemoryBlockMiB: factor, BaselineDigest: strings.Repeat("a", 64)}}
	return reader.podBindings(assignmentClusterID, group, workload, biz.NodeRef{UID: "node-uid", Name: "ani-03"}, []RegisteredDevice{{UUID: liveGPUUUID}})
}

func TestPodBindingsUseCurrentAssignmentWithoutHistoricalTime(t *testing.T) {
	for _, phase := range []string{"Pending", "Running"} {
		t.Run(phase, func(t *testing.T) {
			workload := liveAssignedPod()
			workload.Status.Phase = phase
			workload.Metadata.DeletionTimestamp = "2026-11-04T10:00:00Z"
			workload.Metadata.Annotations["volcano.sh/vgpu-time"] = "unknown"
			bindings, err := assignedBindings(&Provider{}, workload, 1)
			if err != nil || len(bindings) != 1 || bindings[0].MemoryMiB != 4096 || bindings[0].CoreLimitPercent != 10 {
				t.Fatalf("current reservation was lost: %+v, %v", bindings, err)
			}
			if bindings[0].Pod.UID != workload.Metadata.UID || bindings[0].NodeUID != "node-uid" {
				t.Fatalf("allocation identity changed: %+v", bindings[0])
			}
		})
	}
}

func TestPodBindingsUseCurrentVerifiedUnits(t *testing.T) {
	workload := liveAssignedPod()
	first, err := assignedBindings(&Provider{}, workload, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := assignedBindings(&Provider{}, workload, 10)
	if err != nil || len(second) != 1 || second[0].MemoryMiB != 40960 || second[0].MemoryBlockMiB != 10 || first[0].DeviceID != second[0].DeviceID {
		t.Fatalf("current block factor was not applied: %+v, %v", second, err)
	}
	workload.Metadata.Annotations["accelerator.ani.io/memory-block-mib"] = "1"
	if bindings, err := assignedBindings(&Provider{}, workload, 10); err == nil || len(bindings) != 0 {
		t.Fatalf("contradictory owner units accepted: %+v, %v", bindings, err)
	}
}

func TestPodBindingsValidateCurrentContainerAndDeviceMapping(t *testing.T) {
	for _, change := range []func(*pod){
		func(workload *pod) { workload.Metadata.Annotations[AssignedIDs] = "damaged" },
		func(workload *pod) { workload.Metadata.Annotations[AssignedIDs] = gpuUUID + ",NVIDIA,4096,10:" },
		func(workload *pod) { workload.Metadata.Annotations[AssignedIDs] += liveGPUUUID + ",NVIDIA,4096,10:" },
		func(workload *pod) { workload.Metadata.Annotations[AssignedIDs] += ";" },
		func(workload *pod) { workload.Spec.NodeName = "another-node" },
		func(workload *pod) { workload.Spec.Containers = nil },
	} {
		workload := liveAssignedPod()
		change(&workload)
		if bindings, err := assignedBindings(&Provider{}, workload, 1); err == nil || len(bindings) != 0 {
			t.Fatalf("invalid assignment produced partial allocations: %+v, %v", bindings, err)
		}
	}
	workload := liveAssignedPod()
	workload.Spec.Containers = append([]container{{Name: "sidecar"}}, workload.Spec.Containers...)
	bindings, err := assignedBindings(&Provider{}, workload, 1)
	if err != nil || len(bindings) != 1 || bindings[0].Pod.ContainerName != "inference" {
		t.Fatalf("non-GPU sidecar shifted assignment mapping: %+v, %v", bindings, err)
	}
	workload.Metadata.Annotations[AssignedIDs] = ""
	if bindings, err := assignedBindings(&Provider{}, workload, 0); err != nil || len(bindings) != 0 {
		t.Fatalf("valid empty assignment required an allocation decoder: %+v, %v", bindings, err)
	}
}

func signAssignedOwner(t *testing.T, workload *pod, ref biz.UsageRef, key ed25519.PrivateKey, digest string) {
	t.Helper()
	workload.Metadata.Labels = map[string]string{"accelerator.ani.io/resource-tenant-id": ref.TenantID, "accelerator.ani.io/owner-service": ref.OwnerService, "accelerator.ani.io/resource-id": ref.ResourceID, "accelerator.ani.io/create-operation-id": ref.CreateOperationID}
	workload.Metadata.Annotations["accelerator.ani.io/execution-digest"] = digest
	podRef := biz.PodRef{ClusterID: assignmentClusterID, Namespace: workload.Metadata.Namespace, Name: workload.Metadata.Name, UID: workload.Metadata.UID, ContainerName: "inference"}
	payload, err := biz.Canonical(OwnerAssertion{podRef, ref, digest})
	if err != nil {
		t.Fatal(err)
	}
	workload.Metadata.Annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("acc-owner-pod-v2\n"), payload...)))
}

func TestPodBindingsUseCurrentOwnerSignature(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	reader := &Provider{resourceOwnerKey: public}
	workload := liveAssignedPod()
	first, err := assignedBindings(reader, workload, 1)
	if err != nil || len(first) != 1 || first[0].OwnerRef != nil {
		t.Fatalf("unsigned allocation changed: %+v, %v", first, err)
	}
	ref := biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-resource-service", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	digest := strings.Repeat("b", 64)
	signAssignedOwner(t, &workload, ref, key, digest)
	current, err := assignedBindings(reader, workload, 1)
	if err != nil || len(current) != 1 || current[0].OwnerRef == nil || *current[0].OwnerRef != ref || current[0].ExecutionDigest != digest || current[0].DeviceID != first[0].DeviceID {
		t.Fatalf("late current signature was hidden by first observation: %+v, %v", current, err)
	}
	workload.Metadata.Annotations["accelerator.ani.io/owner-signature"] = "invalid"
	current, err = assignedBindings(reader, workload, 1)
	if err != nil || len(current) != 1 || current[0].OwnerRef != nil || current[0].ExecutionDigest != "" || current[0].MemoryMiB != 4096 {
		t.Fatalf("invalid signature acquired ownership or erased capacity: %+v, %v", current, err)
	}
}
