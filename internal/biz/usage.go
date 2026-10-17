package biz

import (
	"context"
	"sort"
	"time"
)

func (u *Usecase) SyncUsage(ctx context.Context, requestID string, p *UsageProjection) (*SyncResult, error) {
	c, ok := CurrentCaller(ctx)
	if !ok || c.Service != "ani-governance" || c.Action != "SyncGpuUsage" {
		return nil, Denied()
	}
	if !Ref(requestID) {
		return nil, Invalid("INVALID_REQUEST_ID")
	}
	if err := ValidateProjection(p); err != nil {
		return nil, err
	}
	var result *SyncResult
	err := u.repo.Transact(ctx, func(tx Transaction) error {
		if p.Plan == nil || p.Plan.Request == nil {
			return Invalid("INVALID_PLAN")
		}
		if err := ValidateRequest(p.Plan.Request, u.maxReplicas); err != nil {
			return err
		}
		profile, err := tx.Profile(ctx, p.Plan.Request.ProfileID, p.Plan.Request.ProfileVersion)
		if err != nil {
			return err
		}
		group, err := tx.Group(ctx, profile.Spec.GroupID)
		if err != nil {
			return err
		}
		if err = ValidatePlan(p.Plan, profile, group, u.maxReplicas); err != nil {
			return err
		}
		result, err = tx.SaveUsage(ctx, *p)
		return err
	})
	return result, err
}
func (u *Usecase) GetUsage(ctx context.Context, a *TenantContext, ref *UsageRef) (*UsageProjection, error) {
	if err := AuthorizeTenant(ctx, a, "GetGpuUsage"); err != nil {
		return nil, err
	}
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	if a.TenantID != ref.TenantID {
		return nil, Denied()
	}
	var p *UsageProjection
	err := u.repo.View(ctx, func(r Reader) error {
		var err error
		p, err = r.Usage(ctx, *ref)
		if IsNotFound(err) {
			return Precondition("USAGE_PROJECTION_NOT_READY")
		}
		return err
	})
	if err == nil {
		p.SourceFactRef = ""
	}
	return p, err
}
func (u *Usecase) ListUsages(ctx context.Context, a *TenantContext, owner string, state UsageState, page *Page) (*ListResult[UsageProjection], error) {
	if err := AuthorizeTenant(ctx, a, "ListGpuUsages"); err != nil {
		return nil, err
	}
	if (owner != "" && owner != "ani-inference" && owner != "ani-resource-service") || (state != 0 && state != Declared && state != Ended) {
		return nil, Invalid("INVALID_USAGE_FILTER")
	}
	res, err := listPage(ctx, u, page, "ListGpuUsages", []any{owner, state}, func(r Reader, after string, n int) ([]UsageProjection, error) {
		return r.Usages(ctx, a.TenantID, owner, state, after, n)
	}, func(p UsageProjection) string { return p.Ref.ResourceID })
	if err == nil {
		for i := range res.Items {
			res.Items[i].SourceFactRef = ""
		}
	}
	return res, err
}
func (u *Usecase) ObserveRelease(ctx context.Context, requestID string, ref *UsageRef, deleteID string, pods []PodRef) (*ReleaseObservation, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	c, ok := CurrentCaller(ctx)
	if !ok || c.Service != ref.OwnerService || c.Action != "ObserveRelease" || c.TenantID != ref.TenantID {
		return nil, Denied()
	}
	if !Ref(requestID) || !UUID(deleteID) || deleteID == ref.CreateOperationID || len(pods) == 0 || len(pods) > 4096 {
		return nil, Invalid("INVALID_RELEASE_SCOPE")
	}
	pods = append([]PodRef{}, pods...)
	sort.Slice(pods, func(i, j int) bool { return podKey(pods[i]) < podKey(pods[j]) })
	for i, p := range pods {
		if err := ValidatePod(p); err != nil {
			return nil, err
		}
		if i > 0 && podKey(pods[i-1]) == podKey(p) {
			return nil, Invalid("DUPLICATE_POD")
		}
	}
	result := &ReleaseObservation{Ref: ref, Finding: AllocationReleased, Observation: &Observation{Complete: true}}
	var evidence []string
	err := u.repo.View(ctx, func(r Reader) error {
		for _, p := range pods {
			claims, releases, err := r.PodEvidence(ctx, p)
			if err != nil {
				return err
			}
			watermarks, err := r.SourceHealth(ctx, p.ClusterID)
			if err != nil {
				return err
			}
			devices := map[string][]Claim{}
			proofs := map[string]ReleaseProof{}
			for _, cl := range claims {
				if cl.OwnerRef != nil && *cl.OwnerRef != *ref {
					return Denied()
				}
				devices[cl.DeviceID] = append(devices[cl.DeviceID], cl)
			}
			for _, proof := range releases {
				if proof.OwnerRef != nil && *proof.OwnerRef != *ref {
					return Denied()
				}
				if proof.OwnerRef != nil && *proof.OwnerRef == *ref && proof.RuntimeExited && proof.AllocationAbsent && proof.NodeOnline {
					proofs[proof.DeviceID] = proof
				}
			}
			if len(devices) == 0 {
				result.Observation.Complete = false
				result.Observation.Issues = append(result.Observation.Issues, "SCOPE_UNOBSERVED")
			}
			for device, cs := range devices {
				proof, hasProof := proofs[device]
				var proofPtr *ReleaseProof
				if hasProof {
					proofPtr = &proof
				}
				merged, err := MergeBinding(NewID(), cs, proofPtr, nil)
				if err != nil {
					return err
				}
				for _, cl := range cs {
					fact := cl
					fact.ReceivedAt = time.Time{}
					digest, err := Hash(fact)
					if err != nil {
						return err
					}
					evidence = append(evidence, digest)
				}
				for _, w := range watermarks {
					if w.NodeUID != cs[0].NodeUID {
						continue
					}
					w.ReceivedAt = time.Time{}
					digest, err := Hash(w)
					if err != nil {
						return err
					}
					evidence = append(evidence, digest)
				}
				released := hasProof && merged.State == ReleaseConfirmed && fresh(proof.ObservedAt, u.now(), u.freshness) && nodeSourcesHealthy(watermarks, proof.NodeUID, u.now(), u.freshness)
				active := false
				trusted := false
				uncertainNode := ""
				for _, cl := range cs {
					uncertainNode = cl.NodeUID
					if cl.OwnerRef != nil && *cl.OwnerRef == *ref {
						trusted = true
					}
					if hasProof && !cl.ObservedAt.After(proof.ObservedAt) {
						continue
					}
					released = false
					if cl.State == Active && fresh(cl.ObservedAt, u.now(), u.freshness) && nodeSourcesHealthy(watermarks, cl.NodeUID, u.now(), u.freshness) {
						active = true
					}
				}
				if !trusted {
					result.Observation.Complete = false
					result.Observation.Issues = append(result.Observation.Issues, "OWNERSHIP_UNVERIFIED")
					continue
				}
				if !merged.Observation.Complete {
					active = false
					released = false
					result.Observation.Issues = append(result.Observation.Issues, merged.Observation.Issues...)
				}
				observed, _ := time.Parse(time.RFC3339Nano, merged.Observation.ObservedAt)
				active = active && merged.State == Active && fresh(observed, u.now(), u.freshness)
				result.Observation.ObservedAt = oldest(result.Observation.ObservedAt, merged.Observation.ObservedAt)
				result.Observation.ReceivedAt = oldest(result.Observation.ReceivedAt, merged.Observation.ReceivedAt)
				if active {
					result.ActivePodUIDs = append(result.ActivePodUIDs, p.UID)
				} else if !released {
					result.Observation.Complete = false
					result.UncertainNodeUIDs = append(result.UncertainNodeUIDs, uncertainNode)
					result.Observation.Issues = append(result.Observation.Issues, "SOURCE_UNKNOWN")
				}
				if hasProof {
					evidence = append(evidence, proof.EvidenceRef)
					result.Observation.ObservedAt = oldest(result.Observation.ObservedAt, proof.ObservedAt.UTC().Format(time.RFC3339Nano))
					result.Observation.ReceivedAt = oldest(result.Observation.ReceivedAt, proof.ReceivedAt.UTC().Format(time.RFC3339Nano))
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result.ActivePodUIDs = unique(result.ActivePodUIDs)
	result.UncertainNodeUIDs = unique(result.UncertainNodeUIDs)
	result.Observation.Issues = unique(result.Observation.Issues)
	sort.Strings(evidence)
	if len(result.ActivePodUIDs) > 0 {
		result.Finding = StillPresent
	} else if !result.Observation.Complete {
		result.Finding = ReleaseUnknown
	}
	result.EvidenceDigest, err = Hash(struct {
		Ref      *UsageRef      `json:"ref"`
		DeleteID string         `json:"delete_operation_id"`
		Pods     []PodRef       `json:"pods"`
		Evidence []string       `json:"source_digests"`
		Finding  ReleaseFinding `json:"finding"`
	}{ref, deleteID, pods, evidence, result.Finding})
	if err != nil {
		return nil, err
	}
	result.EvidenceID = NewID()
	result.Observation.SnapshotRef = result.EvidenceDigest
	return result, nil
}
func podKey(p PodRef) string { return p.ClusterID + "/" + p.UID + "/" + p.ContainerName }
func fresh(observed, now time.Time, ttl time.Duration) bool {
	return !observed.IsZero() && !observed.After(now.Add(5*time.Second)) && now.Sub(observed) <= ttl
}
func oldest(a, b string) string {
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	x, e1 := time.Parse(time.RFC3339Nano, a)
	y, e2 := time.Parse(time.RFC3339Nano, b)
	if e1 != nil || e2 != nil {
		return ""
	}
	if y.Before(x) {
		return b
	}
	return a
}
func unique(xs []string) []string {
	sort.Strings(xs)
	out := []string{}
	for _, x := range xs {
		if x != "" && (len(out) == 0 || out[len(out)-1] != x) {
			out = append(out, x)
		}
	}
	return out
}
func nodeSourcesHealthy(ms []SourceWatermark, node string, now time.Time, ttl time.Duration) bool {
	want := map[string]bool{"kubernetes": false, "podresources": false, "cri": false}
	for _, m := range ms {
		if m.NodeUID == node {
			if _, ok := want[m.Source]; ok {
				want[m.Source] = m.Complete && fresh(m.ObservedAt, now, ttl)
			}
		}
	}
	for _, ok := range want {
		if !ok {
			return false
		}
	}
	return true
}
func bindingsObservation(bs []Binding, now time.Time, ttl time.Duration) *Observation {
	o := &Observation{Complete: len(bs) > 0}
	if len(bs) == 0 {
		o.Issues = []string{"SCOPE_UNOBSERVED"}
	}
	for _, b := range bs {
		if b.Observation == nil {
			o.Complete = false
			continue
		}
		o.ObservedAt = oldest(o.ObservedAt, b.Observation.ObservedAt)
		o.ReceivedAt = oldest(o.ReceivedAt, b.Observation.ReceivedAt)
		t, _ := time.Parse(time.RFC3339Nano, b.Observation.ObservedAt)
		timeKnown := fresh(t, now, ttl)
		if b.State == ReleaseConfirmed {
			// Releases are durable historical facts. Keep their original times,
			// while still rejecting missing, malformed or future evidence.
			received, _ := time.Parse(time.RFC3339Nano, b.Observation.ReceivedAt)
			timeKnown = ValidTime(b.Observation.ObservedAt) && ValidTime(b.Observation.ReceivedAt) && !t.After(now.Add(5*time.Second)) && !received.After(now.Add(5*time.Second)) && !received.Before(t.Add(-5*time.Second))
		}
		if !b.Observation.Complete || !timeKnown || (b.State != Active && b.State != ReleaseConfirmed) {
			o.Complete = false
			o.Issues = append(o.Issues, "SOURCE_UNKNOWN")
		}
		o.Issues = append(o.Issues, b.Observation.Issues...)
	}
	o.Issues = unique(o.Issues)
	return o
}
