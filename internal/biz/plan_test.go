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
	b := &ProviderBaseline{BaselineID: testID, AdapterID: "volcano-hami-factor-v1", VolcanoVersion: "v1.12.0", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "test-revision", MemoryBlockMiB: f, FactorEvidenceRef: "evidence:effective-factor", MemoryEncoding: "VOLCANO_BLOCKS_TO_MIB", SplitCount: 4, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: strings.Repeat("c", 64), SchedulerConfigDigest: strings.Repeat("d", 64), RecipeVersion: "volcano-hami-v1"}
	if mode == Whole {
		b.SplitCount = 1
	}
	b.BaselineDigest, _ = BaselineDigest(b)
	return b
}
func testSpec(mode SupplyMode) *ProfileSpec {
	s := &ProfileSpec{GroupID: testID, Mode: mode, ModelKey: "nvidia-test", SharedMemoryMiB: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}
	if mode == Whole {
		s.SharedMemoryMiB = 0
		s.CoreLimitPercent = 100
		s.IsolationClass = "WHOLE_DEVICE_EXCLUSIVE"
	}
	return s
}
func testPlan(t *testing.T, mode SupplyMode, f uint32) *ResolvedPlan {
	t.Helper()
	s := testSpec(mode)
	b := baselineForTest(f, mode)
	d, _ := SpecDigest(s, b.BaselineID)
	p := &Profile{ProfileID: testID, ProfileVersion: 1, DisplayName: "test", Spec: s, BaselineID: b.BaselineID, SpecDigest: d, Published: true}
	g := &SupplyGroup{GroupID: testID, PoolID: testID, ClusterID: testID, Mode: mode, ModelKey: s.ModelKey, Baseline: b, QueueName: "gpu", Admission: Open}
	r := &GpuRequest{ClusterID: testID, PoolID: testID, ProfileID: testID, ProfileVersion: 1, Replicas: 2, DevicesPerReplica: 1, ContainerName: "inference"}
	plan, err := BuildPlan(r, p, g, 16)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func requireReason(t *testing.T, err error, reason string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Reason != reason {
		t.Fatalf("error=%v, want %s", err, reason)
	}
}
func TestUNIT01ExactMiBAndPerContainerEncoding(t *testing.T) {
	for _, f := range []uint32{1, 256, 1024} {
		p := testPlan(t, Shared, f)
		if p.Encoding.MemoryBlocksPerDevice != 6144/int64(f) || p.Encoding.SharedMemoryMiB != 6144 || p.Totals.SharedMemoryMiB != 12288 || p.Totals.LogicalDeviceCount != 2 {
			t.Fatalf("factor %d: %+v %+v", f, p.Encoding, p.Totals)
		}
		for _, v := range p.Runtime.LimitsPerContainer {
			if v.Key == "volcano.sh/vgpu-memory" && v.Value == "12288" {
				t.Fatal("instance total leaked to per-container request")
			}
		}
	}
	p := testPlan(t, Whole, 1024)
	if p.Totals.ExclusiveDeviceCount != 2 || p.Totals.SharedMemoryMiB != 0 || p.Encoding.MemoryPercentage != 100 {
		t.Fatal(p)
	}
	for _, v := range p.Runtime.LimitsPerContainer {
		if v.Key == "volcano.sh/vgpu-memory" {
			t.Fatal("whole mode has absolute memory")
		}
	}
}
func TestUNIT02GranularityRejectsPublicationAndResolution(t *testing.T) {
	b := baselineForTest(10, Shared)
	s := testSpec(Shared)
	requireReason(t, ValidateSpec(s, b), "MEMORY_GRANULARITY_MISMATCH")
	_, _, err := Render(s, b, "gpu")
	requireReason(t, err, "MEMORY_GRANULARITY_MISMATCH")
}
func TestUNIT03PerDeviceQuantization(t *testing.T) {
	n, err := QuantizedBlocks([]uint64{12285 << 20, 12285 << 20}, 10)
	if err != nil || n != 2456 {
		t.Fatalf("got %d %v", n, err)
	}
	n, err = QuantizedBlocks([]uint64{49140 << 20}, 256)
	if err != nil || n != 191 {
		t.Fatalf("got %d %v", n, err)
	}
}
func TestUNIT04NormalizeOnce(t *testing.T) {
	for _, x := range []struct {
		n    int64
		unit string
	}{{6, "BLOCKS"}, {6144, "MIB"}, {6144 << 20, "BYTES"}} {
		m, err := NormalizeMemory(x.n, x.unit, 1024)
		if err != nil || m != 6144 {
			t.Fatalf("%+v=%d %v", x, m, err)
		}
	}
	_, err := NormalizeMemory(1025, "BYTES", 1024)
	requireReason(t, err, "MEMORY_GRANULARITY_MISMATCH")
}
func TestUNIT05UnknownFactorAndBaseline(t *testing.T) {
	for _, f := range []uint32{0, math.MaxUint32} {
		b := baselineForTest(f, Shared)
		requireReason(t, ValidateSpec(testSpec(Shared), b), "MEMORY_UNIT_UNVERIFIED")
	}
	b := baselineForTest(1024, Shared)
	b.NodeConfigDigest = ""
	requireReason(t, ValidateSpec(testSpec(Shared), b), "BASELINE_INCOMPLETE")
}
func TestUNIT06HistoricalFactorFrozen(t *testing.T) {
	p := testPlan(t, Shared, 1024)
	original := p.ResolutionDigest
	q := testPlan(t, Shared, 256)
	if original == q.ResolutionDigest || p.Encoding.MemoryBlocksPerDevice != 6 {
		t.Fatal("plan was reinterpreted")
	}
	m, err := NormalizeMemory(6, "BLOCKS", p.Encoding.MemoryBlockMiB)
	if err != nil || m != 6144 {
		t.Fatal(m, err)
	}
}
func TestUNIT07RejectsOverflowShapeAndDuplicates(t *testing.T) {
	_, err := Multiply(math.MaxInt64, 2)
	requireReason(t, err, "INTEGER_OVERFLOW")
	_, err = Add(math.MaxInt64, 1)
	requireReason(t, err, "INTEGER_OVERFLOW")
	_, err = EncodeMemory(int64(math.MaxInt32)+1, 1)
	requireReason(t, err, "PROVIDER_INTEGER_OVERFLOW")
	r := *testPlan(t, Shared, 1024).Request
	for _, mutate := range []func(*GpuRequest){func(r *GpuRequest) { r.Replicas = 17 }, func(r *GpuRequest) { r.Replicas = 0 }, func(r *GpuRequest) { r.DevicesPerReplica = 2 }, func(r *GpuRequest) { r.ContainerName = "Bad_Container" }} {
		copy := r
		mutate(&copy)
		requireReason(t, ValidateRequest(&copy, 16), "SHAPE_UNSUPPORTED")
	}
	p := testPlan(t, Shared, 1024)
	p.Runtime.NodeLabels = append(p.Runtime.NodeLabels, p.Runtime.NodeLabels[0])
	_, err = PlanDigest(p)
	requireReason(t, err, "DUPLICATE_OR_EMPTY_KEY")
}
func TestUNIT08CanonicalSemanticContent(t *testing.T) {
	a, err := Canonical(map[string]any{"z": int64(math.MaxInt64), "a": "<&中文>", "b": true})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != `{"a":"<&中文>","b":true,"z":"9223372036854775807"}` {
		t.Fatalf("canonical=%s", a)
	}
	p := testPlan(t, Shared, 1024)
	d := p.ResolutionDigest
	p.Profile.Published = false
	p.Runtime.LimitsPerContainer[0], p.Runtime.LimitsPerContainer[1] = p.Runtime.LimitsPerContainer[1], p.Runtime.LimitsPerContainer[0]
	h, err := PlanDigest(p)
	if err != nil || h != d {
		t.Fatal("ordering/state changed digest", h, err)
	}
	p.Runtime.LimitsPerContainer[0].Value = "99"
	h, err = PlanDigest(p)
	if err != nil || h == d {
		t.Fatal("changed GPU rendering not detected")
	}
	b := baselineForTest(1024, Shared)
	d = b.BaselineDigest
	b.Verification = &Verification{State: Verified, VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	h, err = BaselineDigest(b)
	if err != nil || h != d {
		t.Fatal("observation changed baseline")
	}
}
func TestSyncProjectionFactsAndTerminalShape(t *testing.T) {
	p := &UsageProjection{Ref: &UsageRef{testID, "ani-inference", testID, testID}, Revision: 2, State: Ended, Plan: testPlan(t, Shared, 1024), EndReason: OwnerReleased, SourceFactRef: "quota-operation:" + testID + ":gpu-fully-released", SourceOperationCreatedAt: "2026-09-23T00:00:00Z"}
	p.PayloadDigest, _ = ProjectionDigest(p)
	if err := ValidateProjection(p); err != nil {
		t.Fatal(err)
	}
	p.SourceFactRef = "receipt:any"
	p.PayloadDigest, _ = ProjectionDigest(p)
	requireReason(t, ValidateProjection(p), "INVALID_SOURCE_FACT")
}
func TestCapacityLocalUnknownAndBudgetInvariant(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	b := baselineForTest(1024, Shared)
	s := testSpec(Shared)
	d := DeviceRecord{Device: PhysicalDevice{DeviceID: testID, Node: &NodeRef{UID: "node-1", Name: "node"}, ModelKey: s.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24, MemoryBlockMiB: 1024, SafeMemoryBudgetMiB: 24576, BaselineDigest: b.BaselineDigest}
	claim := Binding{BindingID: testID, Pod: &PodRef{UID: "pod-1"}, PhysicalDeviceID: testID, State: Active, MemoryMiB: 6144, CoreLimitPercent: 25, SourceBaselineDigest: b.BaselineDigest, SourceMemoryBlockMiB: 1024, Observation: &Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano)}}
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
