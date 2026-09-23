-- Explicit, transactional migration. Run as a dedicated migration owner.
-- The application role is granted DML only by the operator after this succeeds.
BEGIN;
SELECT pg_advisory_xact_lock(731903231);
CREATE SCHEMA accelerator;
REVOKE ALL ON SCHEMA accelerator FROM PUBLIC;
CREATE TABLE accelerator.schema_migrations (
  version integer PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE accelerator.clusters (
  cluster_id uuid PRIMARY KEY,
  connection_ref text NOT NULL UNIQUE,
  version bigint NOT NULL CHECK (version > 0),
  payload jsonb NOT NULL,
  CHECK (payload->>'cluster_id' = cluster_id::text)
);
CREATE TABLE accelerator.pools (
  pool_id uuid PRIMARY KEY,
  cluster_id uuid NOT NULL REFERENCES accelerator.clusters(cluster_id),
  payload jsonb NOT NULL,
  UNIQUE (cluster_id, pool_id),
  CHECK (payload->>'pool_id' = pool_id::text AND payload->>'cluster_id' = cluster_id::text)
);
CREATE TABLE accelerator.supply_groups (
  group_id uuid PRIMARY KEY,
  cluster_id uuid NOT NULL,
  pool_id uuid NOT NULL,
  mode integer NOT NULL CHECK (mode IN (1,2)),
  version bigint NOT NULL CHECK (version > 0),
  baseline_digest text NOT NULL CHECK (baseline_digest ~ '^[0-9a-f]{64}$'),
  payload jsonb NOT NULL,
  FOREIGN KEY (cluster_id, pool_id) REFERENCES accelerator.pools(cluster_id, pool_id),
  UNIQUE (cluster_id, group_id),
  CHECK (payload->>'group_id' = group_id::text AND payload->>'cluster_id' = cluster_id::text AND payload->>'pool_id' = pool_id::text)
);
CREATE TABLE accelerator.group_nodes (
  cluster_id uuid NOT NULL,
  node_uid text NOT NULL CHECK (length(node_uid) BETWEEN 1 AND 256),
  node_name text NOT NULL,
  group_id uuid NOT NULL,
  PRIMARY KEY (cluster_id, node_uid),
  UNIQUE (cluster_id, node_name),
  FOREIGN KEY (cluster_id, group_id) REFERENCES accelerator.supply_groups(cluster_id, group_id)
);
CREATE TABLE accelerator.profiles (
  profile_id uuid PRIMARY KEY,
  profile_version bigint NOT NULL CHECK (profile_version = 1),
  cluster_id uuid NOT NULL,
  group_id uuid NOT NULL UNIQUE,
  mode integer NOT NULL CHECK (mode IN (1,2)),
  payload jsonb NOT NULL,
  FOREIGN KEY (cluster_id, group_id) REFERENCES accelerator.supply_groups(cluster_id, group_id),
  UNIQUE (profile_id, profile_version),
  CHECK (payload->>'profile_id' = profile_id::text)
);
CREATE TABLE accelerator.admin_idempotency (
  actor_digest text NOT NULL,
  method text NOT NULL,
  idempotency_key uuid NOT NULL,
  payload_digest text NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
  result jsonb NOT NULL,
  PRIMARY KEY (actor_digest, method, idempotency_key)
);
CREATE TABLE accelerator.usages (
  tenant_id uuid NOT NULL,
  owner_service text NOT NULL CHECK (owner_service = 'ani-inference'),
  resource_id uuid NOT NULL,
  create_operation_id uuid NOT NULL,
  profile_id uuid NOT NULL,
  profile_version bigint NOT NULL,
  revision integer NOT NULL CHECK (revision IN (1,2)),
  plan_digest text NOT NULL CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
  source_created_at timestamptz NOT NULL,
  payload jsonb NOT NULL,
  PRIMARY KEY (tenant_id, owner_service, resource_id),
  UNIQUE (tenant_id, owner_service, resource_id, create_operation_id),
  FOREIGN KEY (profile_id, profile_version) REFERENCES accelerator.profiles(profile_id, profile_version),
  CHECK (payload->'ref'->>'tenant_id' = tenant_id::text AND payload->'ref'->>'resource_id' = resource_id::text AND payload->'ref'->>'create_operation_id' = create_operation_id::text)
);
CREATE TABLE accelerator.usage_revisions (
  tenant_id uuid NOT NULL,
  owner_service text NOT NULL,
  resource_id uuid NOT NULL,
  create_operation_id uuid NOT NULL,
  revision integer NOT NULL CHECK (revision IN (1,2)),
  payload_digest text NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
  PRIMARY KEY (tenant_id, owner_service, resource_id, revision),
  FOREIGN KEY (tenant_id, owner_service, resource_id, create_operation_id)
    REFERENCES accelerator.usages(tenant_id, owner_service, resource_id, create_operation_id)
);
CREATE TABLE accelerator.devices (
  device_id uuid PRIMARY KEY,
  cluster_id uuid NOT NULL REFERENCES accelerator.clusters(cluster_id),
  group_id uuid,
  node_uid text NOT NULL,
  vendor_device_id text NOT NULL,
  observed_at timestamptz NOT NULL,
  payload jsonb NOT NULL,
  UNIQUE (cluster_id, node_uid, vendor_device_id),
  UNIQUE (cluster_id, device_id),
  FOREIGN KEY (cluster_id, group_id) REFERENCES accelerator.supply_groups(cluster_id, group_id),
  CHECK (payload->'device'->>'device_id' = device_id::text AND payload->'device'->>'cluster_id' = cluster_id::text)
);
-- Raw observations belong to the platform inventory. An optional signed owner
-- assertion may precede a tenant projection and is not itself a tenant query.
CREATE TABLE accelerator.claims (
  cluster_id uuid NOT NULL,
  pod_uid text NOT NULL,
  container_name text NOT NULL,
  device_id uuid NOT NULL,
  source text NOT NULL CHECK (source IN ('scheduler','allocation','runtime')),
  observed_at timestamptz NOT NULL,
  payload jsonb NOT NULL,
  PRIMARY KEY (cluster_id, pod_uid, container_name, device_id, source),
  FOREIGN KEY (cluster_id, device_id) REFERENCES accelerator.devices(cluster_id, device_id)
);
CREATE TABLE accelerator.release_proofs (
  cluster_id uuid NOT NULL,
  pod_uid text NOT NULL,
  container_name text NOT NULL,
  device_id uuid NOT NULL,
  observed_at timestamptz NOT NULL,
  payload jsonb NOT NULL,
  PRIMARY KEY (cluster_id, pod_uid, container_name, device_id),
  FOREIGN KEY (cluster_id, device_id) REFERENCES accelerator.devices(cluster_id, device_id)
);
CREATE TABLE accelerator.bindings (
  binding_id uuid PRIMARY KEY,
  cluster_id uuid NOT NULL,
  pod_uid text NOT NULL,
  container_name text NOT NULL,
  device_id uuid NOT NULL,
  tenant_id uuid,
  owner_service text,
  resource_id uuid,
  create_operation_id uuid,
  payload jsonb NOT NULL,
  UNIQUE (cluster_id, pod_uid, container_name, device_id),
  FOREIGN KEY (cluster_id, device_id) REFERENCES accelerator.devices(cluster_id, device_id),
  FOREIGN KEY (tenant_id, owner_service, resource_id, create_operation_id)
    REFERENCES accelerator.usages(tenant_id, owner_service, resource_id, create_operation_id) MATCH FULL,
  CHECK (tenant_id IS NULL OR payload->'verified_usage_ref'->>'tenant_id' = tenant_id::text)
);
CREATE INDEX bindings_tenant_resource ON accelerator.bindings(tenant_id, owner_service, resource_id, create_operation_id, binding_id);
CREATE INDEX claims_owner_ref ON accelerator.claims((payload->'owner_ref'->>'tenant_id'), (payload->'owner_ref'->>'resource_id'));
CREATE TABLE accelerator.source_watermarks (
  cluster_id uuid NOT NULL REFERENCES accelerator.clusters(cluster_id),
  node_uid text NOT NULL,
  source text NOT NULL CHECK (source IN ('kubernetes','podresources','cri')),
  observed_at timestamptz NOT NULL,
  payload jsonb NOT NULL,
  PRIMARY KEY (cluster_id, node_uid, source)
);
INSERT INTO accelerator.schema_migrations(version) VALUES (1);
COMMIT;
