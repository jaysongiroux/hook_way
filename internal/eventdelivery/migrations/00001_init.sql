-- +goose Up
CREATE TABLE hookway_events.namespaces (
 namespace text PRIMARY KEY, key_hash bytea NOT NULL
);
CREATE TABLE hookway_events.subscriptions (
 namespace text NOT NULL, id text NOT NULL, endpoint text NOT NULL, principal jsonb NOT NULL,
 owner text NOT NULL, event text NOT NULL, arguments jsonb NOT NULL, callback text NOT NULL,
 secret bytea NOT NULL, previous_secret bytea, overlap_until timestamptz,
 expires_at timestamptz NOT NULL, active boolean NOT NULL DEFAULT true,
 PRIMARY KEY(namespace,id)
);
CREATE INDEX subscriptions_event ON hookway_events.subscriptions(namespace,endpoint,event) WHERE active;
CREATE TABLE hookway_events.occurrences (
 namespace text NOT NULL, endpoint text NOT NULL, event text NOT NULL, event_id text NOT NULL,
 body bytea NOT NULL, data_hash bytea NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(namespace,endpoint,event,event_id)
);
CREATE TABLE hookway_events.deliveries (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 namespace text NOT NULL, endpoint text NOT NULL, event text NOT NULL, event_id text NOT NULL,
 subscription text NOT NULL, state text NOT NULL DEFAULT 'pending', attempts int NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(), lease text, lease_until timestamptz,
 finished_at timestamptz, outcome text NOT NULL DEFAULT '',
 FOREIGN KEY(namespace,endpoint,event,event_id) REFERENCES hookway_events.occurrences ON DELETE CASCADE,
 FOREIGN KEY(namespace,subscription) REFERENCES hookway_events.subscriptions(namespace,id),
 UNIQUE(namespace,endpoint,event,event_id,subscription)
);
CREATE INDEX deliveries_pending ON hookway_events.deliveries(namespace,available_at) WHERE state='pending';
CREATE INDEX deliveries_finished ON hookway_events.deliveries(namespace,finished_at) WHERE state<>'pending';
-- +goose Down
DROP TABLE hookway_events.deliveries;
DROP TABLE hookway_events.occurrences;
DROP TABLE hookway_events.subscriptions;
DROP TABLE hookway_events.namespaces;
