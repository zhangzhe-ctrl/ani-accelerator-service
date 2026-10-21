package provider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type NodeExitRecovery interface {
	RecoverNodeExit(context.Context, string, string, biz.PodRef, []string) ([]biz.RuntimeAttempt, bool, error)
}

func (p *Provider) ConfigureNodeExitRecovery(recovery NodeExitRecovery) { p.exitRecovery = recovery }

func (p *Provider) loadTelemetry(ctx context.Context, connection string) (map[string]biz.NodeTelemetryView, error) {
	if p.telemetry == nil {
		return nil, biz.Precondition("NODE_TELEMETRY_REQUIRED")
	}
	views, err := p.telemetry.NodeTelemetry(ctx, connection)
	if err != nil {
		return nil, err
	}
	result := make(map[string]biz.NodeTelemetryView, len(views))
	for _, view := range views {
		result[view.State.Identity.NodeUID] = view
	}
	return result, nil
}

func telemetrySource(view biz.NodeTelemetryView, name string, now time.Time, freshness time.Duration) (biz.TelemetrySource, bool) {
	if view.State.Revoked || view.State.Degraded || !now.Before(view.State.LeaseExpiresAt) || view.State.Snapshot == nil {
		return biz.TelemetrySource{}, false
	}
	for _, source := range view.State.Snapshot.Sources {
		if source.Name == name {
			return source, source.Complete && sourceFresh(now, source.ObservedAt, freshness)
		}
	}
	return biz.TelemetrySource{}, false
}

func telemetryInventory(view biz.NodeTelemetryView, now time.Time, freshness time.Duration) ([]Inventory, error) {
	if _, complete := telemetrySource(view, "nvml", now, freshness); !complete {
		return nil, biz.Precondition("NVML_UNAVAILABLE")
	}
	devices := make([]Inventory, 0, len(view.State.Snapshot.Devices))
	seen := map[string]bool{}
	for _, device := range view.State.Snapshot.Devices {
		if !physicalUUID.MatchString(device.UUID) || seen[device.UUID] || device.PhysicalMemoryBytes == 0 {
			return nil, biz.Precondition("NVML_DEVICE_CONFLICT")
		}
		seen[device.UUID] = true
		devices = append(devices, Inventory{UUID: device.UUID, Model: device.Model, Bytes: device.PhysicalMemoryBytes})
	}
	return devices, nil
}

