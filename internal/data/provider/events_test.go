package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"google.golang.org/grpc"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type continuousRuntime struct {
	*capabilityRuntime
	events chan *cri.ContainerEventResponse
}

func (r *continuousRuntime) GetContainerEvents(_ *cri.GetEventsRequest, stream cri.RuntimeService_GetContainerEventsServer) error {
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event := <-r.events:
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}

func runtimeEventFixture(id string, state cri.ContainerState, observed time.Time) *cri.ContainerEventResponse {
	status := &cri.ContainerStatus{Id: id, Metadata: &cri.ContainerMetadata{Name: "workspace"}, Labels: map[string]string{"io.kubernetes.pod.uid": "pod-uid", "io.kubernetes.pod.namespace": "tenant", "io.kubernetes.pod.name": "instance-0"}, State: state}
	if state == cri.ContainerState_CONTAINER_EXITED {
		status.FinishedAt = observed.Add(-time.Millisecond).UnixNano()
	}
	return &cri.ContainerEventResponse{ContainerId: id, ContainerEventType: cri.ContainerEventType_CONTAINER_STOPPED_EVENT, CreatedAt: observed.UnixNano(), PodSandboxStatus: &cri.PodSandboxStatus{Id: "sandbox", Metadata: &cri.PodSandboxMetadata{Uid: "pod-uid", Namespace: "tenant", Name: "instance-0"}}, ContainersStatuses: []*cri.ContainerStatus{status}}
}

