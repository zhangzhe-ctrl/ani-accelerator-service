package provider

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func fixtureWorkload(fixture *nodePluginFixture, uid, phase string, assigned bool) pod {
	workload := liveAssignedPod()
	workload.Metadata.UID = uid
	workload.Metadata.Name = uid
	workload.Status.Phase = phase
	workload.Spec.NodeName = fixture.groups[0].Nodes[0].Name
	workload.Spec.NodeSelector = map[string]string{biz.GroupLabel: fixture.groups[0].GroupID}
	workload.Metadata.Annotations[AssignedIDs] = ""
	if assigned {
		workload.Metadata.Annotations[AssignedIDs] = gpuUUID + ",NVIDIA,409,10:"
	}
	return workload
}

func TestCollectCurrentReservationsWithoutNodeAgent(t *testing.T) {
	fixture := newNodePluginFixture(t)
	fixture.adopted = true
	pending := fixtureWorkload(fixture, "pending-assigned", "Pending", true)
	restarting := fixtureWorkload(fixture, "restarting", "Running", true)
	terminating := fixtureWorkload(fixture, "terminating", "Running", true)
	terminating.Metadata.DeletionTimestamp = time.Now().UTC().Format(time.RFC3339Nano)
	unplaced := fixtureWorkload(fixture, "unplaced", "Pending", false)
	unplaced.Spec.NodeName = ""
	fixture.workloads = []pod{pending, restarting, terminating, unplaced, fixtureWorkload(fixture, "failed", "Failed", true), fixtureWorkload(fixture, "succeeded", "Succeeded", true)}
	adapter := fixture.provider(t, fixture.references)
	batch, err := adapter.Collect(context.Background(), fixture.cluster, fixture.groups)
	if err != nil || !batch.PodListComplete || !batch.NodeListComplete || batch.SnapshotRef != "pods-rv-1" || len(batch.Bindings) != 3 || len(batch.IncompletePodUIDs) != 0 || len(batch.BindingIssues) != 0 {
		t.Fatalf("current collection = %+v, error = %v", batch, err)
	}
	if len(batch.LivePodUIDs) != 4 || slices.Contains(batch.LivePodUIDs, "failed") || slices.Contains(batch.LivePodUIDs, "succeeded") || batch.PendingByGroup[fixture.groups[0].GroupID] != 1 {
		t.Fatalf("terminal membership or pending counting changed: %+v", batch)
	}
	for _, binding := range batch.Bindings {
		if binding.MemoryMiB != 4090 || binding.MemoryBlockMiB != 10 {
			t.Fatalf("allocation units are not current blocks x F: %+v", binding)
		}
	}
	if len(batch.Devices) != 2 || len(batch.InventoryCompleteNodeUIDs) != 2 {
		t.Fatalf("registered device inventory was lost: %+v", batch)
	}
	for _, record := range batch.Devices {
		if !record.Device.Observation.Complete || record.SafeMemoryBudgetMiB != 24570 || record.Device.PhysicalMemoryBytes != 0 {
			t.Fatalf("missing physical bytes blocked registered supply: %+v", record)
		}
	}
}

func TestCollectKeepsSuccessfulPodLifecycleWhenNodeOrConfigFails(t *testing.T) {
	for _, fault := range []string{"node_list", "bad_registration", "factor_drift", "config_unknown"} {
		t.Run(fault, func(t *testing.T) {
			fixture := newNodePluginFixture(t)
			fixture.adopted = true
			fixture.fault = fault
			live := fixtureWorkload(fixture, "live", "Running", true)
			// config_unknown affects the shared-node override.
			if fault == "config_unknown" {
				live.Spec.NodeName = fixture.groups[1].Nodes[0].Name
				live.Metadata.Annotations[AssignedIDs] = gpuUUID2 + ",NVIDIA,409,10:"
			}
			fixture.workloads = []pod{live, fixtureWorkload(fixture, "finished", "Failed", true), fixtureWorkload(fixture, "empty", "Running", false)}
			adapter := fixture.provider(t, fixture.references)
			batch, err := adapter.Collect(context.Background(), fixture.cluster, fixture.groups)
			if err != nil || !batch.PodListComplete || batch.SnapshotRef != "pods-rv-1" || !slices.Contains(batch.IncompletePodUIDs, "live") || slices.Contains(batch.IncompletePodUIDs, "empty") || slices.Contains(batch.LivePodUIDs, "finished") || len(batch.Bindings) != 0 {
				t.Fatalf("failed inventory discarded successful Pod lifecycle: %+v, %v", batch, err)
			}
			if fault == "node_list" && (batch.NodeListComplete || len(batch.Devices) != 0 || len(batch.InventoryCompleteNodeUIDs) != 0) {
				t.Fatalf("failed Node list became an empty current inventory: %+v", batch)
			}
		})
	}
}

