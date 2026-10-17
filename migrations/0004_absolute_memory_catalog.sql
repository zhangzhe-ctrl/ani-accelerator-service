-- One-time explicit cutover after old active GPU work has been closed.
-- This migration never interprets old payloads as the new contract.
BEGIN;
SELECT pg_advisory_xact_lock(731903231);
ALTER TABLE accelerator.profiles DROP CONSTRAINT profiles_group_id_key;
ALTER TABLE accelerator.usages RENAME COLUMN plan_digest TO execution_digest;
ALTER TABLE accelerator.usages DROP CONSTRAINT usage_identity_json;
ALTER TABLE accelerator.usages ADD CONSTRAINT usage_identity_json CHECK ((
 tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid
 AND payload->'ref'->>'tenant_id'=tenant_id::text
 AND payload->'ref'->>'owner_service'=owner_service
 AND payload->'ref'->>'resource_id'=resource_id::text
 AND payload->'ref'->>'create_operation_id'=create_operation_id::text
 AND payload->>'profile_id'=profile_id::text
 AND (payload->>'profile_version')::bigint=profile_version
 AND payload->>'execution_digest'=execution_digest
 AND (payload->>'revision')::integer=revision
 AND (payload->>'state')::integer=revision
 AND (payload->>'source_operation_created_at')::timestamptz=source_created_at
 AND NOT payload ? 'plan') IS TRUE) NOT VALID;
ALTER TABLE accelerator.claims DROP CONSTRAINT claim_owner_json;
ALTER TABLE accelerator.claims ADD CONSTRAINT claim_owner_json CHECK (
 (payload->'owner_ref' IS NULL OR payload->'owner_ref'='null'::jsonb)
 OR (tenant_id IS NOT NULL AND tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid
 AND owner_service IN ('ani-inference','ani-resource-service')
 AND resource_id IS NOT NULL AND create_operation_id IS NOT NULL
 AND payload->>'execution_digest' ~ '^[0-9a-f]{64}$'
 AND NOT payload ? 'plan_digest') IS TRUE) NOT VALID;
ALTER TABLE accelerator.release_proofs DROP CONSTRAINT release_owner_json;
ALTER TABLE accelerator.release_proofs ADD CONSTRAINT release_owner_json CHECK (
 (payload->'owner_ref' IS NULL OR payload->'owner_ref'='null'::jsonb)
 OR (tenant_id IS NOT NULL AND tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid
 AND owner_service IN ('ani-inference','ani-resource-service')
 AND resource_id IS NOT NULL AND create_operation_id IS NOT NULL
 AND payload->>'execution_digest' ~ '^[0-9a-f]{64}$'
 AND NOT payload ? 'plan_digest') IS TRUE) NOT VALID;
-- Only catalog state and revision can change; resource parameters are immutable.
CREATE FUNCTION accelerator.profile_catalog_update() RETURNS trigger LANGUAGE plpgsql AS $body$
BEGIN
 IF (NEW.payload - 'state' - 'revision') IS DISTINCT FROM (OLD.payload - 'state' - 'revision')
 OR ((NEW.payload->>'revision')::bigint = (OLD.payload->>'revision')::bigint + 1
 AND (NEW.payload->>'state')::integer IN (1,2,3)
 AND (OLD.payload->>'state')::integer <> 3) IS NOT TRUE THEN
  RAISE EXCEPTION 'immutable profile parameters or invalid catalog revision' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $body$;
CREATE TRIGGER profile_catalog_update BEFORE UPDATE ON accelerator.profiles
 FOR EACH ROW EXECUTE FUNCTION accelerator.profile_catalog_update();
CREATE INDEX profiles_group_catalog ON accelerator.profiles(group_id,profile_id);
INSERT INTO accelerator.schema_migrations(version) VALUES(4);
COMMIT;
