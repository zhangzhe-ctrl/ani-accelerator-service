package biz

import (
	"context"
	"time"
)

func (u *Usecase) releaseRefreshNeeded(ctx context.Context, cluster Cluster, owner UsageRef, pods []PodRef, seal time.Time) (bool, error) {
	store, ok := u.repo.(NodeTelemetryStore)
	if !ok {
		return true, nil
	}
	views, err := store.NodeTelemetry(ctx, cluster.ConnectionRef)
	if err != nil {
		return false, err
	}
	states := make(map[string]NodeTelemetryState, len(views))
	for _, view := range views {
		states[view.State.Identity.NodeUID] = view.State
	}
	needed := false
	err = u.repo.View(ctx, func(reader Reader) error {
		nodes, err := releaseNodes(ctx, reader, cluster.ClusterID)
		if err != nil {
			return err
		}
		claims, err := reader.OwnerClaims(ctx, owner)
		if err != nil {
			return err
		}
		scope := map[string]PodRef{}
		for _, pod := range pods {
			scope[podKey(pod)] = pod
		}
		for _, claim := range claims {
			nodes[claim.NodeUID] = true
			scope[podKey(claim.Pod)] = claim.Pod
		}
		watermarks, err := reader.SourceHealth(ctx, cluster.ClusterID)
		if err != nil {
			return err
		}
		for node := range nodes {
			state, found := states[node]
			if !found || state.CurrentRevision != state.ConsumedRevision || !nodeSourcesHealthy(watermarks, node, u.now(), u.freshness) {
				needed = true
				return nil
			}
			for _, source := range []string{"kubernetes", "podresources", "cri"} {
				covered := false
				for _, watermark := range watermarks {
					if watermark.NodeUID == node && watermark.Source == source && watermark.ConsumedNodeRevision == state.CurrentRevision && !watermark.ObservedAt.Before(seal) {
						covered = true
					}
				}
				if !covered {
					needed = true
					return nil
				}
			}
		}
		for _, pod := range scope {
			claims, proofs, err := reader.PodEvidence(ctx, pod)
			if err != nil {
				return err
			}
			for _, claim := range claims {
				if claim.OwnerRef == nil || *claim.OwnerRef != owner {
					continue
				}
				current := false
				for _, proof := range proofs {
					if proof.DeviceID == claim.DeviceID && proof.CheckedNodeRevision > 0 && proof.CheckedNodeRevision == states[claim.NodeUID].CurrentRevision {
						current = true
					}
				}
				if !current {
					needed = true
					return nil
				}
			}
		}
		return nil
	})
	return needed, err
}