func telemetryNodeSnapshot(view biz.NodeTelemetryView, now time.Time, freshness time.Duration) nodeSnapshot {
	result := nodeSnapshot{Resources: &pr.ListPodResourcesResponse{}, Statuses: map[string]*cri.ContainerStatusResponse{}, HistoricalStatuses: map[string]bool{}, UnknownPodContainers: map[string][]string{}, FactObservedAt: map[string]time.Time{}, SourceObservedAt: map[string]time.Time{}, SourceReceivedAt: map[string]time.Time{}}
	if view.State.Snapshot == nil {
		return result
	}
	result.ObservedAt = view.State.Snapshot.CaptureEnd
	for _, name := range []string{"nvml", "podresources", "cri"} {
		source, complete := telemetrySource(view, name, now, freshness)
		result.SourceObservedAt[name], result.SourceReceivedAt[name] = source.ObservedAt, source.ReceivedAt
		if name == "cri" {
			result.RuntimeOK = complete
		}
		if name == "podresources" {
			result.ResourcesOK = complete
		}
	}
	pods := map[string]*pr.PodResources{}
	for _, allocation := range view.State.Snapshot.Allocations {
		key := allocation.Namespace + "/" + allocation.PodName
		podResources := pods[key]
		if podResources == nil {
			podResources = &pr.PodResources{Namespace: allocation.Namespace, Name: allocation.PodName}
			pods[key] = podResources
			result.Resources.PodResources = append(result.Resources.PodResources, podResources)
		}
		var containerResources *pr.ContainerResources
		for _, container := range podResources.Containers {
			if container.Name == allocation.ContainerName {
				containerResources = container
				break
			}
		}
		if containerResources == nil {
			containerResources = &pr.ContainerResources{Name: allocation.ContainerName}
			podResources.Containers = append(podResources.Containers, containerResources)
		}
		containerResources.Devices = append(containerResources.Devices, &pr.ContainerDevices{ResourceName: allocation.ResourceName, DeviceIds: allocation.DeviceIDs})
	}
	current := map[string]bool{}
	for _, attempt := range view.State.Snapshot.Containers {
		current[attempt.ContainerID] = true
	}
	for _, attempt := range view.Attempts {
		if attempt.PodUID == "" {
			result.RuntimeOK = false
		}
		if attempt.ContainerName == "" {
			result.UnknownPodContainers[attempt.PodUID] = append(result.UnknownPodContainers[attempt.PodUID], attempt.ContainerID)
			continue
		}
		labels := map[string]string{"io.kubernetes.pod.uid": attempt.PodUID, "io.kubernetes.pod.namespace": attempt.PodNamespace, "io.kubernetes.pod.name": attempt.PodName}
		metadata := &cri.ContainerMetadata{Name: attempt.ContainerName, Attempt: attempt.Attempt}
		state := cri.ContainerState_CONTAINER_UNKNOWN
		switch attempt.State {
		case 1:
			state = cri.ContainerState_CONTAINER_CREATED
		case 2:
			state = cri.ContainerState_CONTAINER_RUNNING
		case 3:
			state = cri.ContainerState_CONTAINER_EXITED
		}
		result.Containers = append(result.Containers, &cri.Container{Id: attempt.ContainerID, PodSandboxId: attempt.SandboxID, Metadata: metadata, Labels: labels, State: state})
		result.Statuses[attempt.ContainerID] = &cri.ContainerStatusResponse{Status: &cri.ContainerStatus{Id: attempt.ContainerID, Metadata: metadata, Labels: labels, State: state, CreatedAt: telemetryNanos(attempt.CreatedAt), StartedAt: telemetryNanos(attempt.StartedAt), FinishedAt: telemetryNanos(attempt.FinishedAt)}, Info: runtimeAttemptInfo(attempt)}
		result.HistoricalStatuses[attempt.ContainerID] = !current[attempt.ContainerID]
		result.FactObservedAt[attempt.ContainerID] = attempt.ObservedAt
	}
	result.SourceGaps = view.State.Gaps
	result.NodeRevision = view.State.CurrentRevision
	return result
}

func telemetryNanos(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}

func runtimeAttemptInfo(attempt biz.RuntimeAttempt) map[string]string {
	environment := make([]string, 0, len(attempt.GPUEnvironment))
	for _, entry := range attempt.GPUEnvironment {
		environment = append(environment, entry.Name+"="+entry.Value)
	}
	payload, _ := json.Marshal(struct {
		RuntimeSpec struct {
			Process struct {
				Env []string `json:"env"`
			} `json:"process"`
		} `json:"runtimeSpec"`
	}{RuntimeSpec: struct {
		Process struct {
			Env []string `json:"env"`
		} `json:"process"`
	}{Process: struct {
		Env []string `json:"env"`
	}{Env: environment}}})
	result := map[string]string{"info": string(payload)}
	if len(attempt.JournalProvenance) > 0 {
		result["journal_evidence"] = string(attempt.JournalProvenance)
		result["journal_sandbox_id"] = attempt.SandboxID
		if !attempt.JournalObservedAt.IsZero() {
			result["journal_observed_at"] = attempt.JournalObservedAt.Format(time.RFC3339Nano)
		}
	}
	return result
}

func runtimeObservation(snapshot nodeSnapshot, status *cri.ContainerStatusResponse) time.Time {
	if observed := snapshot.FactObservedAt[status.Status.Id]; !observed.IsZero() {
		return observed
	}
	return snapshot.ObservedAt
}