func TestRuntimeEventsCaptureExitBeforeCRICollectionAndSurviveRestart(t *testing.T) {
	runtime := &continuousRuntime{capabilityRuntime: newCapabilityRuntime("2.3.4"), events: make(chan *cri.ContainerEventResponse, 4)}
	runtime.listed = nil // GC has removed every container before the poll.
	socket := unixServer(t, "events", func(server *grpc.Server) { cri.RegisterRuntimeServiceServer(server, runtime) })
	node := NodeSource{UID: "node-uid", Name: "ani-03", CRISocket: socket}
	directory := t.TempDir()
	provider := &Provider{runtimeEvents: newRuntimeEvents(directory)}
	if err := provider.runtimeEvents.start([]NodeSource{node}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { provider.Stop(context.Background()) })
	now := time.Now().UTC()
	runtime.events <- runtimeEventFixture("exited-container", cri.ContainerState_CONTAINER_CREATED, now.Add(-3*time.Millisecond))
	runtime.events <- runtimeEventFixture("exited-container", cri.ContainerState_CONTAINER_RUNNING, now.Add(-2*time.Millisecond))
	runtime.events <- runtimeEventFixture("exited-container", cri.ContainerState_CONTAINER_EXITED, now)
	deadline := time.Now().Add(3 * time.Second)
	for {
		provider.runtimeEvents.mu.Lock()
		exited := provider.runtimeEvents.nodes[runtimeNodeKey(node)].States["exited-container"].State == cri.ContainerState_CONTAINER_EXITED
		provider.runtimeEvents.mu.Unlock()
		if exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("continuous subscriber missed the short-lived positive exit")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	provider = &Provider{runtimeEvents: newRuntimeEvents(directory)}
	if err := provider.runtimeEvents.start([]NodeSource{node}); err != nil {
		t.Fatal(err)
	}
	reader, raw, _, _ := releaseFixture("scheduler")
	raw.Containers, raw.Statuses = nil, map[string]*cri.ContainerStatusResponse{}
	raw.ObservedAt = now.Add(time.Second)
	provider.repo = releaseRepository{reader: reader}
	snapshot := provider.runtimeEvents.releaseSnapshot(node, raw)
	proofs, err := provider.releaseProofs(context.Background(), "cluster", node, snapshot, true, raw.ObservedAt)
	if err != nil || len(proofs) != 1 {
		t.Fatalf("persisted exact exit was lost after GC/restart: proofs=%+v error=%v", proofs, err)
	}
	if len(raw.Containers) != 0 || len(raw.Statuses) != 0 {
		t.Fatal("release evidence leaked into live allocation observations")
	}
	info, err := os.Stat(filepath.Join(directory, runtimeNodeKey(node)+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("runtime evidence permissions: %v %v", info, err)
	}
}

func eventStoreFixture(t *testing.T, node NodeSource) *runtimeEvents {
	t.Helper()
	store := newRuntimeEvents(t.TempDir())
	store.nodes[runtimeNodeKey(node)] = runtimeEventFile{NodeUID: node.UID, NodeName: node.Name, Socket: node.CRISocket, States: map[string]runtimeContainerState{}}
	return store
}

func TestRuntimeEventsRetainEveryKnownAttemptUntilPositiveExit(t *testing.T) {
	reader, snapshot, node, _ := releaseFixture("scheduler")
	now := time.Now().UTC()
	snapshot.Containers, snapshot.Statuses = nil, map[string]*cri.ContainerStatusResponse{}
	snapshot.ObservedAt = now
	store := eventStoreFixture(t, node)
	store.observeEvent(node, runtimeEventFixture("old-attempt", cri.ContainerState_CONTAINER_EXITED, now.Add(-time.Second)), now)
	store.observeEvent(node, runtimeEventFixture("new-attempt", cri.ContainerState_CONTAINER_RUNNING, now), now)
	provider := &Provider{repo: releaseRepository{reader: reader}}
	proofs, err := provider.releaseProofs(context.Background(), "cluster", node, store.releaseSnapshot(node, snapshot), true, now)
	if err != nil || len(proofs) != 0 {
		t.Fatal("missing new attempt was treated as exited", proofs, err)
	}
	store.observeEvent(node, runtimeEventFixture("new-attempt", cri.ContainerState_CONTAINER_EXITED, now.Add(time.Second)), now.Add(time.Second))
	proofs, err = provider.releaseProofs(context.Background(), "cluster", node, store.releaseSnapshot(node, snapshot), true, now.Add(time.Second))
	if err != nil || len(proofs) != 1 {
		t.Fatal("all known attempts positively exited but occupancy remained", proofs, err)
	}
	unknown := runtimeEventFixture("unknown-attempt", cri.ContainerState_CONTAINER_CREATED, now.Add(2*time.Second))
	unknown.ContainerEventType = cri.ContainerEventType_CONTAINER_CREATED_EVENT
	unknown.ContainersStatuses = nil
	store.observeEvent(node, unknown, now.Add(2*time.Second))
	proofs, err = provider.releaseProofs(context.Background(), "cluster", node, store.releaseSnapshot(node, snapshot), true, now.Add(2*time.Second))
	if err != nil || len(proofs) != 0 {
		t.Fatal("created attempt with missing container identity was forgotten", proofs, err)
	}
}

func TestRuntimeEventsRejectIncompleteOrConflictingExit(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*cri.ContainerEventResponse)
	}{
		{name: "sandbox_only", change: func(e *cri.ContainerEventResponse) { e.ContainersStatuses = nil }},
		{name: "no_sandbox_identity", change: func(e *cri.ContainerEventResponse) { e.PodSandboxStatus.Metadata = nil }},
		{name: "pod_uid_conflict", change: func(e *cri.ContainerEventResponse) {
			e.ContainersStatuses[0].Labels["io.kubernetes.pod.uid"] = "other-pod"
		}},
		{name: "namespace_conflict", change: func(e *cri.ContainerEventResponse) {
			e.ContainersStatuses[0].Labels["io.kubernetes.pod.namespace"] = "other-tenant"
		}},
		{name: "no_container_name", change: func(e *cri.ContainerEventResponse) { e.ContainersStatuses[0].Metadata = nil }},
		{name: "no_real_exit_timestamp", change: func(e *cri.ContainerEventResponse) { e.ContainersStatuses[0].FinishedAt = 0 }},
		{name: "future_exit_timestamp", change: func(e *cri.ContainerEventResponse) { e.ContainersStatuses[0].FinishedAt += int64(time.Hour) }},
		{name: "unknown_state", change: func(e *cri.ContainerEventResponse) {
			e.ContainersStatuses[0].State = cri.ContainerState_CONTAINER_UNKNOWN
		}},
		{name: "duplicate_status", change: func(e *cri.ContainerEventResponse) {
			e.ContainersStatuses = append(e.ContainersStatuses, e.ContainersStatuses[0])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, snapshot, node, _ := releaseFixture("scheduler")
			snapshot.Containers, snapshot.Statuses = nil, map[string]*cri.ContainerStatusResponse{}
			now := time.Now().UTC()
			snapshot.ObservedAt = now
			store := eventStoreFixture(t, node)
			event := runtimeEventFixture("exited-container", cri.ContainerState_CONTAINER_EXITED, now)
			test.change(event)
			store.observeEvent(node, event, now)
			provider := &Provider{repo: releaseRepository{reader: reader}}
			proofs, err := provider.releaseProofs(context.Background(), "cluster", node, store.releaseSnapshot(node, snapshot), true, now)
			if err != nil || len(proofs) != 0 {
				t.Fatalf("incomplete exit became a release: %+v %v", proofs, err)
			}
		})
	}
}

