package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	nodepb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/node/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type NodeTelemetryServer struct {
	nodepb.UnimplementedNodeTelemetryServiceServer
	store    biz.NodeTelemetryStore
	verifier biz.NodeIdentityVerifier
	mu       sync.Mutex
	sessions map[string]*nodeRecoverySession
}

func NewNodeTelemetryServer(store biz.NodeTelemetryStore, verifier biz.NodeIdentityVerifier) *NodeTelemetryServer {
	return &NodeTelemetryServer{store: store, verifier: verifier, sessions: map[string]*nodeRecoverySession{}}
}

type nodeRecoveryLedger interface {
	RegisterNodeRecovery(context.Context, string, string, uint64, string, biz.PodRef, []string) error
	ReadNodeRecovery(context.Context, string, string, string) (biz.PodRef, []string, error)
}

type nodeRecoveryResult struct {
	attempts []biz.RuntimeAttempt
	found    bool
	err      error
}
type nodeRecoveryWaiter struct{ done chan nodeRecoveryResult }
type nodeRecoverySession struct {
	epoch    uint64
	commands chan *nodepb.RecoverExitRequest
	closed   chan struct{}
	stopped  bool
	pending  map[string]*nodeRecoveryWaiter
}

func (s *NodeTelemetryServer) registerRecoverySession(connection, node string, epoch uint64) *nodeRecoverySession {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := connection + "\x00" + node
	if old := s.sessions[key]; old != nil && old.epoch >= epoch {
		stale := &nodeRecoverySession{epoch: epoch, commands: make(chan *nodepb.RecoverExitRequest), closed: make(chan struct{}), pending: map[string]*nodeRecoveryWaiter{}}
		s.stopRecoverySession(stale)
		return stale
	}
	if old := s.sessions[key]; old != nil {
		s.stopRecoverySession(old)
	}
	session := &nodeRecoverySession{epoch: epoch, commands: make(chan *nodepb.RecoverExitRequest, 16), closed: make(chan struct{}), pending: map[string]*nodeRecoveryWaiter{}}
	s.sessions[key] = session
	return session
}

func (s *NodeTelemetryServer) stopRecoverySession(session *nodeRecoverySession) {
	if session.stopped {
		return
	}
	session.stopped = true
	close(session.closed)
	for _, waiter := range session.pending {
		waiter.done <- nodeRecoveryResult{err: biz.Precondition("NODE_EPOCH_EXPIRED")}
	}
	session.pending = map[string]*nodeRecoveryWaiter{}
}

func (s *NodeTelemetryServer) closeRecoverySession(connection, node string, session *nodeRecoverySession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := connection + "\x00" + node
	if s.sessions[key] == session {
		delete(s.sessions, key)
	}
	s.stopRecoverySession(session)
}

func (s *NodeTelemetryServer) recoveryPending(session *nodeRecoverySession, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !session.stopped && session.pending[id] != nil
}

