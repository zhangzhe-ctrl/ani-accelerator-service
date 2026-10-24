-- name: InsertNodeTelemetryState :exec
INSERT INTO accelerator.node_telemetry_state(connection_ref,node_uid,epoch,active_log_id,payload)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(connection_ref,node_uid) DO NOTHING;

-- name: LockNodeTelemetryState :one
SELECT payload FROM accelerator.node_telemetry_state WHERE connection_ref=$1 AND node_uid=$2 FOR UPDATE;

-- name: ListNodeTelemetryStates :many
SELECT payload FROM accelerator.node_telemetry_state WHERE connection_ref=$1 ORDER BY node_uid;

-- name: UpdateNodeTelemetryState :exec
UPDATE accelerator.node_telemetry_state SET epoch=$3,active_log_id=$4,last_sequence=$5,last_payload_digest=$6,
current_node_revision=$7,consumed_node_revision=$8,payload=$9 WHERE connection_ref=$1 AND node_uid=$2;

-- name: ListNodeRuntimeAttempts :many
SELECT payload FROM accelerator.node_runtime_attempts WHERE connection_ref=$1 AND node_uid=$2 ORDER BY container_id;

-- name: ListNodeRuntimeAttemptsByContainerIDs :many
SELECT payload FROM accelerator.node_runtime_attempts
WHERE connection_ref=$1 AND node_uid=$2 AND container_id=ANY(sqlc.arg(container_ids)::text[])
ORDER BY container_id;

-- name: SaveNodeRuntimeAttempt :exec
INSERT INTO accelerator.node_runtime_attempts(connection_ref,node_uid,container_id,payload) VALUES($1,$2,$3,$4)
ON CONFLICT(connection_ref,node_uid,container_id) DO UPDATE SET payload=EXCLUDED.payload;

-- name: SaveNodeRuntimeAttempts :exec
INSERT INTO accelerator.node_runtime_attempts(connection_ref,node_uid,container_id,payload)
SELECT sqlc.arg(connection_ref)::text,sqlc.arg(node_uid)::text,
attempt.payload->>'ContainerID',attempt.payload
FROM jsonb_array_elements(sqlc.arg(attempts)::jsonb) AS attempt(payload)
ON CONFLICT(connection_ref,node_uid,container_id) DO UPDATE SET payload=EXCLUDED.payload;
