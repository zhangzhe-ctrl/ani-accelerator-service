package provider

import (
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// An admitted Pod keeps its allocation while waiting to create or restart a
// container. A terminal or deleting Pod has no further scheduling intent, but
// its current runtime and device allocation must still be observed separately.
func allocationIntentActive(p pod) bool {
	return p.Metadata.DeletionTimestamp == "" && p.Status.Phase != "Succeeded" && p.Status.Phase != "Failed"
}

func podKeepsAllocationIntent(ref biz.PodRef, node NodeSource, pods []pod) bool {
	for _, current := range pods {
		if current.Metadata.UID != ref.UID {
			continue
		}
		if current.Metadata.Namespace != ref.Namespace || current.Metadata.Name != ref.Name || current.Spec.NodeName != node.Name {
			return true // Do not retire a mismatched identity using another scope.
		}
		return allocationIntentActive(current)
	}
	return false
}

// currentReleaseProof retires an old occupancy using current authoritative
// observations. Historical attempts and event gaps are diagnostic records, not
// prerequisites for normal capacity reconciliation.
func currentReleaseProof(claim biz.Claim, node NodeSource, snapshot nodeSnapshot, pods []pod, now time.Time) *biz.ReleaseProof {
	if !snapshot.RuntimeOK || !snapshot.ResourcesOK || podKeepsAllocationIntent(claim.Pod, node, pods) {
		return nil
	}
	statuses, complete := runtimeFor(snapshot, claim.Pod.UID, claim.Pod.ContainerName)
	if !complete {
		return nil
	}
	for _, status := range statuses {
		if status.Status.State != cri.ContainerState_CONTAINER_EXITED {
			return nil
		}
	}
	resources, complete := resourcesFor(snapshot, claim.Pod.Namespace, claim.Pod.Name, claim.Pod.ContainerName)
	if !complete || (hasGPUResources(resources) && !replacementAllocationObserved(snapshot, claim.Pod, node, pods)) {
		return nil
	}
	observedAt := reconciliationTime(snapshot)
	if observedAt.IsZero() {
		return nil
	}
	digest, _ := biz.Hash(struct {
		Pod      biz.PodRef
		Device   string
		Node     string
		Revision uint64
		Observed time.Time
	}{claim.Pod, claim.DeviceID, node.UID, snapshot.NodeRevision, observedAt})
	return &biz.ReleaseProof{
		Pod:                 claim.Pod,
		DeviceID:            claim.DeviceID,
		NodeUID:             node.UID,
		ObservedAt:          observedAt,
		ReceivedAt:          now,
		EvidenceRef:         "reconcile:" + digest,
		Retirement:          biz.RetirementCurrentState,
		AllocationAbsent:    true,
		NodeOnline:          true,
		OwnerRef:            claim.OwnerRef,
		ExecutionDigest:     claim.ExecutionDigest,
		CheckedNodeRevision: snapshot.NodeRevision,
	}
}

// Never use the end of a multi-source capture to retire a claim newer than the
// observations that established absence. CaptureStart is the common boundary;
// source timestamps can further lower it for a delayed source.
func reconciliationTime(snapshot nodeSnapshot) time.Time {
	observed := snapshot.CaptureStart
	if observed.IsZero() {
		observed = snapshot.ObservedAt
	}
	for _, source := range []string{"cri", "podresources"} {
		if at := snapshot.SourceObservedAt[source]; !at.IsZero() && (observed.IsZero() || at.Before(observed)) {
			observed = at
		}
	}
	return observed
}

// Source observations, not collection receipt times, timestamp current claims.
func currentSourceTime(snapshot nodeSnapshot, source string) time.Time {
	if at := snapshot.SourceObservedAt[source]; !at.IsZero() {
		return at
	}
	return snapshot.ObservedAt
}
