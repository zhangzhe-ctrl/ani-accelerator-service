package data

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func (s *pgStore) Usage(ctx context.Context, r biz.UsageRef) (*biz.UsageProjection, error) {
	return one[biz.UsageProjection](ctx, s, `SELECT payload FROM accelerator.usages WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND create_operation_id=$4`, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID)
}
func (s *pgStore) Usages(ctx context.Context, tenant, owner string, state biz.UsageState, after string, n int) ([]biz.UsageProjection, error) {
	return many[biz.UsageProjection](ctx, s, `SELECT payload FROM accelerator.usages WHERE tenant_id=$1 AND ($2='' OR owner_service=$2) AND ($3=0 OR revision=$3) AND resource_id>COALESCE(NULLIF($4,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY resource_id LIMIT $5`, tenant, owner, int32(state), after, n)
}
func (s *pgStore) SaveUsage(ctx context.Context, p biz.UsageProjection) (*biz.SyncResult, error) {
	r := p.Ref
	// Observation writes acquire device/FK locks before binding locks. Take
	// the same cluster lock before usage/FK writes to keep one global order:
	// cluster -> usage -> binding. This also prevents missed associations.
	if err := s.Lock(ctx, "observation:"+p.Plan.Request.ClusterID); err != nil {
		return nil, err
	}
	if err := s.Lock(ctx, "usage:"+r.TenantID+":"+r.OwnerService+":"+r.ResourceID); err != nil {
		return nil, err
	}
	prior, err := one[biz.UsageProjection](ctx, s, `SELECT payload FROM accelerator.usages WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 FOR UPDATE`, r.TenantID, r.OwnerService, r.ResourceID)
	if err != nil && !biz.IsNotFound(err) {
		return nil, err
	}
	result := &biz.SyncResult{Ref: r, AppliedRevision: p.Revision, State: p.State}
	b, err := jsonBytes(p)
	if err != nil {
		return nil, err
	}
	if prior == nil {
		err = s.exec(ctx, `INSERT INTO accelerator.usages(tenant_id,owner_service,resource_id,create_operation_id,profile_id,profile_version,revision,plan_digest,source_created_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID, p.Plan.Request.ProfileID, p.Plan.Request.ProfileVersion, p.Revision, p.Plan.ResolutionDigest, p.SourceOperationCreatedAt, b)
	} else {
		if *prior.Ref != *p.Ref || prior.Plan.ResolutionDigest != p.Plan.ResolutionDigest || prior.SourceOperationCreatedAt != p.SourceOperationCreatedAt {
			return nil, biz.Conflict("USAGE_PROJECTION_CONFLICT")
		}
		var digest string
		e := s.tx.QueryRow(ctx, `SELECT payload_digest FROM accelerator.usage_revisions WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND revision=$4`, r.TenantID, r.OwnerService, r.ResourceID, p.Revision).Scan(&digest)
		if e == nil && digest != p.PayloadDigest {
			return nil, biz.Conflict("USAGE_PROJECTION_CONFLICT")
		}
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return nil, cleanError(e)
		}
		if p.Revision > prior.Revision {
			err = s.exec(ctx, `UPDATE accelerator.usages SET revision=$1,payload=$2 WHERE tenant_id=$3 AND owner_service=$4 AND resource_id=$5 AND create_operation_id=$6`, p.Revision, b, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID)
		} else {
			result.AppliedRevision = prior.Revision
			result.State = prior.State
			result.Replayed = true
		}
	}
	if err != nil {
		return nil, err
	}
	if err = s.exec(ctx, `INSERT INTO accelerator.usage_revisions(tenant_id,owner_service,resource_id,create_operation_id,revision,payload_digest) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID, p.Revision, p.PayloadDigest); err != nil {
		return nil, err
	}
	// A binding that arrived first becomes tenant-visible only after both the
	// signed owner assertion and the persisted immutable plan match.
	claims, err := many[biz.Claim](ctx, s, `SELECT payload FROM accelerator.claims WHERE payload->'owner_ref'->>'tenant_id'=$1 AND payload->'owner_ref'->>'owner_service'=$2 AND payload->'owner_ref'->>'resource_id'=$3 AND payload->'owner_ref'->>'create_operation_id'=$4 AND cluster_id=$5 ORDER BY pod_uid,container_name,device_id,source`, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID, p.Plan.Request.ClusterID)
	if err != nil {
		return nil, err
	}
	for _, c := range claims {
		if err = s.reconcileBinding(ctx, c.Pod, c.DeviceID); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *pgStore) Devices(ctx context.Context, cluster, group, after string, n int) ([]biz.DeviceRecord, error) {
	return many[biz.DeviceRecord](ctx, s, `SELECT payload FROM accelerator.devices WHERE cluster_id=$1 AND ($2='' OR group_id=NULLIF($2,'')::uuid) AND device_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY device_id LIMIT $4`, cluster, group, after, n)
}
func (s *pgStore) Bindings(ctx context.Context, f biz.BindingFilter, after string, n int) ([]biz.Binding, error) {
	if f.Usage != nil {
		r := f.Usage
		return many[biz.Binding](ctx, s, `SELECT payload FROM accelerator.bindings WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND create_operation_id=$4 AND binding_id>COALESCE(NULLIF($5,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY binding_id LIMIT $6`, r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID, after, n)
	}
	if f.DeviceID != "" {
		var present bool
		err := s.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accelerator.devices WHERE cluster_id=$1 AND device_id=$2)`, f.ClusterID, f.DeviceID).Scan(&present)
		if err != nil {
			return nil, cleanError(err)
		}
		if !present {
			return nil, biz.NotFound()
		}
	}
	return many[biz.Binding](ctx, s, `SELECT payload FROM accelerator.bindings WHERE cluster_id=$1 AND ($2='' OR device_id=NULLIF($2,'')::uuid) AND binding_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY binding_id LIMIT $4`, f.ClusterID, f.DeviceID, after, n)
}
func (s *pgStore) DeviceBindings(ctx context.Context, id string) ([]biz.Binding, error) {
	return many[biz.Binding](ctx, s, `SELECT payload FROM accelerator.bindings WHERE device_id=$1 ORDER BY binding_id`, id)
}
func (s *pgStore) SourceHealth(ctx context.Context, cluster string) ([]biz.SourceWatermark, error) {
	return many[biz.SourceWatermark](ctx, s, `SELECT payload FROM accelerator.source_watermarks WHERE cluster_id=$1 ORDER BY node_uid,source`, cluster)
}
func (s *pgStore) PodEvidence(ctx context.Context, p biz.PodRef) ([]biz.Claim, []biz.ReleaseProof, error) {
	cs, err := many[biz.Claim](ctx, s, `SELECT payload FROM accelerator.claims WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 ORDER BY device_id,source`, p.ClusterID, p.UID, p.ContainerName)
	if err != nil {
		return nil, nil, err
	}
	ps, err := many[biz.ReleaseProof](ctx, s, `SELECT payload FROM accelerator.release_proofs WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 ORDER BY device_id`, p.ClusterID, p.UID, p.ContainerName)
	for _, c := range cs {
		if c.Pod != p {
			return nil, nil, biz.Invalid("POD_SCOPE_MISMATCH")
		}
	}
	return cs, ps, err
}
