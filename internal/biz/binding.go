package biz

import "time"

// MergeBinding reconciles the latest fact from each independent source. The
// caller supplies the existing binding ID so reconciliation is restart stable.
func MergeBinding(id string, claims []Claim, proof *ReleaseProof, usage *UsageProjection) (*Binding, error) {
	if len(claims) == 0 {
		return nil, Invalid("EMPTY_BINDING_EVIDENCE")
	}
	first := claims[0]
	b := &Binding{BindingID: id, Pod: &first.Pod, PhysicalDeviceID: first.DeviceID, State: Active, Observation: &Observation{Complete: true}}
	var current []Claim
	for _, c := range claims {
		if c.Pod != first.Pod || c.DeviceID != first.DeviceID {
			return nil, Invalid("BINDING_SCOPE_MISMATCH")
		}
		if proof != nil && proof.Pod == c.Pod && proof.DeviceID == c.DeviceID && proof.NodeUID == c.NodeUID && proof.RuntimeExited && proof.AllocationAbsent && proof.NodeOnline && !c.ObservedAt.After(proof.ObservedAt) {
			continue
		}
		current = append(current, c)
	}
	if len(current) == 0 && proof != nil {
		b.State = ReleaseConfirmed
		b.MemoryMiB = first.MemoryMiB
		b.CoreLimitPercent = first.CoreLimitPercent
		b.SourceBaselineDigest = first.BaselineDigest
		b.SourceMemoryBlockMiB = first.MemoryBlockMiB
		b.EvidenceRef = proof.EvidenceRef
		b.Observation.ObservedAt = proof.ObservedAt.UTC().Format(time.RFC3339Nano)
		b.Observation.ReceivedAt = proof.ReceivedAt.UTC().Format(time.RFC3339Nano)
	} else {
		base := current[0]
		b.MemoryMiB = base.MemoryMiB
		b.CoreLimitPercent = base.CoreLimitPercent
		b.SourceBaselineDigest = base.BaselineDigest
		b.SourceMemoryBlockMiB = base.MemoryBlockMiB
		var refs []string
		runtimeSeen, allocationSeen := false, false
		for _, c := range current {
			runtimeSeen = runtimeSeen || c.Source == "runtime"
			allocationSeen = allocationSeen || c.Source == "allocation"
			if c.MemoryMiB != base.MemoryMiB || c.CoreLimitPercent != base.CoreLimitPercent || c.BaselineDigest != base.BaselineDigest || c.MemoryBlockMiB != base.MemoryBlockMiB {
				b.State = AllocationUnknown
				b.Observation.Complete = false
				b.Observation.Issues = append(b.Observation.Issues, "BINDING_EVIDENCE_CONFLICT")
				b.MemoryMiB = max(b.MemoryMiB, c.MemoryMiB)
				b.CoreLimitPercent = max(b.CoreLimitPercent, c.CoreLimitPercent)
			}
			if c.State != Active {
				b.State = AllocationUnknown
				b.Observation.Complete = false
				b.Observation.Issues = append(b.Observation.Issues, "SOURCE_UNKNOWN")
			}
			// A scheduler assignment is historical. Current agreeing runtime and
			// allocation probes establish freshness without rewriting that fact.
			if c.Source != "scheduler" {
				b.Observation.ObservedAt = oldest(b.Observation.ObservedAt, c.ObservedAt.UTC().Format(time.RFC3339Nano))
				b.Observation.ReceivedAt = oldest(b.Observation.ReceivedAt, c.ReceivedAt.UTC().Format(time.RFC3339Nano))
			}
			refs = append(refs, c.EvidenceRef)
		}
		if !runtimeSeen || !allocationSeen {
			b.State = AllocationUnknown
			b.Observation.Complete = false
			b.Observation.Issues = append(b.Observation.Issues, "ALLOCATION_UNCONFIRMED")
			if b.Observation.ObservedAt == "" {
				b.Observation.ObservedAt = base.ObservedAt.UTC().Format(time.RFC3339Nano)
				b.Observation.ReceivedAt = base.ReceivedAt.UTC().Format(time.RFC3339Nano)
			}
		}
		b.EvidenceRef, _ = Hash(unique(refs))
	}
	var owner *UsageRef
	digest := ""
	conflict := false
	for _, c := range claims {
		if c.OwnerRef != nil {
			if owner != nil && (*owner != *c.OwnerRef || digest != c.ExecutionDigest) {
				conflict = true
			}
			copy := *c.OwnerRef
			owner = &copy
			digest = c.ExecutionDigest
		}
	}
	if !conflict && owner != nil && usage != nil && usage.Ref != nil && *usage.Ref == *owner && usage.ExecutionDigest == digest && usage.ClusterID == first.Pod.ClusterID && usage.ContainerName == first.Pod.ContainerName {
		b.VerifiedUsageRef = owner
	}
	if conflict {
		b.Observation.Complete = false
		b.Observation.Issues = append(b.Observation.Issues, "OWNERSHIP_CONFLICT")
	}
	b.Observation.Issues = unique(b.Observation.Issues)
	b.Observation.SnapshotRef = b.EvidenceRef
	return b, nil
}
func ValidateObservationBatch(batch ObservationBatch) error {
	if !UUID(batch.ClusterID) {
		return Invalid("INVALID_CLUSTER")
	}
	for _, d := range batch.Devices {
		if d.Device.ClusterID != batch.ClusterID || !UUID(d.Device.DeviceID) || d.Device.Node == nil || !Ref(d.Device.Node.UID) || d.Device.Observation == nil || !ValidTime(d.Device.Observation.ObservedAt) || !ValidTime(d.Device.Observation.ReceivedAt) {
			return Invalid("INVALID_DEVICE_OBSERVATION")
		}
	}
	for _, c := range batch.Claims {
		if err := ValidatePod(c.Pod); err != nil {
			return err
		}
		if c.Pod.ClusterID != batch.ClusterID || !UUID(c.DeviceID) || !Ref(c.NodeUID) || (c.Source != "scheduler" && c.Source != "allocation" && c.Source != "runtime") || (c.State != Active && c.State != AllocationUnknown) || c.MemoryMiB < 0 || c.CoreLimitPercent > 100 || !Digest(c.BaselineDigest) || c.MemoryBlockMiB == 0 || !Ref(c.EvidenceRef) || c.ObservedAt.IsZero() || c.ReceivedAt.Before(c.ObservedAt.Add(-5*time.Second)) {
			return Invalid("INVALID_CLAIM")
		}
		if c.OwnerRef != nil {
			if err := ValidateRef(c.OwnerRef); err != nil {
				return err
			}
			if !Digest(c.ExecutionDigest) {
				return Invalid("INVALID_PLAN")
			}
		}
	}
	for _, p := range batch.Releases {
		if err := ValidatePod(p.Pod); err != nil {
			return err
		}
		if p.Pod.ClusterID != batch.ClusterID || !UUID(p.DeviceID) || !Ref(p.NodeUID) || !Ref(p.EvidenceRef) || p.ObservedAt.IsZero() || !p.RuntimeExited || !p.AllocationAbsent || !p.NodeOnline {
			return Invalid("INVALID_RELEASE_PROOF")
		}
	}
	for _, w := range batch.Watermarks {
		for group, count := range w.PendingByGroup {
			if !UUID(group) || count < 0 || w.Source != "kubernetes" {
				return Invalid("INVALID_PENDING_DEMAND")
			}
		}
		if w.ClusterID != batch.ClusterID || !Ref(w.NodeUID) || (w.Source != "kubernetes" && w.Source != "podresources" && w.Source != "cri") || w.ReceivedAt.IsZero() || (w.Complete && w.ObservedAt.IsZero()) {
			return Invalid("INVALID_WATERMARK")
		}
	}
	return nil
}
