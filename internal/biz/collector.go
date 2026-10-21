package biz

import "context"

// Collect is invoked by the application lifecycle worker, never by a query.
// Watermark failure preserves facts. The adapter returns failed source health
// as data so one failed node does not discard the other nodes' observations.
func (u *Usecase) Collect(ctx context.Context) error {
	var clusters []Cluster
	err := u.repo.View(ctx, func(r Reader) error {
		after := ""
		for {
			cs, err := r.Clusters(ctx, u.connectionRefs(), after, 201)
			if err != nil {
				return err
			}
			clusters = append(clusters, cs...)
			if len(cs) < 201 {
				return nil
			}
			after = cs[len(cs)-1].ClusterID
		}
	})
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		if err = u.collectCluster(ctx, cluster, nil); err != nil {
			return err
		}
	}
	return nil
}

func (u *Usecase) collectCluster(ctx context.Context, cluster Cluster, scope *ReleaseRecheckScope) error {
	var groups []SupplyGroup
	err := u.repo.View(ctx, func(r Reader) error {
		pAfter := ""
		for {
			pools, err := r.Pools(ctx, cluster.ClusterID, pAfter, 201)
			if err != nil {
				return err
			}
			for _, p := range pools {
				gAfter := ""
				for {
					gs, err := r.Groups(ctx, p.PoolID, gAfter, 201)
					if err != nil {
						return err
					}
					groups = append(groups, gs...)
					if len(gs) < 201 {
						break
					}
					gAfter = gs[len(gs)-1].GroupID
				}
			}
			if len(pools) < 201 {
				return nil
			}
			pAfter = pools[len(pools)-1].PoolID
		}
	})
	if err != nil {
		return err
	}
	var batch ObservationBatch
	if provider, ok := u.provider.(NodeFactProvider); ok && scope != nil {
		batch, err = provider.CollectRelease(ctx, cluster, groups, scope)
	} else {
		batch, err = u.provider.Collect(ctx, cluster, groups)
	}
	if err != nil {
		batch = ObservationBatch{ClusterID: cluster.ClusterID}
		for _, group := range groups {
			for _, node := range group.Nodes {
				for _, source := range []string{"kubernetes", "podresources", "cri"} {
					batch.Watermarks = append(batch.Watermarks, SourceWatermark{ClusterID: cluster.ClusterID, NodeUID: node.UID, Source: source, ReceivedAt: u.now(), Complete: false, Issue: "SOURCE_UNKNOWN"})
				}
			}
		}
	}
	if batch.ClusterID != cluster.ClusterID {
		return Invalid("PROVIDER_SCOPE_MISMATCH")
	}
	if err = u.repo.Transact(ctx, func(tx Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
		return err
	}
	return nil
}
