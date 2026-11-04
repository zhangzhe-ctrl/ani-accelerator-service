package biz

import "context"

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
	if (owner != "" && !validOwnerService(owner)) || (state != 0 && state != Declared && state != Ended) {
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
