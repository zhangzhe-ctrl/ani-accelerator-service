package biz

import (
	"strconv"
)

const (
	GroupLabel    = "accelerator.ani.io/supply-group"
	ModelLabel    = "accelerator.ani.io/model-key"
	BaselineLabel = "accelerator.ani.io/baseline-id"
)

func Render(s *ProfileSpec, b *ProviderBaseline, queue string, devices uint32) (*GpuExecution, error) {
	if err := ValidateSpec(s, b); err != nil {
		return nil, err
	}
	if devices < 1 || devices > s.MaxDevicesPerReplica || (s.Mode == Shared && devices != 1) {
		return nil, Invalid("SHAPE_UNSUPPORTED")
	}
	if !Name(queue) {
		return nil, Invalid("INVALID_QUEUE")
	}
	e := &GpuExecution{SchedulerName: "volcano", QueueName: queue, RuntimeClassName: b.RuntimeClassName,
		NodeSelector:   map[string]string{BaselineLabel: b.BaselineID, ModelLabel: s.ModelKey, GroupLabel: s.GroupID},
		PodAnnotations: map[string]string{"volcano.sh/vgpu-mode": "hami-core"},
		Resources:      map[string]string{"volcano.sh/vgpu-number": strconv.FormatUint(uint64(devices), 10), "volcano.sh/vgpu-memory": strconv.FormatInt(s.MemoryMiBPerDevice, 10), "volcano.sh/vgpu-cores": strconv.FormatUint(uint64(s.CoreLimitPercent), 10)}}
	var err error
	e.ExecutionDigest, err = ExecutionDigest(e)
	return e, err
}
func BuildResolution(request *GpuRequest, profile *Profile, group *SupplyGroup, maxReplicas uint32) (*ResolvedGpuRequest, error) {
	if err := ValidateRequest(request, maxReplicas); err != nil {
		return nil, err
	}
	if profile == nil || group == nil || profile.Spec == nil || group.Baseline == nil {
		return nil, Invalid("INVALID_RESOLUTION")
	}
	if request.ProfileID != profile.ProfileID || request.ProfileVersion != profile.ProfileVersion || request.ClusterID != group.ClusterID || request.PoolID != group.PoolID || profile.Spec.GroupID != group.GroupID || profile.Spec.Mode != group.Mode || profile.Spec.ModelKey != group.ModelKey || profile.BaselineID != group.Baseline.BaselineID {
		return nil, Precondition("PROFILE_SCOPE_MISMATCH")
	}
	if d, err := SpecDigest(profile.Spec, profile.BaselineID); err != nil || d != profile.SpecDigest {
		return nil, Invalid("SPEC_DIGEST_MISMATCH")
	}
	execution, err := Render(profile.Spec, group.Baseline, group.QueueName, request.DevicesPerReplica)
	if err != nil {
		return nil, err
	}
	code := "gpu.shared_memory_mib"
	units, err := Multiply(int64(request.Replicas), profile.Spec.MemoryMiBPerDevice)
	if profile.Spec.Mode == Whole {
		code = "gpu.physical.count"
		units, err = Multiply(int64(request.Replicas), int64(request.DevicesPerReplica))
	}
	if err != nil {
		return nil, err
	}
	return &ResolvedGpuRequest{Execution: execution, QuotaItems: []GpuQuotaItem{{QuotaCode: code, Units: units}}, Selection: &GpuDisplay{ProfileID: profile.ProfileID, ProfileVersion: profile.ProfileVersion, ProfileName: profile.DisplayName, ModelKey: profile.Spec.ModelKey, Mode: profile.Spec.Mode}}, nil
}
func ValidateProjection(p *UsageProjection) error {
	if p == nil {
		return Invalid("INVALID_PROJECTION")
	}
	if err := ValidateRef(p.Ref); err != nil {
		return err
	}
	if !UUID(p.ProfileID) || p.ProfileVersion != 1 || !UUID(p.GroupID) || !UUID(p.BaselineID) || !UUID(p.ClusterID) || !Name(p.ContainerName) || !Digest(p.ExecutionDigest) {
		return Invalid("INVALID_PROJECTION")
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
