-- +goose Up
ALTER TABLE hookway_events.subscriptions
 ADD COLUMN generation bigint NOT NULL DEFAULT 1,
 ADD COLUMN version bigint NOT NULL DEFAULT 1,
 ADD COLUMN revoked_at timestamptz,
 ADD COLUMN revoked_by text NOT NULL DEFAULT '',
 ADD COLUMN revoke_reason text NOT NULL DEFAULT '';
ALTER TABLE hookway_events.deliveries
 ADD COLUMN generation bigint NOT NULL DEFAULT 1,
 ADD COLUMN version bigint NOT NULL DEFAULT 1,
 ADD COLUMN cycle integer NOT NULL DEFAULT 1,
 ADD COLUMN cycle_attempts integer NOT NULL DEFAULT 0,
 ADD COLUMN http_status integer;
UPDATE hookway_events.deliveries SET cycle_attempts=attempts;
CREATE TABLE hookway_events.attempts (
 delivery bigint NOT NULL REFERENCES hookway_events.deliveries ON DELETE CASCADE,
 attempt integer NOT NULL, cycle integer NOT NULL, lease text NOT NULL,
 started_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
 http_status integer, outcome text NOT NULL DEFAULT 'unknown', duration_ms bigint,
 PRIMARY KEY(delivery,attempt)
);
CREATE TABLE hookway_events.actions (
 namespace text NOT NULL, target text NOT NULL, action text NOT NULL, actor text NOT NULL,
 key text NOT NULL, input_hash bytea NOT NULL, receipt jsonb NOT NULL,
 delivery bigint REFERENCES hookway_events.deliveries ON DELETE CASCADE,
 subscription text, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(namespace,actor,key)
);
CREATE INDEX subscriptions_operations ON hookway_events.subscriptions(namespace,id);
CREATE INDEX deliveries_operations ON hookway_events.deliveries(namespace,id DESC);
-- +goose Down
DROP TABLE hookway_events.actions;
DROP TABLE hookway_events.attempts;
DROP INDEX hookway_events.deliveries_operations;
DROP INDEX hookway_events.subscriptions_operations;
ALTER TABLE hookway_events.deliveries DROP COLUMN generation, DROP COLUMN version, DROP COLUMN cycle, DROP COLUMN cycle_attempts, DROP COLUMN http_status;
ALTER TABLE hookway_events.subscriptions DROP COLUMN generation, DROP COLUMN version, DROP COLUMN revoked_at, DROP COLUMN revoked_by, DROP COLUMN revoke_reason;
