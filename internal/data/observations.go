package data

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"sort"
	"time"
)

func (s *pgStore) SaveObservation(ctx context.Context, batch biz.ObservationBatch) error {
	if err := biz.ValidateObservationBatch(batch); err != nil {
		return err
	}
	locked := map[string]biz.NodeTelemetryState{}
	nodes := make([]string, 0, len(batch.NodeRevisions))
	for node := range batch.NodeRevisions {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		payload, err := s.q.LockNodeTelemetryState(ctx, batch.ConnectionRef, node)
		if errors.Is(err, pgx.ErrNoRows) && batch.NodeRevisions[node] == 0 {
			continue
		}
		if err != nil {
			return cleanError(err)
		}
		var state biz.NodeTelemetryState
		if json.Unmarshal(payload, &state) != nil {
			return biz.Precondition("STORED_TELEMETRY_INVALID")
		}
		if state.CurrentRevision != batch.NodeRevisions[node] {
			return biz.Conflict("NODE_REVISION_CONFLICT")
		}
		locked[node] = state
	}
	if err := s.Lock(ctx, "observation:"+batch.ClusterID); err != nil {
		return err
	}
	for _, d := range batch.Devices {
		if d.Device.PhysicalMemoryBytes == 0 {
			prior, err := decodeOne[biz.DeviceRecord](s.q.SaveObservationData1(ctx, d.Device.DeviceID))
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
		if err = cleanError(s.q.SaveObservationWrite2(ctx, d.Device.DeviceID, d.Device.ClusterID, d.Device.GroupID, d.Device.Node.UID, d.Device.VendorDeviceID, observed, b)); err != nil {
			return err
		}
	}
	for _, c := range batch.Claims {
		prior, err := decodeOne[biz.Claim](s.q.SaveObservationData3(ctx, c.Pod.ClusterID, c.Pod.UID, c.Pod.ContainerName, c.DeviceID, c.Source))
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
		if err = cleanError(s.q.SaveObservationWrite4(ctx, c.Pod.ClusterID, c.Pod.UID, c.Pod.ContainerName, c.DeviceID, c.Source, c.ObservedAt, b)); err != nil {
			return err
		}
	}
	for _, p := range batch.Releases {
		b, err := jsonBytes(p)
		if err != nil {
			return err
		}
		if err = cleanError(s.q.SaveObservationWrite5(ctx, p.Pod.ClusterID, p.Pod.UID, p.Pod.ContainerName, p.DeviceID, p.ObservedAt, b)); err != nil {
			return err
		}
	}
	for _, w := range batch.Watermarks {
		if !w.Complete {
			prior, err := decodeOne[biz.SourceWatermark](s.q.SaveObservationData6(ctx, w.ClusterID, w.NodeUID, w.Source))
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
		if err = cleanError(s.q.SaveObservationWrite7(ctx, w.ClusterID, w.NodeUID, w.Source, w.ObservedAt, b)); err != nil {
			return err
		}
	}
	// Reconcile all persisted keys, including keys absent from this poll. This
	// preserves unknown occupancy and repairs binding-before-projection races.
	claims, err := decodeMany[biz.Claim](s.q.SaveObservationData8(ctx, batch.ClusterID))
	if err != nil {
		return err
	}
	for _, c := range claims {
		if err = s.reconcileBinding(ctx, c.Pod, c.DeviceID); err != nil {
			return err
		}
	}
	for _, node := range nodes {
		state, found := locked[node]
		if !found {
			continue
		}
		state.ConsumedRevision = state.CurrentRevision
		payload, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err = s.q.UpdateNodeTelemetryState(ctx, batch.ConnectionRef, node, state.Epoch, state.Identity.LogID, state.LastSequence, state.LastPayloadDigest, state.CurrentRevision, state.ConsumedRevision, payload); err != nil {
			return cleanError(err)
		}
	}
	return nil
}

func (s *pgStore) LockReleaseNodes(ctx context.Context, connection string, nodes []string) (map[string]biz.NodeTelemetryState, error) {
	nodes = append([]string(nil), nodes...)
	sort.Strings(nodes)
	states := make(map[string]biz.NodeTelemetryState, len(nodes))
	for _, node := range nodes {
		payload, err := s.q.LockNodeTelemetryState(ctx, connection, node)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, cleanError(err)
		}
		var state biz.NodeTelemetryState
		if json.Unmarshal(payload, &state) != nil {
			return nil, biz.Precondition("STORED_TELEMETRY_INVALID")
		}
		states[node] = state
	}
	return states, nil
}
func (s *pgStore) reconcileBinding(ctx context.Context, p biz.PodRef, device string) error {
	if err := s.Lock(ctx, "binding:"+p.ClusterID+":"+p.UID+":"+p.ContainerName+":"+device); err != nil {
		return err
	}
	claims, err := decodeMany[biz.Claim](s.q.ReconcileBindingData1(ctx, p.ClusterID, p.UID, p.ContainerName, device))
	if err != nil {
		return err
	}
	if len(claims) == 0 {
		return nil
	}
	proof, err := decodeOne[biz.ReleaseProof](s.q.ReconcileBindingData2(ctx, p.ClusterID, p.UID, p.ContainerName, device))
	if err != nil && !biz.IsNotFound(err) {
		return err
	}
	prior, err := decodeOne[biz.Binding](s.q.ReconcileBindingData3(ctx, p.ClusterID, p.UID, p.ContainerName, device))
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
	var tenant, owner, resource, create *string
	if binding.VerifiedUsageRef != nil {
		r := binding.VerifiedUsageRef
		tenant = &r.TenantID
		owner = &r.OwnerService
		resource = &r.ResourceID
		create = &r.CreateOperationID
	}
	return cleanError(s.q.ReconcileBindingWrite4(ctx, id, p.ClusterID, p.UID, p.ContainerName, device, tenant, owner, resource, create, b))
}
