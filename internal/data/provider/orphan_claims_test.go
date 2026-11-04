package provider

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type orphanEvidenceReader struct {
	releaseReader
	proofs []biz.ReleaseProof
	reads  int
}

func (r *orphanEvidenceReader) PodEvidence(context.Context, biz.PodRef) ([]biz.Claim, []biz.ReleaseProof, error) {
	r.reads++
	return r.claims, r.proofs, nil
}

type orphanRepository struct {
	biz.Repository
	reader *orphanEvidenceReader
}

func (r orphanRepository) View(_ context.Context, read func(biz.Reader) error) error {
	return read(r.reader)
}

func retiredOrphanFixture(t *testing.T) (*Provider, *orphanEvidenceReader, nodeSnapshot, NodeSource, time.Time) {
	t.Helper()
	base, snapshot, node, now := releaseFixture("runtime")
	ref := *base.bindings[0].Pod
	ref.ClusterID, ref.UID = biz.NewID(), biz.NewID()
	device := PhysicalID(ref.ClusterID, node.UID, gpuUUID)
	base.bindings[0].Pod, base.bindings[0].PhysicalDeviceID = &ref, device
	base.claims[0].Pod, base.claims[0].DeviceID = ref, device
	base.claims[0].OwnerRef, base.claims[0].ExecutionDigest = nil, ""
	snapshot.Containers, snapshot.Statuses, snapshot.CaptureStart = nil, nil, now
	reader := &orphanEvidenceReader{releaseReader: base}
	adapter := &Provider{repo: orphanRepository{reader: reader}}
	proofs, claims, err := adapter.reconcileBindingsScoped(context.Background(), "test:orphan", ref.ClusterID, node, snapshot, true, now, nil, nil)
	if err != nil || len(proofs) != 1 || len(claims) != 0 {
		t.Fatalf("fixture did not establish current retirement: proofs=%+v claims=%+v error=%v", proofs, claims, err)
	}
	binding, err := biz.MergeBinding(base.bindings[0].BindingID, base.claims, &proofs[0], nil)
	if err != nil || binding.State != biz.ReleaseConfirmed {
		t.Fatalf("fixture binding was not retired: binding=%+v error=%v", binding, err)
	}
	reader.bindings, reader.proofs, reader.reads = []biz.Binding{*binding}, proofs, 0
	return adapter, reader, snapshot, node, now
}

func currentOrphanSnapshot(t *testing.T, node NodeSource, captured time.Time, attempt biz.RuntimeAttempt) nodeSnapshot {
	t.Helper()
	state := biz.NodeTelemetryState{
		Identity: biz.NodeIdentity{LogID: "orphan-log", NodeUID: node.UID, NodeName: node.Name},
		Epoch:    1, CurrentRevision: 7, LeaseExpiresAt: captured.Add(time.Minute),
		Snapshot: &biz.TelemetrySnapshot{CaptureEpoch: 1, CaptureStart: captured, CaptureEnd: captured, Sources: []biz.TelemetrySource{
			{Name: "cri", ObservedAt: captured, Complete: true},
			{Name: "podresources", ObservedAt: captured, Complete: true},
		}},
	}
	digest, _ := biz.Hash(attempt)
	history, err := biz.MergeNodeReport(&state, nil, biz.NodeReport{Epoch: 1, Sequence: 1, LogID: state.Identity.LogID, PayloadDigest: digest, Attempts: []biz.RuntimeAttempt{attempt}}, attempt.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	return telemetryNodeSnapshot(biz.NodeTelemetryView{State: state, Attempts: history}, attempt.ObservedAt, time.Minute)
}

func TestRetiredBindingCountsRuntimeReportedAfterPodAPIDeletion(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*biz.RuntimeAttempt)
	}{
		{name: "running"},
		{name: "created", change: func(a *biz.RuntimeAttempt) { a.State = 1 }},
		{name: "unknown", change: func(a *biz.RuntimeAttempt) { a.State = 4 }},
		{name: "GPU environment unavailable", change: func(a *biz.RuntimeAttempt) { a.GPUEnvironment = nil }},
		{name: "different GPU", change: func(a *biz.RuntimeAttempt) { a.GPUEnvironment[0].Value = gpuUUID2 }},
		{name: "container name unavailable", change: func(a *biz.RuntimeAttempt) { a.ContainerName = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, reader, _, node, retiredAt := retiredOrphanFixture(t)
			ref := *reader.bindings[0].Pod
			attempt := biz.RuntimeAttempt{ContainerID: "new-running", PodUID: ref.UID, PodNamespace: ref.Namespace, PodName: ref.Name, ContainerName: ref.ContainerName, State: 2, ObservedAt: retiredAt.Add(time.Second), GPUEnvironment: []biz.GPUEnvironmentEntry{
				{Name: "NVIDIA_VISIBLE_DEVICES", Value: gpuUUID},
				{Name: "CUDA_DEVICE_MEMORY_LIMIT_0", Value: "1024m"},
				{Name: "CUDA_DEVICE_SM_LIMIT", Value: "10"},
			}}
			if test.change != nil {
				test.change(&attempt)
			}
			snapshot := currentOrphanSnapshot(t, node, retiredAt, attempt)
			proofs, claims, err := adapter.reconcileBindingsScoped(context.Background(), "test:orphan", ref.ClusterID, node, snapshot, true, attempt.ObservedAt, nil, nil)
			if err != nil || len(proofs) != 0 || len(claims) != 1 || claims[0].Source != "runtime" || !claims[0].ObservedAt.Equal(attempt.ObservedAt) {
				t.Fatalf("post-retirement runtime was lost or retimed: proofs=%+v claims=%+v error=%v", proofs, claims, err)
			}
			if test.name != "running" && claims[0].State != biz.AllocationUnknown {
				t.Fatalf("incomplete current runtime became a confirmed allocation: %+v", claims)
			}
			binding, err := biz.MergeBinding(reader.bindings[0].BindingID, claims, &reader.proofs[0], nil)
			if err != nil || binding.State == biz.ReleaseConfirmed || binding.BindingID != reader.bindings[0].BindingID {
				t.Fatalf("accepted retirement hid newer occupancy: binding=%+v error=%v", binding, err)
			}
			claim := reader.claims[0]
			device := biz.DeviceRecord{Device: biz.PhysicalDevice{DeviceID: claim.DeviceID, Node: &biz.NodeRef{UID: node.UID, Name: node.Name}, ModelKey: "test", PhysicalMemoryBytes: 4096 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: attempt.ObservedAt.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 4096, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 4096, BaselineDigest: claim.BaselineDigest}
			capacity, err := biz.ComputeDeviceCapacity(device, biz.ProfileSpec{Mode: biz.Shared, ModelKey: "test", MemoryMiBPerDevice: 1024, CoreLimitPercent: 10}, biz.ProviderBaseline{MemoryBlockMiB: 1, SplitCount: 4, BaselineDigest: claim.BaselineDigest}, []biz.Binding{*binding}, attempt.ObservedAt, time.Minute, true)
			if err != nil || capacity.KnownCommittedUnits != 1 || capacity.Complete || capacity.KnownFreeUnits != 0 {
				t.Fatalf("orphan occupancy still exposed free capacity: %+v error=%v", capacity, err)
			}
			if !snapshot.RuntimeOK {
				t.Fatal("binding-local uncertainty invalidated the entire runtime source")
			}
		})
	}
}

