-- Monitoring-only history: explicit migration, no automatic startup DDL.
-- No pruning is installed: active relations and unresolved reused source keys
-- must survive indefinitely. Future retention may prune proven retired periods
-- only after 30 days, while preserving all unresolved source conflict facts.
BEGIN;
SELECT pg_advisory_xact_lock(731903231);
CREATE TABLE accelerator.gpu_metric_index (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO accelerator.gpu_metric_index(singleton) VALUES(true);
CREATE TABLE accelerator.gpu_metric_history (
 revision bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 event_id uuid NOT NULL UNIQUE,
 binding_id uuid NOT NULL,
 source_key text NOT NULL,
 signature text NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 payload jsonb NOT NULL,
 tenant_id uuid GENERATED ALWAYS AS ((payload->'scope'->'ref'->>'tenant_id')::uuid) STORED,
 owner_service text GENERATED ALWAYS AS (payload->'scope'->'ref'->>'owner_service') STORED,
 resource_id uuid GENERATED ALWAYS AS ((payload->'scope'->'ref'->>'resource_id')::uuid) STORED,
 create_operation_id uuid GENERATED ALWAYS AS ((payload->'scope'->'ref'->>'create_operation_id')::uuid) STORED,
 FOREIGN KEY (tenant_id,owner_service,resource_id,create_operation_id)
 REFERENCES accelerator.usages(tenant_id,owner_service,resource_id,create_operation_id) MATCH FULL
);
CREATE INDEX gpu_metric_history_binding ON accelerator.gpu_metric_history(binding_id,revision DESC);
CREATE INDEX gpu_metric_history_source ON accelerator.gpu_metric_history(source_key,revision);
CREATE INDEX gpu_metric_history_tenant ON accelerator.gpu_metric_history((payload->'scope'->'ref'->>'tenant_id'));
-- Existing rows are not backdated. Reconciliation records their first verified
-- monitoring evidence after this migration; earlier history remains unavailable.
INSERT INTO accelerator.schema_migrations(version) VALUES(8);
COMMIT;
