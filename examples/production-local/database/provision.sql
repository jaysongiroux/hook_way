-- Run only as the cluster administrator on the dedicated local replay database.
-- Safe to repeat: existing data is retained; only named replay objects change owner.
\set ON_ERROR_STOP on
\getenv migration_password REPLAY_MIGRATION_PASSWORD
\getenv runtime_password REPLAY_RUNTIME_PASSWORD
\getenv administrator_password PGPASSWORD
-- Fail before altering any role if direct Compose bypassed the Make preflight.
SELECT length(:'migration_password') >= 32
  AND length(:'runtime_password') >= 32
  AND :'migration_password' <> :'runtime_password'
  AND :'migration_password' <> :'administrator_password'
  AND :'runtime_password' <> :'administrator_password' AS credentials_ok \gset
\if :credentials_ok
\else
  \echo Migration/runtime passwords must be distinct from each other and the administrator, and at least 32 bytes.
  -- PostgreSQL 17's \quit has no exit-code argument. This generic failed SQL
  -- exits nonzero through ON_ERROR_STOP without logging any password literals.
  SELECT 1 / 0;
\endif

SELECT format('CREATE ROLE replay_owner LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD %L', :'migration_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'replay_owner') \gexec
SELECT format('CREATE ROLE replay_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD %L', :'runtime_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'replay_runtime') \gexec

ALTER ROLE replay_owner WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD :'migration_password';
ALTER ROLE replay_runtime WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD :'runtime_password';
REVOKE replay_owner FROM replay_runtime;
ALTER DATABASE replay OWNER TO replay_owner;
ALTER SCHEMA public OWNER TO replay_owner;
REVOKE ALL ON DATABASE replay FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM replay_runtime;
GRANT CONNECT ON DATABASE replay TO replay_runtime;

-- Transfer only known current-schema tables/owned sequences from the earlier
-- local deployment's administrator. Never use REASSIGN OWNED across a cluster.
SELECT format('ALTER TABLE public.%I OWNER TO replay_owner', c.relname)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'r'
  AND c.relname IN ('goose_db_version', 'records', 'record_artifacts', 'retry_runs',
                   'record_events', 'usage', 'delivery_resolutions') \gexec
SELECT format('ALTER SEQUENCE public.%I OWNER TO replay_owner', c.relname)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'S'
  AND c.relname IN ('goose_db_version_id_seq', 'record_events_id_seq') \gexec
