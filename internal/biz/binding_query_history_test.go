package biz

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Only the repository port is replaced. Authorization, pagination, binding
// query projection and observation aggregation run through the real use case.
type bindingQueryReader struct {
	Reader
	bindings []Binding
	now      time.Time
	healthy  bool
}

func (r *bindingQueryReader) Cluster(context.Context, string) (*Cluster, error) {
	return &Cluster{ClusterID: testID, ConnectionRef: "test:binding"}, nil
}
func (r *bindingQueryReader) Bindings(context.Context, BindingFilter, string, int) ([]Binding, error) {
	result := append([]Binding{}, r.bindings...)
	for i := range result {
		if result[i].Observation != nil {
			copy := *result[i].Observation
			copy.Issues = append([]string(nil), copy.Issues...)
			result[i].Observation = &copy
		}
	}
	return result, nil
}
func (r *bindingQueryReader) PodEvidence(_ context.Context, pod PodRef) ([]Claim, []ReleaseProof, error) {
	return []Claim{{Pod: pod, DeviceID: testID, NodeUID: "node-1"}}, nil, nil
}
func (r *bindingQueryReader) SourceHealth(context.Context, string) ([]SourceWatermark, error) {
	var result []SourceWatermark
	for _, source := range []string{"kubernetes", "podresources", "cri"} {
		result = append(result, SourceWatermark{ClusterID: testID, NodeUID: "node-1", Source: source, ObservedAt: r.now, Complete: r.healthy})
	}
	return result, nil
}

type bindingQueryRepo struct{ reader Reader }

func (r bindingQueryRepo) View(_ context.Context, read func(Reader) error) error {
	return read(r.reader)
}
func (bindingQueryRepo) Transact(context.Context, func(Transaction) error) error {
	panic("binding queries must not write")
}

