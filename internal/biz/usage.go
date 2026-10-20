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
		// A trusted thin projection is immutable lifecycle data, independent
		// of today's directory availability or admission state.
		// The authenticated Governance fact writer has no delegated user
		// grant. Verify immutable relation identity without checking current
		// catalog state or recalculating any accepted resource quantities.
		profile, err := tx.Profile(ctx, p.ProfileID, p.ProfileVersion)
		if err != nil {
			return err
		}
		if profile.Spec == nil || profile.Spec.GroupID != p.GroupID || profile.BaselineID != p.BaselineID {
			return Invalid("PROFILE_SCOPE_MISMATCH")
		}
		group, err := tx.Group(ctx, p.GroupID)
		if err != nil {
			return err
		}
		if group.ClusterID != p.ClusterID {
			return Invalid("PROFILE_SCOPE_MISMATCH")
		}
		if err := u.authorizeCluster(ctx, tx, p.ClusterID); err != nil {
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
func (u *Usecase) ObserveRelease(ctx context.Context, requestID string, ref *UsageRef, deleteID, clusterID, sealedAt string, pods []PodRef) (*ReleaseObservation, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	c, ok := CurrentCaller(ctx)
	if !ok || c.Service != ref.OwnerService || c.Action != "ObserveRelease" || c.TenantID != ref.TenantID {
		return nil, Denied()
	}
	fence, fenceErr := time.Parse(time.RFC3339Nano, sealedAt)
	if !Ref(requestID) || !UUID(deleteID) || deleteID == ref.CreateOperationID || !UUID(clusterID) || fenceErr != nil || fence.IsZero() || fence.After(u.now().Add(5*time.Second)) || len(pods) > 4096 {
		return nil, Invalid("INVALID_RELEASE_SCOPE")
	}
	pods = append([]PodRef{}, pods...)
	sort.Slice(pods, func(i, j int) bool { return podKey(pods[i]) < podKey(pods[j]) })
	for i, p := range pods {
		if err := ValidatePod(p); err != nil {
			return nil, err
		}
		if p.ClusterID != clusterID {
			return nil, Invalid("POD_SCOPE_MISMATCH")
		}
		if i > 0 && podKey(pods[i-1]) == podKey(p) {
			return nil, Invalid("DUPLICATE_POD")
		}
	}
	result := &ReleaseObservation{Ref: ref, Finding: AllocationReleased, Observation: &Observation{Complete: true}}
	var evidence []string
	err := u.repo.View(ctx, func(r Reader) error {
		if err := u.authorizeCluster(ctx, r, clusterID); err != nil {
			return err
		}
		// Read the complete owner scope even before an asynchronous Usage exists.
		// A caller-supplied Pod list cannot omit a historical or replaced Pod.
		ownerClaims, err := r.OwnerClaims(ctx, *ref)
		if err != nil {
			return err
		}
		scope := map[string]PodRef{}
		for _, p := range pods {
			scope[podKey(p)] = p
		}
		for _, claim := range ownerClaims {
			if claim.Pod.ClusterID != clusterID {
				return Denied()
			}
			scope[podKey(claim.Pod)] = claim.Pod
		}
		watermarks, err := r.SourceHealth(ctx, clusterID)
		if err != nil {
			return err
		}
		nodes, err := releaseNodes(ctx, r, clusterID)
		if err != nil {
			return err
		}
		for _, claim := range ownerClaims {
			nodes[claim.NodeUID] = true
		}
		unknown := func(issue, node string) {
			result.Observation.Complete = false
			result.Observation.Issues = append(result.Observation.Issues, issue)
			if node != "" {
				result.UncertainNodeUIDs = append(result.UncertainNodeUIDs, node)
			}
		}
		if len(nodes) == 0 {
			unknown("SCOPE_UNOBSERVED", "")
		}
		for node := range nodes {
			if !nodeSourcesHealthy(watermarks, node, u.now(), u.freshness) {
				unknown("SOURCE_UNKNOWN", node)
			}
			for _, source := range []string{"kubernetes", "podresources", "cri"} {
				found := false
				for _, watermark := range watermarks {
					if watermark.NodeUID != node || watermark.Source != source {
						continue
					}
					found = true
					if !watermark.Complete || watermark.ObservedAt.Before(fence) {
						unknown("SOURCE_BEFORE_SEAL", node)
					}
					receivedAt := watermark.ReceivedAt
					watermark.ReceivedAt = time.Time{}
					digest, err := Hash(watermark)
					if err != nil {
						return err
					}
					evidence = append(evidence, digest)
					result.Observation.ObservedAt = oldest(result.Observation.ObservedAt, watermark.ObservedAt.UTC().Format(time.RFC3339Nano))
					result.Observation.ReceivedAt = oldest(result.Observation.ReceivedAt, receivedAt.UTC().Format(time.RFC3339Nano))
				}
				if !found {
					unknown("SOURCE_UNKNOWN", node)
				}
			}
		}
		// Unattributed live allocations can belong to an unseen short-lived Pod.
		// Complete watermarks alone never erase those persisted facts.
		for after := ""; ; {
			bindings, err := r.Bindings(ctx, BindingFilter{ClusterID: clusterID}, after, 200)
			if err != nil {
				return err
			}
			for _, binding := range bindings {
				if binding.Pod == nil {
					if binding.State != ReleaseConfirmed {
						unknown("OWNERSHIP_UNVERIFIED", "")
					}
					continue
				}
				if binding.VerifiedUsageRef != nil {
					if *binding.VerifiedUsageRef == *ref {
						scope[podKey(*binding.Pod)] = *binding.Pod
					}
					continue
				}
				claims, _, err := r.PodEvidence(ctx, *binding.Pod)
				if err != nil {
					return err
				}
				trusted := false
				var owner *UsageRef
				for _, claim := range claims {
					if claim.OwnerRef != nil {
						if owner != nil && *owner != *claim.OwnerRef {
							unknown("OWNERSHIP_CONFLICT", claim.NodeUID)
						}
						owner = claim.OwnerRef
						trusted = true
					}
				}
				if !trusted && binding.State != ReleaseConfirmed {
					unknown("OWNERSHIP_UNVERIFIED", "")
				}
			}
			if len(bindings) < 200 {
				break
			}
			after = bindings[len(bindings)-1].BindingID
		}
		pods = pods[:0]
		for _, p := range scope {
			pods = append(pods, p)
		}
		sort.Slice(pods, func(i, j int) bool { return podKey(pods[i]) < podKey(pods[j]) })
		for _, p := range pods {
			claims, releases, err := r.PodEvidence(ctx, p)
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
				// A positive exit fact is durable. Fresh complete source watermarks
				// cover the seal; a later claim below still invalidates this proof.
				released := hasProof && merged.State == ReleaseConfirmed && nodeSourcesHealthy(watermarks, proof.NodeUID, u.now(), u.freshness)
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
		Ref       *UsageRef      `json:"ref"`
		DeleteID  string         `json:"delete_operation_id"`
		ClusterID string         `json:"cluster_id"`
		SealedAt  string         `json:"sealed_at"`
		Pods      []PodRef       `json:"pods"`
		Evidence  []string       `json:"source_digests"`
		Finding   ReleaseFinding `json:"finding"`
	}{ref, deleteID, clusterID, sealedAt, pods, evidence, result.Finding})
	if err != nil {
		return nil, err
	}
	result.EvidenceID = NewID()
	result.Observation.SnapshotRef = result.EvidenceDigest
	return result, nil
}

// Every adopted GPU node remains in scope even when its group is closed.
func releaseNodes(ctx context.Context, r Reader, cluster string) (map[string]bool, error) {
	nodes := map[string]bool{}
	for poolAfter := ""; ; {
		pools, err := r.Pools(ctx, cluster, poolAfter, 200)
		if err != nil {
			return nil, err
		}
		for _, pool := range pools {
			for groupAfter := ""; ; {
				groups, err := r.Groups(ctx, pool.PoolID, groupAfter, 200)
				if err != nil {
					return nil, err
				}
				for _, group := range groups {
					for _, node := range group.Nodes {
						nodes[node.UID] = true
					}
				}
				if len(groups) < 200 {
					break
				}
				groupAfter = groups[len(groups)-1].GroupID
			}
		}
		if len(pools) < 200 {
			break
		}
		poolAfter = pools[len(pools)-1].PoolID
	}
	return nodes, nil
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
