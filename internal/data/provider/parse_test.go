package provider

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

const gpuUUID = "GPU-01234567-89ab-cdef-0123-456789abcdef"

func TestPinnedUpstreamFormats(t *testing.T) {
	ds, err := ParseNodeDevices(gpuUUID + ",4,24,NVIDIA-Test,true,hami-core:")
	if err != nil || len(ds) != 1 || ds[0].MemoryBlocks != 24 || ds[0].SplitCount != 4 {
		t.Fatal(ds, err)
	}
	as, err := ParseAssignments(gpuUUID + ",NVIDIA,6,25:")
	if err != nil || len(as) != 1 || as[0][0].MemoryBlocks != 6 {
		t.Fatal(as, err)
	}
	for _, bad := range []string{"GPU-0,4,24,NVIDIA-Test,true,hami-core:", gpuUUID + ",-1,24,NVIDIA-Test,true,hami-core:", gpuUUID + ",4,2147483648,NVIDIA-Test,true,hami-core:", gpuUUID + ",4,24,NVIDIA-Test,true,mig:"} {
		if _, err = ParseNodeDevices(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if PhysicalID("cluster", "node-a", gpuUUID) == PhysicalID("cluster", "node-b", gpuUUID) {
		t.Fatal("node identity lost")
	}
	tm, err := ParseHandshake("Reported 2026-09-23 00:00:00.123 +0000 UTC m=+1.2")
	if err != nil || tm.Format(time.RFC3339Nano) != "2026-09-23T00:00:00.123Z" {
		t.Fatal(tm, err)
	}
	if _, err = ParseHandshake(""); err == nil {
		t.Fatal("missing source time accepted")
	}
	want := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	got, err := ParseAssignedTime(strconv.FormatInt(want.Unix(), 10))
	if err != nil || !got.Equal(want) {
		t.Fatal("Volcano Unix seconds changed allocation chronology", got, err)
	}
	for _, bad := range []string{"", "0", "-1", "+1", "1.5", strconv.FormatInt(want.UnixNano(), 10)} {
		if _, err = ParseAssignedTime(bad); err == nil {
			t.Fatal("invalid allocation time accepted", bad)
		}
	}
}
func TestRuntimeLimitsAreMiBNotBlocks(t *testing.T) {
	raw := `{"runtimeSpec":{"process":{"env":["NVIDIA_VISIBLE_DEVICES=` + gpuUUID + `","CUDA_DEVICE_MEMORY_LIMIT_0=6144m","CUDA_DEVICE_SM_LIMIT=25"]}}}`
	a, err := ParseContainerdInfo(raw)
	if err != nil || a.MemoryMiB != 6144 || a.CorePercent != 25 || a.UUID != gpuUUID {
		t.Fatal(a, err)
	}
	if _, err = ParseContainerdInfo(strings.Replace(raw, "6144m", "6144", 1)); err == nil {
		t.Fatal("unitless runtime limit accepted")
	}
	if _, err = ParseContainerdInfo(strings.Replace(raw, gpuUUID, "0", 1)); err == nil {
		t.Fatal("runtime index treated as physical UUID")
	}
}

func TestRuntimeEnvironmentUsesEffectiveGPUSelection(t *testing.T) {
	raw := `{"runtimeSpec":{"process":{"env":["NVIDIA_VISIBLE_DEVICES=all","CUDA_DEVICE_MEMORY_LIMIT_0=49140m","CUDA_DEVICE_SM_LIMIT=100","NVIDIA_VISIBLE_DEVICES=` + gpuUUID + `"]}}}`
	allocation, err := ParseContainerdInfo(raw)
	if err != nil || allocation.UUID != gpuUUID || allocation.MemoryMiB != 49140 || allocation.CorePercent != 100 {
		t.Fatalf("effective runtime allocation = %+v, error = %v", allocation, err)
	}
}

func TestRuntimeEnvironmentValidatesEffectiveValues(t *testing.T) {
	const otherGPU = "GPU-ffffffff-ffff-ffff-ffff-ffffffffffff"
	base := []string{"NVIDIA_VISIBLE_DEVICES=" + gpuUUID, "CUDA_DEVICE_MEMORY_LIMIT_0=6144m", "CUDA_DEVICE_SM_LIMIT=25"}
	for _, test := range []struct {
		name      string
		overrides []string
		want      RuntimeAllocation
		wantError string
	}{
		{name: "identical_gpu", overrides: []string{"NVIDIA_VISIBLE_DEVICES=" + gpuUUID}, want: RuntimeAllocation{gpuUUID, 6144, 25}},
		{name: "different_gpu", overrides: []string{"NVIDIA_VISIBLE_DEVICES=" + otherGPU}, want: RuntimeAllocation{otherGPU, 6144, 25}},
		{name: "effective_all", overrides: []string{"NVIDIA_VISIBLE_DEVICES=all"}, wantError: "PROVIDER_DEVICE_IDENTITY"},
		{name: "effective_void", overrides: []string{"NVIDIA_VISIBLE_DEVICES=void"}, wantError: "PROVIDER_DEVICE_IDENTITY"},
		{name: "effective_empty_gpu", overrides: []string{"NVIDIA_VISIBLE_DEVICES="}, wantError: "PROVIDER_DEVICE_IDENTITY"},
		{name: "shadowed_invalid_memory", overrides: []string{"CUDA_DEVICE_MEMORY_LIMIT_0=invalid", "CUDA_DEVICE_MEMORY_LIMIT_0=3072m"}, want: RuntimeAllocation{gpuUUID, 3072, 25}},
		{name: "effective_unitless_memory", overrides: []string{"CUDA_DEVICE_MEMORY_LIMIT_0=3072"}, wantError: "MEMORY_UNIT_MISMATCH"},
		{name: "effective_empty_memory", overrides: []string{"CUDA_DEVICE_MEMORY_LIMIT_0="}, wantError: "MEMORY_UNIT_MISMATCH"},
		{name: "effective_negative_memory", overrides: []string{"CUDA_DEVICE_MEMORY_LIMIT_0=-1m"}, wantError: "RUNTIME_LIMIT_INVALID"},
		{name: "shadowed_invalid_cores", overrides: []string{"CUDA_DEVICE_SM_LIMIT=101", "CUDA_DEVICE_SM_LIMIT=50"}, want: RuntimeAllocation{gpuUUID, 6144, 50}},
		{name: "effective_invalid_cores", overrides: []string{"CUDA_DEVICE_SM_LIMIT=101"}, wantError: "RUNTIME_LIMIT_INVALID"},
		{name: "effective_empty_cores", overrides: []string{"CUDA_DEVICE_SM_LIMIT="}, wantError: "RUNTIME_LIMIT_INVALID"},
		{name: "unsupported_second_device", overrides: []string{"CUDA_DEVICE_MEMORY_LIMIT_1=6144m"}, wantError: "SHAPE_UNSUPPORTED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := append(append([]string(nil), base...), test.overrides...)
			raw, err := json.Marshal(map[string]any{"runtimeSpec": map[string]any{"process": map[string]any{"env": environment}}})
			if err != nil {
				t.Fatal(err)
			}
			allocation, err := ParseContainerdInfo(string(raw))
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError {
					t.Fatalf("allocation = %+v, error = %v, want error = %s", allocation, err, test.wantError)
				}
				return
			}
			if err != nil || allocation != test.want {
				t.Fatalf("allocation = %+v, error = %v, want = %+v", allocation, err, test.want)
			}
		})
	}
}
