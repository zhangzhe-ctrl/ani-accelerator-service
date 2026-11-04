package data

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"time"
)

func (s *pgStore) Usage(ctx context.Context, r biz.UsageRef) (*biz.UsageProjection, error) {
	return decodeOne[biz.UsageProjection](s.q.UsageData1(ctx, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID))
}
func (s *pgStore) Usages(ctx context.Context, tenant, owner string, state biz.UsageState, after string, n int) ([]biz.UsageProjection, error) {
	return decodeMany[biz.UsageProjection](s.q.UsagesData1(ctx, tenant, owner, int32(state), after, int32(n)))
}
func (s *pgStore) SaveUsage(ctx context.Context, p biz.UsageProjection) (*biz.SyncResult, error) {
	r := p.Ref
	// Observation writes acquire device/FK locks before binding locks. Take
	// the same cluster lock before usage/FK writes to keep one global order:
	// cluster -> usage -> binding. This also prevents missed associations.
	if err := s.Lock(ctx, "observation:"+p.ClusterID); err != nil {
		return nil, err
	}
	if err := s.Lock(ctx, "usage:"+r.TenantID+":"+r.OwnerService+":"+r.ResourceID); err != nil {
		return nil, err
	}
	prior, err := decodeOne[biz.UsageProjection](s.q.SaveUsageData1(ctx, r.TenantID, r.OwnerService, r.ResourceID))
	if err != nil && !biz.IsNotFound(err) {
		return nil, err
	}
	result := &biz.SyncResult{Ref: r, AppliedRevision: p.Revision, State: p.State}
	b, err := jsonBytes(p)
	if err != nil {
		return nil, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, p.SourceOperationCreatedAt)
	if err != nil {
		return nil, biz.Invalid("SOURCE_TIME_INVALID")
	}
	if prior == nil {
		err = cleanError(s.q.SaveUsageWrite2(ctx, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID, p.ProfileID, p.ProfileVersion, int32(p.Revision), p.ExecutionDigest, createdAt, b))
	} else {
		if *prior.Ref != *p.Ref || prior.ExecutionDigest != p.ExecutionDigest || prior.SourceOperationCreatedAt != p.SourceOperationCreatedAt || prior.ProfileID != p.ProfileID || prior.ProfileVersion != p.ProfileVersion || prior.GroupID != p.GroupID || prior.BaselineID != p.BaselineID || prior.ClusterID != p.ClusterID || prior.ContainerName != p.ContainerName {
			return nil, biz.Conflict("USAGE_PROJECTION_CONFLICT")
		}
		digest, e := s.q.UsageRevisionDigest(ctx, r.TenantID, r.OwnerService, r.ResourceID, int32(p.Revision))
		if e == nil && digest != p.PayloadDigest {
			return nil, biz.Conflict("USAGE_PROJECTION_CONFLICT")
		}
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return nil, cleanError(e)
		}
		if p.Revision > prior.Revision {
			err = cleanError(s.q.SaveUsageWrite3(ctx, int32(p.Revision), b, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID))
		} else {
			result.AppliedRevision = prior.Revision
			result.State = prior.State
			result.Replayed = true
		}
	}
	if err != nil {
		return nil, err
	}
	if err = cleanError(s.q.SaveUsageWrite4(ctx, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID, int32(p.Revision), p.PayloadDigest)); err != nil {
		return nil, err
	}
	// Both new projections and replays only link still-unassociated candidates.
	// State, quantities and observation timestamps are never reconstructed here.
	if err := s.linkPendingBindings(ctx, p); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *pgStore) Devices(ctx context.Context, cluster, group, after string, n int) ([]biz.DeviceRecord, error) {
	return decodeMany[biz.DeviceRecord](s.q.DevicesData1(ctx, cluster, group, after, int32(n)))
}
func (s *pgStore) Bindings(ctx context.Context, f biz.BindingFilter, after string, n int) ([]biz.Binding, error) {
	if f.Usage != nil {
		r := f.Usage
		return decodeMany[biz.Binding](s.q.BindingsData1(ctx, &r.TenantID, &r.OwnerService, &r.ResourceID, &r.CreateOperationID, after, int32(n)))
	}
	if f.DeviceID != "" {
		present, err := s.q.DevicePresent(ctx, f.ClusterID, f.DeviceID)
		if err != nil {
			return nil, cleanError(err)
		}
		if !present {
			return nil, biz.NotFound()
		}
	}
	return decodeMany[biz.Binding](s.q.BindingsData2(ctx, f.ClusterID, f.DeviceID, after, int32(n)))
}
func (s *pgStore) linkPendingBindings(ctx context.Context, usage biz.UsageProjection) error {
	ref := usage.Ref
	payload, err := jsonBytes(ref)
	if err != nil {
		return err
	}
	return cleanError(s.q.LinkPendingBindingOwners(ctx, ref.TenantID, ref.OwnerService,
		ref.ResourceID, ref.CreateOperationID, usage.ClusterID, usage.ContainerName,
		usage.ExecutionDigest, payload))
}
