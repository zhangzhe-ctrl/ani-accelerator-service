package provider

import (
	"sort"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type orphanObservation struct {
	runtime    []*cri.ContainerStatusResponse
	unknownIDs []string
	resources  []*pr.ContainerDevices
}

// Only current signals for an already known binding are considered. Pod API
// absence neither discards those signals nor creates occupancy by itself.
func observeOrphanBinding(ref biz.PodRef, node NodeSource, snapshot nodeSnapshot, pods []pod) *orphanObservation {
	for _, current := range pods {
		if current.Metadata.UID == ref.UID {
			return nil // The normal Pod collection owns this UID.
		}
	}
	result := &orphanObservation{}
	seen := map[string]bool{}
	for _, container := range snapshot.Containers {
		if container == nil || snapshot.HistoricalStatuses[container.Id] || container.Labels["io.kubernetes.pod.uid"] != ref.UID {
			continue
		}
		if container.Metadata != nil && container.Metadata.Name != "" && container.Metadata.Name != ref.ContainerName {
			continue
		}
		seen[container.Id] = true
		status := snapshot.Statuses[container.Id]
		if status == nil || status.Status == nil || status.Status.Labels["io.kubernetes.pod.uid"] != ref.UID || status.Status.Metadata == nil || status.Status.Metadata.Name != ref.ContainerName {
			result.unknownIDs = append(result.unknownIDs, container.Id)
			continue
		}
		if status.Status.State != cri.ContainerState_CONTAINER_EXITED {
			result.runtime = append(result.runtime, status)
		}
	}
	for _, id := range snapshot.UnknownPodContainers[ref.UID] {
		if !seen[id] && !snapshot.HistoricalStatuses[id] {
			result.unknownIDs = append(result.unknownIDs, id)
			seen[id] = true
		}
	}
	if resources, complete := resourcesFor(snapshot, ref.Namespace, ref.Name, ref.ContainerName); complete && hasGPUResources(resources) && !replacementAllocationObserved(snapshot, ref, node, pods) {
		result.resources = resources
	}
	if len(result.runtime) == 0 && len(result.unknownIDs) == 0 && len(result.resources) == 0 {
		return nil
	}
	return result
}

func (observation *orphanObservation) claims(prior biz.Claim, snapshot nodeSnapshot, now time.Time) []biz.Claim {
	var result []biz.Claim
	runtimeKnown := false
	if len(observation.runtime) != 0 || len(observation.unknownIDs) != 0 {
		claim := prior
		claim.Source, claim.State = "runtime", biz.AllocationUnknown
		claim.ObservedAt, claim.ReceivedAt = time.Time{}, now
		ids := append([]string(nil), observation.unknownIDs...)
		for _, status := range observation.runtime {
			ids = append(ids, status.Status.Id)
			if at := runtimeObservation(snapshot, status); at.After(claim.ObservedAt) {
				claim.ObservedAt = at
			}
		}
		for _, id := range observation.unknownIDs {
			at := snapshot.FactObservedAt[id]
			if at.IsZero() {
				at = currentSourceTime(snapshot, "cri")
			}
			if at.After(claim.ObservedAt) {
				claim.ObservedAt = at
			}
		}
		if snapshot.RuntimeOK && len(observation.runtime) == 1 && len(observation.unknownIDs) == 0 {
			status := observation.runtime[0]
			allocation, err := ParseContainerdInfo(status.Info["info"])
			if err == nil && status.Status.State == cri.ContainerState_CONTAINER_RUNNING && PhysicalID(prior.Pod.ClusterID, prior.NodeUID, allocation.UUID) == prior.DeviceID {
				claim.State, claim.MemoryMiB, claim.CoreLimitPercent = biz.Active, allocation.MemoryMiB, allocation.CorePercent
				runtimeKnown = true
			}
		}
		sort.Strings(ids)
		claim.Cursor, _ = biz.Hash(ids)
		claim.EvidenceRef = "cri-orphan:" + claim.Cursor
		if !claim.ObservedAt.IsZero() {
			result = append(result, claim)
		}
	}
	if len(observation.resources) != 0 {
		claim := prior
		claim.Source, claim.State = "allocation", biz.AllocationUnknown
		if runtimeKnown {
			claim.State = biz.Active
		}
		claim.ObservedAt, claim.ReceivedAt = currentSourceTime(snapshot, "podresources"), now
		claim.Cursor, _ = biz.Hash(observation.resources)
		claim.EvidenceRef = "podresources:" + prior.Pod.UID + ":" + prior.Pod.ContainerName
		if !claim.ObservedAt.IsZero() {
			result = append(result, claim)
		}
	}
	return result
}

// API lifecycle changes do not create a newer telemetry observation. Retain a
// source's existing payload at the same timestamp instead of rewriting it as
// orphan/unknown and conflicting with its immutable source revision.
func newerSourceClaim(current biz.Claim, previous []biz.Claim) bool {
	for _, prior := range previous {
		if prior.Pod == current.Pod && prior.DeviceID == current.DeviceID && prior.NodeUID == current.NodeUID && prior.Source == current.Source && !current.ObservedAt.After(prior.ObservedAt) {
			return false
		}
	}
	return true
}