func readBindingQuery(t *testing.T, now time.Time, healthy bool, bindings ...Binding) *ListResult[Binding] {
	t.Helper()
	cursor, err := NewCursorCodec([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	reader := &bindingQueryReader{bindings: bindings, now: now, healthy: healthy}
	u := &Usecase{connections: map[string]bool{"test:binding": true}, repo: bindingQueryRepo{reader}, cursor: cursor, freshness: 120 * time.Second, now: func() time.Time { return now }}
	actor := Actor{Type: "user", ID: "binding-query-test"}
	ctx := WithCaller(context.Background(), Caller{Service: "ani-governance", Actor: actor, Action: "AdminListBindings"})
	result, err := u.AdminListBindings(ctx, &AdminRead{RequestID: "binding-history", Actor: &actor}, testID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func queryBinding(state AllocationState, observed time.Time) Binding {
	stamp := observed.UTC().Format(time.RFC3339Nano)
	return Binding{BindingID: testID, Pod: &PodRef{ClusterID: testID, Namespace: "test", Name: "workload", UID: "pod-1", ContainerName: "inference"}, PhysicalDeviceID: testID, State: state, EvidenceRef: "release:historical-proof", Observation: &Observation{Complete: true, ObservedAt: stamp, ReceivedAt: stamp, SnapshotRef: "release:historical-proof"}}
}

func TestBindingQueryHistoricalReleaseRemainsKnown(t *testing.T) {
	now := time.Date(2026, 10, 16, 19, 16, 48, 0, time.UTC)
	for _, test := range []struct {
		name     string
		state    AllocationState
		age      time.Duration
		healthy  bool
		change   func(*Binding)
		want     AllocationState
		complete bool
	}{
		{name: "released_old_healthy", state: ReleaseConfirmed, age: 4 * time.Minute, healthy: true, want: ReleaseConfirmed, complete: true},
		{name: "released_old_sources_offline", state: ReleaseConfirmed, age: 4 * time.Minute, want: ReleaseConfirmed, complete: true},
		{name: "released_fresh_sources_offline", state: ReleaseConfirmed, age: time.Second, want: ReleaseConfirmed, complete: true},
		{name: "active_fresh_healthy", state: Active, age: time.Second, healthy: true, want: Active, complete: true},
		{name: "active_expired", state: Active, age: 4 * time.Minute, healthy: true, want: AllocationUnknown},
		{name: "active_sources_offline", state: Active, age: time.Second, want: AllocationUnknown},
		{name: "unknown_stays_incomplete", state: AllocationUnknown, age: time.Second, healthy: true, change: func(b *Binding) { b.Observation.Complete = false }, want: AllocationUnknown},
		{name: "unknown_cannot_become_complete", state: AllocationUnknown, age: time.Second, healthy: true, want: AllocationUnknown},
		{name: "released_incomplete", state: ReleaseConfirmed, age: 4 * time.Minute, change: func(b *Binding) { b.Observation.Complete = false }, want: AllocationUnknown},
		{name: "released_missing_observation", state: ReleaseConfirmed, age: time.Second, healthy: true, change: func(b *Binding) { b.Observation = nil }, want: AllocationUnknown},
		{name: "released_bad_observed_time", state: ReleaseConfirmed, age: time.Second, healthy: true, change: func(b *Binding) { b.Observation.ObservedAt = "invalid" }, want: AllocationUnknown},
		{name: "released_zero_observed_time", state: ReleaseConfirmed, age: time.Second, healthy: true, change: func(b *Binding) { b.Observation.ObservedAt = time.Time{}.Format(time.RFC3339Nano) }, want: AllocationUnknown},
		{name: "released_future_observed_time", state: ReleaseConfirmed, age: time.Second, healthy: true, change: func(b *Binding) { b.Observation.ObservedAt = now.Add(time.Minute).Format(time.RFC3339Nano) }, want: AllocationUnknown},
		{name: "released_bad_received_time", state: ReleaseConfirmed, age: time.Second, healthy: true, change: func(b *Binding) { b.Observation.ReceivedAt = "invalid" }, want: AllocationUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := queryBinding(test.state, now.Add(-test.age))
			if test.change != nil {
				test.change(&binding)
			}
			result := readBindingQuery(t, now, test.healthy, binding)
			if len(result.Items) != 1 || result.Items[0].State != test.want || result.Items[0].Observation == nil || result.Items[0].Observation.Complete != test.complete || result.Observation.Complete != test.complete {
				t.Fatalf("item/aggregate disagreed with historical fact: %+v aggregate=%+v", result.Items, result.Observation)
			}
			if test.want == ReleaseConfirmed && !reflect.DeepEqual(result.Items[0], binding) {
				t.Fatalf("historical release changed: before=%+v after=%+v", binding, result.Items[0])
			}
		})
	}
}

func TestBindingQueryAggregatesOldReleaseWithCurrentActive(t *testing.T) {
	now := time.Date(2026, 10, 16, 19, 16, 48, 0, time.UTC)
	released := queryBinding(ReleaseConfirmed, now.Add(-4*time.Minute))
	active := queryBinding(Active, now.Add(-time.Second))
	active.BindingID = "10000000-0000-4000-8000-000000000002"
	active.Pod = &PodRef{ClusterID: testID, Namespace: "test", Name: "replacement", UID: "pod-2", ContainerName: "inference"}
	result := readBindingQuery(t, now, true, released, active)
	if !result.Observation.Complete || len(result.Items) != 2 || result.Items[0].State != ReleaseConfirmed || result.Items[1].State != Active {
		t.Fatalf("mixed historical/current query lost certainty: %+v", result)
	}
	if result.Observation.ObservedAt != released.Observation.ObservedAt || result.Observation.ReceivedAt != released.Observation.ReceivedAt {
		t.Fatalf("aggregate fabricated current time for historical release: %+v", result.Observation)
	}
}

func TestBindingReleaseHistoryDoesNotRetireNewerClaims(t *testing.T) {
	now := time.Date(2026, 10, 16, 19, 16, 48, 0, time.UTC)
	baseline := baselineForTest(1, Shared)
	pod := PodRef{ClusterID: testID, Namespace: "test", Name: "workload", UID: "pod-1", ContainerName: "inference"}
	proof := &ReleaseProof{Pod: pod, DeviceID: testID, NodeUID: "node-1", ObservedAt: now.Add(-4 * time.Minute), ReceivedAt: now.Add(-4 * time.Minute), EvidenceRef: "release:old", RuntimeExited: true, AllocationAbsent: true, NodeOnline: true}
	var claims []Claim
	for _, source := range []string{"runtime", "allocation"} {
		claims = append(claims, Claim{Pod: pod, DeviceID: testID, NodeUID: "node-1", Source: source, State: Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: baseline.BaselineDigest, MemoryBlockMiB: 1, ObservedAt: now, ReceivedAt: now, EvidenceRef: source + ":new"})
	}
	binding, err := MergeBinding(testID, claims, proof, nil)
	if err != nil || binding.State != Active || !binding.Observation.Complete {
		t.Fatal(binding, err)
	}
	spec := testSpec(Shared)
	device := DeviceRecord{Device: PhysicalDevice{DeviceID: testID, Node: &NodeRef{UID: "node-1", Name: "node"}, ModelKey: spec.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: baseline.BaselineDigest}
	capacity, err := ComputeDeviceCapacity(device, *spec, *baseline, []Binding{*binding}, now, 120*time.Second, true)
	if err != nil || !capacity.Complete || capacity.KnownCommittedUnits != 1 || capacity.KnownFreeUnits != 3 {
		t.Fatal(capacity, err)
	}
}