func TestRetiredBindingSkipsHistoryAndReplacementAllocation(t *testing.T) {
	adapter, reader, snapshot, node, now := retiredOrphanFixture(t)
	ref := *reader.bindings[0].Pod
	check := func(pods []pod) {
		t.Helper()
		proofs, claims, err := adapter.reconcileBindingsScoped(context.Background(), "test:orphan", ref.ClusterID, node, snapshot, true, now, nil, pods)
		if err != nil || len(proofs) != 0 || len(claims) != 0 || reader.reads != 0 {
			t.Fatalf("idle retired binding loaded history or resumed occupancy: proofs=%+v claims=%+v reads=%d error=%v", proofs, claims, reader.reads, err)
		}
	}
	check(nil)
	replacement := pod{Metadata: metadata{UID: biz.NewID(), Namespace: ref.Namespace, Name: ref.Name}}
	replacement.Spec.NodeName, replacement.Status.Phase = node.Name, "Running"
	attempt := biz.RuntimeAttempt{ContainerID: "replacement", PodUID: replacement.Metadata.UID, PodNamespace: ref.Namespace, PodName: ref.Name, ContainerName: ref.ContainerName, State: 2, ObservedAt: now.Add(time.Second), GPUEnvironment: []biz.GPUEnvironmentEntry{{Name: "NVIDIA_VISIBLE_DEVICES", Value: gpuUUID}, {Name: "CUDA_DEVICE_MEMORY_LIMIT_0", Value: "1024m"}, {Name: "CUDA_DEVICE_SM_LIMIT", Value: "10"}}}
	snapshot = currentOrphanSnapshot(t, node, now, attempt)
	snapshot.Resources = &pr.ListPodResourcesResponse{PodResources: []*pr.PodResources{{Namespace: ref.Namespace, Name: ref.Name, Containers: []*pr.ContainerResources{{Name: ref.ContainerName, Devices: []*pr.ContainerDevices{{ResourceName: "volcano.sh/vgpu-number", DeviceIds: []string{"replacement-slot"}}}}}}}}
	check([]pod{replacement})
}

func TestOrphanTransitionDoesNotRewriteSameSourceObservation(t *testing.T) {
	adapter, reader, _, node, now := retiredOrphanFixture(t)
	ref := *reader.bindings[0].Pod
	attempt := biz.RuntimeAttempt{ContainerID: "same-observation", PodUID: ref.UID, PodNamespace: ref.Namespace, PodName: ref.Name, ContainerName: ref.ContainerName, State: 4, ObservedAt: now.Add(time.Second)}
	snapshot := currentOrphanSnapshot(t, node, now, attempt)
	reader.bindings[0].State = biz.AllocationUnknown
	reader.claims[0].ObservedAt = attempt.ObservedAt
	proofs, claims, err := adapter.reconcileBindingsScoped(context.Background(), "test:orphan", ref.ClusterID, node, snapshot, true, attempt.ObservedAt, nil, nil)
	if err != nil || len(proofs) != 0 || len(claims) != 0 {
		t.Fatalf("API absence rewrote an existing telemetry revision: proofs=%+v claims=%+v error=%v", proofs, claims, err)
	}
}
