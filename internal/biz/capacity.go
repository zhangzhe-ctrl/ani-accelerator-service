package biz

import (
	"context"
	"time"
)

func (u *Usecase) GetCapacity(ctx context.Context, a *TenantContext, id string, version uint64) (*CapacityView, error) {
	if err := AuthorizeTenant(ctx, a, "GetCapacity"); err != nil {
		return nil, err
	}
	return u.getCapacity(ctx, id, version, false)
}
func (u *Usecase) AdminGetCapacity(ctx context.Context, a *AdminRead, id string, version uint64, devices bool) (*CapacityView, error) {
	if err := AuthorizeAdmin(ctx, a, "AdminGetCapacity"); err != nil {
		return nil, err
	}
	return u.getCapacity(ctx, id, version, devices)
}
func (u *Usecase) getCapacity(ctx context.Context, id string, version uint64, details bool) (*CapacityView, error) {
	if !UUID(id) || version == 0 {
		return nil, Invalid("INVALID_PROFILE")
	}
	if version != 1 {
		return nil, NotFound()
	}
	var v *CapacityView
	err := u.repo.View(ctx, func(r Reader) error {
		p, err := r.Profile(ctx, id, version)
		if err != nil {
			return err
		}
		g, err := r.Group(ctx, p.Spec.GroupID)
		if err != nil {
			return err
		}
		if err = u.authorizeCluster(ctx, r, g.ClusterID); err != nil {
			return err
		}
		v, err = u.capacity(ctx, r, p, g, details)
		return err
	})
	return v, err
}
func (u *Usecase) CheckFit(ctx context.Context, a *TenantContext, request *GpuRequest) (*GpuFit, error) {
	if err := AuthorizeTenant(ctx, a, "CheckGpuFit"); err != nil {
		return nil, err
	}
	// Capacity units are per physical device, not multi-device placements.
	// Do not turn a two-device request into a single-device replica estimate.
	if request != nil && request.DevicesPerReplica > 1 {
		return nil, Invalid("SHAPE_UNSUPPORTED")
	}
	if _, err := u.resolve(ctx, request); err != nil {
		return nil, err
	}
	v, err := u.getCapacity(ctx, request.ProfileID, request.ProfileVersion, false)
	if err != nil {
		return nil, err
	}
	f := &GpuFit{Status: FitEstimate, EstimatedReplicas: v.KnownFreeUnits, EstimateKnown: v.Observation.Complete, Observation: v.Observation, Reasons: []string{"CPU_NOT_CHECKED", "NETWORK_NOT_CHECKED", "STORAGE_NOT_CHECKED", "ESTIMATE_NOT_RESERVATION"}}
	if !v.Observation.Complete {
		f.Status = FitUnknown
		f.Reasons = append(f.Reasons, "SOURCE_UNKNOWN")
	} else if v.KnownFreeUnits < int64(request.Replicas) {
		f.Status = WaitForCapacity
		f.Reasons = append(f.Reasons, "WAIT_FOR_CAPACITY")
	}
	return f, nil
}
func (u *Usecase) capacity(ctx context.Context, r Reader, p *Profile, g *SupplyGroup, details bool) (*CapacityView, error) {
	v := &CapacityView{ProfileID: p.ProfileID, ProfileVersion: p.ProfileVersion, Mode: p.Spec.Mode, Observation: &Observation{Complete: true}}
	watermarks, err := r.SourceHealth(ctx, g.ClusterID)
	if err != nil {
		return nil, err
	}
	after := ""
	for _, w := range watermarks {
		if w.Source == "kubernetes" && w.Complete && fresh(w.ObservedAt, u.now(), u.freshness) {
			v.UnplacedRequestedUnits, err = Add(v.UnplacedRequestedUnits, w.PendingByGroup[g.GroupID])
			if err != nil {
				return nil, err
			}
		}
	}
	var snapshot []string
	for {
		ds, err := r.Devices(ctx, g.ClusterID, g.GroupID, after, 201)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			bs, err := r.DeviceBindings(ctx, d.Device.DeviceID)
			if err != nil {
				return nil, err
			}
			healthy := d.Device.Node != nil && nodeSourcesHealthy(watermarks, d.Device.Node.UID, u.now(), u.freshness)
			capacity, err := ComputeDeviceCapacity(d, *p.Spec, *g.Baseline, bs, u.now(), u.freshness, healthy)
			if err != nil {
				return nil, err
			}
			v.PhysicalDeviceCount++
			if v.NominalUnits, err = Add(v.NominalUnits, capacity.NominalUnits); err != nil {
				return nil, err
			}
			if v.KnownCommittedUnits, err = Add(v.KnownCommittedUnits, capacity.KnownCommittedUnits); err != nil {
				return nil, err
			}
			if v.KnownFreeUnits, err = Add(v.KnownFreeUnits, capacity.KnownFreeUnits); err != nil {
				return nil, err
			}
			if !capacity.Complete {
				v.Observation.Complete = false
				v.Observation.Issues = append(v.Observation.Issues, capacity.Issues...)
			}
			if d.Device.Observation != nil {
				v.Observation.ObservedAt = oldest(v.Observation.ObservedAt, d.Device.Observation.ObservedAt)
				v.Observation.ReceivedAt = oldest(v.Observation.ReceivedAt, d.Device.Observation.ReceivedAt)
				snapshot = append(snapshot, d.Device.Observation.SnapshotRef)
			}
			if details {
				v.Devices = append(v.Devices, capacity)
			}
		}
		if len(ds) < 201 {
			break
		}
		after = ds[len(ds)-1].Device.DeviceID
	}
	if v.PhysicalDeviceCount == 0 {
		v.Observation.Complete = false
		v.Observation.Issues = append(v.Observation.Issues, "SOURCE_UNKNOWN")
	}
	v.Observation.Issues = unique(v.Observation.Issues)
	v.Observation.SnapshotRef, _ = Hash(unique(snapshot))
	return v, nil
}
func ComputeDeviceCapacity(d DeviceRecord, s ProfileSpec, b ProviderBaseline, bindings []Binding, now time.Time, ttl time.Duration, sourcesHealthy bool) (DeviceCapacity, error) {
	c := DeviceCapacity{DeviceID: d.Device.DeviceID, SafeMemoryBudgetMiB: d.SafeMemoryBudgetMiB, ProviderMemoryBlocks: d.ProviderMemoryBlocks, MemoryBlockMiB: d.MemoryBlockMiB, Complete: true}
	unknown := func(reason string) { c.Complete = false; c.Issues = append(c.Issues, reason) }
	if d.Device.Observation == nil || d.Device.Node == nil || !sourcesHealthy {
		unknown("SOURCE_UNKNOWN")
	} else {
		t, _ := time.Parse(time.RFC3339Nano, d.Device.Observation.ObservedAt)
		if !d.Device.Observation.Complete || !fresh(t, now, ttl) {
			unknown("SOURCE_STALE")
		}
	}
	if d.Device.Health != "HEALTHY" {
		unknown("DEVICE_UNHEALTHY")
	}
	if d.MemoryBlockMiB != b.MemoryBlockMiB || d.BaselineDigest != b.BaselineDigest || d.Device.ModelKey != s.ModelKey {
		unknown("CONFIG_DRIFT")
	}
	var err error
	c.ReportedMemoryMiB, err = NormalizeMemory(d.ProviderMemoryBlocks, "BLOCKS", d.MemoryBlockMiB)
	if err != nil {
		unknown("MEMORY_UNIT_UNVERIFIED")
	}
	if d.SafeMemoryBudgetMiB <= 0 || d.Device.PhysicalMemoryBytes == 0 {
		unknown("SOURCE_UNKNOWN")
	} else if d.SafeMemoryBudgetMiB > c.ReportedMemoryMiB || uint64(d.SafeMemoryBudgetMiB) > d.Device.PhysicalMemoryBytes/(1<<20) {
		unknown("CAPACITY_INVARIANT_VIOLATION")
	}
	if s.Mode == Whole {
		c.NominalUnits = 1
	} else if s.MemoryMiBPerDevice > 0 {
		c.NominalUnits = min(int64(b.SplitCount), d.SafeMemoryBudgetMiB/s.MemoryMiBPerDevice)
		if b.CoreBudgetAdditive && s.CoreLimitPercent > 0 {
			c.NominalUnits = min(c.NominalUnits, 100/int64(s.CoreLimitPercent))
		}
	} else {
		unknown("CAPACITY_INVARIANT_VIOLATION")
	}
	var cores int64
	seen := map[string]bool{}
	for _, binding := range bindings {
		if binding.State == ReleaseConfirmed {
			continue
		}
		if binding.Pod == nil || binding.PhysicalDeviceID != d.Device.DeviceID || seen[binding.BindingID] {
			unknown("BINDING_EVIDENCE_CONFLICT")
			continue
		}
		seen[binding.BindingID] = true
		c.KnownCommittedUnits++
		c.CommittedMemoryMiB, err = Add(c.CommittedMemoryMiB, binding.MemoryMiB)
		if err != nil {
			unknown("CAPACITY_INVARIANT_VIOLATION")
			continue
		}
		cores, err = Add(cores, int64(binding.CoreLimitPercent))
		if err != nil {
			return c, err
		}
		if binding.State != Active || binding.Observation == nil {
			unknown("SOURCE_UNKNOWN")
		} else {
			t, _ := time.Parse(time.RFC3339Nano, binding.Observation.ObservedAt)
			if !binding.Observation.Complete || !fresh(t, now, ttl) {
				unknown("SOURCE_UNKNOWN")
			}
			for _, issue := range binding.Observation.Issues {
				unknown(issue)
			}
		}
		if binding.SourceBaselineDigest != b.BaselineDigest || binding.SourceMemoryBlockMiB != b.MemoryBlockMiB {
			unknown("CONFIG_DRIFT")
		}
	}
	if c.KnownCommittedUnits > int64(b.SplitCount) || (d.SafeMemoryBudgetMiB > 0 && c.CommittedMemoryMiB > d.SafeMemoryBudgetMiB) || (b.CoreBudgetAdditive && cores > 100) || (s.Mode == Whole && c.KnownCommittedUnits > 1) {
		unknown("CAPACITY_INVARIANT_VIOLATION")
	}
	if c.Complete {
		if s.Mode == Whole {
			c.KnownFreeUnits = 1 - c.KnownCommittedUnits
		} else {
			c.KnownFreeUnits = min(int64(b.SplitCount)-c.KnownCommittedUnits, (d.SafeMemoryBudgetMiB-c.CommittedMemoryMiB)/s.MemoryMiBPerDevice)
			if b.CoreBudgetAdditive {
				c.KnownFreeUnits = min(c.KnownFreeUnits, (100-cores)/int64(s.CoreLimitPercent))
			}
		}
		if c.KnownFreeUnits < 0 {
			unknown("CAPACITY_INVARIANT_VIOLATION")
			c.KnownFreeUnits = 0
		}
	}
	c.Issues = unique(c.Issues)
	return c, nil
}
