package provider

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type nodeTelemetryFixture struct {
	mu        sync.Mutex
	store     biz.NodeTelemetryStore
	source    NodeSource
	inventory inventoryReader
	identity  biz.NodeIdentity
	state     biz.NodeTelemetryState
}

type fixedTelemetryInventory []Inventory

func (inventory fixedTelemetryInventory) Read(context.Context) ([]Inventory, error) {
	return append([]Inventory(nil), inventory...), nil
}

func newNodeTelemetryFixture(t *testing.T, store biz.NodeTelemetryStore, connection string, source NodeSource, inventory inventoryReader) *nodeTelemetryFixture {
	t.Helper()
	now := time.Now().UTC()
	identity := biz.NodeIdentity{ConnectionRef: connection, NodeUID: source.UID, NodeName: source.Name, PodUID: biz.NewID(), PodNamespace: "ani-node-system", PodName: "controlled-agent", ServiceAccount: "ani-node-agent", DaemonSetUID: biz.NewID(), InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), TokenExpiresAt: now.Add(time.Minute)}
	state, err := store.OpenNodeSession(context.Background(), identity, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return &nodeTelemetryFixture{store: store, source: source, inventory: inventory, identity: identity, state: state}
}

func (fixture *nodeTelemetryFixture) refresh(t *testing.T) {
	t.Helper()
	if err := fixture.refreshReport(); err != nil {
		t.Fatal(err)
	}
}

func (fixture *nodeTelemetryFixture) refreshReport() error {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	ctx := context.Background()
	start := time.Now().UTC()
	if !start.Before(fixture.state.LeaseExpiresAt.Add(-5 * time.Second)) {
		fixture.identity.TokenExpiresAt = start.Add(time.Minute)
		state, err := fixture.store.RenewNodeSession(ctx, fixture.identity, fixture.state.Epoch, start.Add(30*time.Second))
		if err != nil {
			return err
		}
		fixture.state = state
	}
	local := readNode(ctx, fixture.source)
	devices, inventoryError := fixture.inventory.Read(ctx)
	end := time.Now().UTC()
	snapshot := &biz.TelemetrySnapshot{CaptureEpoch: fixture.state.Epoch, CaptureStart: start, CaptureEnd: end}
	for _, source := range []struct {
		name     string
		complete bool
	}{{"nvml", inventoryError == nil}, {"podresources", local.ResourcesOK}, {"cri", local.RuntimeOK}} {
		health := biz.TelemetrySource{Name: source.name, ObservedAt: end, Complete: source.complete}
		if !source.complete {
			health.Issues = []string{"CONTROLLED_SOURCE_UNAVAILABLE"}
		}
		snapshot.Sources = append(snapshot.Sources, health)
	}
	if inventoryError == nil {
		for _, device := range devices {
			snapshot.Devices = append(snapshot.Devices, biz.TelemetryDevice{UUID: device.UUID, Model: device.Model, PhysicalMemoryBytes: device.Bytes})
		}
	}
	if local.Resources != nil {
		for _, pod := range local.Resources.PodResources {
			for _, container := range pod.Containers {
				for _, resource := range container.Devices {
					snapshot.Allocations = append(snapshot.Allocations, biz.TelemetryAllocation{Namespace: pod.Namespace, PodName: pod.Name, ContainerName: container.Name, ResourceName: resource.ResourceName, DeviceIDs: append([]string(nil), resource.DeviceIds...)})
				}
			}
		}
	}
	for _, container := range local.Containers {
		status := local.Statuses[container.Id]
		if status == nil || status.Status == nil {
			continue
		}
		attempt := biz.RuntimeAttempt{ContainerID: container.Id, SandboxID: container.PodSandboxId, PodUID: status.Status.Labels["io.kubernetes.pod.uid"], PodNamespace: status.Status.Labels["io.kubernetes.pod.namespace"], PodName: status.Status.Labels["io.kubernetes.pod.name"], ObservedAt: end}
		if status.Status.Metadata != nil {
			attempt.ContainerName = status.Status.Metadata.Name
			attempt.Attempt = status.Status.Metadata.Attempt
		}
		switch status.Status.State {
		case cri.ContainerState_CONTAINER_CREATED:
			attempt.State = 1
		case cri.ContainerState_CONTAINER_RUNNING:
			attempt.State = 2
		case cri.ContainerState_CONTAINER_EXITED:
			attempt.State = 3
		default:
			attempt.State = 4
		}
		if status.Status.CreatedAt > 0 {
			attempt.CreatedAt = time.Unix(0, status.Status.CreatedAt).UTC()
		}
		if status.Status.StartedAt > 0 {
			attempt.StartedAt = time.Unix(0, status.Status.StartedAt).UTC()
		}
		if status.Status.FinishedAt > 0 {
			attempt.FinishedAt = time.Unix(0, status.Status.FinishedAt).UTC()
		}
		var verbose struct {
			RuntimeSpec struct {
				Process struct {
					Environment []string `json:"env"`
				} `json:"process"`
			} `json:"runtimeSpec"`
		}
		if json.Unmarshal([]byte(status.Info["info"]), &verbose) == nil {
			for _, entry := range verbose.RuntimeSpec.Process.Environment {
				name, value, ok := strings.Cut(entry, "=")
				if ok && (name == "NVIDIA_VISIBLE_DEVICES" || name == "CUDA_DEVICE_SM_LIMIT" || strings.HasPrefix(name, "CUDA_DEVICE_MEMORY_LIMIT")) {
					attempt.GPUEnvironment = append(attempt.GPUEnvironment, biz.GPUEnvironmentEntry{Name: name, Value: value})
				}
			}
		}
		snapshot.Containers = append(snapshot.Containers, attempt)
	}
	report := biz.NodeReport{Epoch: fixture.state.Epoch, Sequence: fixture.state.LastSequence + 1, LogID: fixture.identity.LogID, OriginPodUID: fixture.identity.PodUID, OriginInstanceID: fixture.identity.InstanceID, OriginBootID: fixture.identity.BootID, Snapshot: snapshot}
	digest, err := biz.Hash(snapshot)
	if err != nil {
		return err
	}
	report.PayloadDigest = digest
	fixture.state, err = fixture.store.SaveNodeReport(ctx, fixture.identity.ConnectionRef, fixture.identity.NodeUID, report, end)
	return err
}

