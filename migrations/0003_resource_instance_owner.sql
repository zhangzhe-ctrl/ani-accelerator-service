-- Fixed Resource instance owner integration. Historical migrations remain intact.
BEGIN;
SELECT pg_advisory_xact_lock(731903231);
ALTER TABLE accelerator.usages DROP CONSTRAINT usages_owner_service_check,
 ADD CONSTRAINT usages_owner_service_check CHECK (owner_service IN ('ani-inference','ani-resource-service'));
ALTER TABLE accelerator.claims DROP CONSTRAINT claim_owner_json,
 ADD CONSTRAINT claim_owner_json CHECK (
  (payload->'owner_ref' IS NULL OR payload->'owner_ref'='null'::jsonb)
  OR (tenant_id IS NOT NULL AND tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid
      AND owner_service IN ('ani-inference','ani-resource-service')
      AND resource_id IS NOT NULL AND create_operation_id IS NOT NULL
      AND payload->>'plan_digest' ~ '^[0-9a-f]{64}$') IS TRUE);
ALTER TABLE accelerator.release_proofs DROP CONSTRAINT release_owner_json,
 ADD CONSTRAINT release_owner_json CHECK (
  (payload->'owner_ref' IS NULL OR payload->'owner_ref'='null'::jsonb)
  OR (tenant_id IS NOT NULL AND tenant_id<>'00000000-0000-0000-0000-000000000000'::uuid
      AND owner_service IN ('ani-inference','ani-resource-service')
      AND resource_id IS NOT NULL AND create_operation_id IS NOT NULL
      AND payload->>'plan_digest' ~ '^[0-9a-f]{64}$') IS TRUE);
INSERT INTO accelerator.schema_migrations(version) VALUES(3);
COMMIT;
