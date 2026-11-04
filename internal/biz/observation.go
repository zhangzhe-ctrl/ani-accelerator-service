package biz

import (
	"sort"
	"time"
)

func podKey(p PodRef) string { return p.ClusterID + "/" + p.UID + "/" + p.ContainerName }
func fresh(observed, now time.Time, ttl time.Duration) bool {
	return !observed.IsZero() && !observed.After(now.Add(5*time.Second)) && now.Sub(observed) <= ttl
}
func oldest(a, b string) string {
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	x, e1 := time.Parse(time.RFC3339Nano, a)
	y, e2 := time.Parse(time.RFC3339Nano, b)
	if e1 != nil || e2 != nil {
		return ""
	}
	if y.Before(x) {
		return b
	}
	return a
}
func unique(xs []string) []string {
	sort.Strings(xs)
	out := []string{}
	for _, x := range xs {
		if x != "" && (len(out) == 0 || out[len(out)-1] != x) {
			out = append(out, x)
		}
	}
	return out
}
func bindingsObservation(bs []Binding, now time.Time, ttl time.Duration) *Observation {
	o := &Observation{Complete: len(bs) > 0}
	if len(bs) == 0 {
		o.Issues = []string{"SCOPE_UNOBSERVED"}
	}
	for _, b := range bs {
		if b.Observation == nil {
			o.Complete = false
			continue
		}
		o.ObservedAt = oldest(o.ObservedAt, b.Observation.ObservedAt)
		o.ReceivedAt = oldest(o.ReceivedAt, b.Observation.ReceivedAt)
		t, _ := time.Parse(time.RFC3339Nano, b.Observation.ObservedAt)
		timeKnown := fresh(t, now, ttl)
		if b.State == ReleaseConfirmed {
			// Ended allocations retain their completed observation timestamps.
			// Current source availability does not put them back into occupancy.
			received, _ := time.Parse(time.RFC3339Nano, b.Observation.ReceivedAt)
			timeKnown = ValidTime(b.Observation.ObservedAt) && ValidTime(b.Observation.ReceivedAt) && !t.After(now.Add(5*time.Second)) && !received.After(now.Add(5*time.Second)) && !received.Before(t.Add(-5*time.Second))
		}
		if !b.Observation.Complete || !timeKnown || (b.State != Active && b.State != ReleaseConfirmed) {
			o.Complete = false
			o.Issues = append(o.Issues, "SOURCE_UNKNOWN")
		}
		o.Issues = append(o.Issues, b.Observation.Issues...)
	}
	o.Issues = unique(o.Issues)
	return o
}
