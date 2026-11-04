package provider

import (
	"math"
	"testing"
	"time"
)

func TestDeviceBudgetUsesRegisteredBlocksAndVerifiedFactor(t *testing.T) {
	for _, test := range []struct {
		name    string
		blocks  int64
		factor  uint32
		want    int64
		invalid bool
	}{
		{name: "factor_one", blocks: 16384, factor: 1, want: 16384},
		{name: "factor_ten", blocks: 2458, factor: 10, want: 24580},
		{name: "factor_unknown", blocks: 100, factor: 2, invalid: true},
		{name: "zero_registration", factor: 1, invalid: true},
		{name: "negative_registration", blocks: -1, factor: 1, invalid: true},
		{name: "overflow", blocks: math.MaxInt64, factor: 10, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget, err := deviceMemoryBudget(test.blocks, test.factor)
			if test.invalid {
				if err == nil {
					t.Fatal("invalid registration or factor accepted")
				}
				return
			}
			if err != nil || budget != test.want {
				t.Fatalf("budget = %d, error = %v, want = %d", budget, err, test.want)
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
