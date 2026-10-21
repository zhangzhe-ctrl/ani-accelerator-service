-- Extend only the reviewed finite owner set; preserve all existing scope rules.
BEGIN;
SELECT pg_advisory_xact_lock(731903231);
ALTER TABLE accelerator.usages DROP CONSTRAINT usages_owner_service_check,
 ADD CONSTRAINT usages_owner_service_check CHECK (owner_service IN ('ani-inference','ani-resource-service','ani-modeldev-service'));
ALTER TABLE accelerator.claims DROP CONSTRAINT claim_owner_json,
 ADD CONSTRAINT claim_owner_json CHECK (
  (payload->'owner_ref' IS NULL OR payload->'owner_ref'='null'::jsonb)
  OR (tenant_id IS NOT NULL AND tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid
      AND owner_service IN ('ani-inference','ani-resource-service','ani-modeldev-service')
      AND resource_id IS NOT NULL AND create_operation_id IS NOT NULL
      AND payload->>'execution_digest' ~ '^[0-9a-f]{64}$'
      AND NOT payload ? 'plan_digest') IS TRUE) NOT VALID;
ALTER TABLE accelerator.release_proofs DROP CONSTRAINT release_owner_json,
 ADD CONSTRAINT release_owner_json CHECK (
  (payload->'owner_ref' IS NULL OR payload->'owner_ref'='null'::jsonb)
  OR (tenant_id IS NOT NULL AND tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid
      AND owner_service IN ('ani-inference','ani-resource-service','ani-modeldev-service')
      AND resource_id IS NOT NULL AND create_operation_id IS NOT NULL
      AND payload->>'execution_digest' ~ '^[0-9a-f]{64}$'
      AND NOT payload ? 'plan_digest') IS TRUE) NOT VALID;
INSERT INTO accelerator.schema_migrations(version) VALUES(7);
COMMIT;
