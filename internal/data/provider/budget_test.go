package provider

import (
	"testing"
	"time"
)

func TestDeviceBudgetUsesBothLiveLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		blocks  int64
		factor  uint32
		bytes   uint64
		want    int64
		invalid bool
	}{
		{name: "plugin_lower", blocks: 15360, factor: 1, bytes: 16127 << 20, want: 15360},
		{name: "nvml_lower", blocks: 16384, factor: 1, bytes: 16127 << 20, want: 16127},
		{name: "nvml_fractional_mib", blocks: 16384, factor: 1, bytes: (16127 << 20) + (1 << 19), want: 16127},
		{name: "different_card", blocks: 12288, factor: 1, bytes: 24576 << 20, want: 12288},
		{name: "zero_registration", factor: 1, bytes: 16127 << 20, invalid: true},
		{name: "missing_nvml", blocks: 15360, factor: 1, invalid: true},
		{name: "old_factor", blocks: 60, factor: 256, bytes: 16127 << 20, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deviceMemoryBudget(tc.blocks, tc.factor, tc.bytes)
			if tc.invalid {
				if err == nil {
					t.Fatal("missing or mismatched source accepted")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("budget = %d, error = %v, want = %d", got, err, tc.want)
			}
		})
	}
}

func TestSourceFreshRejectsStaleAndFutureObservations(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for _, observed := range []time.Time{time.Time{}, now.Add(-time.Minute - time.Nanosecond), now.Add(time.Hour)} {
		if sourceFresh(now, observed, time.Minute) {
			t.Fatal("unknown source time accepted", observed)
		}
	}
	if !sourceFresh(now, now.Add(time.Millisecond), time.Minute) {
		t.Fatal("collection sampling skew rejected")
	}
}
