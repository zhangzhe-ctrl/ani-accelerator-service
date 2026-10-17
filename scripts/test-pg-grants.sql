-- Explicit grants for a task database after migration or isolated restore.
\if :{?runtime_role}
\else
\set runtime_role acc_runtime
\endif
SELECT format('REVOKE CREATE, TEMPORARY ON DATABASE %I FROM PUBLIC',current_database()) \gexec
SELECT format('REVOKE CONNECT ON DATABASE %I FROM PUBLIC',current_database()) \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I',current_database(), :'runtime_role') \gexec
GRANT USAGE ON SCHEMA accelerator TO :"runtime_role";
GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA accelerator TO :"runtime_role";
REVOKE INSERT ON accelerator.schema_migrations FROM :"runtime_role";
GRANT UPDATE ON accelerator.profiles, accelerator.supply_groups, accelerator.usages,
 accelerator.devices, accelerator.claims, accelerator.release_proofs,
 accelerator.bindings, accelerator.source_watermarks TO :"runtime_role";
