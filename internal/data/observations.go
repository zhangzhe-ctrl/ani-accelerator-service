package data

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func (s *pgStore) SaveObservation(ctx context.Context, batch biz.ObservationBatch) error {
	if err := biz.ValidateObservationBatch(batch); err != nil {
		return err
	}
	if err := s.Lock(ctx, "observation:"+batch.ClusterID); err != nil {
		return err
	}
	row, err := s.q.LockCollectionStateData(ctx, batch.ClusterID)
	if err != nil {
		return cleanError(err)
	}
	if row.ObservationRevision != batch.BaseObservationRevision {
		return biz.Conflict("OBSERVATION_REVISION_CONFLICT")
	}
	if row.ObservationRevision >= math.MaxInt64 {
		return biz.Precondition("OBSERVATION_REVISION_EXHAUSTED")
	}
	state, err := decodeOne[biz.CollectionState](row.CurrentObservation, nil)
	if err != nil {
		return err
	}
	for groupID := range batch.PendingByGroup {
		group, err := s.Group(ctx, groupID)
		if err != nil {
			return err
		}
		if group.ClusterID != batch.ClusterID {
			return biz.Invalid("INVALID_PENDING_DEMAND")
		}
	}
	if err := s.reconcileDeviceInventory(ctx, batch); err != nil {
		return err
	}
	revision := row.ObservationRevision + 1
	if batch.PodListComplete {
		if err := s.replaceCurrentBindings(ctx, batch, revision); err != nil {
			return err
		}
	}

	issues := make([]string, 0, len(batch.BindingIssues))
	for _, issue := range batch.BindingIssues {
		if issue != "" {
			issues = append(issues, issue)
		}
	}
	if (!batch.PodListComplete || len(batch.IncompletePodUIDs) > 0) && len(issues) == 0 {
		issues = append(issues, "SOURCE_UNKNOWN")
	}
	slices.Sort(issues)
	issues = slices.Compact(issues)
	observation := &biz.Observation{
		Complete:   batch.PodListComplete && len(batch.IncompletePodUIDs) == 0 && len(issues) == 0,
		ReceivedAt: batch.ReceivedAt.UTC().Format(time.RFC3339Nano), Issues: issues,
	}
	if batch.PodListComplete {
		observation.ObservedAt = batch.ObservedAt.UTC().Format(time.RFC3339Nano)
		observation.SnapshotRef = batch.SnapshotRef
		state.PendingByGroup = make(map[string]int64, len(batch.PendingByGroup))
		for group, count := range batch.PendingByGroup {
			state.PendingByGroup[group] = count
		}
	} else if state.Observation != nil {
		observation.ObservedAt = state.Observation.ObservedAt
		observation.SnapshotRef = state.Observation.SnapshotRef
	}
	if state.PendingByGroup == nil {
		state.PendingByGroup = map[string]int64{}
	}
	state.Observation = observation
	payload, err := jsonBytes(state)
	if err != nil {
		return err
	}
	count, err := s.q.SaveCollectionStateCAS(ctx, batch.ClusterID, batch.BaseObservationRevision, payload)
	if err != nil {
		return cleanError(err)
	}
	if count != 1 {
		return biz.Conflict("OBSERVATION_REVISION_CONFLICT")
	}
	return nil
}

func (s *pgStore) reconcileDeviceInventory(ctx context.Context, batch biz.ObservationBatch) error {
	deviceIDs := make([]string, 0, len(batch.Devices))
	for _, device := range batch.Devices {
		payload, err := jsonBytes(device)
		if err != nil {
			return err
		}
		observed, _ := time.Parse(time.RFC3339Nano, device.Device.Observation.ObservedAt)
		count, err := s.q.SaveObservationWrite2(ctx, device.Device.DeviceID, batch.ClusterID,
			device.Device.GroupID, device.Device.Node.UID, device.Device.VendorDeviceID, observed, payload)
		if err != nil {
			return cleanError(err)
		}
		if count != 1 {
			return biz.Conflict("DEVICE_IDENTITY_CONFLICT")
		}
		deviceIDs = append(deviceIDs, device.Device.DeviceID)
	}
	if batch.NodeListComplete {
		if err := s.q.RetireMissingNodeDevices(ctx, batch.ClusterID, append([]string{}, batch.LiveNodeUIDs...)); err != nil {
			return cleanError(err)
		}
	}
	if len(batch.InventoryCompleteNodeUIDs) > 0 {
		if err := s.q.RetireMissingInventoryDevices(ctx, batch.ClusterID, batch.InventoryCompleteNodeUIDs, deviceIDs); err != nil {
			return cleanError(err)
		}
	}
	// Retained current devices have no fresh registration record this round.
	// Their old identity/budget remains, while capacity and physical bytes are unknown.
	return cleanError(s.q.MarkUnobservedDevicesUnknown(ctx, batch.ClusterID, deviceIDs, batch.ReceivedAt.UTC().Format(time.RFC3339Nano)))
}

func (s *pgStore) replaceCurrentBindings(ctx context.Context, batch biz.ObservationBatch, revision uint64) error {
	observation := biz.Observation{
		Complete: true, ObservedAt: batch.ObservedAt.UTC().Format(time.RFC3339Nano),
		ReceivedAt:  batch.ReceivedAt.UTC().Format(time.RFC3339Nano),
		SnapshotRef: batch.SnapshotRef, Issues: []string{},
	}
	for _, observed := range batch.Bindings {
		prior, err := decodeOne[biz.Binding](s.q.CurrentBindingByKeyData(ctx, batch.ClusterID, observed.Pod.UID, observed.Pod.ContainerName, observed.DeviceID))
		if err != nil && !biz.IsNotFound(err) {
			return err
		}
		id := biz.NewID()
		if prior != nil {
			id = prior.BindingID
		}
		var usage *biz.UsageProjection
		var candidate []byte
		if observed.OwnerRef != nil {
			usage, err = s.Usage(ctx, *observed.OwnerRef)
			if err != nil && !biz.IsNotFound(err) {
				return err
			}
			candidate, err = jsonBytes(biz.BindingOwnerCandidate{Ref: *observed.OwnerRef, ExecutionDigest: observed.ExecutionDigest})
			if err != nil {
				return err
			}
		}
		binding, err := biz.BuildCurrentBinding(id, observed, observation, usage)
		if err != nil {
			return err
		}
		payload, err := jsonBytes(binding)
		if err != nil {
			return err
		}
		var tenant, owner, resource, create *string
		if ref := binding.VerifiedUsageRef; ref != nil {
			tenant, owner, resource, create = &ref.TenantID, &ref.OwnerService, &ref.ResourceID, &ref.CreateOperationID
		}
		if err := s.q.UpsertCurrentBindingWrite(ctx, id, batch.ClusterID, observed.Pod.UID,
			observed.Pod.ContainerName, observed.DeviceID, tenant, owner, resource, create, payload, candidate, revision); err != nil {
			return cleanError(err)
		}
	}
	if len(batch.IncompletePodUIDs) > 0 {
		if err := s.q.MarkIncompleteCurrentBindings(ctx, batch.ClusterID, batch.IncompletePodUIDs, revision, observation.ReceivedAt); err != nil {
			return cleanError(err)
		}
	}
	return cleanError(s.q.EndMissingCurrentBindings(ctx, batch.ClusterID, revision,
		observation.ObservedAt, observation.ReceivedAt, batch.SnapshotRef))
}
