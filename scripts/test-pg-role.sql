-- Explicit test setup, only for a fresh isolated database. No catalog seed.
CREATE ROLE acc_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;
REVOKE CREATE, TEMPORARY ON DATABASE accelerator FROM PUBLIC;
GRANT CONNECT ON DATABASE accelerator TO acc_runtime;
GRANT USAGE ON SCHEMA accelerator TO acc_runtime;
GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA accelerator TO acc_runtime;
REVOKE INSERT ON accelerator.schema_migrations FROM acc_runtime;
GRANT UPDATE ON accelerator.supply_groups, accelerator.usages,
  accelerator.devices, accelerator.claims, accelerator.release_proofs,
  accelerator.bindings, accelerator.source_watermarks TO acc_runtime;
