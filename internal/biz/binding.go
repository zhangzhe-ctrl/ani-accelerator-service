package biz

import "time"

// MatchBindingOwner links a current signed assertion to immutable Usage identity.
// Usage lifecycle state does not control whether a GPU allocation still exists.
func MatchBindingOwner(pod PodRef, candidate *BindingOwnerCandidate, usage *UsageProjection) *UsageRef {
	if candidate == nil || usage == nil || usage.Ref == nil ||
		candidate.Ref != *usage.Ref || candidate.ExecutionDigest != usage.ExecutionDigest ||
		usage.ClusterID != pod.ClusterID || usage.ContainerName != pod.ContainerName {
		return nil
	}
	ref := candidate.Ref
	return &ref
}

// BuildCurrentBinding preserves the caller's stable binding ID and projects one
// successfully decoded Kubernetes/Volcano assignment. It has no history input.
func BuildCurrentBinding(id string, observed ObservedBinding, observation Observation, usage *UsageProjection) (Binding, error) {
	if !UUID(id) || !observation.Complete || !ValidTime(observation.ObservedAt) ||
		!ValidTime(observation.ReceivedAt) || observation.SnapshotRef == "" {
		return Binding{}, Invalid("INVALID_BINDING_OBSERVATION")
	}
	if err := validateObservedBinding(observed); err != nil {
		return Binding{}, err
	}
	var candidate *BindingOwnerCandidate
	if observed.OwnerRef != nil {
		candidate = &BindingOwnerCandidate{Ref: *observed.OwnerRef, ExecutionDigest: observed.ExecutionDigest}
	}
	return Binding{
		BindingID: id, Pod: &observed.Pod, PhysicalDeviceID: observed.DeviceID,
		State: Active, MemoryMiB: observed.MemoryMiB, CoreLimitPercent: observed.CoreLimitPercent,
		SourceBaselineDigest: observed.BaselineDigest, SourceMemoryBlockMiB: observed.MemoryBlockMiB,
		EvidenceRef: observation.SnapshotRef, Observation: &observation,
		VerifiedUsageRef: MatchBindingOwner(observed.Pod, candidate, usage),
	}, nil
}

func validateObservedBinding(observed ObservedBinding) error {
	if err := ValidatePod(observed.Pod); err != nil {
		return err
	}
	if !UUID(observed.DeviceID) || !Ref(observed.NodeUID) || observed.MemoryMiB <= 0 ||
		observed.CoreLimitPercent > 100 || !Digest(observed.BaselineDigest) || !SupportedMemoryFactor(observed.MemoryBlockMiB) {
		return Invalid("INVALID_CURRENT_ASSIGNMENT")
	}
	if observed.OwnerRef == nil {
		if observed.ExecutionDigest != "" {
			return Invalid("INVALID_OWNER_CANDIDATE")
		}
		return nil
	}
	if err := ValidateRef(observed.OwnerRef); err != nil {
		return err
	}
	if !Digest(observed.ExecutionDigest) {
		return Invalid("INVALID_PLAN")
	}
	return nil
}

func ValidateObservationBatch(batch ObservationBatch) error {
	if !UUID(batch.ClusterID) {
		return Invalid("INVALID_CLUSTER")
	}
	if batch.ReceivedAt.IsZero() || batch.PodListComplete && (batch.ObservedAt.IsZero() ||
		batch.SnapshotRef == "" || batch.ReceivedAt.Before(batch.ObservedAt.Add(-5*time.Second))) {
		return Invalid("INVALID_COLLECTION_OBSERVATION")
	}
	if !batch.PodListComplete && (len(batch.LivePodUIDs) != 0 || len(batch.IncompletePodUIDs) != 0 ||
		len(batch.Bindings) != 0 || len(batch.PendingByGroup) != 0) {
		return Invalid("INCOMPLETE_POD_LIST")
	}
	if !batch.NodeListComplete && (len(batch.LiveNodeUIDs) != 0 || len(batch.InventoryCompleteNodeUIDs) != 0) {
		return Invalid("INCOMPLETE_NODE_LIST")
	}
	livePods, err := observationIdentities(batch.LivePodUIDs)
	if err != nil {
		return err
	}
	incompletePods, err := observationIdentities(batch.IncompletePodUIDs)
	if err != nil {
		return err
	}
	for pod := range incompletePods {
		if !livePods[pod] {
			return Invalid("INVALID_INCOMPLETE_POD")
		}
	}
	liveNodes, err := observationIdentities(batch.LiveNodeUIDs)
	if err != nil {
		return err
	}
	completeInventory, err := observationIdentities(batch.InventoryCompleteNodeUIDs)
	if err != nil {
		return err
	}
	for node := range completeInventory {
		if !liveNodes[node] {
			return Invalid("INVALID_INVENTORY_SCOPE")
		}
	}
	devices := make(map[string]DeviceRecord, len(batch.Devices))
	for _, d := range batch.Devices {
		if d.Device.ClusterID != batch.ClusterID || !UUID(d.Device.DeviceID) || d.Device.Node == nil ||
			!Ref(d.Device.Node.UID) || !Text(d.Device.Node.Name, 253) || !Ref(d.Device.VendorDeviceID) ||
			d.Device.Observation == nil ||
			(d.Device.Observation.Complete || d.Device.Observation.ObservedAt != "") && !ValidTime(d.Device.Observation.ObservedAt) ||
			!ValidTime(d.Device.Observation.ReceivedAt) || d.Device.GroupID != "" && !UUID(d.Device.GroupID) {
			return Invalid("INVALID_DEVICE_OBSERVATION")
		}
		if _, exists := devices[d.Device.DeviceID]; exists {
			return Invalid("DUPLICATE_DEVICE")
		}
		if batch.NodeListComplete && !liveNodes[d.Device.Node.UID] {
			return Invalid("DEVICE_NODE_MISMATCH")
		}
		devices[d.Device.DeviceID] = d
	}
	keys := make(map[string]bool, len(batch.Bindings))
	for _, observed := range batch.Bindings {
		if err := validateObservedBinding(observed); err != nil {
			return err
		}
		if observed.Pod.ClusterID != batch.ClusterID || !livePods[observed.Pod.UID] || incompletePods[observed.Pod.UID] {
			return Invalid("BINDING_SCOPE_MISMATCH")
		}
		device, exists := devices[observed.DeviceID]
		if !exists || device.Device.Node.UID != observed.NodeUID {
			return Invalid("BINDING_DEVICE_MISMATCH")
		}
		key := podKey(observed.Pod) + "/" + observed.DeviceID
		if keys[key] {
			return Invalid("DUPLICATE_BINDING")
		}
		keys[key] = true
	}
	for group, count := range batch.PendingByGroup {
		if !UUID(group) || count < 0 {
			return Invalid("INVALID_PENDING_DEMAND")
		}
	}
	return nil
}

func observationIdentities(values []string) (map[string]bool, error) {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if !Ref(value) || set[value] {
			return nil, Invalid("INVALID_OBSERVATION_IDENTITY")
		}
		set[value] = true
	}
	return set, nil
}
