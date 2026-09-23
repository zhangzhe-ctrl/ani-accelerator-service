-- Upgrade existing v1 data transactionally. Invalid legacy JSON aborts the
-- migration; repair from authoritative facts, never coerce tenant identities.
BEGIN;
SELECT pg_advisory_xact_lock(731903231);
DO $migration$
DECLARE relation regclass; policy_name text;
BEGIN
  FOR relation IN SELECT c.oid::regclass FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='accelerator' AND c.relkind='r' LOOP
    EXECUTE format('ALTER TABLE %s DISABLE ROW LEVEL SECURITY', relation);
    EXECUTE format('ALTER TABLE %s NO FORCE ROW LEVEL SECURITY', relation);
    FOR policy_name IN SELECT polname FROM pg_policy WHERE polrelid=relation LOOP
      EXECUTE format('DROP POLICY %I ON %s', policy_name, relation);
    END LOOP;
  END LOOP;
END $migration$;
ALTER TABLE accelerator.clusters ADD CONSTRAINT cluster_identity_json CHECK ((payload->>'cluster_id'=cluster_id::text AND payload->>'connection_ref'=connection_ref AND (payload->>'version')::bigint=version) IS TRUE);
ALTER TABLE accelerator.pools ADD CONSTRAINT pool_identity_json CHECK ((payload->>'pool_id'=pool_id::text AND payload->>'cluster_id'=cluster_id::text) IS TRUE);
ALTER TABLE accelerator.supply_groups ADD CONSTRAINT group_identity_json CHECK ((payload->>'group_id'=group_id::text AND payload->>'cluster_id'=cluster_id::text AND payload->>'pool_id'=pool_id::text AND (payload->>'version')::bigint=version AND (payload->>'mode')::integer=mode AND payload->'baseline'->>'baseline_digest'=baseline_digest) IS TRUE);
ALTER TABLE accelerator.profiles ADD CONSTRAINT profile_identity_json CHECK ((payload->>'profile_id'=profile_id::text AND (payload->>'profile_version')::bigint=profile_version AND payload->'spec'->>'group_id'=group_id::text AND (payload->'spec'->>'mode')::integer=mode) IS TRUE);
ALTER TABLE accelerator.usages ADD CONSTRAINT usage_identity_json CHECK ((tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid AND payload->'ref'->>'tenant_id'=tenant_id::text AND payload->'ref'->>'owner_service'=owner_service AND payload->'ref'->>'resource_id'=resource_id::text AND payload->'ref'->>'create_operation_id'=create_operation_id::text AND payload->'plan'->'request'->>'profile_id'=profile_id::text AND (payload->'plan'->'request'->>'profile_version')::bigint=profile_version AND payload->'plan'->>'resolution_digest'=plan_digest AND (payload->>'revision')::integer=revision AND (payload->>'state')::integer=revision AND (payload->>'source_operation_created_at')::timestamptz=source_created_at) IS TRUE);
ALTER TABLE accelerator.bindings ADD CONSTRAINT binding_identity_json CHECK ((payload->>'binding_id'=binding_id::text AND payload->'pod'->>'cluster_id'=cluster_id::text AND payload->'pod'->>'uid'=pod_uid AND payload->'pod'->>'container_name'=container_name AND payload->>'physical_device_id'=device_id::text) IS TRUE);
ALTER TABLE accelerator.bindings ADD CONSTRAINT binding_tenant_json CHECK (
  (tenant_id IS NULL AND owner_service IS NULL AND resource_id IS NULL AND create_operation_id IS NULL AND (payload->'verified_usage_ref' IS NULL OR payload->'verified_usage_ref'='null'::jsonb))
  OR (tenant_id IS NOT NULL AND (payload->'verified_usage_ref'->>'tenant_id'=tenant_id::text AND payload->'verified_usage_ref'->>'owner_service'=owner_service AND payload->'verified_usage_ref'->>'resource_id'=resource_id::text AND payload->'verified_usage_ref'->>'create_operation_id'=create_operation_id::text) IS TRUE)
);
ALTER TABLE accelerator.devices ADD CONSTRAINT device_identity_json CHECK ((payload->'device'->>'device_id'=device_id::text AND payload->'device'->>'cluster_id'=cluster_id::text AND (NULLIF(payload->'device'->>'group_id','') IS NOT DISTINCT FROM group_id::text) AND payload->'device'->'node'->>'uid'=node_uid AND payload->'device'->>'vendor_device_id'=vendor_device_id) IS TRUE);
-- Claims and release evidence are global inventory facts. Signed owner claims
-- may arrive before usages; explicit generated scope prevents JSON-only tenant
-- search and cannot be overridden independently of the verified payload.
ALTER TABLE accelerator.claims ADD COLUMN tenant_id uuid GENERATED ALWAYS AS ((payload->'owner_ref'->>'tenant_id')::uuid) STORED,
 ADD COLUMN owner_service text GENERATED ALWAYS AS (payload->'owner_ref'->>'owner_service') STORED,
 ADD COLUMN resource_id uuid GENERATED ALWAYS AS ((payload->'owner_ref'->>'resource_id')::uuid) STORED,
 ADD COLUMN create_operation_id uuid GENERATED ALWAYS AS ((payload->'owner_ref'->>'create_operation_id')::uuid) STORED,
 ADD CONSTRAINT claim_identity_json CHECK ((payload->'pod'->>'cluster_id'=cluster_id::text AND payload->'pod'->>'uid'=pod_uid AND payload->'pod'->>'container_name'=container_name AND payload->>'device_id'=device_id::text AND payload->>'source'=source) IS TRUE),
 ADD CONSTRAINT claim_owner_json CHECK ((payload->'owner_ref' IS NULL OR payload->'owner_ref'='null'::jsonb) OR (tenant_id IS NOT NULL AND tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid AND owner_service='ani-inference' AND resource_id IS NOT NULL AND create_operation_id IS NOT NULL AND payload->>'plan_digest' ~ '^[0-9a-f]{64}$') IS TRUE);
