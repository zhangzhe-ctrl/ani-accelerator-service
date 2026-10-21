package biz

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"time"
)

type NodeIdentityRequest struct {
	ConnectionRef, PodNamespace, PodName, PodUID, NodeUID, NodeName string
	InstanceID, LogID, BootID, Token                                string
}

type NodeIdentity struct {
	ConnectionRef, NodeUID, NodeName, PodUID, PodNamespace, PodName string
	ServiceAccount, DaemonSetUID, InstanceID, LogID, BootID         string
	TokenExpiresAt                                                  time.Time
}

type NodeIdentityVerifier interface {
	VerifyNodeIdentity(context.Context, NodeIdentityRequest) (NodeIdentity, error)
}

type TelemetrySource struct {
	Name       string    `json:"name"`
	ObservedAt time.Time `json:"observed_at"`
	ReceivedAt time.Time `json:"received_at"`
	Complete   bool      `json:"complete"`
	Issues     []string  `json:"issues"`
}

type TelemetryDevice struct {
	UUID                string `json:"uuid"`
	Model               string `json:"model"`
	PhysicalMemoryBytes uint64 `json:"physical_memory_bytes"`
}

type TelemetryAllocation struct {
	Namespace, PodName, ContainerName, ResourceName string
	DeviceIDs                                       []string
}

type GPUEnvironmentEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type RuntimeAttempt struct {
	ContainerID, SandboxID, PodUID, PodNamespace, PodName, ContainerName string
	Attempt                                                              uint32
	State                                                                uint32
	EventType                                                            *uint32
	ObservedAt, CreatedAt, StartedAt, FinishedAt                         time.Time
	GPUEnvironment                                                       []GPUEnvironmentEntry
	Issues                                                               []string
	JournalProvenance                                                    json.RawMessage
}

type TelemetrySnapshot struct {
	CaptureEpoch             uint64
	CaptureStart, CaptureEnd time.Time
	Sources                  []TelemetrySource
	Devices                  []TelemetryDevice
	Allocations              []TelemetryAllocation
	Containers               []RuntimeAttempt
}

type TelemetryGap struct {
	Sources                  []string
	Reason, LastCursor       string
	Start, End, DiscoveredAt time.Time
}

type NodeTelemetryState struct {
	RecoveryRequests                  map[string]NodeRecoveryRequest
	Identity                          NodeIdentity
	Epoch                             uint64
	LeaseExpiresAt                    time.Time
	Revoked                           bool
	LastSequence                      uint64
	LastPayloadDigest                 string
	CurrentRevision, ConsumedRevision uint64
	Snapshot                          *TelemetrySnapshot
	Gaps                              map[string]TelemetryGap
	Degraded                          bool
	Issues                            []string
	LastHeartbeatAt                   time.Time
}

type NodeRecoveryRequest struct {
	RequestID   string
	Pod         PodRef
	KnownIDs    []string
	IssuedEpoch uint64
}

type NodeTelemetryView struct {
	State    NodeTelemetryState
	Attempts []RuntimeAttempt
}

type NodeReport struct {
	Epoch, Sequence                                                    uint64
	LogID, PayloadDigest, OriginPodUID, OriginInstanceID, OriginBootID string
	Snapshot                                                           *TelemetrySnapshot
	Attempts                                                           []RuntimeAttempt
	Gap                                                                *TelemetryGap
	JournalRequestID                                                   string
}

type NodeTelemetryStore interface {
	OpenNodeSession(context.Context, NodeIdentity, time.Time) (NodeTelemetryState, error)
	RenewNodeSession(context.Context, NodeIdentity, uint64, time.Time) (NodeTelemetryState, error)
	RevokeNodeSession(context.Context, string, string, uint64) error
	HeartbeatNodeSession(context.Context, string, string, uint64, bool, []string, time.Time) error
	SaveNodeReport(context.Context, string, string, NodeReport, time.Time) (NodeTelemetryState, error)
	NodeTelemetry(context.Context, string) ([]NodeTelemetryView, error)
}