func (s *NodeTelemetryServer) RecoverNodeExit(parent context.Context, connection, node string, pod biz.PodRef, knownIDs []string) ([]biz.RuntimeAttempt, bool, error) {
	if !biz.Ref(connection) || !biz.UUID(node) || !biz.UUID(pod.UID) || biz.ValidatePod(pod) != nil || len(knownIDs) > 16 {
		return nil, false, biz.Invalid("JOURNAL_SCOPE_INVALID")
	}
	known := append([]string(nil), knownIDs...)
	sort.Strings(known)
	for index, id := range known {
		if !nodeContainerID.MatchString(id) || index > 0 && known[index-1] == id {
			return nil, false, biz.Invalid("JOURNAL_SCOPE_INVALID")
		}
	}
	ledger, ok := s.store.(nodeRecoveryLedger)
	if !ok {
		return nil, false, biz.Precondition("JOURNAL_LEDGER_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	requestID := biz.NewID()
	views, err := s.store.NodeTelemetry(ctx, connection)
	if err != nil {
		return nil, false, err
	}
	for _, view := range views {
		if view.State.Identity.NodeUID == node {
			for id, scope := range view.State.RecoveryRequests {
				if scope.Pod == pod && sameRecoveryIDs(scope.KnownIDs, known) {
					requestID = id
				}
			}
		}
	}
	s.mu.Lock()
	session := s.sessions[connection+"\x00"+node]
	if session == nil || session.stopped {
		s.mu.Unlock()
		return nil, false, biz.Precondition("NODE_AGENT_UNAVAILABLE")
	}
	if len(session.pending) >= 16 || session.pending[requestID] != nil {
		s.mu.Unlock()
		return nil, false, biz.Precondition("JOURNAL_REQUEST_LIMIT")
	}
	waiter := &nodeRecoveryWaiter{done: make(chan nodeRecoveryResult, 1)}
	session.pending[requestID] = waiter
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(session.pending, requestID); s.mu.Unlock() }()
	if err = ledger.RegisterNodeRecovery(ctx, connection, node, session.epoch, requestID, pod, known); err != nil {
		return nil, false, err
	}
	command := &nodepb.RecoverExitRequest{RequestId: requestID, NodeUid: node, PodUid: pod.UID, PodNamespace: pod.Namespace, PodName: pod.Name, ContainerName: pod.ContainerName, KnownContainerIds: known}
	select {
	case session.commands <- command:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-session.closed:
		return nil, false, biz.Precondition("NODE_EPOCH_EXPIRED")
	}
	select {
	case result := <-waiter.done:
		return result.attempts, result.found, result.err
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-session.closed:
		return nil, false, biz.Precondition("NODE_EPOCH_EXPIRED")
	}
}

func sameRecoveryIDs(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

var nodeContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var nodeJournalIdentity = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validateNodeJournalResult(journal *nodepb.JournalExitResult, pod biz.PodRef, knownIDs []string, report *biz.NodeReport, now time.Time) error {
	if journal == nil || len(journal.Issues) > 32 || len(journal.Provenance) > 16 {
		return biz.Invalid("JOURNAL_RESULT_INVALID")
	}
	if !journal.Found {
		if len(journal.Attempts) != 0 || len(journal.Provenance) != 0 {
			return biz.Invalid("JOURNAL_RESULT_INVALID")
		}
		return nil
	}
	if len(journal.Attempts) == 0 || len(journal.Attempts) != len(journal.Provenance) {
		return biz.Invalid("JOURNAL_PROVENANCE_REQUIRED")
	}
	seen := map[string]bool{}
	machine := ""
	for index, attempt := range report.Attempts {
		provenance := journal.Provenance[index]
		if !nodeContainerID.MatchString(attempt.ContainerID) || seen[attempt.ContainerID] || attempt.PodUID != pod.UID || attempt.PodNamespace != pod.Namespace || attempt.PodName != pod.Name || attempt.ContainerName != pod.ContainerName || attempt.State != 3 || attempt.FinishedAt.IsZero() || attempt.ObservedAt.IsZero() || attempt.ObservedAt.Before(attempt.FinishedAt) {
			return biz.Invalid("JOURNAL_SCOPE_MISMATCH")
		}
		seen[attempt.ContainerID] = true
		if provenance == nil || !nodeJournalIdentity.MatchString(provenance.MachineId) || !nodeJournalIdentity.MatchString(provenance.BootId) || provenance.Unit != "containerd.service" || provenance.Comm != "containerd" || provenance.Uid != "0" || !filepath.IsAbs(provenance.Exe) || filepath.Clean(provenance.Exe) != provenance.Exe || filepath.Base(provenance.Exe) != "containerd" || provenance.Transport != "stdout" || !nodeContainerID.MatchString(provenance.SandboxId) || provenance.Cursor == "" || len(provenance.Cursor) > 4096 || provenance.SandboxCursor == "" || len(provenance.SandboxCursor) > 4096 || provenance.CreateCursor == "" || len(provenance.CreateCursor) > 4096 || provenance.Cursor == provenance.CreateCursor || provenance.CreateCursor == provenance.SandboxCursor || provenance.Cursor == provenance.SandboxCursor || provenance.SandboxMonotonic == 0 || provenance.CreateMonotonic < provenance.SandboxMonotonic || provenance.ExitMonotonic < provenance.CreateMonotonic {
			return biz.Invalid("JOURNAL_PROVENANCE_INVALID")
		}
		if machine != "" && machine != provenance.MachineId {
			return biz.Invalid("JOURNAL_PROVENANCE_INVALID")
		}
		machine = provenance.MachineId
		if _, err := nodeTime(provenance.CreatedAt, true, now); err != nil {
			return err
		}
		finished, err := nodeTime(provenance.ExitedAt, true, now)
		if err != nil || !finished.Equal(attempt.FinishedAt) {
			return biz.Invalid("JOURNAL_PROVENANCE_INVALID")
		}
		if attempt.SandboxID != "" && attempt.SandboxID != provenance.SandboxId {
			return biz.Invalid("JOURNAL_SCOPE_MISMATCH")
		}
		report.Attempts[index].SandboxID = provenance.SandboxId
		report.Attempts[index].JournalProvenance, err = json.Marshal(provenance)
		if err != nil {
			return err
		}
	}
	for _, known := range knownIDs {
		if !seen[known] {
			return biz.Invalid("JOURNAL_KNOWN_ATTEMPT_MISSING")
		}
	}
	for index := range report.Attempts {
		report.Attempts[index].JournalObservedAt = report.Attempts[index].ObservedAt
	}
	return nil
}

func (s *NodeTelemetryServer) finishNodeRecovery(ctx context.Context, connection, node string, epoch uint64, session *nodeRecoverySession, report biz.NodeReport, found bool) {
	result := nodeRecoveryResult{found: found}
	views, err := s.store.NodeTelemetry(ctx, connection)
	if err != nil {
		result.err = err
		result.found = false
	} else {
		ids := map[string]bool{}
		for _, attempt := range report.Attempts {
			ids[attempt.ContainerID] = true
		}
		matched := false
		for _, view := range views {
			if view.State.Identity.NodeUID != node {
				continue
			}
			matched = true
			if view.State.Epoch != epoch || view.State.Revoked {
				result.err = biz.Precondition("NODE_EPOCH_EXPIRED")
				result.found = false
				break
			}
			for _, attempt := range view.Attempts {
				if ids[attempt.ContainerID] {
					result.attempts = append(result.attempts, attempt)
					if attempt.State != 3 || attempt.FinishedAt.IsZero() || len(attempt.JournalProvenance) == 0 || attempt.JournalObservedAt.IsZero() {
						result.found = false
					}
				}
			}
		}
		if !matched || len(result.attempts) != len(ids) {
			result.found = false
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if waiter := session.pending[report.JournalRequestID]; waiter != nil {
		delete(session.pending, report.JournalRequestID)
		waiter.done <- result
	}
}

func nodeStreamError(err error) error {
	if err == nil {
		return nil
	}
	if business, ok := err.(*biz.Error); ok {
		code := codes.FailedPrecondition
		switch business.Kind {
		case "invalid":
			code = codes.InvalidArgument
		case "conflict":
			code = codes.Aborted
		case "unavailable":
			code = codes.Unavailable
		}
		return status.Error(code, business.Reason)
	}
	return status.Error(codes.Unavailable, "NODE_TELEMETRY_UNAVAILABLE")
}

func nodeWelcome(state biz.NodeTelemetryState) *nodepb.CenterMessage {
	return &nodepb.CenterMessage{Message: &nodepb.CenterMessage_Welcome{Welcome: &nodepb.Welcome{NodeUid: state.Identity.NodeUID, NodeName: state.Identity.NodeName, Epoch: state.Epoch, LeaseExpiresAt: timestamppb.New(state.LeaseExpiresAt), ActiveLogId: state.Identity.LogID, LastCommittedSequence: state.LastSequence, LastPayloadDigest: state.LastPayloadDigest}}}
}

func (s *NodeTelemetryServer) Connect(stream grpc.BidiStreamingServer[nodepb.AgentMessage, nodepb.CenterMessage]) error {
	type received struct {
		message *nodepb.AgentMessage
		err     error
	}
	receives := make(chan received, 1)
	go func() {
		for {
			message, err := stream.Recv()
			select {
			case receives <- received{message, err}:
			case <-stream.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	lease := time.NewTimer(10 * time.Second)
	defer lease.Stop()
	var first received
	select {
	case first = <-receives:
	case <-lease.C:
		return status.Error(codes.DeadlineExceeded, "NODE_HELLO_TIMEOUT")
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	if first.err != nil {
		return first.err
	}
	if first.message == nil || hasUnknown(first.message.ProtoReflect()) {
		return status.Error(codes.InvalidArgument, "UNKNOWN_PROTO_FIELD")
	}
	hello := first.message.GetHello()
	if hello == nil || hello.ProtocolVersion != 1 || !biz.Ref(hello.ConnectionRef) || !biz.Name(hello.PodNamespace) || !biz.Text(hello.PodName, 253) || !biz.UUID(hello.PodUid) || !biz.UUID(hello.InstanceId) || !biz.UUID(hello.LogId) || !biz.UUID(hello.BootId) || (hello.NodeUid != "" && !biz.UUID(hello.NodeUid)) || !biz.Text(hello.NodeName, 253) || len(hello.ServiceAccountToken) > 16384 || hello.ServiceAccountToken == "" {
		return status.Error(codes.InvalidArgument, "NODE_HELLO_INVALID")
	}
	request := biz.NodeIdentityRequest{ConnectionRef: hello.ConnectionRef, PodNamespace: hello.PodNamespace, PodName: hello.PodName, PodUID: hello.PodUid, NodeUID: hello.NodeUid, NodeName: hello.NodeName, InstanceID: hello.InstanceId, LogID: hello.LogId, BootID: hello.BootId, Token: hello.ServiceAccountToken}
	identity, err := s.verifier.VerifyNodeIdentity(stream.Context(), request)
	if err != nil {
		return status.Error(codes.PermissionDenied, "NODE_IDENTITY_DENIED")
	}
	expires := minTime(identity.TokenExpiresAt, time.Now().Add(30*time.Second))
	state, err := s.store.OpenNodeSession(stream.Context(), identity, expires)
	if err != nil {
		return nodeStreamError(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.store.RevokeNodeSession(ctx, identity.ConnectionRef, identity.NodeUID, state.Epoch)
	}()
	if err = stream.Send(nodeWelcome(state)); err != nil {
		return err
	}
	session := s.registerRecoverySession(identity.ConnectionRef, identity.NodeUID, state.Epoch)
	defer s.closeRecoverySession(identity.ConnectionRef, identity.NodeUID, session)
	resetNodeLease(lease, state.LeaseExpiresAt)
	processedReports := uint64(0)
	for {
		var next received
		select {
		case next = <-receives:
		case command := <-session.commands:
			if !s.recoveryPending(session, command.RequestId) {
				continue
			}
			if !time.Now().Before(state.LeaseExpiresAt) {
				return status.Error(codes.Unauthenticated, "NODE_LEASE_EXPIRED")
			}
			verified, verifyErr := s.verifier.VerifyNodeIdentity(stream.Context(), request)
			if verifyErr != nil || !biz.SameNodeIdentity(identity, verified) {
				return status.Error(codes.PermissionDenied, "NODE_IDENTITY_DENIED")
			}
			if err = stream.Send(&nodepb.CenterMessage{Message: &nodepb.CenterMessage_RecoverExit{RecoverExit: command}}); err != nil {
				return err
			}
			continue
		case <-session.closed:
			return status.Error(codes.FailedPrecondition, "NODE_EPOCH_EXPIRED")
		case <-lease.C:
			return status.Error(codes.Unauthenticated, "NODE_LEASE_EXPIRED")
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
		if next.err == io.EOF {
			return nil
		}
		if next.err != nil {
			return next.err
		}
		message := next.message
		if message == nil || hasUnknown(message.ProtoReflect()) || proto.Size(message) > 8<<20 {
			return status.Error(codes.InvalidArgument, "NODE_MESSAGE_INVALID")
		}
		if renew := message.GetRenew(); renew != nil {
			if renew.Epoch != state.Epoch || renew.ServiceAccountToken == "" || len(renew.ServiceAccountToken) > 16384 {
				return status.Error(codes.InvalidArgument, "NODE_RENEW_INVALID")
			}
			request.Token = renew.ServiceAccountToken
			verified, verifyErr := s.verifier.VerifyNodeIdentity(stream.Context(), request)
			if verifyErr != nil || !biz.SameNodeIdentity(identity, verified) {
				return status.Error(codes.PermissionDenied, "NODE_IDENTITY_DENIED")
			}
			state, err = s.store.RenewNodeSession(stream.Context(), verified, state.Epoch, minTime(verified.TokenExpiresAt, time.Now().Add(30*time.Second)))
			if err != nil {
				return nodeStreamError(err)
			}
			resetNodeLease(lease, state.LeaseExpiresAt)
			if err = stream.Send(nodeWelcome(state)); err != nil {
				return err
			}
			continue
		}
		verificationStarted := time.Now()
		verified, verifyErr := s.verifier.VerifyNodeIdentity(stream.Context(), request)
		verificationDuration := time.Since(verificationStarted)
		if verifyErr != nil || !biz.SameNodeIdentity(identity, verified) {
			return status.Error(codes.PermissionDenied, "NODE_IDENTITY_DENIED")
		}
		if heartbeat := message.GetHeartbeat(); heartbeat != nil {
			if len(heartbeat.Issues) > 32 {
				return status.Error(codes.InvalidArgument, "NODE_HEARTBEAT_INVALID")
			}
			if err = s.store.HeartbeatNodeSession(stream.Context(), identity.ConnectionRef, identity.NodeUID, heartbeat.Epoch, heartbeat.Degraded, heartbeat.Issues, time.Now().UTC()); err != nil {
				return nodeStreamError(err)
			}
			continue
		}
		if report := message.GetReport(); report != nil {
			decodeStarted := time.Now()
			converted, convertErr := decodeNodeReport(report, time.Now().UTC())
			if convertErr != nil {
				return nodeStreamError(convertErr)
			}
			var recoveryPod biz.PodRef
			if converted.JournalRequestID != "" && !(converted.Sequence == state.LastSequence && converted.PayloadDigest == state.LastPayloadDigest) {
				ledger, ok := s.store.(nodeRecoveryLedger)
				if !ok {
					return status.Error(codes.Unavailable, "JOURNAL_LEDGER_UNAVAILABLE")
				}
				var knownIDs []string
				recoveryPod, knownIDs, err = ledger.ReadNodeRecovery(stream.Context(), identity.ConnectionRef, identity.NodeUID, converted.JournalRequestID)
				if err != nil {
					return nodeStreamError(err)
				}
				if err = validateNodeJournalResult(report.Record.GetJournalExit(), recoveryPod, knownIDs, &converted, time.Now().UTC()); err != nil {
					return nodeStreamError(err)
				}
			}
			decodeDuration := time.Since(decodeStarted)
			saveStarted := time.Now()
			state, err = s.store.SaveNodeReport(stream.Context(), identity.ConnectionRef, identity.NodeUID, converted, time.Now().UTC())
			saveDuration := time.Since(saveStarted)
			if err != nil {
				return nodeStreamError(err)
			}
			sendStarted := time.Now()
			if err = stream.Send(&nodepb.CenterMessage{Message: &nodepb.CenterMessage_Ack{Ack: &nodepb.Ack{LogId: converted.LogID, Sequence: state.LastSequence, PayloadDigest: state.LastPayloadDigest}}}); err != nil {
				return err
			}
			processedReports++
			if processedReports == 1 || processedReports%16 == 0 {
				slog.InfoContext(stream.Context(), "node_telemetry_report_duration", "verify_ms", verificationDuration.Milliseconds(), "decode_ms", decodeDuration.Milliseconds(), "save_ms", saveDuration.Milliseconds(), "send_ms", time.Since(sendStarted).Milliseconds(), "total_ms", time.Since(verificationStarted).Milliseconds())
			}
			if converted.JournalRequestID != "" && recoveryPod.UID != "" {
				s.finishNodeRecovery(stream.Context(), identity.ConnectionRef, identity.NodeUID, state.Epoch, session, converted, report.Record.GetJournalExit().Found)
			}
			continue
		}
		return status.Error(codes.InvalidArgument, "NODE_MESSAGE_ORDER_INVALID")
	}
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func resetNodeLease(timer *time.Timer, expires time.Time) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(max(time.Until(expires), time.Nanosecond))
}

func nodeTime(value *timestamppb.Timestamp, required bool, now time.Time) (time.Time, error) {
	if value == nil {
		if required {
			return time.Time{}, biz.Invalid("NODE_TIME_REQUIRED")
		}
		return time.Time{}, nil
	}
	if value.CheckValid() != nil || value.AsTime().IsZero() || value.AsTime().After(now.Add(5*time.Second)) {
		return time.Time{}, biz.Invalid("NODE_TIME_INVALID")
	}
	return value.AsTime().UTC(), nil
}

func sourceName(source nodepb.Source) (string, error) {
	switch source {
	case nodepb.Source_SOURCE_NVML:
		return "nvml", nil
	case nodepb.Source_SOURCE_POD_RESOURCES:
		return "podresources", nil
	case nodepb.Source_SOURCE_CRI:
		return "cri", nil
	case nodepb.Source_SOURCE_CRI_EVENTS:
		return "cri_events", nil
	case nodepb.Source_SOURCE_JOURNAL:
		return "journal", nil
	default:
		return "", biz.Invalid("NODE_SOURCE_INVALID")
	}
}

func decodeRuntimeFact(fact *nodepb.RuntimeFact, now time.Time) (biz.RuntimeAttempt, error) {
	var result biz.RuntimeAttempt
	if fact == nil || len(fact.ContainerId) > 1024 || fact.State < nodepb.ContainerState_CONTAINER_STATE_CREATED || fact.State > nodepb.ContainerState_CONTAINER_STATE_UNKNOWN || len(fact.Issues) > 32 {
		return result, biz.Invalid("RUNTIME_FACT_INVALID")
	}
	result = biz.RuntimeAttempt{ContainerID: fact.ContainerId, SandboxID: fact.SandboxId, PodUID: fact.PodUid, PodNamespace: fact.PodNamespace, PodName: fact.PodName, ContainerName: fact.ContainerName, Attempt: fact.Attempt, State: uint32(fact.State), Issues: fact.Issues}
	var err error
	result.ObservedAt, err = nodeTime(fact.ObservedAt, true, now)
	if err != nil {
		return result, err
	}
	result.CreatedAt, err = nodeTime(fact.CreatedAt, false, now)
	if err != nil {
		return result, err
	}
	result.StartedAt, err = nodeTime(fact.StartedAt, false, now)
	if err != nil {
		return result, err
	}
	result.FinishedAt, err = nodeTime(fact.FinishedAt, false, now)
	if err != nil {
		return result, err
	}
	if fact.EventType != nil {
		value := uint32(*fact.EventType)
		if value < 1 || value > 4 {
			return result, biz.Invalid("RUNTIME_EVENT_TYPE_INVALID")
		}
		result.EventType = &value
	}
	if result.PodUID == "" || result.ContainerName == "" || result.ContainerID == "" || (result.State == 3 && result.FinishedAt.IsZero()) {
		result.State = 4
		result.Issues = append(result.Issues, "RUNTIME_METADATA_UNKNOWN")
	}
	if result.ContainerID == "" {
		digest, _ := biz.Hash(result)
		result.ContainerID = "unresolved:" + digest
	}
	for _, entry := range fact.GpuEnvironment {
		if entry == nil || !(entry.Name == "NVIDIA_VISIBLE_DEVICES" || entry.Name == "CUDA_DEVICE_SM_LIMIT" || strings.HasPrefix(entry.Name, "CUDA_DEVICE_MEMORY_LIMIT_")) || len(entry.Name) > 256 || len(entry.Value) > 16384 {
			return result, biz.Invalid("GPU_ENVIRONMENT_INVALID")
		}
		result.GPUEnvironment = append(result.GPUEnvironment, biz.GPUEnvironmentEntry{Name: entry.Name, Value: entry.Value})
	}
	return result, nil
}

func decodeNodeReport(report *nodepb.AgentReport, now time.Time) (biz.NodeReport, error) {
	var result biz.NodeReport
	record := report.GetRecord()
	if record == nil || record.ProtocolVersion != 1 || !biz.UUID(record.LogId) || record.Sequence == 0 || !biz.UUID(record.OriginPodUid) || !biz.UUID(record.OriginInstanceId) || !biz.UUID(record.OriginBootId) || !biz.Digest(record.PayloadDigest) {
		return result, biz.Invalid("NODE_RECORD_INVALID")
	}
	copyRecord := proto.Clone(record).(*nodepb.ImmutableRecord)
	copyRecord.PayloadDigest = ""
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(copyRecord)
	if err != nil {
		return result, biz.Invalid("NODE_RECORD_INVALID")
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != record.PayloadDigest {
		return result, biz.Invalid("NODE_PAYLOAD_DIGEST_MISMATCH")
	}
	result = biz.NodeReport{Epoch: report.Epoch, Sequence: record.Sequence, LogID: record.LogId, PayloadDigest: record.PayloadDigest, OriginPodUID: record.OriginPodUid, OriginInstanceID: record.OriginInstanceId, OriginBootID: record.OriginBootId}
	if snapshot := record.GetSnapshot(); snapshot != nil {
		converted := &biz.TelemetrySnapshot{CaptureEpoch: snapshot.CaptureEpoch}
		converted.CaptureStart, err = nodeTime(snapshot.CaptureStart, true, now)
		if err != nil {
			return result, err
		}
		converted.CaptureEnd, err = nodeTime(snapshot.CaptureEnd, true, now)
		if err != nil {
			return result, err
		}
		if converted.CaptureEnd.Before(converted.CaptureStart) || snapshot.CaptureEpoch == 0 || len(snapshot.Sources) > 5 {
			return result, biz.Invalid("NODE_SNAPSHOT_INVALID")
		}
		seen := map[string]bool{}
		for _, health := range snapshot.Sources {
			if health == nil {
				return result, biz.Invalid("NODE_SOURCE_INVALID")
			}
			name, nameErr := sourceName(health.Source)
			if nameErr != nil {
				return result, nameErr
			}
			observed, timeErr := nodeTime(health.ObservedAt, true, now)
			if timeErr != nil {
				return result, timeErr
			}
			if seen[name] || observed.Before(converted.CaptureStart) || observed.After(converted.CaptureEnd) || len(health.Issues) > 32 {
				return result, biz.Invalid("NODE_SOURCE_INVALID")
			}
			seen[name] = true
			converted.Sources = append(converted.Sources, biz.TelemetrySource{Name: name, ObservedAt: observed, Complete: health.Complete, Issues: health.Issues})
		}
		for _, name := range []string{"nvml", "podresources", "cri"} {
			if !seen[name] {
				return result, biz.Invalid("NODE_SOURCE_REQUIRED")
			}
		}
		for _, device := range snapshot.Devices {
			if device == nil || !biz.Text(device.Uuid, 128) || !biz.Text(device.Model, 256) || device.PhysicalMemoryBytes == 0 {
				return result, biz.Invalid("NODE_DEVICE_INVALID")
			}
			converted.Devices = append(converted.Devices, biz.TelemetryDevice{UUID: device.Uuid, Model: device.Model, PhysicalMemoryBytes: device.PhysicalMemoryBytes})
		}
		for _, allocation := range snapshot.Allocations {
			if allocation == nil {
				return result, biz.Invalid("NODE_ALLOCATION_INVALID")
			}
			converted.Allocations = append(converted.Allocations, biz.TelemetryAllocation{Namespace: allocation.Namespace, PodName: allocation.PodName, ContainerName: allocation.ContainerName, ResourceName: allocation.ResourceName, DeviceIDs: allocation.DeviceIds})
		}
		for _, fact := range snapshot.Containers {
			attempt, decodeErr := decodeRuntimeFact(fact, now)
			if decodeErr != nil {
				return result, decodeErr
			}
			converted.Containers = append(converted.Containers, attempt)
		}
		result.Snapshot = converted
		return result, nil
	}
	if facts := record.GetRuntimeFacts(); facts != nil {
		if len(facts.Items) == 0 || len(facts.Items) > 256 {
			return result, biz.Invalid("RUNTIME_BATCH_INVALID")
		}
		for _, fact := range facts.Items {
			attempt, decodeErr := decodeRuntimeFact(fact, now)
			if decodeErr != nil {
				return result, decodeErr
			}
			result.Attempts = append(result.Attempts, attempt)
		}
		return result, nil
	}
	if gap := record.GetSourceGap(); gap != nil {
		converted := &biz.TelemetryGap{Reason: gap.Reason, LastCursor: gap.LastCursor}
		if !biz.Text(gap.Reason, 256) || len(gap.Sources) == 0 || len(gap.Sources) > 5 {
			return result, biz.Invalid("SOURCE_GAP_INVALID")
		}
		for _, source := range gap.Sources {
			name, nameErr := sourceName(source)
			if nameErr != nil {
				return result, nameErr
			}
			converted.Sources = append(converted.Sources, name)
		}
		converted.Start, err = nodeTime(gap.Start, false, now)
		if err != nil {
			return result, err
		}
		converted.End, err = nodeTime(gap.End, false, now)
		if err != nil {
			return result, err
		}
		converted.DiscoveredAt, err = nodeTime(gap.DiscoveredAt, true, now)
		if err != nil {
			return result, err
		}
		result.Gap = converted
		return result, nil
	}
	if journal := record.GetJournalExit(); journal != nil {
		if !biz.UUID(journal.RequestId) || len(journal.Attempts) > 16 {
			return result, biz.Invalid("JOURNAL_RESULT_INVALID")
		}
		result.JournalRequestID = journal.RequestId
		for _, fact := range journal.Attempts {
			attempt, decodeErr := decodeRuntimeFact(fact, now)
			if decodeErr != nil {
				return result, decodeErr
			}
			attempt.JournalProvenance, _ = json.Marshal(journal.Provenance)
			result.Attempts = append(result.Attempts, attempt)
		}
		return result, nil
	}
	return result, biz.Invalid("NODE_PAYLOAD_REQUIRED")
}
