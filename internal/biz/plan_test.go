package biz

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testID = "10000000-0000-4000-8000-000000000001"

func baselineForTest(f uint32, mode SupplyMode) *ProviderBaseline {
	b := &ProviderBaseline{BaselineID: testID, AdapterID: "volcano-hami-mib-v1", VolcanoVersion: "v1.15.2", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "test-revision", MemoryBlockMiB: f, MemoryEncoding: "MIB", SplitCount: 10, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: strings.Repeat("c", 64), SchedulerConfigDigest: strings.Repeat("d", 64), RecipeVersion: "volcano-hami-mib-v1", RuntimeClassName: "nvidia"}
	if mode == Whole {
		b.SplitCount = 1
	}
	b.BaselineDigest, _ = BaselineDigest(b)
	return b
}
func testSpec(mode SupplyMode) *ProfileSpec {
	s := &ProfileSpec{GroupID: testID, Mode: mode, ModelKey: "nvidia-test", MemoryMiBPerDevice: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}
	if mode == Whole {
		s.MemoryMiBPerDevice = 15360
		s.CoreLimitPercent = 100
		s.IsolationClass = "WHOLE_DEVICE_EXCLUSIVE"
	}
	return s
}
func testProfile(mode SupplyMode) (*Profile, *SupplyGroup) {
	s := testSpec(mode)
	b := baselineForTest(1, mode)
	d, _ := SpecDigest(s, b.BaselineID)
	p := &Profile{ProfileID: testID, ProfileVersion: 1, DisplayName: "test", Spec: s, BaselineID: b.BaselineID, SpecDigest: d, State: ProfileActive, Revision: 1}
	g := &SupplyGroup{GroupID: testID, PoolID: testID, ClusterID: testID, Mode: mode, ModelKey: s.ModelKey, Baseline: b, QueueName: "gpu", Admission: Open}
	return p, g
}
func testPlan(t *testing.T, mode SupplyMode, _ uint32) *ResolvedGpuRequest {
	t.Helper()
	p, g := testProfile(mode)
	v, e := BuildResolution(quantityRequest(1, 2), p, g, 16, nil)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func requireReason(t *testing.T, err error, reason string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Reason != reason {
		t.Fatalf("error=%v want=%s", err, reason)
	}
}
func TestAbsoluteMemorySharedAndWhole(t *testing.T) {
	for _, mode := range []SupplyMode{Shared, Whole} {
		v := testPlan(t, mode, 1)
		memory := "6144"
		code := "gpu.shared_memory_mib"
		units := int64(12288)
		if mode == Whole {
			memory = "15360"
			code = "gpu.physical.count"
			units = 2
		}
		if len(v.Execution.Resources) != 3 || v.Execution.Resources["volcano.sh/vgpu-memory"] != memory || v.Execution.RuntimeClassName != "nvidia" {
			t.Fatal(v)
		}
		if len(v.QuotaItems) != 1 || v.QuotaItems[0].QuotaCode != code || v.QuotaItems[0].Units != units {
			t.Fatal(v.QuotaItems)
		}
	}
	for _, f := range []uint32{0, 256, 1024, math.MaxUint32} {
		requireReason(t, ValidateSpec(testSpec(Shared), baselineForTest(f, Shared)), "MEMORY_UNIT_MISMATCH")
	}
}
func TestOneMiBStepAndIntegerSafety(t *testing.T) {
	s := testSpec(Shared)
	s.MemoryMiBPerDevice = 1
	e, err := Render(s, baselineForTest(1, Shared), "gpu", 1, nil)
	if err != nil || e.Resources["volcano.sh/vgpu-memory"] != "1" {
		t.Fatal(e, err)
	}
	_, err = Multiply(math.MaxInt64, 2)
	requireReason(t, err, "INTEGER_OVERFLOW")
	_, err = Add(math.MaxInt64, 1)
	requireReason(t, err, "INTEGER_OVERFLOW")
	s.MemoryMiBPerDevice = int64(math.MaxInt32) + 1
	_, err = Render(s, baselineForTest(1, Shared), "gpu", 1, nil)
	requireReason(t, err, "PROVIDER_INTEGER_OVERFLOW")
	n, err := QuantizedBlocks([]uint64{15360 << 20, 15361 << 20}, 1)
	if err != nil || n != 30721 {
		t.Fatal(n, err)
	}
	_, err = NormalizeMemory(1, "BLOCKS", 256)
	requireReason(t, err, "MEMORY_UNIT_MISMATCH")
}
func TestExecutionDigestFixedFieldsAndCatalogIndependence(t *testing.T) {
	p, g := testProfile(Shared)
	v, err := BuildResolution(quantityRequest(1, 1), p, g, 16, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := v.Execution.ExecutionDigest
	p.State = ProfileDisabled
	p.Revision++
	p.DisplayName = "changed display"
	w, err := BuildResolution(quantityRequest(1, 1), p, g, 16, nil)
	if err != nil || w.Execution.ExecutionDigest != d {
		t.Fatal(w, err)
	}
	v.Execution.Resources["volcano.sh/vgpu-memory"] = "1"
	h, err := ExecutionDigest(v.Execution)
	if err != nil || h == d {
		t.Fatal("execution tampering not committed", err)
	}
	b, err := Canonical(map[string]any{"z": int64(math.MaxInt64), "a": "<&中文>", "b": true})
	if err != nil || string(b) != `{"a":"<&中文>","b":true,"z":"9223372036854775807"}` {
		t.Fatal(string(b), err)
	}
}
func testProjection(t *testing.T) *UsageProjection {
	p := &UsageProjection{Ref: &UsageRef{testID, "ani-resource-service", testID, testID}, Revision: 2, State: Ended, ProfileID: testID, ProfileVersion: 1, GroupID: testID, BaselineID: testID, ClusterID: testID, ContainerName: "workspace", ExecutionDigest: testPlan(t, Shared, 1).Execution.ExecutionDigest, EndReason: OwnerReleased, SourceFactRef: "quota-operation:" + testID + ":gpu-fully-released", SourceOperationCreatedAt: "2026-09-23T00:00:00Z"}
	p.PayloadDigest, _ = ProjectionDigest(p)
	return p
}
func TestSyncProjectionFactsAndTerminalShape(t *testing.T) {
	p := testProjection(t)
	if err := ValidateProjection(p); err != nil {
		t.Fatal(err)
	}
	p.Ref.OwnerService = "unknown-owner"
	p.PayloadDigest, _ = ProjectionDigest(p)
	requireReason(t, ValidateProjection(p), "OWNER_UNSUPPORTED")
	p.Ref.OwnerService = "ani-resource-service"
	p.SourceFactRef = "receipt:any"
	p.PayloadDigest, _ = ProjectionDigest(p)
	requireReason(t, ValidateProjection(p), "INVALID_SOURCE_FACT")
}
func TestCapacityLocalUnknownAndBudgetInvariant(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	b := baselineForTest(1, Shared)
	s := testSpec(Shared)
	d := DeviceRecord{Device: PhysicalDevice{DeviceID: testID, Node: &NodeRef{UID: "node-1", Name: "node"}, ModelKey: s.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: b.BaselineDigest}
	claim := Binding{BindingID: testID, Pod: &PodRef{UID: "pod-1"}, PhysicalDeviceID: testID, State: Active, MemoryMiB: 6144, CoreLimitPercent: 25, SourceBaselineDigest: b.BaselineDigest, SourceMemoryBlockMiB: 1, Observation: &Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano)}}
	c, err := ComputeDeviceCapacity(d, *s, *b, []Binding{claim}, now, time.Minute, true)
	if err != nil || !c.Complete || c.NominalUnits != 4 || c.KnownFreeUnits != 3 {
		t.Fatal(c, err)
	}
	claim.State = AllocationUnknown
	c, err = ComputeDeviceCapacity(d, *s, *b, []Binding{claim}, now, time.Minute, true)
	if err != nil || c.Complete || c.KnownCommittedUnits != 1 || c.KnownFreeUnits != 0 {
		t.Fatal(c, err)
	}
	claim.State = Active
	claim.MemoryMiB = 30000
	c, err = ComputeDeviceCapacity(d, *s, *b, []Binding{claim}, now, time.Minute, true)
	if err != nil || c.Complete || !reflect.DeepEqual(c.Issues, []string{"CAPACITY_INVARIANT_VIOLATION"}) {
		t.Fatal(c, err)
	}
	claim.State = ReleaseConfirmed
	c, err = ComputeDeviceCapacity(d, *s, *b, []Binding{claim}, now, time.Minute, true)
	if err != nil || c.KnownFreeUnits != 4 {
		t.Fatal(c, err)
	}
}
