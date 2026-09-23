package data

import (
	"context"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"time"
)

func (s *pgStore) SaveObservation(ctx context.Context, batch biz.ObservationBatch) error {
	if err := biz.ValidateObservationBatch(batch); err != nil {
		return err
	}
	if err := s.Lock(ctx, "observation:"+batch.ClusterID); err != nil {
		return err
	}
	for _, d := range batch.Devices {
		if d.Device.PhysicalMemoryBytes == 0 {
			prior, err := one[biz.DeviceRecord](ctx, s, `SELECT payload FROM accelerator.devices WHERE device_id=$1`, d.Device.DeviceID)
			if err != nil && !biz.IsNotFound(err) {
				return err
			}
			if prior != nil {
				d.Device.PhysicalMemoryBytes = prior.Device.PhysicalMemoryBytes
				// Preserve the last trusted inventory observation, while reporting
				// the new failed reception separately. It cannot become free capacity.
				d.Device.Observation.ObservedAt = prior.Device.Observation.ObservedAt
			}
		}
		b, err := jsonBytes(d)
		if err != nil {
			return err
		}
		observed, _ := time.Parse(time.RFC3339Nano, d.Device.Observation.ObservedAt)
		if err = s.exec(ctx, `INSERT INTO accelerator.devices(device_id,cluster_id,group_id,node_uid,vendor_device_id,observed_at,payload) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7) ON CONFLICT (device_id) DO UPDATE SET group_id=EXCLUDED.group_id,observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload WHERE accelerator.devices.cluster_id=EXCLUDED.cluster_id AND accelerator.devices.node_uid=EXCLUDED.node_uid AND accelerator.devices.vendor_device_id=EXCLUDED.vendor_device_id AND accelerator.devices.observed_at<=EXCLUDED.observed_at`, d.Device.DeviceID, d.Device.ClusterID, d.Device.GroupID, d.Device.Node.UID, d.Device.VendorDeviceID, observed, b); err != nil {
			return err
		}
	}
	for _, c := range batch.Claims {
		prior, err := one[biz.Claim](ctx, s, `SELECT payload FROM accelerator.claims WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4 AND source=$5`, c.Pod.ClusterID, c.Pod.UID, c.Pod.ContainerName, c.DeviceID, c.Source)
		if err != nil && !biz.IsNotFound(err) {
			return err
		}
		if prior != nil && prior.ObservedAt.Equal(c.ObservedAt) {
			x, y := *prior, c
			x.ReceivedAt = time.Time{}
			y.ReceivedAt = time.Time{}
			a, _ := biz.Hash(x)
			b, _ := biz.Hash(y)
			if a != b {
				return biz.Conflict("SOURCE_REVISION_CONFLICT")
			}
		}
		b, err := jsonBytes(c)
		if err != nil {
			return err
		}
		if err = s.exec(ctx, `INSERT INTO accelerator.claims(cluster_id,pod_uid,container_name,device_id,source,observed_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (cluster_id,pod_uid,container_name,device_id,source) DO UPDATE SET observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload WHERE accelerator.claims.observed_at<EXCLUDED.observed_at`, c.Pod.ClusterID, c.Pod.UID, c.Pod.ContainerName, c.DeviceID, c.Source, c.ObservedAt, b); err != nil {
			return err
		}
	}
	for _, p := range batch.Releases {
		b, err := jsonBytes(p)
		if err != nil {
			return err
		}
		if err = s.exec(ctx, `INSERT INTO accelerator.release_proofs(cluster_id,pod_uid,container_name,device_id,observed_at,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (cluster_id,pod_uid,container_name,device_id) DO UPDATE SET observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload WHERE accelerator.release_proofs.observed_at<EXCLUDED.observed_at`, p.Pod.ClusterID, p.Pod.UID, p.Pod.ContainerName, p.DeviceID, p.ObservedAt, b); err != nil {
			return err
		}
	}
	for _, w := range batch.Watermarks {
		if !w.Complete {
			prior, err := one[biz.SourceWatermark](ctx, s, `SELECT payload FROM accelerator.source_watermarks WHERE cluster_id=$1 AND node_uid=$2 AND source=$3`, w.ClusterID, w.NodeUID, w.Source)
			if err != nil && !biz.IsNotFound(err) {
				return err
			}
			w.ObservedAt = time.Time{}
			if prior != nil {
				w.ObservedAt = prior.ObservedAt
				w.Cursor = prior.Cursor
			}
		}
		if w.ObservedAt.IsZero() {
			w.ObservedAt = time.Unix(0, 0).UTC()
		}
		b, err := jsonBytes(w)
		if err != nil {
			return err
		}
		if err = s.exec(ctx, `INSERT INTO accelerator.source_watermarks(cluster_id,node_uid,source,observed_at,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT (cluster_id,node_uid,source) DO UPDATE SET observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload WHERE (accelerator.source_watermarks.payload->>'received_at')::timestamptz <= (EXCLUDED.payload->>'received_at')::timestamptz`, w.ClusterID, w.NodeUID, w.Source, w.ObservedAt, b); err != nil {
			return err
		}
	}
	// Reconcile all persisted keys, including keys absent from this poll. This
	// preserves unknown occupancy and repairs binding-before-projection races.
	claims, err := many[biz.Claim](ctx, s, `SELECT DISTINCT ON (pod_uid,container_name,device_id) payload FROM accelerator.claims WHERE cluster_id=$1 ORDER BY pod_uid,container_name,device_id,source`, batch.ClusterID)
	if err != nil {
		return err
	}
	for _, c := range claims {
		if err = s.reconcileBinding(ctx, c.Pod, c.DeviceID); err != nil {
			return err
		}
	}
	return nil
}
func (s *pgStore) reconcileBinding(ctx context.Context, p biz.PodRef, device string) error {
	if err := s.Lock(ctx, "binding:"+p.ClusterID+":"+p.UID+":"+p.ContainerName+":"+device); err != nil {
		return err
	}
	claims, err := many[biz.Claim](ctx, s, `SELECT payload FROM accelerator.claims WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4 ORDER BY source`, p.ClusterID, p.UID, p.ContainerName, device)
	if err != nil {
		return err
	}
	if len(claims) == 0 {
		return nil
	}
	proof, err := one[biz.ReleaseProof](ctx, s, `SELECT payload FROM accelerator.release_proofs WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4`, p.ClusterID, p.UID, p.ContainerName, device)
	if err != nil && !biz.IsNotFound(err) {
		return err
	}
	prior, err := one[biz.Binding](ctx, s, `SELECT payload FROM accelerator.bindings WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4`, p.ClusterID, p.UID, p.ContainerName, device)
	if err != nil && !biz.IsNotFound(err) {
		return err
	}
	id := biz.NewID()
	if prior != nil {
		id = prior.BindingID
	}
	var usage *biz.UsageProjection
	for _, c := range claims {
		if c.OwnerRef != nil {
			usage, err = s.Usage(ctx, *c.OwnerRef)
			if err != nil && !biz.IsNotFound(err) {
				return err
			}
			break
		}
	}
	binding, err := biz.MergeBinding(id, claims, proof, usage)
	if err != nil {
		return err
	}
	b, err := jsonBytes(binding)
	if err != nil {
		return err
	}
	var tenant, owner, resource, create any
	if binding.VerifiedUsageRef != nil {
		r := binding.VerifiedUsageRef
		tenant = r.TenantID
		owner = r.OwnerService
		resource = r.ResourceID
		create = r.CreateOperationID
	}
	return s.exec(ctx, `INSERT INTO accelerator.bindings(binding_id,cluster_id,pod_uid,container_name,device_id,tenant_id,owner_service,resource_id,create_operation_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (cluster_id,pod_uid,container_name,device_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,owner_service=EXCLUDED.owner_service,resource_id=EXCLUDED.resource_id,create_operation_id=EXCLUDED.create_operation_id,payload=EXCLUDED.payload`, id, p.ClusterID, p.UID, p.ContainerName, device, tenant, owner, resource, create, b)
}
