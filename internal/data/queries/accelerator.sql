-- name: LockWrite1 :exec
SELECT pg_advisory_xact_lock(hashtextextended($1,731903231));

-- name: RememberWrite1 :exec
INSERT INTO accelerator.admin_idempotency(actor_digest,method,idempotency_key,payload_digest,result) VALUES($1,$2,$3,$4,$5);

-- name: ClusterData1 :one
SELECT payload FROM accelerator.clusters WHERE cluster_id=$1;

-- name: ClustersData1 :many
SELECT payload FROM accelerator.clusters WHERE connection_ref=ANY($1::text[]) AND cluster_id>COALESCE(NULLIF($2,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY cluster_id LIMIT $3;

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

-- name: DevicesData1 :many
SELECT payload FROM accelerator.devices WHERE inventory_present AND cluster_id=$1 AND ($2='' OR group_id=NULLIF($2,'')::uuid) AND device_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY device_id LIMIT $4;

-- name: BindingsData1 :many
SELECT payload FROM accelerator.bindings WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND create_operation_id=$4 AND binding_id>COALESCE(NULLIF($5,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY binding_id LIMIT $6;

-- name: BindingsData2 :many
SELECT payload FROM accelerator.bindings WHERE cluster_id=$1 AND ($2='' OR device_id=NULLIF($2,'')::uuid) AND binding_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY binding_id LIMIT $4;

-- name: UsageRevisionDigest :one
SELECT payload_digest FROM accelerator.usage_revisions WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3 AND revision=$4;

-- name: DevicePresent :one
SELECT EXISTS(SELECT 1 FROM accelerator.devices WHERE cluster_id=$1 AND device_id=$2);

-- name: RuntimeRestricted :one
SELECT accelerator.runtime_restricted()::boolean AS restricted;

-- name: SchemaVersion :one
SELECT COALESCE(max(version),0)::integer AS version FROM accelerator.schema_migrations;

-- name: UpdateProfileCAS :execrows
UPDATE accelerator.profiles SET payload=$1 WHERE profile_id=$2 AND (payload->>'revision')::bigint=$3::bigint;

-- name: CollectionStateData :one
SELECT observation_revision,current_observation FROM accelerator.clusters WHERE cluster_id=$1::uuid;

-- name: LockCollectionStateData :one
SELECT observation_revision,current_observation FROM accelerator.clusters WHERE cluster_id=$1::uuid FOR UPDATE;

-- name: SaveCollectionStateCAS :execrows
UPDATE accelerator.clusters SET observation_revision=observation_revision+1,current_observation=$3::jsonb WHERE cluster_id=$1::uuid AND observation_revision=$2::bigint;

-- name: SaveObservationWrite2 :execrows
INSERT INTO accelerator.devices(device_id,cluster_id,group_id,node_uid,vendor_device_id,observed_at,payload,inventory_present)
VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,true)
ON CONFLICT (device_id) DO UPDATE SET group_id=EXCLUDED.group_id,observed_at=EXCLUDED.observed_at,payload=EXCLUDED.payload,inventory_present=true
WHERE accelerator.devices.cluster_id=EXCLUDED.cluster_id AND accelerator.devices.node_uid=EXCLUDED.node_uid AND accelerator.devices.vendor_device_id=EXCLUDED.vendor_device_id;

-- name: RetireMissingNodeDevices :exec
UPDATE accelerator.devices SET inventory_present=false
WHERE cluster_id=$1::uuid AND inventory_present AND NOT(node_uid=ANY($2::text[]));

-- name: RetireMissingInventoryDevices :exec
UPDATE accelerator.devices SET inventory_present=false
WHERE cluster_id=$1::uuid AND inventory_present AND node_uid=ANY($2::text[]) AND NOT(device_id=ANY($3::uuid[]));

-- name: MarkUnobservedDevicesUnknown :exec
UPDATE accelerator.devices
SET payload=jsonb_set(jsonb_set(payload,'{device,physical_memory_bytes}','0'::jsonb,true),'{device,observation}',
 jsonb_build_object('complete',false,
  'observed_at',COALESCE(payload#>>'{device,observation,observed_at}',''),
  'received_at',$3::text,'issues',jsonb_build_array('SOURCE_UNKNOWN'),
  'snapshot_ref',COALESCE(payload#>>'{device,observation,snapshot_ref}','')),true)
WHERE cluster_id=$1::uuid AND inventory_present AND NOT(device_id=ANY($2::uuid[]));

-- name: CurrentBindingByKeyData :one
SELECT payload FROM accelerator.bindings WHERE cluster_id=$1 AND pod_uid=$2 AND container_name=$3 AND device_id=$4;

-- name: UpsertCurrentBindingWrite :exec
INSERT INTO accelerator.bindings(binding_id,cluster_id,pod_uid,container_name,device_id,tenant_id,owner_service,resource_id,create_operation_id,payload,owner_candidate,last_observation_revision)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (cluster_id,pod_uid,container_name,device_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,owner_service=EXCLUDED.owner_service,resource_id=EXCLUDED.resource_id,create_operation_id=EXCLUDED.create_operation_id,payload=EXCLUDED.payload,owner_candidate=EXCLUDED.owner_candidate,last_observation_revision=EXCLUDED.last_observation_revision;

-- name: MarkIncompleteCurrentBindings :exec
UPDATE accelerator.bindings
SET last_observation_revision=$3::bigint,
 payload=jsonb_set(jsonb_set(payload,'{state}','3'::jsonb,true),'{observation}',
  jsonb_build_object('complete',false,
   'observed_at',COALESCE(payload#>>'{observation,observed_at}',''),
   'received_at',$4::text,'issues',jsonb_build_array('SOURCE_UNKNOWN'),
   'snapshot_ref',COALESCE(payload#>>'{observation,snapshot_ref}','')),true)
WHERE cluster_id=$1::uuid AND allocation_state IN (1,3) AND pod_uid=ANY($2::text[]);

-- name: EndMissingCurrentBindings :exec
UPDATE accelerator.bindings
SET last_observation_revision=$2::bigint,
 payload=jsonb_set(jsonb_set(jsonb_set(payload,'{state}','2'::jsonb,true),'{evidence_ref}',to_jsonb($5::text),true),'{observation}',
  jsonb_build_object('complete',true,'observed_at',$3::text,'received_at',$4::text,
   'issues','[]'::jsonb,'snapshot_ref',$5::text),true)
WHERE cluster_id=$1::uuid AND allocation_state IN (1,3) AND last_observation_revision<>$2::bigint;

-- name: LinkPendingBindingOwners :exec
UPDATE accelerator.bindings
SET tenant_id=$1::uuid,owner_service=$2::text,resource_id=$3::uuid,create_operation_id=$4::uuid,
 payload=jsonb_set(payload,'{verified_usage_ref}',$8::jsonb,true)
WHERE tenant_id IS NULL AND owner_candidate IS NOT NULL
 AND cluster_id=$5::uuid AND container_name=$6::text
 AND owner_candidate->'ref'->>'tenant_id'=($1::uuid)::text
 AND owner_candidate->'ref'->>'owner_service'=$2::text
 AND owner_candidate->'ref'->>'resource_id'=($3::uuid)::text
 AND owner_candidate->'ref'->>'create_operation_id'=($4::uuid)::text
 AND owner_candidate->>'execution_digest'=$7::text;

-- name: CurrentDeviceBindingsData :many
SELECT payload FROM accelerator.bindings WHERE device_id=$1::uuid AND allocation_state IN (1,3) ORDER BY binding_id;

-- name: CurrentBindingMetricsData :many
SELECT b.payload AS binding_payload,d.payload AS device_payload
FROM accelerator.bindings b
JOIN accelerator.clusters c ON c.cluster_id=b.cluster_id
JOIN accelerator.devices d ON d.cluster_id=b.cluster_id AND d.device_id=b.device_id AND d.inventory_present
WHERE c.connection_ref=ANY($1::text[])
 AND b.allocation_state=1 AND b.tenant_id IS NOT NULL
 AND b.payload->'verified_usage_ref' IS NOT NULL
 AND b.payload#>>'{observation,complete}'='true'
 AND NULLIF(b.payload#>>'{observation,observed_at}','')::timestamptz>=$2::timestamptz
 AND NULLIF(b.payload#>>'{observation,observed_at}','')::timestamptz<=$3::timestamptz
ORDER BY b.cluster_id,b.binding_id;
