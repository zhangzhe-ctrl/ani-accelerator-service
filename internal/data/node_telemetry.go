package data

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data/accdb"
)

func (p *Postgres) nodeTransaction(ctx context.Context, connection, node string, update func(*accdb.Queries, *biz.NodeTelemetryState) error) (biz.NodeTelemetryState, error) {
	var state biz.NodeTelemetryState
	started := time.Now()
	var beginDuration, lockDuration, applyDuration, encodeDuration, updateDuration, commitDuration time.Duration
	var payloadBytes int
	defer func() {
		if time.Since(started) >= time.Second {
			slog.InfoContext(ctx, "node_telemetry_transaction_duration", "begin_ms", beginDuration.Milliseconds(), "lock_ms", lockDuration.Milliseconds(), "apply_ms", applyDuration.Milliseconds(), "encode_ms", encodeDuration.Milliseconds(), "update_ms", updateDuration.Milliseconds(), "commit_ms", commitDuration.Milliseconds(), "payload_bytes", payloadBytes, "total_ms", time.Since(started).Milliseconds())
		}
	}()
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	beginDuration = time.Since(started)
	if err != nil {
		return state, cleanError(err)
	}
	defer tx.Rollback(context.Background())
	queries := accdb.New(tx)
	phaseStarted := time.Now()
	stored, err := queries.LockNodeTelemetryState(ctx, connection, node)
	lockDuration = time.Since(phaseStarted)
	if err != nil {
		return state, cleanError(err)
	}
	if json.Unmarshal(stored, &state) != nil {
		return state, biz.Precondition("STORED_TELEMETRY_INVALID")
	}
	phaseStarted = time.Now()
	err = update(queries, &state)
	applyDuration = time.Since(phaseStarted)
	if err != nil {
		return state, err
	}
	phaseStarted = time.Now()
	payload, err := json.Marshal(state)
	encodeDuration = time.Since(phaseStarted)
	payloadBytes = len(payload)
	if err != nil {
		return state, err
	}
	phaseStarted = time.Now()
	err = queries.UpdateNodeTelemetryState(ctx, connection, node, state.Epoch, state.Identity.LogID, state.LastSequence, state.LastPayloadDigest, state.CurrentRevision, state.ConsumedRevision, payload)
	updateDuration = time.Since(phaseStarted)
	if err != nil {
		return state, cleanError(err)
	}
	phaseStarted = time.Now()
	err = tx.Commit(ctx)
	commitDuration = time.Since(phaseStarted)
	return state, cleanError(err)
}

func (p *Postgres) OpenNodeSession(ctx context.Context, identity biz.NodeIdentity, lease time.Time) (biz.NodeTelemetryState, error) {
	initial := biz.NodeTelemetryState{Identity: identity, Gaps: map[string]biz.TelemetryGap{}}
	payload, err := json.Marshal(initial)
	if err != nil {
		return initial, err
	}
	if err = accdb.New(p.pool).InsertNodeTelemetryState(ctx, identity.ConnectionRef, identity.NodeUID, 1, identity.LogID, payload); err != nil {
		return initial, cleanError(err)
	}
	return p.nodeTransaction(ctx, identity.ConnectionRef, identity.NodeUID, func(queries *accdb.Queries, state *biz.NodeTelemetryState) error {
		if state.Identity.LogID != identity.LogID {
			return biz.Conflict("LOCAL_LOG_ID_MISMATCH")
		}
		if state.CurrentRevision == 0 || state.Revoked || !time.Now().Before(state.LeaseExpiresAt) || state.Identity.PodUID != identity.PodUID {
			state.CurrentRevision++
		}
		state.Identity, state.LeaseExpiresAt, state.Revoked = identity, lease, false
		state.Epoch++
		return nil
	})
}

func (p *Postgres) RenewNodeSession(ctx context.Context, identity biz.NodeIdentity, epoch uint64, lease time.Time) (biz.NodeTelemetryState, error) {
	return p.nodeTransaction(ctx, identity.ConnectionRef, identity.NodeUID, func(queries *accdb.Queries, state *biz.NodeTelemetryState) error {
		if state.Epoch != epoch || state.Revoked || !time.Now().Before(state.LeaseExpiresAt) || !biz.SameNodeIdentity(state.Identity, identity) {
			return biz.Precondition("NODE_EPOCH_EXPIRED")
		}
		state.Identity.TokenExpiresAt, state.LeaseExpiresAt = identity.TokenExpiresAt, lease
		return nil
	})
}

