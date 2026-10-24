package biz

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func quantityProfile(t *testing.T, mode SupplyMode, maximum uint32) (*Profile, *SupplyGroup) {
	t.Helper()
	p, g := testProfile(mode)
	p.Spec.MaxDevicesPerReplica = maximum
	p.SpecDigest, _ = SpecDigest(p.Spec, p.BaselineID)
	return p, g
}
func quantityRequest(devices, replicas uint32) *GpuRequest {
	return &GpuRequest{ClusterID: testID, PoolID: testID, ProfileID: testID, ProfileVersion: 1, Replicas: replicas, DevicesPerReplica: devices, ContainerName: "workspace"}
}
func TestWholeQuantityUsesOneResourcesAuthority(t *testing.T) {
	p, g := quantityProfile(t, Whole, 4)
	for _, replicas := range []uint32{1, 3} {
		for _, devices := range []uint32{1, 2, 4} {
			v, err := BuildResolution(quantityRequest(devices, replicas), p, g, 16, nil)
			if err != nil {
				t.Fatal(err)
			}
			if v.Execution.Resources["volcano.sh/vgpu-number"] != strconv.Itoa(int(devices)) || v.Execution.Resources["volcano.sh/vgpu-memory"] != "15360" || v.Execution.Resources["volcano.sh/vgpu-cores"] != "100" || v.QuotaItems[0].Units != int64(replicas*devices) {
				t.Fatal(v)
			}
		}
	}
	for _, devices := range []uint32{0, 5} {
		_, err := BuildResolution(quantityRequest(devices, 1), p, g, 16, nil)
		requireReason(t, err, "SHAPE_UNSUPPORTED")
	}
	p, g = quantityProfile(t, Shared, 1)
	_, err := BuildResolution(quantityRequest(2, 1), p, g, 16, nil)
	requireReason(t, err, "SHAPE_UNSUPPORTED")
}

// All external ports are deliberately absent: an unexpected resolution or
// capacity read panics instead of fabricating a usable two-device estimate.
type noQuantityIO struct {
	Repository
	Provider
}

func TestQuantityTwoDeviceFitRejectsBeforeExternalReads(t *testing.T) {
	codec, err := NewCursorCodec([]byte(strings.Repeat("q", 32)))
	if err != nil {
		t.Fatal(err)
	}
	ports := noQuantityIO{}
	u, err := NewUsecase(ports, ports, codec, nil, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{Type: "user", ID: "quantity-test"}
	a := &TenantContext{RequestID: "quantity-fit", TenantID: testID, Actor: &actor}
	ctx := WithCaller(context.Background(), Caller{Service: "ani-governance", Actor: actor, TenantID: testID, Action: "CheckGpuFit"})
	_, err = u.CheckFit(ctx, a, quantityRequest(2, 1))
	requireReason(t, err, "SHAPE_UNSUPPORTED")
	_, err = u.CheckFit(context.Background(), a, quantityRequest(2, 1))
	requireReason(t, err, "PERMISSION_DENIED")
}
