package biz

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func quantityProfile(t *testing.T, mode SupplyMode, maximum uint32) (*Profile, *SupplyGroup) {
	t.Helper()
	spec := testSpec(mode)
	spec.MaxDevicesPerReplica = maximum
	baseline := baselineForTest(1024, mode)
	digest, err := SpecDigest(spec, baseline.BaselineID)
	if err != nil {
		t.Fatal(err)
	}
	profile := &Profile{ProfileID: testID, ProfileVersion: 1, DisplayName: "quantity contract", Spec: spec, BaselineID: baseline.BaselineID, SpecDigest: digest, Published: true}
	group := &SupplyGroup{GroupID: testID, PoolID: testID, ClusterID: testID, Mode: mode, ModelKey: spec.ModelKey, Baseline: baseline, QueueName: "gpu", Admission: Open}
	return profile, group
}

func quantityRequest(devices, replicas uint32) *GpuRequest {
	return &GpuRequest{ClusterID: testID, PoolID: testID, ProfileID: testID, ProfileVersion: 1, Replicas: replicas, DevicesPerReplica: devices, ContainerName: "workspace"}
}

func TestQuantitySameWholeProfileBuildsOneAndTwoDevices(t *testing.T) {
	profile, group := quantityProfile(t, Whole, 2)
	digests := make(map[string]bool)
	for _, replicas := range []uint32{1, 3} {
		for _, devices := range []uint32{1, 2} {
			request := quantityRequest(devices, replicas)
			plan, err := BuildPlan(request, profile, group, 16)
			if err != nil {
				t.Fatal(err)
			}
			limits := make(map[string]string)
			for _, field := range plan.Runtime.LimitsPerContainer {
				limits[field.Key] = field.Value
			}
			if limits["volcano.sh/vgpu-number"] != strconv.FormatUint(uint64(devices), 10) || limits["volcano.sh/vgpu-cores"] != "100" || limits["volcano.sh/vgpu-memory-percentage"] != "100" || len(limits) != 3 {
				t.Fatalf("wrong whole limits: %+v", limits)
			}
			if plan.Totals.ExclusiveDeviceCount != int64(replicas)*int64(devices) || plan.Totals.LogicalDeviceCount != 0 || plan.Totals.SharedMemoryMiB != 0 {
				t.Fatalf("wrong whole totals: %+v", plan.Totals)
			}
			if digests[plan.ResolutionDigest] {
				t.Fatal("different request quantities have the same digest")
			}
			digests[plan.ResolutionDigest] = true
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			var saved ResolvedPlan
			if err := json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			if err := ValidatePlan(&saved, profile, group, 16); err != nil {
				t.Fatal("serialized plan did not rebuild", err)
			}
			if replicas == 1 {
				t.Logf("BUILDER_VECTOR Whole%d %s", devices, raw)
			}
		}
	}
}

func TestQuantitySharedRetainsSingleDeviceEncoding(t *testing.T) {
	profile, group := quantityProfile(t, Shared, 1)
	for _, replicas := range []uint32{1, 3} {
		plan, err := BuildPlan(quantityRequest(1, replicas), profile, group, 16)
		if err != nil {
			t.Fatal(err)
		}
		limits := make(map[string]string)
		for _, field := range plan.Runtime.LimitsPerContainer {
			limits[field.Key] = field.Value
		}
		if limits["volcano.sh/vgpu-number"] != "1" || limits["volcano.sh/vgpu-memory"] != "6" || limits["volcano.sh/vgpu-cores"] != "25" || len(limits) != 3 || plan.Encoding.MemoryBlockMiB != 1024 || plan.Encoding.SharedMemoryMiB != 6144 {
			t.Fatalf("shared encoding changed: limits=%+v encoding=%+v", limits, plan.Encoding)
		}
		if plan.Totals.ExclusiveDeviceCount != 0 || plan.Totals.LogicalDeviceCount != int64(replicas) || plan.Totals.SharedMemoryMiB != int64(replicas)*6144 {
			t.Fatalf("wrong shared totals: %+v", plan.Totals)
		}
		if err := ValidatePlan(plan, profile, group, 16); err != nil {
			t.Fatal(err)
		}
		if replicas == 1 {
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("BUILDER_VECTOR Shared1 %s", raw)
		}
	}
}

