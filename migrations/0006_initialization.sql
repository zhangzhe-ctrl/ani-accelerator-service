BEGIN;
SELECT pg_advisory_xact_lock(731903231);
CREATE TABLE accelerator.initialization_operations (
 operation_id uuid PRIMARY KEY,
 connection_ref text NOT NULL,
 version bigint NOT NULL CHECK(version > 0),
 state text NOT NULL CHECK(state IN ('ACCEPTED','RUNNING','SUCCEEDED','BLOCKED','CONFLICT','FAILED')),
 lease_owner text NOT NULL DEFAULT '',
 lease_epoch bigint NOT NULL DEFAULT 0 CHECK(lease_epoch >= 0),
 lease_expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 CHECK(payload->>'operation_id'=operation_id::text),
 CHECK(payload->>'connection_ref'=connection_ref),
 CHECK((payload->>'version')::bigint=version),
 CHECK(payload->>'state'=state)
);
CREATE INDEX initialization_operations_work ON accelerator.initialization_operations(state,lease_expires_at,created_at);
CREATE TABLE accelerator.initialization_steps (
 step_id uuid PRIMARY KEY,
 operation_id uuid NOT NULL REFERENCES accelerator.initialization_operations(operation_id),
 object_key text NOT NULL,
 step_kind text NOT NULL CHECK(step_kind IN ('VALIDATE','ENSURE_CLUSTER','ENSURE_POOL','ADOPT_GROUP','ENSURE_LABELS','CREATE_PROFILE','VERIFY_OPEN','OPEN_GROUP','COMPLETE')),
 step_order integer NOT NULL CHECK(step_order >= 0),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 UNIQUE(operation_id,object_key,step_kind),
 UNIQUE(operation_id,step_order),
 CHECK(payload->>'step_id'=step_id::text),
 CHECK(payload->>'object_key'=object_key),
 CHECK(payload->>'kind'=step_kind)
);
INSERT INTO accelerator.schema_migrations(version) VALUES(6);
COMMIT;