ALTER TABLE accelerator.release_proofs ADD COLUMN tenant_id uuid GENERATED ALWAYS AS ((payload->'owner_ref'->>'tenant_id')::uuid) STORED,
 ADD COLUMN owner_service text GENERATED ALWAYS AS (payload->'owner_ref'->>'owner_service') STORED,
 ADD COLUMN resource_id uuid GENERATED ALWAYS AS ((payload->'owner_ref'->>'resource_id')::uuid) STORED,
 ADD COLUMN create_operation_id uuid GENERATED ALWAYS AS ((payload->'owner_ref'->>'create_operation_id')::uuid) STORED,
 ADD CONSTRAINT release_identity_json CHECK ((payload->'pod'->>'cluster_id'=cluster_id::text AND payload->'pod'->>'uid'=pod_uid AND payload->'pod'->>'container_name'=container_name AND payload->>'device_id'=device_id::text) IS TRUE),
 ADD CONSTRAINT release_owner_json CHECK ((payload->'owner_ref' IS NULL OR payload->'owner_ref'='null'::jsonb) OR (tenant_id IS NOT NULL AND tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid AND owner_service='ani-inference' AND resource_id IS NOT NULL AND create_operation_id IS NOT NULL AND payload->>'plan_digest' ~ '^[0-9a-f]{64}$') IS TRUE);
ALTER TABLE accelerator.source_watermarks ADD CONSTRAINT watermark_identity_json CHECK ((payload->>'cluster_id'=cluster_id::text AND payload->>'node_uid'=node_uid AND payload->>'source'=source) IS TRUE);
DROP INDEX accelerator.claims_owner_ref;
CREATE INDEX claims_owner_ref ON accelerator.claims(tenant_id,owner_service,resource_id,create_operation_id,cluster_id);
-- SECURITY INVOKER: current_user is always the connecting runtime principal.
CREATE FUNCTION accelerator.runtime_restricted() RETURNS boolean LANGUAGE SQL STABLE SECURITY INVOKER AS $function$
 SELECT NOT (r.rolsuper OR r.rolbypassrls OR r.rolcreatedb OR r.rolcreaterole
 OR has_database_privilege(current_user,current_database(),'CREATE')
 OR has_database_privilege(current_user,current_database(),'TEMPORARY')
 OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname NOT LIKE 'pg_%' AND has_schema_privilege(current_user,n.oid,'CREATE'))
 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='accelerator' AND (pg_has_role(current_user,c.relowner,'USAGE') OR c.relrowsecurity OR c.relforcerowsecurity))
 OR EXISTS(SELECT 1 FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='accelerator'))
 FROM pg_roles r WHERE r.rolname=current_user;
$function$;
INSERT INTO accelerator.schema_migrations(version) VALUES(2);
COMMIT;
