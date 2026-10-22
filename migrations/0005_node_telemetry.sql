BEGIN;
SELECT pg_advisory_xact_lock(731903231);
CREATE TABLE accelerator.node_telemetry_state (
 connection_ref text NOT NULL,
 node_uid text NOT NULL,
 epoch bigint NOT NULL CHECK(epoch > 0),
 active_log_id text NOT NULL,
 last_sequence bigint NOT NULL DEFAULT 0 CHECK(last_sequence >= 0),
 last_payload_digest text NOT NULL DEFAULT '',
 current_node_revision bigint NOT NULL DEFAULT 1 CHECK(current_node_revision > 0),
 consumed_node_revision bigint NOT NULL DEFAULT 0 CHECK(consumed_node_revision >= 0 AND consumed_node_revision <= current_node_revision),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload) = 'object'),
 PRIMARY KEY(connection_ref,node_uid)
);
CREATE TABLE accelerator.node_runtime_attempts (
 connection_ref text NOT NULL,
 node_uid text NOT NULL,
 container_id text NOT NULL CHECK(container_id <> ''),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload) = 'object'),
 PRIMARY KEY(connection_ref,node_uid,container_id),
 FOREIGN KEY(connection_ref,node_uid) REFERENCES accelerator.node_telemetry_state(connection_ref,node_uid)
);
INSERT INTO accelerator.schema_migrations(version) VALUES(5);
COMMIT;
