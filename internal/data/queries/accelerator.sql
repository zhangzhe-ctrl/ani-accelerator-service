-- name: LockWrite1 :exec
SELECT pg_advisory_xact_lock(hashtextextended($1,731903231));

-- name: RememberWrite1 :exec
INSERT INTO accelerator.admin_idempotency(actor_digest,method,idempotency_key,payload_digest,result) VALUES($1,$2,$3,$4,$5);

-- name: ClusterData1 :one
SELECT payload FROM accelerator.clusters WHERE cluster_id=$1;

-- name: ClustersData1 :many
SELECT payload FROM accelerator.clusters WHERE ($1 OR cluster_id::text=ANY($2::text[])) AND cluster_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY cluster_id LIMIT $4;

-- name: PoolData1 :one
SELECT payload FROM accelerator.pools WHERE pool_id=$1;

-- name: PoolsData1 :many
SELECT payload FROM accelerator.pools WHERE cluster_id=$1 AND pool_id>COALESCE(NULLIF($2,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY pool_id LIMIT $3;

-- name: GroupData1 :one
SELECT payload FROM accelerator.supply_groups WHERE group_id=$1;

-- name: GroupsData1 :many
SELECT payload FROM accelerator.supply_groups WHERE pool_id=$1 AND group_id>COALESCE(NULLIF($2,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY group_id LIMIT $3;

-- name: ProfileData1 :one
SELECT payload FROM accelerator.profiles WHERE profile_id=$1 AND profile_version=$2;

-- name: ProfilesData1 :many
SELECT payload FROM accelerator.profiles WHERE cluster_id=$1 AND ($2=0 OR mode=$2) AND profile_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY profile_id LIMIT $4;

-- name: InsertClusterWrite1 :exec
INSERT INTO accelerator.clusters(cluster_id,connection_ref,version,payload) VALUES($1,$2,$3,$4);

-- name: InsertPoolWrite1 :exec
INSERT INTO accelerator.pools(pool_id,cluster_id,payload) VALUES($1,$2,$3);

-- name: InsertGroupWrite1 :exec
INSERT INTO accelerator.supply_groups(group_id,cluster_id,pool_id,mode,version,baseline_digest,payload) VALUES($1,$2,$3,$4,$5,$6,$7);

-- name: InsertGroupWrite2 :exec
INSERT INTO accelerator.group_nodes(cluster_id,node_uid,node_name,group_id) VALUES($1,$2,$3,$4);

-- name: InsertProfileWrite1 :exec
INSERT INTO accelerator.profiles(profile_id,profile_version,cluster_id,group_id,mode,payload) VALUES($1,$2,$3,$4,$5,$6);

-- name: ReplayRecord :one
SELECT payload_digest,result FROM accelerator.admin_idempotency WHERE actor_digest=$1 AND method=$2 AND idempotency_key=$3;

-- name: UpdateGroupCAS :execrows
UPDATE accelerator.supply_groups SET version=$1,payload=$2 WHERE group_id=$3 AND version=$4 AND baseline_digest=$5;

-- name: UsageData1 :one
SELECT payload FROM accelerator.usages WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND create_operation_id=$4;

-- name: UsagesData1 :many
SELECT payload FROM accelerator.usages WHERE tenant_id=$1 AND ($2='' OR owner_service=$2) AND ($3=0 OR revision=$3) AND resource_id>COALESCE(NULLIF($4,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY resource_id LIMIT $5;

-- name: SaveUsageData1 :one
SELECT payload FROM accelerator.usages WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 FOR UPDATE;

-- name: SaveUsageWrite2 :exec
INSERT INTO accelerator.usages(tenant_id,owner_service,resource_id,create_operation_id,profile_id,profile_version,revision,execution_digest,source_created_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- name: SaveUsageWrite3 :exec
UPDATE accelerator.usages SET revision=$1,payload=$2 WHERE tenant_id=$3 AND owner_service=$4 AND resource_id=$5 AND create_operation_id=$6;

-- name: SaveUsageWrite4 :exec
INSERT INTO accelerator.usage_revisions(tenant_id,owner_service,resource_id,create_operation_id,revision,payload_digest) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING;

-- name: SaveUsageData5 :many
SELECT payload FROM accelerator.claims WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND create_operation_id=$4 AND cluster_id=$5 ORDER BY pod_uid,container_name,device_id,source;

-- name: DevicesData1 :many
SELECT payload FROM accelerator.devices WHERE cluster_id=$1 AND ($2='' OR group_id=NULLIF($2,'')::uuid) AND device_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY device_id LIMIT $4;

-- name: BindingsData1 :many
SELECT payload FROM accelerator.bindings WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND create_operation_id=$4 AND binding_id>COALESCE(NULLIF($5,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY binding_id LIMIT $6;

-- name: BindingsData2 :many
SELECT payload FROM accelerator.bindings WHERE cluster_id=$1 AND ($2='' OR device_id=NULLIF($2,'')::uuid) AND binding_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY binding_id LIMIT $4;

-- name: DeviceBindingsData1 :many
SELECT payload FROM accelerator.bindings WHERE device_id=$1 ORDER BY binding_id;

-- name: SourceHealthData1 :many
SELECT payload FROM accelerator.source_watermarks WHERE cluster_id=$1 ORDER BY node_uid,source;

-- name: PodEvidenceData1 :many
SELECT payload FROM accelerator.claims WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 ORDER BY device_id,source;

-- name: PodEvidenceData2 :many
SELECT payload FROM accelerator.release_proofs WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 ORDER BY device_id;

-- name: UsageRevisionDigest :one
SELECT payload_digest FROM accelerator.usage_revisions WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND revision=$4;

-- name: DevicePresent :one
SELECT EXISTS(SELECT 1 FROM accelerator.devices WHERE cluster_id=$1 AND device_id=$2);

-- name: SaveObservationData1 :one
SELECT payload FROM accelerator.devices WHERE device_id=$1;

-- name: SaveObservationWrite2 :exec
INSERT INTO accelerator.devices(device_id,cluster_id,group_id,node_uid,vendor_device_id,observed_at,payload) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7) ON CONFLICT (device_id) DO UPDATE SET group_id=EXCLUDED.group_id,observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload WHERE accelerator.devices.cluster_id=EXCLUDED.cluster_id AND accelerator.devices.node_uid=EXCLUDED.node_uid AND accelerator.devices.vendor_device_id=EXCLUDED.vendor_device_id AND accelerator.devices.observed_at<=EXCLUDED.observed_at;

-- name: SaveObservationData3 :one
SELECT payload FROM accelerator.claims WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4 AND source=$5;

-- name: SaveObservationWrite4 :exec
INSERT INTO accelerator.claims(cluster_id,pod_uid,container_name,device_id,source,observed_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (cluster_id,pod_uid,container_name,device_id,source) DO UPDATE SET observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload WHERE accelerator.claims.observed_at<EXCLUDED.observed_at;

-- name: SaveObservationWrite5 :exec
INSERT INTO accelerator.release_proofs(cluster_id,pod_uid,container_name,device_id,observed_at,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (cluster_id,pod_uid,container_name,device_id) DO UPDATE SET observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload WHERE accelerator.release_proofs.observed_at<EXCLUDED.observed_at;

-- name: SaveObservationData6 :one
SELECT payload FROM accelerator.source_watermarks WHERE cluster_id=$1 AND node_uid=$2 AND source=$3;

-- name: SaveObservationWrite7 :exec
INSERT INTO accelerator.source_watermarks(cluster_id,node_uid,source,observed_at,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT (cluster_id,node_uid,source) DO UPDATE SET observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload WHERE (accelerator.source_watermarks.payload->>'received_at')::timestamptz <= (EXCLUDED.payload->>'received_at')::timestamptz;

-- name: SaveObservationData8 :many
SELECT DISTINCT ON (pod_uid,container_name,device_id) payload FROM accelerator.claims WHERE cluster_id=$1 ORDER BY pod_uid,container_name,device_id,source;

-- name: ReconcileBindingData1 :many
SELECT payload FROM accelerator.claims WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4 ORDER BY source;

-- name: ReconcileBindingData2 :one
SELECT payload FROM accelerator.release_proofs WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4;

-- name: ReconcileBindingData3 :one
SELECT payload FROM accelerator.bindings WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4;

-- name: ReconcileBindingWrite4 :exec
INSERT INTO accelerator.bindings(binding_id,cluster_id,pod_uid,container_name,device_id,tenant_id,owner_service,resource_id,create_operation_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (cluster_id,pod_uid,container_name,device_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,owner_service=EXCLUDED.owner_service,resource_id=EXCLUDED.resource_id,create_operation_id=EXCLUDED.create_operation_id,payload=EXCLUDED.payload;

-- name: RuntimeRestricted :one
SELECT accelerator.runtime_restricted()::boolean AS restricted;

-- name: SchemaVersion :one
SELECT COALESCE(max(version),0)::integer AS version FROM accelerator.schema_migrations;

-- name: UpdateProfileCAS :execrows
UPDATE accelerator.profiles SET payload=$1 WHERE profile_id=$2 AND (payload->>'revision')::bigint=$3::bigint;
