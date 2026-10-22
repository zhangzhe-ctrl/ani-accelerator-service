-- name: InitializationOperationData :one
SELECT payload,lease_owner,lease_epoch,lease_expires_at FROM accelerator.initialization_operations WHERE operation_id=$1;

-- name: LatestInitializationOperationData :one
SELECT payload,lease_owner,lease_epoch,lease_expires_at FROM accelerator.initialization_operations WHERE connection_ref=$1 ORDER BY created_at DESC,operation_id DESC LIMIT 1;

-- name: LockInitializationOperationData :one
SELECT payload,lease_owner,lease_epoch,lease_expires_at FROM accelerator.initialization_operations WHERE operation_id=$1 FOR UPDATE;

-- name: InsertInitializationOperation :exec
INSERT INTO accelerator.initialization_operations(operation_id,connection_ref,version,state,lease_owner,lease_epoch,lease_expires_at,created_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: SaveInitializationOperationCAS :execrows
UPDATE accelerator.initialization_operations SET version=$1,state=$2,lease_owner=$3,lease_epoch=$4,lease_expires_at=$5,payload=$6 WHERE operation_id=$7 AND version=$8 AND lease_epoch=$9;

-- name: SaveInitializationStep :exec
INSERT INTO accelerator.initialization_steps(step_id,operation_id,object_key,step_kind,step_order,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(step_id) DO UPDATE SET payload=EXCLUDED.payload WHERE accelerator.initialization_steps.operation_id=EXCLUDED.operation_id AND accelerator.initialization_steps.object_key=EXCLUDED.object_key AND accelerator.initialization_steps.step_kind=EXCLUDED.step_kind AND accelerator.initialization_steps.step_order=EXCLUDED.step_order;

-- name: InitializationWorkData :many
SELECT operation_id FROM accelerator.initialization_operations WHERE connection_ref=ANY($1::text[]) AND state IN ('ACCEPTED','RUNNING') AND lease_expires_at<=clock_timestamp() ORDER BY created_at,operation_id LIMIT $2;