func TestQuantityRejectsUnsupportedRequestsAndCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mode             SupplyMode
		maximum, devices uint32
	}{
		{"whole-max1-request2", Whole, 1, 2},
		{"whole-request0", Whole, 2, 0},
		{"whole-request3", Whole, 2, 3},
		{"whole-max0", Whole, 0, 1},
		{"whole-max3", Whole, 3, 1},
		{"shared-request2", Shared, 1, 2},
		{"shared-max2", Shared, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, group := quantityProfile(t, tc.mode, tc.maximum)
			_, err := BuildPlan(quantityRequest(tc.devices, 1), profile, group, 16)
			requireReason(t, err, "SHAPE_UNSUPPORTED")
		})
	}
}

func TestQuantityRejectsTamperingEvenWithRecomputedDigest(t *testing.T) {
	for _, field := range []string{"number", "total"} {
		t.Run(field, func(t *testing.T) {
			profile, group := quantityProfile(t, Whole, 2)
			plan, err := BuildPlan(quantityRequest(2, 3), profile, group, 16)
			if err != nil {
				t.Fatal(err)
			}
			if field == "total" {
				plan.Totals.ExclusiveDeviceCount = 3
			} else {
				for i := range plan.Runtime.LimitsPerContainer {
					if plan.Runtime.LimitsPerContainer[i].Key == "volcano.sh/vgpu-number" {
						plan.Runtime.LimitsPerContainer[i].Value = "1"
					}
				}
			}
			plan.ResolutionDigest, err = PlanDigest(plan)
			if err != nil {
				t.Fatal(err)
			}
			requireReason(t, ValidatePlan(plan, profile, group, 16), "PLAN_DIGEST_MISMATCH")
		})
	}
}

// All external ports are deliberately absent: an unexpected resolution or
// capacity read panics instead of fabricating a usable two-device estimate.
type noQuantityIO struct {
	Repository
	Provider
	ConsumptionEvidence
}

func TestQuantityTwoDeviceFitRejectsBeforeExternalReads(t *testing.T) {
	codec, err := NewCursorCodec([]byte(strings.Repeat("q", 32)))
	if err != nil {
		t.Fatal(err)
	}
	ports := noQuantityIO{}
	u, err := NewUsecase(ports, ports, ports, codec, nil, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{Type: "user", ID: "quantity-test"}
	a := &TenantContext{RequestID: "quantity-fit", TenantID: testID, Actor: &actor}
	ctx := WithCaller(context.Background(), Caller{Service: "ani-governance", Actor: actor, TenantID: testID, Action: "CheckGpuFit", ClusterIDs: []string{testID}})
	_, err = u.CheckFit(ctx, a, quantityRequest(2, 1))
	requireReason(t, err, "SHAPE_UNSUPPORTED")
	_, err = u.CheckFit(context.Background(), a, quantityRequest(2, 1))
	requireReason(t, err, "PERMISSION_DENIED")
}

func TestQuantityInferenceBuilderVectors(t *testing.T) {
	whole, wholeGroup := quantityProfile(t, Whole, 2)
	shared, sharedGroup := quantityProfile(t, Shared, 1)
	for _, tc := range []struct {
		name    string
		profile *Profile
		group   *SupplyGroup
		devices uint32
	}{{"Whole1", whole, wholeGroup, 1}, {"Whole2", whole, wholeGroup, 2}, {"Shared1", shared, sharedGroup, 1}} {
		request := quantityRequest(tc.devices, 1)
		request.ContainerName = "kserve-container"
		plan, err := BuildPlan(request, tc.profile, tc.group, 16)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidatePlan(plan, tc.profile, tc.group, 16); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("INFERENCE_BUILDER_VECTOR %s %s", tc.name, raw)
	}
}
