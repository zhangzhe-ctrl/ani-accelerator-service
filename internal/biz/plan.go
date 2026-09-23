package biz

import "strconv"

const (
	GroupLabel    = "accelerator.ani.io/supply-group"
	ModelLabel    = "accelerator.ani.io/model-key"
	BaselineLabel = "accelerator.ani.io/baseline-id"
)

func Render(s *ProfileSpec, b *ProviderBaseline, queue string) (*RuntimeFragment, *MemoryEncoding, error) {
	if err := ValidateSpec(s, b); err != nil {
		return nil, nil, err
	}
	if !Name(queue) {
		return nil, nil, Invalid("INVALID_QUEUE")
	}
	e := &MemoryEncoding{MemoryBlockMiB: b.MemoryBlockMiB, Policy: "EXACT"}
	r := &RuntimeFragment{SchedulerName: "volcano", QueueName: queue, RecipeVersion: b.RecipeVersion,
		NodeLabels:         []KeyValue{{BaselineLabel, b.BaselineID}, {ModelLabel, s.ModelKey}, {GroupLabel, s.GroupID}},
		PodAnnotations:     []KeyValue{{"volcano.sh/vgpu-mode", "hami-core"}},
		LimitsPerContainer: []KeyValue{{"volcano.sh/vgpu-cores", strconv.FormatUint(uint64(s.CoreLimitPercent), 10)}, {"volcano.sh/vgpu-number", "1"}}}
	if s.Mode == Whole {
		e.MemoryPercentage = 100
		r.LimitsPerContainer = append(r.LimitsPerContainer, KeyValue{"volcano.sh/vgpu-memory-percentage", "100"})
	} else {
		q, err := EncodeMemory(s.SharedMemoryMiB, b.MemoryBlockMiB)
		if err != nil {
			return nil, nil, err
		}
		e.MemoryBlocksPerDevice = q
		e.SharedMemoryMiB = s.SharedMemoryMiB
		r.LimitsPerContainer = append(r.LimitsPerContainer, KeyValue{"volcano.sh/vgpu-memory", strconv.FormatInt(q, 10)})
	}
	r.LimitsPerContainer, _ = sortedFields(r.LimitsPerContainer)
	return r, e, nil
}
func BuildPlan(request *GpuRequest, profile *Profile, group *SupplyGroup, maxReplicas uint32) (*ResolvedPlan, error) {
	if err := ValidateRequest(request, maxReplicas); err != nil {
		return nil, err
	}
	if profile == nil || group == nil || profile.Spec == nil || group.Baseline == nil {
		return nil, Invalid("INVALID_PLAN")
	}
	if request.ProfileID != profile.ProfileID || request.ProfileVersion != profile.ProfileVersion || request.ClusterID != group.ClusterID || request.PoolID != group.PoolID || profile.Spec.GroupID != group.GroupID || profile.Spec.Mode != group.Mode || profile.Spec.ModelKey != group.ModelKey || profile.BaselineID != group.Baseline.BaselineID {
		return nil, Precondition("PROFILE_SCOPE_MISMATCH")
	}
	if d, err := SpecDigest(profile.Spec, profile.BaselineID); err != nil || d != profile.SpecDigest {
		return nil, Invalid("SPEC_DIGEST_MISMATCH")
	}
	r, e, err := Render(profile.Spec, group.Baseline, group.QueueName)
	if err != nil {
		return nil, err
	}
	totals := &ResourceTotals{}
	if profile.Spec.Mode == Shared {
		totals.LogicalDeviceCount = int64(request.Replicas)
		totals.SharedMemoryMiB, err = Multiply(int64(request.Replicas), profile.Spec.SharedMemoryMiB)
		if err != nil {
			return nil, err
		}
	} else {
		totals.ExclusiveDeviceCount = int64(request.Replicas)
	}
	p := &ResolvedPlan{SchemaVersion: 1, Request: request, Profile: profile, Encoding: e, Totals: totals, Runtime: r, BaselineDigest: group.Baseline.BaselineDigest}
	p.ResolutionDigest, err = PlanDigest(p)
	return p, err
}

// ValidatePlan rebuilds from the persisted immutable profile/group, not from
// the caller's encoding, factor or resources. Admission does not invalidate an
// already resolved plan; current evidence availability is not a sync gate.
func ValidatePlan(p *ResolvedPlan, profile *Profile, group *SupplyGroup, maxReplicas uint32) error {
	if p == nil || p.SchemaVersion != 1 || !Digest(p.ResolutionDigest) {
		return Invalid("INVALID_PLAN")
	}
	expected, err := BuildPlan(p.Request, profile, group, maxReplicas)
	if err != nil {
		return err
	}
	d, err := PlanDigest(p)
	if err != nil {
		return err
	}
	if d != p.ResolutionDigest || d != expected.ResolutionDigest {
		return Invalid("PLAN_DIGEST_MISMATCH")
	}
	return nil
}
func ValidateProjection(p *UsageProjection) error {
	if p == nil {
		return Invalid("INVALID_PROJECTION")
	}
	if err := ValidateRef(p.Ref); err != nil {
		return err
	}
	if !ValidTime(p.SourceOperationCreatedAt) {
		return Invalid("INVALID_SOURCE_TIME")
	}
	suffix := ""
	switch {
	case p.Revision == 1 && p.State == Declared && p.EndReason == 0:
		suffix = "accepted"
	case p.Revision == 2 && p.State == Ended && p.EndReason == CanceledUnsent:
		suffix = "canceled-unsent"
	case p.Revision == 2 && p.State == Ended && p.EndReason == OwnerReleased:
		suffix = "gpu-fully-released"
	default:
		return Invalid("INVALID_USAGE_REVISION")
	}
	if p.SourceFactRef != "quota-operation:"+p.Ref.CreateOperationID+":"+suffix {
		return Invalid("INVALID_SOURCE_FACT")
	}
	d, err := ProjectionDigest(p)
	if err != nil {
		return err
	}
	if d != p.PayloadDigest {
		return Invalid("PAYLOAD_DIGEST_MISMATCH")
	}
	return nil
}