func (p *Postgres) RevokeNodeSession(ctx context.Context, connection, node string, epoch uint64) error {
	_, err := p.nodeTransaction(ctx, connection, node, func(queries *accdb.Queries, state *biz.NodeTelemetryState) error {
		if state.Epoch == epoch && !state.Revoked {
			state.Revoked = true
			state.CurrentRevision++
		}
		return nil
	})
	return err
}

func (p *Postgres) HeartbeatNodeSession(ctx context.Context, connection, node string, epoch uint64, degraded bool, issues []string, now time.Time) error {
	_, err := p.nodeTransaction(ctx, connection, node, func(queries *accdb.Queries, state *biz.NodeTelemetryState) error {
		if state.Epoch != epoch || state.Revoked || !now.Before(state.LeaseExpiresAt) {
			return biz.Precondition("NODE_EPOCH_EXPIRED")
		}
		if state.Degraded != degraded {
			state.CurrentRevision++
		}
		state.Degraded, state.Issues, state.LastHeartbeatAt = degraded, issues, now
		return nil
	})
	return err
}

func (p *Postgres) SaveNodeReport(ctx context.Context, connection, node string, report biz.NodeReport, now time.Time) (biz.NodeTelemetryState, error) {
	return p.nodeTransaction(ctx, connection, node, func(queries *accdb.Queries, state *biz.NodeTelemetryState) error {
		started := time.Now()
		var listDuration, decodeDuration, mergeDuration, encodeDuration, writeDuration time.Duration
		var existingCount, changedCount int
		defer func() {
			if time.Since(started) >= time.Second {
				slog.InfoContext(ctx, "node_telemetry_attempt_duration", "list_ms", listDuration.Milliseconds(), "decode_ms", decodeDuration.Milliseconds(), "merge_ms", mergeDuration.Milliseconds(), "encode_ms", encodeDuration.Milliseconds(), "write_ms", writeDuration.Milliseconds(), "existing_count", existingCount, "changed_count", changedCount, "total_ms", time.Since(started).Milliseconds())
			}
		}()
		rows, err := queries.ListNodeRuntimeAttempts(ctx, connection, node)
		listDuration = time.Since(started)
		phaseStarted := time.Now()
		attempts, err := decodeMany[biz.RuntimeAttempt](rows, err)
		decodeDuration = time.Since(phaseStarted)
		existingCount = len(attempts)
		if err != nil {
			return err
		}
		previousAttempts := make(map[string]biz.RuntimeAttempt, len(attempts))
		for _, attempt := range attempts {
			previousAttempts[attempt.ContainerID] = attempt
		}
		lastSequence := state.LastSequence
		if report.JournalRequestID != "" && !(report.Sequence == state.LastSequence && report.PayloadDigest == state.LastPayloadDigest) {
			if _, found := state.RecoveryRequests[report.JournalRequestID]; !found {
				return biz.Precondition("JOURNAL_REQUEST_UNKNOWN")
			}
		}
		phaseStarted = time.Now()
		attempts, err = biz.MergeNodeReport(state, attempts, report, now)
		mergeDuration = time.Since(phaseStarted)
		if err != nil {
			return err
		}
		if state.LastSequence == lastSequence {
			return nil
		}
		if report.JournalRequestID != "" {
			delete(state.RecoveryRequests, report.JournalRequestID)
		}
		var changed []biz.RuntimeAttempt
		for _, attempt := range attempts {
			if previous, found := previousAttempts[attempt.ContainerID]; found {
				compared := attempt
				if string(previous.JournalProvenance) == "null" {
					previous.JournalProvenance = nil
				}
				if string(compared.JournalProvenance) == "null" {
					compared.JournalProvenance = nil
				}
				if reflect.DeepEqual(previous, compared) {
					continue
				}
			}
			changed = append(changed, attempt)
		}
		if len(changed) == 0 {
			return nil
		}
		changedCount = len(changed)
		phaseStarted = time.Now()
		payload, err := json.Marshal(changed)
		encodeDuration = time.Since(phaseStarted)
		if err != nil {
			return err
		}
		phaseStarted = time.Now()
		err = queries.SaveNodeRuntimeAttempts(ctx, connection, node, payload)
		writeDuration = time.Since(phaseStarted)
		return cleanError(err)
	})
}

