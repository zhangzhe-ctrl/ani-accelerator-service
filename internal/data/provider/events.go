package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Runtime events contain no verbose GPU allocation. Keep their actual container
// identity/state separately; they may only supplement positive release evidence.
type runtimeContainerState struct {
	ID, PodUID, Namespace, PodName, ContainerName string
	State                                         cri.ContainerState
	FinishedAt, ObservedAt                        int64
}

type runtimeEventFile struct {
	NodeUID, NodeName, Socket string
	States                    map[string]runtimeContainerState
}

type runtimeEvents struct {
	mu        sync.Mutex
	directory string
	nodes     map[string]runtimeEventFile
	failed    map[string]bool
	dirty     map[string]bool
	cancel    context.CancelFunc
	lock      *os.File
	wg        sync.WaitGroup
}

func runtimeNodeKey(n NodeSource) string {
	h := sha256.Sum256([]byte(n.UID + "\x00" + n.CRISocket))
	return hex.EncodeToString(h[:])
}

func newRuntimeEvents(directory string) *runtimeEvents {
	return &runtimeEvents{directory: directory, nodes: map[string]runtimeEventFile{}, failed: map[string]bool{}, dirty: map[string]bool{}}
}

func (p *Provider) Start(context.Context) error {
	if p.runtimeEvents == nil {
		return nil
	}
	var nodes []NodeSource
	seen := map[string]bool{}
	for _, connection := range p.connections {
		for _, node := range connection.config.Nodes {
			key := runtimeNodeKey(node)
			if !seen[key] {
				seen[key] = true
				nodes = append(nodes, node)
			}
		}
	}
	return p.runtimeEvents.start(nodes)
}

