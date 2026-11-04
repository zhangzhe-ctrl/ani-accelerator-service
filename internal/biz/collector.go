package biz

import "context"

// Collect is invoked by the application lifecycle worker, never by a query.
// Failed lists remain explicit observations; successful Pod and device
// observations can be applied independently within the same batch.
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
		if err = u.collectCluster(ctx, cluster); err != nil {
			return err
		}
	}
	return nil
}

func (u *Usecase) collectCluster(ctx context.Context, cluster Cluster) error {
	var groups []SupplyGroup
	var baseRevision uint64
	err := u.repo.View(ctx, func(r Reader) error {
		state, err := r.CollectionState(ctx, cluster.ClusterID)
		if err != nil {
			return err
		}
		baseRevision = state.Revision
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
	batch, err := u.provider.Collect(ctx, cluster, groups)
	if err != nil {
		batch = ObservationBatch{ClusterID: cluster.ClusterID, ReceivedAt: u.now(), BindingIssues: []string{"SOURCE_UNKNOWN"}}
	}
	batch.BaseObservationRevision = baseRevision
	if batch.ClusterID != cluster.ClusterID {
		return Invalid("PROVIDER_SCOPE_MISMATCH")
	}
	if err = u.repo.Transact(ctx, func(tx Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
		return err
	}
	return nil
}