func (p *Postgres) RegisterNodeRecovery(ctx context.Context, connection, node string, epoch uint64, requestID string, pod biz.PodRef, knownIDs []string) error {
	if !biz.UUID(requestID) || biz.ValidatePod(pod) != nil || !biz.UUID(pod.UID) || len(knownIDs) > 16 {
		return biz.Invalid("JOURNAL_SCOPE_INVALID")
	}
	known := append([]string(nil), knownIDs...)
	sort.Strings(known)
	for index, id := range known {
		if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" || index > 0 && known[index-1] == id {
			return biz.Invalid("JOURNAL_SCOPE_INVALID")
		}
	}
	_, err := p.nodeTransaction(ctx, connection, node, func(_ *accdb.Queries, state *biz.NodeTelemetryState) error {
		if state.Epoch != epoch || state.Revoked || !time.Now().Before(state.LeaseExpiresAt) {
			return biz.Precondition("NODE_EPOCH_EXPIRED")
		}
		if state.RecoveryRequests == nil {
			state.RecoveryRequests = map[string]biz.NodeRecoveryRequest{}
		}
		if previous, found := state.RecoveryRequests[requestID]; found {
			if previous.Pod != pod || !reflect.DeepEqual(previous.KnownIDs, known) {
				return biz.Conflict("JOURNAL_SCOPE_CONFLICT")
			}
			return nil
		}
		for _, previous := range state.RecoveryRequests {
			if previous.Pod == pod && reflect.DeepEqual(previous.KnownIDs, known) {
				return biz.Conflict("JOURNAL_SCOPE_IN_FLIGHT")
			}
		}
		if len(state.RecoveryRequests) >= 16 {
			return biz.Precondition("JOURNAL_REQUEST_LIMIT")
		}
		state.RecoveryRequests[requestID] = biz.NodeRecoveryRequest{RequestID: requestID, Pod: pod, KnownIDs: known, IssuedEpoch: epoch}
		return nil
	})
	return err
}

func (p *Postgres) ReadNodeRecovery(ctx context.Context, connection, node, requestID string) (biz.PodRef, []string, error) {
	views, err := p.NodeTelemetry(ctx, connection)
	if err != nil {
		return biz.PodRef{}, nil, err
	}
	for _, view := range views {
		if view.State.Identity.NodeUID == node {
			scope, found := view.State.RecoveryRequests[requestID]
			if !found {
				return biz.PodRef{}, nil, biz.Precondition("JOURNAL_REQUEST_UNKNOWN")
			}
			return scope.Pod, append([]string(nil), scope.KnownIDs...), nil
		}
	}
	return biz.PodRef{}, nil, biz.NotFound()
}

func (p *Postgres) NodeTelemetry(ctx context.Context, connection string) ([]biz.NodeTelemetryView, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, cleanError(err)
	}
	defer tx.Rollback(context.Background())
	queries := accdb.New(tx)
	rows, err := queries.ListNodeTelemetryStates(ctx, connection)
	states, err := decodeMany[biz.NodeTelemetryState](rows, err)
	if err != nil {
		return nil, err
	}
	views := make([]biz.NodeTelemetryView, 0, len(states))
	for _, state := range states {
		rows, err = queries.ListNodeRuntimeAttempts(ctx, connection, state.Identity.NodeUID)
		attempts, err := decodeMany[biz.RuntimeAttempt](rows, err)
		if err != nil {
			return nil, err
		}
		views = append(views, biz.NodeTelemetryView{State: state, Attempts: attempts})
	}
	if err = tx.Commit(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return nil, cleanError(err)
	}
	return views, nil
}