func MergeNodeReport(state *NodeTelemetryState, attempts []RuntimeAttempt, report NodeReport, now time.Time) ([]RuntimeAttempt, error) {
	if state.Revoked || report.Epoch != state.Epoch || !now.Before(state.LeaseExpiresAt) {
		return nil, Precondition("NODE_EPOCH_EXPIRED")
	}
	if report.LogID != state.Identity.LogID {
		return nil, Conflict("LOCAL_LOG_ID_MISMATCH")
	}
	if report.Sequence == state.LastSequence && report.Sequence != 0 {
		if report.PayloadDigest != state.LastPayloadDigest {
			return nil, Conflict("SEQUENCE_CONTENT_CONFLICT")
		}
		return attempts, nil
	}
	if report.Sequence < state.LastSequence {
		return nil, Conflict("SEQUENCE_COMPACTED")
	}
	if report.Sequence != state.LastSequence+1 {
		return nil, Conflict("OUT_OF_ORDER")
	}
	before, err := telemetryMeaning(*state, attempts)
	if err != nil {
		return nil, err
	}
	incoming := append([]RuntimeAttempt(nil), report.Attempts...)
	if report.Snapshot != nil {
		incoming = append(incoming, report.Snapshot.Containers...)
		if report.Snapshot.CaptureEpoch == state.Epoch && (state.Snapshot == nil || !report.Snapshot.CaptureEnd.Before(state.Snapshot.CaptureEnd)) {
			state.Snapshot = report.Snapshot
			for sourceIndex := range state.Snapshot.Sources {
				state.Snapshot.Sources[sourceIndex].ReceivedAt = now
			}
		}
	}
	byID := make(map[string]RuntimeAttempt, len(attempts)+len(incoming))
	for _, attempt := range attempts {
		byID[attempt.ContainerID] = attempt
	}
	for _, attempt := range incoming {
		if attempt.ContainerID == "" {
			return nil, Invalid("RUNTIME_CONTAINER_ID_REQUIRED")
		}
		previous, found := byID[attempt.ContainerID]
		if !found {
			byID[attempt.ContainerID] = attempt
			continue
		}
		if attempt.ObservedAt.Before(previous.ObservedAt) {
			continue
		}
		if (previous.PodUID != "" && attempt.PodUID != "" && previous.PodUID != attempt.PodUID) || (previous.ContainerName != "" && attempt.ContainerName != "" && previous.ContainerName != attempt.ContainerName) {
			previous.State = 4
			previous.Issues = []string{"RUNTIME_IDENTITY_CONFLICT"}
			previous.ObservedAt = attempt.ObservedAt
			byID[attempt.ContainerID] = previous
			continue
		}
		if attempt.PodUID == "" {
			attempt.PodUID = previous.PodUID
		}
		if attempt.PodNamespace == "" {
			attempt.PodNamespace = previous.PodNamespace
		}
		if attempt.PodName == "" {
			attempt.PodName = previous.PodName
		}
		if attempt.ContainerName == "" {
			attempt.ContainerName = previous.ContainerName
		}
		if len(attempt.GPUEnvironment) == 0 {
			attempt.GPUEnvironment = previous.GPUEnvironment
		}
		if attempt.EventType == nil {
			attempt.EventType = previous.EventType
		}
		if attempt.State == 3 && (attempt.FinishedAt.IsZero() || (previous.State == 2 && attempt.FinishedAt.Before(previous.ObservedAt))) {
			previous.State = 4
			previous.Issues = []string{"RUNTIME_EXIT_ORDER_UNKNOWN"}
			previous.ObservedAt = attempt.ObservedAt
			byID[attempt.ContainerID] = previous
			continue
		}
		byID[attempt.ContainerID] = attempt
	}
	attempts = make([]RuntimeAttempt, 0, len(byID))
	for _, attempt := range byID {
		attempts = append(attempts, attempt)
	}
	sort.Slice(attempts, func(left, right int) bool { return attempts[left].ContainerID < attempts[right].ContainerID })
	if report.Gap != nil {
		if state.Gaps == nil {
			state.Gaps = map[string]TelemetryGap{}
		}
		for _, source := range report.Gap.Sources {
			state.Gaps[source] = *report.Gap
		}
	}
	after, err := telemetryMeaning(*state, attempts)
	if err != nil {
		return nil, err
	}
	if before != after {
		state.CurrentRevision++
	}
	state.LastSequence, state.LastPayloadDigest = report.Sequence, report.PayloadDigest
	return attempts, nil
}

