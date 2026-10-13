-- Run as replay_owner after every successful migration; maintain the explicit
-- object list when migrations add live objects. Runtime never owns the schema.
\set ON_ERROR_STOP on
REVOKE ALL ON SCHEMA public FROM replay_runtime;
GRANT USAGE ON SCHEMA public TO replay_runtime;
REVOKE ALL ON TABLE public.goose_db_version FROM replay_runtime;
GRANT SELECT ON TABLE public.goose_db_version TO replay_runtime;
REVOKE ALL ON TABLE public.records, public.record_artifacts, public.retry_runs,
  public.record_events, public.usage, public.delivery_resolutions FROM replay_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.records, public.record_artifacts,
  public.retry_runs, public.record_events, public.usage, public.delivery_resolutions
  TO replay_runtime;
REVOKE ALL ON SEQUENCE public.record_events_id_seq FROM replay_runtime;
GRANT USAGE, SELECT ON SEQUENCE public.record_events_id_seq TO replay_runtime;
SELECT format('REVOKE ALL ON SEQUENCE public.%I FROM replay_runtime', c.relname)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'S'
  AND c.relname = 'goose_db_version_id_seq' \gexec
