-- One-time database setup for development, CI and the evaluation. Run as a superuser.
-- The server must load pg_stat_statements: shared_preload_libraries = 'pg_stat_statements'.
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'qpi') THEN
    CREATE ROLE qpi LOGIN SUPERUSER PASSWORD 'qpi';            -- injects faults and fixes (ALTER SYSTEM, DROP INDEX)
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'qpi_app') THEN
    CREATE ROLE qpi_app LOGIN PASSWORD 'qpi_app';              -- the application workload
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'qpi_monitor') THEN
    CREATE ROLE qpi_monitor LOGIN PASSWORD 'qpi_monitor';      -- the collector: read-only
  END IF;
END $$;
GRANT pg_monitor TO qpi_monitor;
SELECT 'CREATE DATABASE qpi OWNER qpi' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'qpi')\gexec
\c qpi
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
