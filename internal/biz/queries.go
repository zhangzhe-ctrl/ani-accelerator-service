package biz

import "context"

type ListResult[T any] struct {
	Items       []T          `json:"items"`
	NextToken   string       `json:"next_token"`
	Observation *Observation `json:"observation,omitempty"`
}

type ProfileListResult struct {
	Items     []Profile      `json:"items"`
	NextToken string         `json:"next_token"`
	Scopes    []ProfileScope `json:"scopes"`
}

func listPage[T any](ctx context.Context, u *Usecase, page *Page, method string, filter any, read func(Reader, string, int) ([]T, error), id func(T) string) (*ListResult[T], error) {
	scope, err := PageScope(ctx, method, filter)
	if err != nil {
		return nil, err
	}
	after, size, err := u.cursor.Decode(page, scope)
	if err != nil {
		return nil, err
	}
	result := &ListResult[T]{Items: []T{}}
	err = u.repo.View(ctx, func(r Reader) error {
		items, err := read(r, after, size+1)
		if err != nil {
			return err
		}
		if len(items) > size {
			items = items[:size]
			result.NextToken = u.cursor.Encode(scope, id(items[len(items)-1]))
		}
		if items != nil {
			result.Items = items
		}
		return nil
	})
	return result, err
}
func (u *Usecase) ListClusters(ctx context.Context, a *AdminRead, page *Page) (*ListResult[Cluster], error) {
	if err := AuthorizeAdmin(ctx, a, "ListClusters"); err != nil {
		return nil, err
	}
	c, _ := CurrentCaller(ctx)
	return listPage(ctx, u, page, "ListClusters", nil, func(r Reader, after string, n int) ([]Cluster, error) { return r.Clusters(ctx, c.ClusterIDs, after, n) }, func(c Cluster) string { return c.ClusterID })
}
func (u *Usecase) ListPools(ctx context.Context, a *AdminRead, cluster string, page *Page) (*ListResult[Pool], error) {
	if err := AuthorizeAdmin(ctx, a, "ListPools"); err != nil {
		return nil, err
	}
	if !UUID(cluster) {
		return nil, Invalid("INVALID_CLUSTER")
	}
	if err := AuthorizeCluster(ctx, cluster); err != nil {
		return nil, err
	}
	return listPage(ctx, u, page, "ListPools", cluster, func(r Reader, after string, n int) ([]Pool, error) {
		if _, err := r.Cluster(ctx, cluster); err != nil {
			return nil, err
		}
		return r.Pools(ctx, cluster, after, n)
	}, func(p Pool) string { return p.PoolID })
}
func (u *Usecase) ListSupplyGroups(ctx context.Context, a *AdminRead, pool string, page *Page) (*ListResult[SupplyGroup], error) {
	if err := AuthorizeAdmin(ctx, a, "ListSupplyGroups"); err != nil {
		return nil, err
	}
	if !UUID(pool) {
		return nil, Invalid("INVALID_POOL")
	}
	return listPage(ctx, u, page, "ListSupplyGroups", pool, func(r Reader, after string, n int) ([]SupplyGroup, error) {
		p, err := r.Pool(ctx, pool)
		if err != nil {
			return nil, err
		}
		if err = AuthorizeCluster(ctx, p.ClusterID); err != nil {
			return nil, err
		}
		return r.Groups(ctx, pool, after, n)
	}, func(g SupplyGroup) string { return g.GroupID })
}
func (u *Usecase) AdminListProfiles(ctx context.Context, a *AdminRead, cluster string, page *Page) (*ProfileListResult, error) {
	if err := AuthorizeAdmin(ctx, a, "AdminListProfiles"); err != nil {
		return nil, err
	}
	return u.listProfiles(ctx, cluster, 0, page, "AdminListProfiles")
}
func (u *Usecase) ListProfiles(ctx context.Context, a *TenantContext, cluster string, mode SupplyMode, page *Page) (*ProfileListResult, error) {
	if err := AuthorizeTenant(ctx, a, "ListProfiles"); err != nil {
		return nil, err
	}
	return u.listProfiles(ctx, cluster, mode, page, "ListProfiles")
}
func (u *Usecase) listProfiles(ctx context.Context, cluster string, mode SupplyMode, page *Page, method string) (*ProfileListResult, error) {
	if !UUID(cluster) || (mode != 0 && mode != Whole && mode != Shared) {
		return nil, Invalid("INVALID_PROFILE_FILTER")
	}
	if err := AuthorizeCluster(ctx, cluster); err != nil {
		return nil, err
	}
	scopes := make(map[string]ProfileScope)
	result, err := listPage(ctx, u, page, method, []any{cluster, mode}, func(r Reader, after string, n int) ([]Profile, error) {
		if _, err := r.Cluster(ctx, cluster); err != nil {
			return nil, err
		}
		profiles, err := r.Profiles(ctx, cluster, mode, after, n)
		if err != nil {
			return nil, err
		}
		for _, profile := range profiles {
			if profile.Spec == nil {
				return nil, Precondition("PROFILE_SCOPE_MISMATCH")
			}
			group, err := r.Group(ctx, profile.Spec.GroupID)
			if err != nil {
				return nil, err
			}
			if group == nil {
				return nil, Precondition("PROFILE_SCOPE_MISMATCH")
			}
			pool, err := r.Pool(ctx, group.PoolID)
			if err != nil {
				return nil, err
			}
			scope, err := profileCatalogScope(profile, group, pool, cluster)
			if err != nil {
				return nil, err
			}
			if _, exists := scopes[profile.ProfileID]; exists {
				return nil, Precondition("PROFILE_SCOPE_MISMATCH")
			}
			scopes[profile.ProfileID] = scope
		}
		return profiles, nil
	}, func(p Profile) string { return p.ProfileID })
	if err != nil {
		return nil, err
	}
	out := &ProfileListResult{Items: result.Items, NextToken: result.NextToken, Scopes: make([]ProfileScope, 0, len(result.Items))}
	for _, profile := range result.Items {
		out.Scopes = append(out.Scopes, scopes[profile.ProfileID])
	}
	return out, nil
}

