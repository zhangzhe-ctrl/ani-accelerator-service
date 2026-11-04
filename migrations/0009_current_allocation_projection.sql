-- Stop all schema-8 writers before this explicit, transactional migration.
-- Existing occupancy stays unknown until a complete Kubernetes observation.
BEGIN;
SELECT pg_advisory_xact_lock(731903231);

ALTER TABLE accelerator.clusters
 ADD COLUMN observation_revision bigint NOT NULL DEFAULT 0 CHECK (observation_revision >= 0),
 ADD COLUMN current_observation jsonb NOT NULL DEFAULT
 '{"observation":{"complete":false,"observed_at":"","received_at":"","issues":["SOURCE_UNKNOWN"],"snapshot_ref":""},"pending_by_group":{}}'::jsonb,
 ADD CONSTRAINT cluster_current_observation_json CHECK (
   (jsonb_typeof(current_observation->'observation') = 'object'
    AND jsonb_typeof(current_observation->'pending_by_group') = 'object') IS TRUE
 );

ALTER TABLE accelerator.bindings
 ADD COLUMN last_observation_revision bigint NOT NULL DEFAULT 0 CHECK (last_observation_revision >= 0),
 ADD COLUMN allocation_state integer GENERATED ALWAYS AS ((payload->>'state')::integer) STORED,
 ADD COLUMN owner_candidate jsonb;
ALTER TABLE accelerator.bindings
 ALTER COLUMN allocation_state SET NOT NULL,
 ADD CONSTRAINT binding_allocation_state CHECK (allocation_state IN (1,2,3)),
 ADD CONSTRAINT binding_owner_candidate_json CHECK (
   owner_candidate IS NULL OR (
     jsonb_typeof(owner_candidate) = 'object'
     AND jsonb_typeof(owner_candidate->'ref') = 'object'
     AND (owner_candidate->'ref'->>'tenant_id')::uuid <> '00000000-0000-0000-0000-000000000000'::uuid
     AND owner_candidate->'ref'->>'owner_service' IN ('ani-inference','ani-resource-service','ani-modeldev-service')
     AND (owner_candidate->'ref'->>'resource_id')::uuid <> '00000000-0000-0000-0000-000000000000'::uuid
     AND (owner_candidate->'ref'->>'create_operation_id')::uuid <> '00000000-0000-0000-0000-000000000000'::uuid
     AND owner_candidate->>'execution_digest' ~ '^[0-9a-f]{64}$'
   ) IS TRUE
 );

ALTER TABLE accelerator.devices
 ADD COLUMN inventory_present boolean NOT NULL DEFAULT true;

UPDATE accelerator.bindings
SET payload = jsonb_set(payload, '{observation}',
    COALESCE(NULLIF(payload->'observation', 'null'::jsonb), '{}'::jsonb)
    || '{"complete":false,"issues":["SOURCE_UNKNOWN"]}'::jsonb, true)
WHERE allocation_state IN (1,3);

CREATE INDEX bindings_current_device
 ON accelerator.bindings(device_id,binding_id) WHERE allocation_state IN (1,3);
CREATE INDEX bindings_current_cluster
 ON accelerator.bindings(cluster_id,pod_uid,container_name,device_id) WHERE allocation_state IN (1,3);
CREATE INDEX bindings_pending_owner
 ON accelerator.bindings(
   cluster_id,
   (owner_candidate->'ref'->>'tenant_id'),
   (owner_candidate->'ref'->>'owner_service'),
   (owner_candidate->'ref'->>'resource_id'),
   (owner_candidate->'ref'->>'create_operation_id'),
   (owner_candidate->>'execution_digest'),
   container_name,binding_id
 ) WHERE tenant_id IS NULL AND owner_candidate IS NOT NULL;
CREATE INDEX devices_current_group
 ON accelerator.devices(cluster_id,group_id,device_id) WHERE inventory_present;
CREATE INDEX devices_current_node
 ON accelerator.devices(cluster_id,node_uid,device_id) WHERE inventory_present;

DROP TABLE accelerator.node_runtime_attempts;
DROP TABLE accelerator.node_telemetry_state;
DROP TABLE accelerator.claims;
DROP TABLE accelerator.release_proofs;
DROP TABLE accelerator.source_watermarks;
DROP TABLE accelerator.gpu_metric_history;
DROP TABLE accelerator.gpu_metric_index;

INSERT INTO accelerator.schema_migrations(version) VALUES(9);
COMMIT;
