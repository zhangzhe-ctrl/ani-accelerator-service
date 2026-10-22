package biz

import (
	"math"
	"testing"
)

func TestFactor10ResolutionKeepsMiBQuotaAndFreezesBlockEncoding(t *testing.T) {
	profile, group := testProfile(Shared)
	group.Baseline = baselineForTest(10, Shared)
	profile.Spec.MemoryMiBPerDevice = 3840
	profile.SpecDigest, _ = SpecDigest(profile.Spec, profile.BaselineID)
	resolved, err := BuildResolution(quantityRequest(1, 1), profile, group, 16, map[string]string{"kubernetes.io/hostname": "node3"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Execution.Resources["volcano.sh/vgpu-memory"] != "384" || resolved.Execution.PodAnnotations["accelerator.ani.io/memory-block-mib"] != "10" || resolved.QuotaItems[0].Units != 3840 {
		t.Fatalf("wrong units: %+v %+v", resolved.Execution, resolved.QuotaItems)
	}
	if resolved.Execution.NodeSelector["kubernetes.io/hostname"] != "node3" {
		t.Fatalf("factor10 execution lost RuntimeClass placement: %+v", resolved.Execution)
	}
	profile.Spec.MemoryMiBPerDevice = 6144
	_, err = Render(profile.Spec, group.Baseline, "gpu", 1, nil)
	requireReason(t, err, "MEMORY_GRANULARITY_MISMATCH")
}

func TestFactor10NormalizationDoesNotMultiplyRuntimeMiBTwice(t *testing.T) {
	for _, input := range []struct {
		value int64
		unit  string
	}{{384, "BLOCKS"}, {3840, "MIB"}, {3840 << 20, "BYTES"}} {
		actual, err := NormalizeMemory(input.value, input.unit, 10)
		if err != nil || actual != 3840 {
			t.Fatalf("%+v: got %d %v", input, actual, err)
		}
	}
	blocks, err := QuantizedBlocks([]uint64{15369 << 20, 15369 << 20}, 10)
	if err != nil || blocks != 3072 {
		t.Fatalf("per-device tail: %d %v", blocks, err)
	}
	_, err = EncodeMemory(math.MaxInt32+3, 10)
	requireReason(t, err, "PROVIDER_INTEGER_OVERFLOW")
	for _, factor := range []uint32{0, 2, 256, 1024, math.MaxUint32} {
		_, err = EncodeMemory(3840, factor)
		requireReason(t, err, "MEMORY_UNIT_MISMATCH")
	}
}

func TestFactor10WholeExecutionRetainsPhysicalCardBilling(t *testing.T) {
	profile, group := testProfile(Whole)
	group.Baseline = baselineForTest(10, Whole)
	profile.Spec.MaxDevicesPerReplica = 2
	profile.SpecDigest, _ = SpecDigest(profile.Spec, profile.BaselineID)
	resolved, err := BuildResolution(quantityRequest(2, 1), profile, group, 16, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Execution.Resources["volcano.sh/vgpu-memory"] != "1536" || resolved.Execution.Resources["volcano.sh/vgpu-number"] != "2" || resolved.QuotaItems[0].QuotaCode != "gpu.physical.count" || resolved.QuotaItems[0].Units != 2 {
		t.Fatalf("wrong whole units: %+v %+v", resolved.Execution, resolved.QuotaItems)
	}
}