func profileCatalogScope(p Profile, g *SupplyGroup, pool *Pool, cluster string) (ProfileScope, error) {
	if !UUID(p.ProfileID) || p.ProfileVersion == 0 || p.Spec == nil || g == nil || pool == nil || g.Baseline == nil ||
		!UUID(p.Spec.GroupID) || p.Spec.GroupID != g.GroupID || g.ClusterID != cluster || !UUID(g.PoolID) ||
		g.PoolID != pool.PoolID || pool.ClusterID != cluster || p.BaselineID != g.Baseline.BaselineID ||
		p.Spec.Mode != g.Mode || p.Spec.ModelKey != g.ModelKey {
		return ProfileScope{}, Precondition("PROFILE_SCOPE_MISMATCH")
	}
	digest, err := SpecDigest(p.Spec, p.BaselineID)
	if err != nil {
		return ProfileScope{}, err
	}
	if !Digest(p.SpecDigest) || digest != p.SpecDigest {
		return ProfileScope{}, Invalid("SPEC_DIGEST_MISMATCH")
	}
	return ProfileScope{ProfileID: p.ProfileID, ProfileVersion: p.ProfileVersion, SpecDigest: p.SpecDigest, ClusterID: cluster, PoolID: pool.PoolID}, nil
}
func (u *Usecase) ListDevices(ctx context.Context, a *AdminRead, cluster, group string, page *Page) (*ListResult[PhysicalDevice], error) {
	if err := AuthorizeAdmin(ctx, a, "ListDevices"); err != nil {
		return nil, err
	}
	if !UUID(cluster) || (group != "" && !UUID(group)) {
		return nil, Invalid("INVALID_DEVICE_FILTER")
	}
	if err := AuthorizeCluster(ctx, cluster); err != nil {
		return nil, err
	}
	return listPage(ctx, u, page, "ListDevices", []string{cluster, group}, func(r Reader, after string, n int) ([]PhysicalDevice, error) {
		if _, err := r.Cluster(ctx, cluster); err != nil {
			return nil, err
		}
		if group != "" {
			g, err := r.Group(ctx, group)
			if err != nil {
				return nil, err
			}
			if g.ClusterID != cluster {
				return nil, NotFound()
			}
		}
		ds, err := r.Devices(ctx, cluster, group, after, n)
		if err != nil {
			return nil, err
		}
		result := make([]PhysicalDevice, 0, len(ds))
		for _, d := range ds {
			result = append(result, d.Device)
		}
		return result, nil
	}, func(d PhysicalDevice) string { return d.DeviceID })
}
func (u *Usecase) AdminListBindings(ctx context.Context, a *AdminRead, cluster, device string, page *Page) (*ListResult[Binding], error) {
	if err := AuthorizeAdmin(ctx, a, "AdminListBindings"); err != nil {
		return nil, err
	}
	if !UUID(cluster) || (device != "" && !UUID(device)) {
		return nil, Invalid("INVALID_BINDING_FILTER")
	}
	if err := AuthorizeCluster(ctx, cluster); err != nil {
		return nil, err
	}
	f := BindingFilter{ClusterID: cluster, DeviceID: device}
	res, err := listPage(ctx, u, page, "AdminListBindings", f, func(r Reader, after string, n int) ([]Binding, error) {
		if _, err := r.Cluster(ctx, cluster); err != nil {
			return nil, err
		}
		return u.bindingViews(ctx, r, f, after, n)
	}, func(b Binding) string { return b.BindingID })
	if err == nil {
		res.Observation = bindingsObservation(res.Items, u.now(), u.freshness)
	}
	return res, err
}
func (u *Usecase) ListBindings(ctx context.Context, a *TenantContext, ref *UsageRef, page *Page) (*ListResult[Binding], error) {
	if err := AuthorizeTenant(ctx, a, "ListBindings"); err != nil {
		return nil, err
	}
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	if a.TenantID != ref.TenantID {
		return nil, Denied()
	}
	f := BindingFilter{Usage: ref}
	res, err := listPage(ctx, u, page, "ListBindings", f, func(r Reader, after string, n int) ([]Binding, error) { return u.bindingViews(ctx, r, f, after, n) }, func(b Binding) string { return b.BindingID })
	if err == nil {
		res.Observation = bindingsObservation(res.Items, u.now(), u.freshness)
		for i := range res.Items {
			b := &res.Items[i]
			b.PhysicalDeviceID = ""
			b.EvidenceRef = ""
			b.SourceBaselineDigest = ""
			b.Pod = &PodRef{ContainerName: b.Pod.ContainerName}
			if b.Observation != nil {
				b.Observation.SnapshotRef = ""
			}
		}
		res.Observation.SnapshotRef = ""
	}
	return res, err
}

