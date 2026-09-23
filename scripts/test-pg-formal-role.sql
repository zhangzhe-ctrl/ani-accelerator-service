-- Separate formal-build runtime for the A-layer database.
CREATE ROLE acc_formal_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
\set runtime_role acc_formal_runtime
\ir test-pg-grants.sql
