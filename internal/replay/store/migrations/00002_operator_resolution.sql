-- +goose Up
ALTER TABLE failures ADD COLUMN resolution_source text NOT NULL DEFAULT ''
    CHECK (resolution_source IN ('', 'retry_succeeded', 'operator_confirmed'));
UPDATE failures SET resolution_source = 'retry_succeeded' WHERE status = 'resolved';
ALTER TABLE failure_events DROP CONSTRAINT failure_events_event_check;
ALTER TABLE failure_events ADD CONSTRAINT failure_events_event_check
    CHECK (event IN ('captured', 'run_requested', 'run_finished', 'late_result',
                    'payload_downloaded', 'dismissed', 'children_created', 'operator_resolved'));
CREATE TABLE failure_resolutions (
    failure_id uuid PRIMARY KEY REFERENCES failures(id) ON DELETE CASCADE,
    actor text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    note text NOT NULL,
    reference text NOT NULL DEFAULT '',
    resolved_at timestamptz NOT NULL,
    CONSTRAINT failure_resolutions_idempotency UNIQUE(actor, idempotency_key)
);
-- +goose Down
DROP TABLE failure_resolutions;
-- Preserve existing audit evidence. Downgrade is rejected while operator
-- resolutions exist; deleting audit evidence requires normal retention GC.
ALTER TABLE failure_events DROP CONSTRAINT failure_events_event_check;
ALTER TABLE failure_events ADD CONSTRAINT failure_events_event_check
    CHECK (event IN ('captured', 'run_requested', 'run_finished', 'late_result',
                    'payload_downloaded', 'dismissed', 'children_created'));
ALTER TABLE failures DROP COLUMN resolution_source;