func TestRuntimeEventsCurrentRunningAndFailedPersistenceBlockRelease(t *testing.T) {
	reader, snapshot, node, _ := releaseFixture("scheduler")
	now := time.Now().UTC()
	store := eventStoreFixture(t, node)
	store.observeEvent(node, runtimeEventFixture("exited-container", cri.ContainerState_CONTAINER_EXITED, now), now)
	snapshot.Statuses["exited-container"].Status.State = cri.ContainerState_CONTAINER_RUNNING
	provider := &Provider{repo: releaseRepository{reader: reader}}
	proofs, err := provider.releaseProofs(context.Background(), "cluster", node, store.releaseSnapshot(node, snapshot), true, now)
	if err != nil || len(proofs) != 0 {
		t.Fatal("historical exit replaced current running state", proofs, err)
	}
	store.directory = filepath.Join(t.TempDir(), "missing-directory")
	store.observeEvent(node, runtimeEventFixture("another-attempt", cri.ContainerState_CONTAINER_EXITED, now.Add(time.Second)), now.Add(time.Second))
	if store.releaseSnapshot(node, snapshot).RuntimeOK {
		t.Fatal("failed durable write left release source complete")
	}
}

func TestMergeJournalExitsCannotHideKnownOrCurrentAttempts(t *testing.T) {
	_, snapshot, _, _ := releaseFixture("scheduler")
	pod := biz.PodRef{UID: "pod-uid", ContainerName: "workspace", Namespace: "tenant", Name: "instance-0"}
	live := snapshot.Statuses["exited-container"]
	live.Status.State = cri.ContainerState_CONTAINER_RUNNING
	positive := runtimeEventFixture("exited-container", cri.ContainerState_CONTAINER_EXITED, time.Now().UTC()).ContainersStatuses[0]
	recovered := []*cri.ContainerStatusResponse{{Status: positive}}
	result := mergeJournalExits([]*cri.ContainerStatusResponse{live}, recovered, nil, pod)
	if len(result) != 1 || result[0].Status.State != cri.ContainerState_CONTAINER_RUNNING {
		t.Fatal("journal replaced a live running container")
	}
	result = mergeJournalExits([]*cri.ContainerStatusResponse{live}, recovered, map[string]bool{"exited-container": true}, pod)
	if len(result) != 1 || result[0].Status.State != cri.ContainerState_CONTAINER_EXITED {
		t.Fatal("authoritative same-ID exit failed to complete cached attempt")
	}
	missing := &cri.ContainerStatusResponse{Status: &cri.ContainerStatus{Id: "missing-attempt", State: cri.ContainerState_CONTAINER_UNKNOWN}}
	result = mergeJournalExits([]*cri.ContainerStatusResponse{live, missing}, recovered, map[string]bool{"exited-container": true}, pod)
	if len(result) != 2 || !needsJournalExit(result) {
		t.Fatal("journal omitted a previously observed attempt")
	}
}

func TestRuntimeEventsUnchangedSnapshotDoesNotRewriteDurableState(t *testing.T) {
	_, snapshot, node, _ := releaseFixture("scheduler")
	status := snapshot.Statuses["exited-container"].Status
	status.Labels["io.kubernetes.pod.namespace"] = "tenant"
	status.Labels["io.kubernetes.pod.name"] = "instance-0"
	snapshot.ObservedAt = time.Now().UTC()
	status.FinishedAt = snapshot.ObservedAt.Add(-time.Second).UnixNano()
	store := eventStoreFixture(t, node)
	store.observeSnapshot(node, snapshot)
	path := filepath.Join(store.directory, runtimeNodeKey(node)+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Unix(100, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	snapshot.ObservedAt = snapshot.ObservedAt.Add(time.Second)
	store.observeSnapshot(node, snapshot)
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatal("unchanged poll rewrote the durable event file", info, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("unchanged poll renewed historical state", err)
	}
}

func TestRuntimeEventsDirectoryHasOneWriter(t *testing.T) {
	directory := t.TempDir()
	first := &Provider{runtimeEvents: newRuntimeEvents(directory)}
	if err := first.runtimeEvents.start(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Stop(context.Background()) })
	second := &Provider{runtimeEvents: newRuntimeEvents(directory)}
	if err := second.runtimeEvents.start(nil); err == nil {
		t.Fatal("second process could overwrite the runtime event directory")
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.runtimeEvents.start(nil); err != nil {
		t.Fatal("stopped writer did not release its directory lock", err)
	}
	t.Cleanup(func() { second.Stop(context.Background()) })
}
