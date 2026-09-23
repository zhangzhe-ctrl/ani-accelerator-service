-- Explicit test setup, only for a fresh isolated database. No catalog seed.
CREATE ROLE acc_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
\ir test-pg-grants.sql
