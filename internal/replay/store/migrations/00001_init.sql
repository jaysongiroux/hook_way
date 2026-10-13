-- +goose Up
CREATE TABLE records (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('failure', 'inspection')),
    replay_mode text CHECK (replay_mode IN ('resume', 'publish_only')),
    delivery_status text CHECK (delivery_status IN ('open', 'retrying', 'resolved', 'unknown')),
    dismissed_at timestamptz,
    retryable boolean NOT NULL,
    not_retryable_reason text NOT NULL,
    payload_unavailable_reason text CHECK (payload_unavailable_reason IN ('too_large', 'unsupported')),
    flow text NOT NULL,
    node_id text NOT NULL,
    node_type text NOT NULL,
    destination_id text,
    destination_type text,
    target text,
    rendered_target text,
    event_id text NOT NULL,
    request_id text NOT NULL,
    trace_id text NOT NULL,
    idempotency_key text NOT NULL,
    received_at timestamptz NOT NULL,
    occurred_at timestamptz NOT NULL,
    inbound_status integer NOT NULL,
    error_class text,
    error_message text,
    attempts integer CHECK (attempts >= 0),
    node_config_hash text NOT NULL,
    destination_hash text,
    core_version text NOT NULL,
    core_instance text NOT NULL,
    snapshot_version integer NOT NULL,
    snapshot_size bigint NOT NULL CHECK (snapshot_size >= 0),
    snapshot_sha256 text NOT NULL,
    payload_size bigint NOT NULL CHECK (payload_size >= 0),
    payload_sha256 text NOT NULL,
    payload_content_type text NOT NULL,
    payload_filename text NOT NULL,
    has_artifacts boolean NOT NULL,
    ingest_hash text NOT NULL,
    parent_id uuid REFERENCES records(id) ON DELETE SET NULL,
    root_id uuid NOT NULL,
    captured_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    resolution_source text CHECK (resolution_source IN ('retry_succeeded', 'operator_confirmed')),
    UNIQUE(id, kind),
    CONSTRAINT records_kind_fields CHECK (
      (kind = 'failure' AND replay_mode IS NOT NULL AND delivery_status IS NOT NULL
       AND inbound_status BETWEEN 200 AND 299 AND error_class IS NOT NULL
       AND error_class IN ('timeout','5xx','4xx','429','connection','queue_full','panic','other')
       AND error_message IS NOT NULL AND attempts IS NOT NULL)
      OR
      (kind = 'inspection' AND node_type = 'inspect' AND replay_mode IS NULL AND delivery_status IS NULL
       AND retryable = false AND not_retryable_reason = 'inspection_only'
       AND inbound_status BETWEEN 100 AND 599 AND error_class IS NULL AND error_message IS NULL AND attempts IS NULL
       AND destination_id IS NULL AND destination_type IS NULL AND target IS NULL AND rendered_target IS NULL
       AND destination_hash IS NULL AND resolution_source IS NULL
       AND snapshot_sha256 = '' AND snapshot_size = 0 AND snapshot_version = 0)
    ),
    CONSTRAINT records_publish_fields CHECK (replay_mode IS DISTINCT FROM 'publish_only' OR
      (destination_id IS NOT NULL AND destination_id <> '' AND destination_type IS NOT NULL AND destination_type <> ''
       AND target IS NOT NULL AND target <> '' AND rendered_target IS NOT NULL AND rendered_target <> '')),
    CONSTRAINT records_artifact_metadata CHECK (
      ((snapshot_sha256 = '' AND snapshot_size = 0 AND snapshot_version = 0)
       OR (snapshot_sha256 ~ '^[0-9a-f]{64}$' AND snapshot_size > 0 AND snapshot_version > 0))
      AND ((payload_sha256 = '' AND payload_size = 0 AND payload_unavailable_reason IS NOT NULL)
       OR (payload_sha256 ~ '^[0-9a-f]{64}$' AND payload_unavailable_reason IS NULL))
      AND (NOT has_artifacts OR dismissed_at IS NULL)
    ),
    CONSTRAINT records_retry_capture CHECK (kind <> 'failure' OR
      (retryable AND not_retryable_reason = '' AND snapshot_sha256 <> '' AND payload_sha256 <> '') OR
      (NOT retryable AND not_retryable_reason IN ('too_large','unsupported')
       AND payload_unavailable_reason IS NOT NULL AND payload_unavailable_reason = not_retryable_reason
       AND snapshot_sha256 = '' AND payload_sha256 = '')),
    CONSTRAINT records_resolution CHECK (
      (delivery_status IS DISTINCT FROM 'resolved' AND resolution_source IS NULL)
      OR (delivery_status = 'resolved' AND resolution_source IS NOT NULL)),
    CONSTRAINT records_dismissal CHECK (dismissed_at IS NULL OR
      kind = 'inspection' OR delivery_status IN ('open','unknown'))
);
CREATE INDEX records_occurred_at ON records(occurred_at DESC, id DESC);
CREATE INDEX records_kind_occurred_at ON records(kind, occurred_at DESC, id DESC);
CREATE INDEX records_delivery_occurred_at ON records(delivery_status, occurred_at DESC, id DESC);
CREATE INDEX records_flow_node ON records(flow, node_id, occurred_at DESC);
CREATE INDEX records_destination ON records(destination_id, occurred_at DESC);
CREATE INDEX records_event ON records(event_id);
CREATE INDEX records_root ON records(root_id);
CREATE INDEX records_parent ON records(parent_id);
CREATE INDEX records_expires ON records(expires_at);