func (u *Usecase) bindingViews(ctx context.Context, r Reader, f BindingFilter, after string, n int) ([]Binding, error) {
	bs, err := r.Bindings(ctx, f, after, n)
	if err != nil {
		return nil, err
	}
	for i := range bs {
		b := &bs[i]
		if b.Pod == nil {
			continue
		}
		observation := bindingsObservation([]Binding{*b}, u.now(), u.freshness)
		// A complete persisted release retires the covered historical claims.
		// Current source availability cannot undo that established fact.
		if b.State == ReleaseConfirmed && observation.Complete {
			continue
		}
		claims, _, err := r.PodEvidence(ctx, *b.Pod)
		if err != nil {
			return nil, err
		}
		watermarks, err := r.SourceHealth(ctx, b.Pod.ClusterID)
		if err != nil {
			return nil, err
		}
		healthy := false
		for _, c := range claims {
			if c.DeviceID == b.PhysicalDeviceID {
				healthy = nodeSourcesHealthy(watermarks, c.NodeUID, u.now(), u.freshness)
				break
			}
		}
		if !healthy || !observation.Complete {
			b.State = AllocationUnknown
			if b.Observation == nil {
				b.Observation = &Observation{}
			}
			b.Observation.Complete = false
			b.Observation.Issues = unique(append(b.Observation.Issues, "SOURCE_UNKNOWN"))
		}
	}
	return bs, nil
}
