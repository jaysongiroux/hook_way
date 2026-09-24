-- +goose Up
CREATE TABLE failures (
    id                   uuid        PRIMARY KEY,
    kind                 text        NOT NULL CHECK (kind IN ('resume', 'publish_only')),
    status               text        NOT NULL CHECK (status IN ('open', 'retrying', 'resolved', 'unknown', 'dismissed')),
    retryable            boolean     NOT NULL,
    not_retryable_reason text        NOT NULL,
    flow                 text        NOT NULL,
    node_id              text        NOT NULL,
    node_type            text        NOT NULL,
    destination_id       text        NOT NULL,
    destination_type     text        NOT NULL,
    target               text        NOT NULL,
    rendered_target      text        NOT NULL,
    event_id             text        NOT NULL,
    request_id           text        NOT NULL,
    trace_id             text        NOT NULL,
    idempotency_key      text        NOT NULL,
    received_at          timestamptz NOT NULL,
    failed_at            timestamptz NOT NULL,
    inbound_status       integer     NOT NULL,
    error_class          text        NOT NULL,
    error_message        text        NOT NULL,
    attempts             integer     NOT NULL,
    node_config_hash     text        NOT NULL,
    destination_hash     text        NOT NULL,
    core_version         text        NOT NULL,
    core_instance        text        NOT NULL,
    snapshot_version     integer     NOT NULL,
    snapshot_size        bigint      NOT NULL,
    snapshot_sha256      text        NOT NULL,
    payload_size         bigint      NOT NULL,
    payload_sha256       text        NOT NULL,
    payload_content_type text        NOT NULL,
    payload_filename     text        NOT NULL,
    -- has_artifacts is false for metadata-only records and after dismiss.
    has_artifacts        boolean     NOT NULL,
    -- ingest_hash is the sha256 of the ingested metadata; an ingest retry
    -- with the same id is a duplicate only when it matches.
    ingest_hash          text        NOT NULL,
    parent_id            uuid        REFERENCES failures (id) ON DELETE SET NULL,
    -- root_id correlates a record with its descendants; not a foreign key,
    -- so GC can delete a root before its children expire.
    root_id              uuid        NOT NULL,
    captured_at          timestamptz NOT NULL,
    expires_at           timestamptz NOT NULL
);
CREATE INDEX failures_failed_at ON failures (failed_at DESC, id DESC);
CREATE INDEX failures_status_failed_at ON failures (status, failed_at DESC, id DESC);
CREATE INDEX failures_flow_node ON failures (flow, node_id, failed_at DESC);
CREATE INDEX failures_destination ON failures (destination_id, failed_at DESC);
CREATE INDEX failures_event ON failures (event_id);
CREATE INDEX failures_root ON failures (root_id);
CREATE INDEX failures_parent ON failures (parent_id);
CREATE INDEX failures_expires ON failures (expires_at);

-- Kept apart from failures so list queries never touch TOAST.
CREATE TABLE failure_artifacts (
    failure_id uuid  PRIMARY KEY REFERENCES failures (id) ON DELETE CASCADE,
    snapshot   bytea NOT NULL,
    payload    bytea NOT NULL
);

CREATE TABLE retry_runs (
    run_id             uuid        PRIMARY KEY,
    failure_id         uuid        NOT NULL REFERENCES failures (id) ON DELETE CASCADE,
    status             text        NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'blocked', 'unknown')),
    requested_by       text        NOT NULL,
    idempotency_key    text        NOT NULL,
    request_hash       text        NOT NULL,
    ack_duplicate_risk boolean     NOT NULL,
    created_at         timestamptz NOT NULL,
    claimed_at         timestamptz,
    lease_expires_at   timestamptz,
    worker_id          text        NOT NULL DEFAULT '',
    finished_at        timestamptz,
    result             jsonb
);
CREATE UNIQUE INDEX retry_runs_one_active ON retry_runs (failure_id) WHERE status IN ('queued', 'running');
CREATE UNIQUE INDEX retry_runs_idempotency ON retry_runs (requested_by, idempotency_key);
CREATE INDEX retry_runs_status_created ON retry_runs (status, created_at);
CREATE INDEX retry_runs_failure ON retry_runs (failure_id, created_at DESC);

CREATE TABLE failure_events (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    failure_id uuid        NOT NULL REFERENCES failures (id) ON DELETE CASCADE,
    event      text        NOT NULL CHECK (event IN ('captured', 'run_requested', 'run_finished', 'late_result',
                                                     'payload_downloaded', 'dismissed', 'children_created')),
    actor      text        NOT NULL,
    at         timestamptz NOT NULL,
    detail     jsonb       NOT NULL
);
CREATE INDEX failure_events_failure ON failure_events (failure_id, id);

-- One row: records not dismissed, and artifact bytes currently stored.
CREATE TABLE usage (
    id      boolean PRIMARY KEY DEFAULT true CHECK (id),
    records bigint  NOT NULL CHECK (records >= 0),
    bytes   bigint  NOT NULL CHECK (bytes >= 0)
);
INSERT INTO usage (id, records, bytes) VALUES (true, 0, 0);

-- +goose Down
DROP TABLE usage;
DROP TABLE failure_events;
DROP TABLE retry_runs;
DROP TABLE failure_artifacts;
DROP TABLE failures;
