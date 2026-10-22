SELECT container_id,xmin::text
FROM accelerator.node_runtime_attempts
WHERE connection_ref=$1 AND node_uid=$2;