func TestProviderContinuousNodeTelemetryPreservesReleaseRevision(t *testing.T) {
	providerControlledSources(t, "v1.16.0", "revision_continuity")
}

func TestProviderNewSessionWithoutFreshSnapshotCannotFreeOrReleaseGPU(t *testing.T) {
	providerControlledSources(t, "v1.16.0", "session_cache")
}

func providerSessionCacheContract(t *testing.T, database *data.Postgres, adapter *Provider, cluster biz.Cluster, group biz.SupplyGroup, usecase *biz.Usecase, owner context.Context, ref *biz.UsageRef, pod biz.PodRef, runtime *replayState, fixture *nodeTelemetryFixture, capacity func() *biz.CapacityView) {
	t.Helper()
	ctx := context.Background()
	bindings := func() string {
		t.Helper()
		var digest string
		if err := database.View(ctx, func(reader biz.Reader) error {
			rows, err := reader.Bindings(ctx, biz.BindingFilter{ClusterID: cluster.ClusterID, Pod: &pod}, "", 201)
			if err != nil {
				return err
			}
			digest, err = biz.Hash(rows)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return digest
	}
	before := bindings()
	previousSources := map[string]biz.SourceWatermark{}
	if err := database.View(ctx, func(reader biz.Reader) error {
		rows, err := reader.SourceHealth(ctx, cluster.ClusterID)
		for _, source := range rows {
			previousSources[source.Source] = source
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var err error
	fixture.state, err = database.OpenNodeSession(ctx, fixture.identity, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Inspect(ctx, cluster, group); err == nil || err.Error() != "NVML_UNAVAILABLE" {
		t.Fatalf("new session reused old snapshot as live inventory: %v", err)
	}
	if bindings() != before {
		t.Fatal("opening a new telemetry session changed business bindings")
	}
	runtime.mu.Lock()
	runtime.exited, runtime.absent, runtime.apiDeleted = true, true, true
	runtime.mu.Unlock()
	batch, err := adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Releases) != 0 {
		t.Fatal("empty snapshot cache became positive release evidence")
	}
	for _, source := range batch.Watermarks {
		if source.Source != "kubernetes" && (source.Complete || !source.ObservedAt.IsZero() || source.ReceivedAt.IsZero() || source.Issue != "SOURCE_UNKNOWN") {
			t.Fatal("missing current-epoch snapshot became a fresh node observation")
		}
	}
	if err = database.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
		t.Fatal(err)
	}
	if err = database.View(ctx, func(reader biz.Reader) error {
		rows, err := reader.SourceHealth(ctx, cluster.ClusterID)
		for _, source := range rows {
			if source.Source != "kubernetes" && (source.Complete || !source.ObservedAt.Equal(previousSources[source.Source].ObservedAt) || source.Issue != "SOURCE_UNKNOWN") {
				t.Fatal("failure reception repackaged the previous complete source as fresh")
			}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	view := capacity()
	if view.Observation.Complete || view.KnownFreeUnits != 0 || view.KnownCommittedUnits != 1 {
		t.Fatal("cache absence exposed free capacity or forgot committed occupancy")
	}
	observed, err := usecase.ObserveRelease(owner, "new-session-release", ref, biz.NewID(), cluster.ClusterID, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), []biz.PodRef{pod})
	if err != nil || observed.Finding != biz.ReleaseUnknown || observed.Observation.Complete {
		t.Fatalf("empty cache released owner occupancy: result=%+v error=%v", observed, err)
	}
	fixture.refresh(t)
	if _, err = adapter.Inspect(ctx, cluster, group); err != nil {
		t.Fatalf("current epoch complete snapshot did not recover inventory: %v", err)
	}
	batch, err = adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
	if err != nil || len(batch.Releases) != 1 {
		t.Fatalf("fresh actual exit and allocation absence did not recover release proof: releases=%d error=%v", len(batch.Releases), err)
	}
}

func providerRevisionContinuity(t *testing.T, database *data.Postgres, adapter *Provider, cluster biz.Cluster, group biz.SupplyGroup, owner *biz.UsageRef, pod biz.PodRef, state *replayState, fixtures []*nodeTelemetryFixture) {
	t.Helper()
	ctx := context.Background()
	seal := time.Now().Add(-time.Second).UTC()
	reportContext, stopReports := context.WithCancel(ctx)
	reportsDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			for _, fixture := range fixtures {
				if err := fixture.refreshReport(); err != nil {
					reportsDone <- err
					return
				}
			}
			select {
			case <-reportContext.Done():
				reportsDone <- nil
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() {
		stopReports()
		if err := <-reportsDone; err != nil {
			t.Error(err)
		}
	}()
	store := func(batch biz.ObservationBatch) error {
		return database.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) })
	}
	for iteration := 0; iteration < 3; iteration++ {
		batch, err := adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
		if err != nil {
			t.Fatal(err)
		}
		if len(batch.NodeRevisions) != 2 {
			t.Fatalf("collection omitted a reporting node: %+v", batch.NodeRevisions)
		}
		for _, fixture := range fixtures {
			fixture.refresh(t)
		}
		if err = store(batch); err != nil {
			t.Fatalf("unchanged continuous reports prevented atomic collection: %v", err)
		}
	}
	state.mu.Lock()
	state.exited, state.absent, state.apiDeleted = true, true, true
	state.mu.Unlock()
	for _, fixture := range fixtures {
		fixture.refresh(t)
	}
	oldProof, err := adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
	if err != nil {
		t.Fatal(err)
	}
	if len(oldProof.Releases) == 0 {
		t.Fatal("legitimate exit and allocation absence produced no proof")
	}
	if err = store(oldProof); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.exited, state.absent, state.apiDeleted = false, false, false
	state.runtimeID = "cri-container-second"
	state.finishedAt = time.Time{}
	state.mu.Unlock()
	fixtures[0].refresh(t)
	if err = store(oldProof); err == nil || err.Error() != "NODE_REVISION_CONFLICT" {
		t.Fatalf("old release batch survived a new Running fact: %v", err)
	}
	codec, err := biz.NewCursorCodec([]byte(strings.Repeat("r", 32)))
	if err != nil {
		t.Fatal(err)
	}
	usecase, err := biz.NewUsecase(database, adapter, codec, []string{cluster.ConnectionRef}, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observe := func() *biz.ReleaseObservation {
		t.Helper()
		ownerContext := biz.WithCaller(ctx, biz.Caller{Service: owner.OwnerService, Action: "ObserveRelease", TenantID: owner.TenantID})
		callContext, cancel := context.WithTimeout(ownerContext, 5*time.Second)
		defer cancel()
		result, err := usecase.ObserveRelease(callContext, "continuous-node-revision", owner, biz.NewID(), cluster.ClusterID, seal.Format(time.RFC3339Nano), []biz.PodRef{pod})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := observe(); result.Finding == biz.AllocationReleased {
		t.Fatal("new Running reused an old persisted release proof")
	}
	state.mu.Lock()
	state.runtimeUnknown = true
	state.mu.Unlock()
	fixtures[0].refresh(t)
	if result := observe(); result.Finding == biz.AllocationReleased {
		t.Fatal("new UNKNOWN reused an old persisted release proof")
	}
	state.mu.Lock()
	state.runtimeUnknown = false
	state.exited, state.absent, state.apiDeleted = true, true, true
	state.finishedAt = time.Time{}
	state.mu.Unlock()
	for _, fixture := range fixtures {
		fixture.refresh(t)
	}
	if result := observe(); result.Finding != biz.AllocationReleased || !result.Observation.Complete {
		t.Fatalf("legitimate cleanup did not converge within Resource's five-second budget: %+v", result)
	}
	views, err := database.NodeTelemetry(ctx, cluster.ConnectionRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatal("continuous node sessions lost identity scope")
	}
	for _, view := range views {
		if view.State.ConsumedRevision != view.State.CurrentRevision || view.State.LastSequence < 4 {
			t.Fatalf("node reports were not durably consumed: %+v", view.State)
		}
	}
}
