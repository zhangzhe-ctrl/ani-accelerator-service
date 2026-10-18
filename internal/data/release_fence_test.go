package data

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresResourceReleaseOwnerWideFence(t *testing.T) {
	f := pgSetup(t)
	ref := &biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-resource-service", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	ctx := biz.WithCaller(context.Background(), biz.Caller{Service: ref.OwnerService, Action: "ObserveRelease", TenantID: ref.TenantID})
	seal := time.Now().UTC().Add(-time.Second)
	now := time.Now().UTC()
	node := f.group.Nodes[0]
	store := func(batch biz.ObservationBatch) {
		t.Helper()
		if err := f.db.Transact(context.Background(), func(tx biz.Transaction) error { return tx.SaveObservation(context.Background(), batch) }); err != nil {
			t.Fatal(err)
		}
	}
	watermarks := func(observed time.Time, complete bool) []biz.SourceWatermark {
		var result []biz.SourceWatermark
		for _, source := range []string{"kubernetes", "podresources", "cri"} {
			result = append(result, biz.SourceWatermark{ClusterID: f.cluster.ClusterID, NodeUID: node.UID, Source: source, Cursor: biz.NewID(), ObservedAt: observed, ReceivedAt: time.Now().UTC(), Complete: complete})
		}
		return result
	}
	observe := func(want biz.ReleaseFinding) *biz.ReleaseObservation {
		t.Helper()
		result, err := f.u.ObserveRelease(ctx, "resource-fence", ref, biz.NewID(), f.cluster.ClusterID, seal.Format(time.RFC3339Nano), nil)
		if err != nil || result.Finding != want {
			t.Fatalf("want=%v result=%+v err=%v", want, result, err)
		}
		return result
	}
	// No API Pod/claim does not mean empty if the global sources were not read.
	observe(biz.ReleaseUnknown)
	store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Watermarks: watermarks(seal.Add(-time.Second), true)})
	observe(biz.ReleaseUnknown)
	store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Watermarks: watermarks(now, true)})
	if result := observe(biz.AllocationReleased); !result.Observation.Complete || len(result.Observation.Issues) != 0 || len(result.EvidenceDigest) != 64 {
		t.Fatal(result)
	}
	store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Watermarks: watermarks(now, false)})
	observe(biz.ReleaseUnknown)
	store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Watermarks: watermarks(time.Now().UTC(), true)})
	device := biz.NewID()
	pod := biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "short-lived", UID: "pod-" + biz.NewID(), ContainerName: "workspace"}
	record := biz.DeviceRecord{Device: biz.PhysicalDevice{DeviceID: device, ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID, Node: &node, VendorDeviceID: "GPU-fence", ModelKey: f.group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest}
	claim := biz.Claim{Pod: pod, DeviceID: device, NodeUID: node.UID, Source: "runtime", ObservedAt: now, ReceivedAt: now, State: biz.Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: f.group.Baseline.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "cri:real-boundary", ExecutionDigest: ""}
	store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Devices: []biz.DeviceRecord{record}, Claims: []biz.Claim{claim}})
	// An unsigned allocation cannot be filtered out just because OwnerRef is nil.
	observe(biz.ReleaseUnknown)
	claim.OwnerRef, claim.ExecutionDigest = ref, f.projection(1).ExecutionDigest
	claim.ObservedAt, claim.ReceivedAt = now.Add(time.Millisecond), time.Now().UTC()
	var signedClaims []biz.Claim
	for _, source := range []string{"scheduler", "allocation", "runtime"} {
		claim.Source = source
		signedClaims = append(signedClaims, claim)
	}
	store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Claims: signedClaims})
	// The request Pod list is still empty: ACC must discover this owner claim.
	observe(biz.StillPresent)
	proof := biz.ReleaseProof{Pod: pod, DeviceID: device, NodeUID: node.UID, ObservedAt: time.Now().UTC().Add(time.Second), ReceivedAt: time.Now().UTC(), EvidenceRef: "release:positive-source", RuntimeExited: true, AllocationAbsent: true, NodeOnline: true, OwnerRef: ref, ExecutionDigest: claim.ExecutionDigest}
	store(biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Releases: []biz.ReleaseProof{proof}})
	observe(biz.AllocationReleased)
	wrong := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-inference", Action: "ObserveRelease", TenantID: ref.TenantID})
	if _, err := f.u.ObserveRelease(wrong, "resource-fence", ref, biz.NewID(), f.cluster.ClusterID, seal.Format(time.RFC3339Nano), nil); err == nil {
		t.Fatal("foreign owner accepted")
	}
	other := pgSetup(t)
	if _, err := f.u.ObserveRelease(ctx, "resource-fence", ref, biz.NewID(), other.cluster.ClusterID, seal.Format(time.RFC3339Nano), nil); err == nil {
		t.Fatal("owner claims in another cluster omitted")
	}
}

func TestPostgresResourceReleaseDurablePositiveFactsHaveNoExpiry(t *testing.T) {
	f := pgSetup(t)
	ref := &biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-resource-service", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	now, old := time.Now().UTC(), time.Now().UTC().Add(-10*time.Minute)
	node, device := f.group.Nodes[0], biz.NewID()
	pod := biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "already-exited", UID: "pod-" + biz.NewID(), ContainerName: "workspace"}
	digest := f.projection(1).ExecutionDigest
	batch := biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Devices: []biz.DeviceRecord{{Device: biz.PhysicalDevice{DeviceID: device, ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID, Node: &node, VendorDeviceID: "GPU-delayed-release", ModelKey: f.group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest}}}
	for _, source := range []string{"scheduler", "allocation", "runtime"} {
		batch.Claims = append(batch.Claims, biz.Claim{Pod: pod, DeviceID: device, NodeUID: node.UID, Source: source, ObservedAt: old, ReceivedAt: old, State: biz.Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: f.group.Baseline.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "historical:" + source, OwnerRef: ref, ExecutionDigest: digest})
	}
	for _, source := range []string{"kubernetes", "podresources", "cri"} {
		batch.Watermarks = append(batch.Watermarks, biz.SourceWatermark{ClusterID: f.cluster.ClusterID, NodeUID: node.UID, Source: source, ObservedAt: now, ReceivedAt: now, Complete: true})
	}
	batch.Releases = []biz.ReleaseProof{{Pod: pod, DeviceID: device, NodeUID: node.UID, ObservedAt: old.Add(time.Second), ReceivedAt: old.Add(time.Second), EvidenceRef: "persisted:positive-exit", RuntimeExited: true, AllocationAbsent: true, NodeOnline: true, OwnerRef: ref, ExecutionDigest: digest}}
	if err := f.db.Transact(context.Background(), func(tx biz.Transaction) error { return tx.SaveObservation(context.Background(), batch) }); err != nil {
		t.Fatal(err)
	}
	ctx := biz.WithCaller(context.Background(), biz.Caller{Service: ref.OwnerService, Action: "ObserveRelease", TenantID: ref.TenantID})
	result, err := f.u.ObserveRelease(ctx, "delayed-resource-delete", ref, biz.NewID(), f.cluster.ClusterID, now.Add(-time.Second).Format(time.RFC3339Nano), nil)
	if err != nil || result.Finding != biz.AllocationReleased || !result.Observation.Complete {
		t.Fatal(result, err)
	}
}