func (p *Provider) Stop(ctx context.Context) error {
	if p.runtimeEvents == nil {
		return nil
	}
	p.runtimeEvents.mu.Lock()
	cancel := p.runtimeEvents.cancel
	p.runtimeEvents.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		p.runtimeEvents.wg.Wait()
		p.runtimeEvents.mu.Lock()
		if p.runtimeEvents.lock != nil {
			syscall.Flock(int(p.runtimeEvents.lock.Fd()), syscall.LOCK_UN)
			p.runtimeEvents.lock.Close()
			p.runtimeEvents.lock = nil
		}
		p.runtimeEvents.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *runtimeEvents) start(nodes []NodeSource) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		return nil
	}
	if err := os.MkdirAll(e.directory, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(e.directory, ".writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return biz.Precondition("RUNTIME_EVENT_STORE_IN_USE")
	}
	ready := false
	defer func() {
		if !ready {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			lock.Close()
		}
	}()
	for _, node := range nodes {
		key := runtimeNodeKey(node)
		record := runtimeEventFile{NodeUID: node.UID, NodeName: node.Name, Socket: node.CRISocket, States: map[string]runtimeContainerState{}}
		file, err := os.Open(filepath.Join(e.directory, key+".json"))
		if err == nil {
			err = decodeStrict(file, &record)
			file.Close()
			if err != nil || record.NodeUID != node.UID || record.NodeName != node.Name || record.Socket != node.CRISocket || record.States == nil || len(record.States) > 65536 {
				return biz.Precondition("RUNTIME_EVENT_STORE_INVALID")
			}
			for id, state := range record.States {
				if id != state.ID || !validRuntimeState(state, time.Now().UTC()) {
					return biz.Precondition("RUNTIME_EVENT_STORE_INVALID")
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		e.nodes[key] = record
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.lock = lock
	ready = true
	for _, node := range nodes {
		e.wg.Add(1)
		go func() { defer e.wg.Done(); e.watch(ctx, node) }()
	}
	return nil
}

func (e *runtimeEvents) watch(ctx context.Context, node NodeSource) {
	for ctx.Err() == nil {
		conn, err := unixClient(node.CRISocket)
		if err == nil {
			stream, err := cri.NewRuntimeServiceClient(conn).GetContainerEvents(ctx, &cri.GetEventsRequest{})
			if err == nil {
				for {
					event, err := stream.Recv()
					if err != nil {
						break
					}
					e.observeEvent(node, event, time.Now().UTC())
				}
			}
			conn.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func validRuntimeState(s runtimeContainerState, now time.Time) bool {
	nameKnown := biz.Name(s.ContainerName) || s.ContainerName == "" && s.State == cri.ContainerState_CONTAINER_UNKNOWN && s.FinishedAt == 0
	return biz.Ref(s.ID) && biz.Ref(s.PodUID) && biz.Name(s.Namespace) && biz.Name(s.PodName) && nameKnown && s.ObservedAt > 0 && s.ObservedAt <= now.Add(5*time.Second).UnixNano() && s.State >= cri.ContainerState_CONTAINER_CREATED && s.State <= cri.ContainerState_CONTAINER_UNKNOWN && (s.State != cri.ContainerState_CONTAINER_EXITED || s.FinishedAt > 0 && s.FinishedAt <= s.ObservedAt+int64(5*time.Second))
}

func (e *runtimeEvents) observeEvent(node NodeSource, event *cri.ContainerEventResponse, now time.Time) {
	if event == nil || event.ContainerId == "" || event.PodSandboxStatus == nil || event.PodSandboxStatus.Id == "" || event.PodSandboxStatus.Metadata == nil || event.CreatedAt <= 0 || event.CreatedAt > now.Add(5*time.Second).UnixNano() {
		return
	}
	pod := event.PodSandboxStatus.Metadata
	var states []runtimeContainerState
	seen := map[string]bool{}
	for _, status := range event.ContainersStatuses {
		if status == nil || status.Metadata == nil || status.Labels["io.kubernetes.pod.uid"] != pod.Uid || status.Labels["io.kubernetes.pod.namespace"] != pod.Namespace || status.Labels["io.kubernetes.pod.name"] != pod.Name {
			return
		}
		state := runtimeContainerState{ID: status.Id, PodUID: pod.Uid, Namespace: pod.Namespace, PodName: pod.Name, ContainerName: status.Metadata.Name, State: status.State, FinishedAt: status.FinishedAt, ObservedAt: event.CreatedAt}
		if !validRuntimeState(state, now) || seen[state.ID] {
			return
		}
		seen[state.ID] = true
		states = append(states, state)
	}
	if !seen[event.ContainerId] && event.ContainerId != event.PodSandboxStatus.Id && (event.ContainerEventType == cri.ContainerEventType_CONTAINER_CREATED_EVENT || event.ContainerEventType == cri.ContainerEventType_CONTAINER_STARTED_EVENT) {
		// A new attempt with missing status metadata is known to exist, even
		// though it cannot yet be mapped to one of the Pod's containers.
		state := runtimeContainerState{ID: event.ContainerId, PodUID: pod.Uid, Namespace: pod.Namespace, PodName: pod.Name, State: cri.ContainerState_CONTAINER_UNKNOWN, ObservedAt: event.CreatedAt}
		if !validRuntimeState(state, now) {
			return
		}
		states = append(states, state)
	}
	e.save(node, states)
}

func (e *runtimeEvents) observeSnapshot(node NodeSource, snapshot nodeSnapshot) {
	if !snapshot.RuntimeOK {
		return
	}
	var states []runtimeContainerState
	for _, response := range snapshot.Statuses {
		status := response.Status
		if status == nil || status.Metadata == nil {
			continue
		}
		state := runtimeContainerState{ID: status.Id, PodUID: status.Labels["io.kubernetes.pod.uid"], Namespace: status.Labels["io.kubernetes.pod.namespace"], PodName: status.Labels["io.kubernetes.pod.name"], ContainerName: status.Metadata.Name, State: status.State, FinishedAt: status.FinishedAt, ObservedAt: snapshot.ObservedAt.UnixNano()}
		if validRuntimeState(state, snapshot.ObservedAt) {
			states = append(states, state)
		}
	}
	e.save(node, states)
}

func (e *runtimeEvents) save(node NodeSource, states []runtimeContainerState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := runtimeNodeKey(node)
	record, exists := e.nodes[key]
	if !exists {
		return
	}
	for _, state := range states {
		old, exists := record.States[state.ID]
		if exists && state.ContainerName == "" {
			state.ContainerName = old.ContainerName
		}
		if exists && (old.PodUID != state.PodUID || old.ContainerName != "" && old.ContainerName != state.ContainerName || old.Namespace != state.Namespace || old.PodName != state.PodName) || len(record.States) >= 65536 && !exists {
			e.failed[key] = true
			return
		}
		if exists && (old.ObservedAt >= state.ObservedAt || old.State == state.State && old.FinishedAt == state.FinishedAt) {
			continue
		}
		record.States[state.ID] = state
		e.dirty[key] = true
	}
	if !e.dirty[key] {
		return
	}
	e.nodes[key] = record
	err := writeRuntimeEvents(e.directory, key, record)
	e.failed[key] = err != nil
	if err == nil {
		e.dirty[key] = false
	}
}

func writeRuntimeEvents(directory, key string, record runtimeEventFile) error {
	file, err := os.CreateTemp(directory, ".runtime-events-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = json.NewEncoder(file).Encode(record); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(directory, key+".json"))
	}
	if err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (e *runtimeEvents) releaseSnapshot(node NodeSource, snapshot nodeSnapshot) nodeSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := runtimeNodeKey(node)
	if e.failed[key] {
		snapshot.RuntimeOK = false
		return snapshot
	}
	statuses := make(map[string]*cri.ContainerStatusResponse, len(snapshot.Statuses))
	for id, status := range snapshot.Statuses {
		statuses[id] = status
	}
	snapshot.Statuses = statuses
	snapshot.HistoricalStatuses = map[string]bool{}
	snapshot.UnknownPodContainers = map[string][]string{}
	snapshot.Containers = append([]*cri.Container(nil), snapshot.Containers...)
	for id, state := range e.nodes[key].States {
		if _, exists := statuses[id]; exists {
			continue // A currently observed running/unknown attempt always blocks.
		}
		if state.ContainerName == "" {
			snapshot.UnknownPodContainers[state.PodUID] = append(snapshot.UnknownPodContainers[state.PodUID], id)
			continue
		}
		labels := map[string]string{"io.kubernetes.pod.uid": state.PodUID, "io.kubernetes.pod.namespace": state.Namespace, "io.kubernetes.pod.name": state.PodName}
		snapshot.Containers = append(snapshot.Containers, &cri.Container{Id: id, Metadata: &cri.ContainerMetadata{Name: state.ContainerName}, Labels: labels})
		statuses[id] = &cri.ContainerStatusResponse{Status: &cri.ContainerStatus{Id: id, Metadata: &cri.ContainerMetadata{Name: state.ContainerName}, Labels: labels, State: state.State, FinishedAt: state.FinishedAt}}
		snapshot.HistoricalStatuses[id] = true
	}
	return snapshot
}