-- Metadata listing never reads large artifact bytes. NULL means absent; bytea '' means an empty file.
CREATE TABLE record_artifacts (
    record_id uuid PRIMARY KEY,
    record_kind text NOT NULL CHECK(record_kind IN ('failure', 'inspection')),
    FOREIGN KEY(record_id, record_kind) REFERENCES records(id, kind) ON DELETE CASCADE,
    snapshot bytea,
    payload bytea,
    CHECK (snapshot IS NOT NULL OR payload IS NOT NULL),
    CHECK (record_kind <> 'inspection' OR snapshot IS NULL)
);
CREATE TABLE retry_runs (
    run_id uuid PRIMARY KEY,
    record_id uuid NOT NULL,
    record_kind text NOT NULL DEFAULT 'failure' CHECK(record_kind = 'failure'),
    FOREIGN KEY(record_id, record_kind) REFERENCES records(id, kind) ON DELETE CASCADE,
    status text NOT NULL CHECK(status IN ('queued','running','succeeded','failed','blocked','unknown')),
    requested_by text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    ack_duplicate_risk boolean NOT NULL,
    created_at timestamptz NOT NULL,
    claimed_at timestamptz,
    lease_expires_at timestamptz,
    worker_id text NOT NULL DEFAULT '',
    finished_at timestamptz,
    result jsonb
);
CREATE UNIQUE INDEX retry_runs_one_active ON retry_runs(record_id) WHERE status IN ('queued','running');
CREATE UNIQUE INDEX retry_runs_idempotency ON retry_runs(requested_by, idempotency_key);
CREATE INDEX retry_runs_status_created ON retry_runs(status, created_at);
CREATE INDEX retry_runs_record ON retry_runs(record_id, created_at DESC);
CREATE TABLE record_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    record_id uuid NOT NULL REFERENCES records(id) ON DELETE CASCADE,
    event text NOT NULL CHECK(event IN ('captured','run_requested','run_finished','late_result','payload_downloaded','dismissed','children_created','operator_resolved')),
    actor text NOT NULL,
    at timestamptz NOT NULL,
    detail jsonb NOT NULL
);
CREATE INDEX record_events_record ON record_events(record_id, id);
CREATE TABLE delivery_resolutions (
    record_id uuid PRIMARY KEY,
    record_kind text NOT NULL DEFAULT 'failure' CHECK(record_kind = 'failure'),
    FOREIGN KEY(record_id, record_kind) REFERENCES records(id, kind) ON DELETE CASCADE,
    actor text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    note text NOT NULL,
    reference text NOT NULL DEFAULT '',
    resolved_at timestamptz NOT NULL,
    CONSTRAINT delivery_resolutions_idempotency UNIQUE(actor, idempotency_key)
);
CREATE TABLE usage (
    id boolean PRIMARY KEY DEFAULT true CHECK(id),
    records bigint NOT NULL CHECK(records >= 0),
    bytes bigint NOT NULL CHECK(bytes >= 0)
);
INSERT INTO usage(id, records, bytes) VALUES(true, 0, 0);
-- +goose Down
DROP TABLE usage;
DROP TABLE delivery_resolutions;
DROP TABLE record_events;
DROP TABLE retry_runs;
DROP TABLE record_artifacts;
DROP TABLE records;
