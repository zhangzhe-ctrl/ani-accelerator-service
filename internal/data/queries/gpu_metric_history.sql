-- name: CaptureGpuMetricScope :exec
INSERT INTO accelerator.gpu_metric_history(event_id,binding_id,source_key,signature,payload)
SELECT $1,$2,$3,$4,$5
WHERE COALESCE((SELECT signature FROM accelerator.gpu_metric_history WHERE binding_id=$2 ORDER BY revision DESC LIMIT 1),'')<>$4;

-- name: GpuMetricIndex :one
SELECT (SELECT count(*)::bigint FROM accelerator.gpu_metric_history) AS revision, started_at FROM accelerator.gpu_metric_index WHERE singleton=true;

-- name: GpuMetricEvents :many
WITH requested_keys AS (
 SELECT DISTINCT h.source_key FROM accelerator.gpu_metric_history h
 JOIN accelerator.clusters c ON c.cluster_id=(h.payload->'scope'->>'cluster_id')::uuid
 WHERE c.connection_ref=ANY($1::text[])
 AND ($2::text='' OR c.cluster_id::text=$2)
 AND ($3::text='' OR h.payload->'scope'->'ref'->>'tenant_id'=$3)
 AND ($4::text='' OR h.payload->'scope'->'ref'->>'owner_service'=$4)
 AND ($5::text='' OR h.payload->'scope'->'ref'->>'resource_id'=$5)
 AND ($6::text='' OR h.payload->'scope'->'ref'->>'create_operation_id'=$6)
)
SELECT h.revision,h.source_key,h.recorded_at,h.payload
FROM accelerator.gpu_metric_history h JOIN requested_keys k ON h.source_key=k.source_key
ORDER BY h.revision LIMIT 8193;
