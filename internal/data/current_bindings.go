package data

import (
	"context"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func (s *pgStore) CollectionState(ctx context.Context, cluster string) (*biz.CollectionState, error) {
	row, err := s.q.CollectionStateData(ctx, cluster)
	if err != nil {
		return nil, cleanError(err)
	}
	state, err := decodeOne[biz.CollectionState](row.CurrentObservation, nil)
	if err != nil {
		return nil, err
	}
	state.Revision = row.ObservationRevision
	return state, nil
}

func (s *pgStore) CurrentDeviceBindings(ctx context.Context, device string) ([]biz.Binding, error) {
	return decodeMany[biz.Binding](s.q.CurrentDeviceBindingsData(ctx, device))
}

func (s *pgStore) CurrentBindingMetrics(ctx context.Context, connections []string, observedAfter, observedBefore time.Time) ([]biz.CurrentBindingMetric, error) {
	result := []biz.CurrentBindingMetric{}
	if len(connections) == 0 {
		return result, nil
	}
	if observedAfter.IsZero() || observedBefore.Before(observedAfter) {
		return nil, biz.Invalid("INVALID_OBSERVATION_RANGE")
	}
	rows, err := s.q.CurrentBindingMetricsData(ctx, connections, observedAfter, observedBefore)
	if err != nil {
		return nil, cleanError(err)
	}
	for _, row := range rows {
		binding, err := decodeOne[biz.Binding](row.BindingPayload, nil)
		if err != nil {
			return nil, err
		}
		device, err := decodeOne[biz.DeviceRecord](row.DevicePayload, nil)
		if err != nil {
			return nil, err
		}
		if binding.Pod == nil || biz.ValidatePod(*binding.Pod) != nil ||
			biz.ValidateRef(binding.VerifiedUsageRef) != nil || device.Device.Node == nil ||
			!biz.Ref(device.Device.Node.UID) || !biz.Text(device.Device.Node.Name, 253) ||
			biz.NormalizeGPUUUID(device.Device.VendorDeviceID) == "" ||
			binding.PhysicalDeviceID != device.Device.DeviceID || binding.Pod.ClusterID != device.Device.ClusterID {
			return nil, biz.Precondition("CURRENT_BINDING_INVALID")
		}
		result = append(result, biz.CurrentBindingMetric{
			Pod: *binding.Pod, Node: *device.Device.Node, GPUUUID: device.Device.VendorDeviceID,
			UsageRef: *binding.VerifiedUsageRef,
		})
	}
	return result, nil
}
