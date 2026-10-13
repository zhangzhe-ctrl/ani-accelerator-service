package provider

import (
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
