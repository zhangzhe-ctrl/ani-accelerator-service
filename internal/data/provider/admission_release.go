package provider

import (
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func admissionReleaseProof(claim biz.Claim, claims []biz.Claim, node NodeSource, snapshot nodeSnapshot, pods []pod, now time.Time) *biz.ReleaseProof {
	if !snapshot.RuntimeOK || !snapshot.ResourcesOK {
		return nil
	}
	resources, ok := resourcesFor(snapshot, claim.Pod.Namespace, claim.Pod.Name, claim.Pod.ContainerName)
	if !ok || hasGPUResources(resources) {
		return nil
	}
	statuses, ok := runtimeFor(snapshot, claim.Pod.UID, claim.Pod.ContainerName)
	if !ok || len(statuses) != 0 || !rejectedBeforeRuntime(claim.Pod, node, claims, snapshot, pods) {
		return nil
	}
	for _, pod := range pods {
		if pod.Metadata.UID == claim.Pod.UID {
			digest, _ := biz.Hash(pod)
			return &biz.ReleaseProof{Pod: claim.Pod, DeviceID: claim.DeviceID, NodeUID: node.UID, ObservedAt: snapshot.ObservedAt, ReceivedAt: now, EvidenceRef: "admission-rejected:" + digest, AdmissionRejected: true, AllocationAbsent: true, NodeOnline: true, OwnerRef: claim.OwnerRef, ExecutionDigest: claim.ExecutionDigest, CheckedNodeRevision: snapshot.NodeRevision}
		}
	}
	return nil
}

// A kubelet admission rejection precedes container creation. Missing Pods,
// failed containers and missing CRI records alone do not establish this fact.
// The caller separately requires current healthy CRI/PodResources and absence
// of both the allocation and container attempts for the exact Pod UID.
func rejectedBeforeRuntime(ref biz.PodRef, node NodeSource, claims []biz.Claim, snapshot nodeSnapshot, pods []pod) bool {
	if len(claims) == 0 || len(snapshot.UnknownPodContainers[ref.UID]) != 0 {
		return false
	}
	for _, claim := range claims {
		if claim.Pod != ref || claim.NodeUID != node.UID || claim.Source != "scheduler" {
			return false
		}
	}
	for _, pod := range pods {
		if pod.Metadata.UID != ref.UID || pod.Metadata.Name != ref.Name || pod.Metadata.Namespace != ref.Namespace || pod.Spec.NodeName != node.Name {
			continue
		}
		if pod.Status.Phase != "Failed" || pod.Status.Reason != "UnexpectedAdmissionError" || len(pod.Status.ContainerStatuses) != 0 || len(pod.Status.InitContainerStatuses) != 0 || len(pod.Status.EphemeralContainerStatuses) != 0 {
			return false
		}
		created, err := time.Parse(time.RFC3339Nano, pod.Metadata.CreationTimestamp)
		if err != nil || created.IsZero() || created.After(snapshot.ObservedAt) {
			return false
		}
		// Complete current snapshots cannot explain a missing historical
		// container when event collection had a gap during this Pod's lifetime.
		for _, source := range []string{"cri", "cri_events"} {
			if gap, found := snapshot.SourceGaps[source]; found && (gap.End.IsZero() || !gap.End.Before(created)) {
				return false
			}
		}
		return true
	}
	return false
}