func TestCollectSeparatesIncompleteAndEmptyPodLists(t *testing.T) {
	for _, fault := range []string{"pod_list", "pod_page", "pod_revision", ""} {
		t.Run("source_"+fault, func(t *testing.T) {
			fixture := newNodePluginFixture(t)
			fixture.adopted = true
			fixture.fault = fault
			adapter := fixture.provider(t, fixture.references)
			batch, err := adapter.Collect(context.Background(), fixture.cluster, fixture.groups)
			if err != nil || batch.PodListComplete != (fault == "") || !batch.NodeListComplete || len(batch.Devices) != 2 {
				t.Fatalf("independent source completeness changed: %+v, %v", batch, err)
			}
			if fault != "" && (batch.SnapshotRef != "" || !batch.ObservedAt.IsZero() || batch.PendingByGroup != nil || len(batch.Bindings) != 0 || len(batch.LivePodUIDs) != 0) {
				t.Fatalf("failed list supplied replacement fields: %+v", batch)
			}
		})
	}
}

func TestCollectUnhealthyNodesRetainAllocationAndInventory(t *testing.T) {
	for _, fault := range []string{"node_offline", "unhealthy"} {
		t.Run(fault, func(t *testing.T) {
			fixture := newNodePluginFixture(t)
			fixture.adopted = true
			fixture.fault = fault
			fixture.workloads = []pod{fixtureWorkload(fixture, "live", "Running", true)}
			batch, err := fixture.provider(t, fixture.references).Collect(context.Background(), fixture.cluster, fixture.groups)
			if err != nil || len(batch.Bindings) != 1 || len(batch.InventoryCompleteNodeUIDs) != 2 || len(batch.Devices) != 2 || len(batch.IncompletePodUIDs) != 0 {
				t.Fatalf("health erased a current assignment or inventory: %+v, %v", batch, err)
			}
			for _, record := range batch.Devices {
				if record.Device.Observation.Complete {
					t.Fatalf("unavailable card became allocatable: %+v", record)
				}
			}
		})
	}
}

func TestCollectMalformedPodDoesNotEraseOtherPodLifecycle(t *testing.T) {
	fixture := newNodePluginFixture(t)
	fixture.adopted = true
	bad := fixtureWorkload(fixture, "malformed", "Running", true)
	bad.Metadata.Annotations[AssignedIDs] = "broken"
	fixture.workloads = []pod{bad, fixtureWorkload(fixture, "valid", "Pending", true), fixtureWorkload(fixture, "terminal", "Succeeded", true)}
	batch, err := fixture.provider(t, fixture.references).Collect(context.Background(), fixture.cluster, fixture.groups)
	if err != nil || !batch.PodListComplete || len(batch.IncompletePodUIDs) != 1 || batch.IncompletePodUIDs[0] != "malformed" || len(batch.Bindings) != 1 || batch.Bindings[0].Pod.UID != "valid" || slices.Contains(batch.LivePodUIDs, "terminal") {
		t.Fatalf("one damaged Pod lost independent current lifecycle: %+v, %v", batch, err)
	}
}

func TestProviderRuntimeClassSelectorConflictsRemainRejected(t *testing.T) {
	fixture := newNodePluginFixture(t)
	fixture.fault = "runtimeclass_selector"
	if _, err := fixture.provider(t, fixture.references).Inspect(context.Background(), fixture.cluster, fixture.groups[0]); err == nil {
		t.Fatal("RuntimeClass selector conflict was accepted")
	}
}