func telemetryMeaning(state NodeTelemetryState, attempts []RuntimeAttempt) (string, error) {
	type sourceMeaning struct {
		Name     string
		Complete bool
		Issues   []string
	}
	type attemptMeaning struct {
		ContainerID, SandboxID, PodUID, PodNamespace, PodName, ContainerName string
		Attempt, State                                                       uint32
		EventType                                                            *uint32
		CreatedAt, StartedAt, FinishedAt                                     time.Time
		GPUEnvironment                                                       []GPUEnvironmentEntry
		Issues                                                               []string
		JournalProvenance                                                    json.RawMessage
	}
	var sources []sourceMeaning
	var devices []TelemetryDevice
	var allocations []TelemetryAllocation
	if state.Snapshot != nil {
		for _, source := range state.Snapshot.Sources {
			sources = append(sources, sourceMeaning{source.Name, source.Complete, source.Issues})
		}
		devices = append([]TelemetryDevice(nil), state.Snapshot.Devices...)
		allocations = canonicalTelemetryAllocations(state.Snapshot.Allocations)
	}
	sort.Slice(devices, func(left, right int) bool { return devices[left].UUID < devices[right].UUID })
	sort.Slice(sources, func(left, right int) bool { return sources[left].Name < sources[right].Name })
	meaning := make([]attemptMeaning, 0, len(attempts))
	for _, attempt := range attempts {
		meaning = append(meaning, attemptMeaning{attempt.ContainerID, attempt.SandboxID, attempt.PodUID, attempt.PodNamespace, attempt.PodName, attempt.ContainerName, attempt.Attempt, attempt.State, attempt.EventType, attempt.CreatedAt, attempt.StartedAt, attempt.FinishedAt, attempt.GPUEnvironment, attempt.Issues, attempt.JournalProvenance})
	}
	sort.Slice(meaning, func(left, right int) bool { return meaning[left].ContainerID < meaning[right].ContainerID })
	return Hash(struct {
		Sources           []sourceMeaning
		Devices           []TelemetryDevice
		Allocations       []TelemetryAllocation
		Attempts          []attemptMeaning
		Gaps              map[string]TelemetryGap
		Revoked, Degraded bool
	}{sources, devices, allocations, meaning, state.Gaps, state.Revoked, state.Degraded})
}

func canonicalTelemetryAllocations(input []TelemetryAllocation) []TelemetryAllocation {
	type rankedAllocation struct {
		value TelemetryAllocation
		key   string
		hash  string
	}
	ranked := make([]rankedAllocation, len(input))
	for index, allocation := range input {
		allocation.DeviceIDs = append([]string(nil), allocation.DeviceIDs...)
		sort.Strings(allocation.DeviceIDs)
		digest, _ := Hash(allocation)
		ranked[index] = rankedAllocation{value: allocation, key: allocation.Namespace + "\x00" + allocation.PodName + "\x00" + allocation.ContainerName + "\x00" + allocation.ResourceName, hash: digest}
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].key == ranked[right].key {
			return ranked[left].hash < ranked[right].hash
		}
		return ranked[left].key < ranked[right].key
	})
	var output []TelemetryAllocation
	if len(input) > 0 {
		output = make([]TelemetryAllocation, len(input))
	}
	for index, allocation := range ranked {
		output[index] = allocation.value
	}
	return output
}

func SameNodeIdentity(left, right NodeIdentity) bool {
	left.TokenExpiresAt, right.TokenExpiresAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(left, right)
}
