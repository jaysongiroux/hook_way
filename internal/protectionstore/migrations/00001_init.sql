-- +goose Up
CREATE TABLE hookway_protection.policies (
 namespace text NOT NULL,
 id text NOT NULL,
 document jsonb NOT NULL,
 fingerprint text NOT NULL,
 live_entries bigint NOT NULL DEFAULT 0 CHECK(live_entries >= 0),
 pending jsonb,
 update_at bigint,
 cursor text NOT NULL DEFAULT '',
 PRIMARY KEY(namespace,id)
);
CREATE TABLE hookway_protection.entries (
 namespace text NOT NULL,
 policy_id text NOT NULL,
 identity_hash text NOT NULL,
 state jsonb NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(namespace,policy_id,identity_hash),
 FOREIGN KEY(namespace,policy_id) REFERENCES hookway_protection.policies(namespace,id)
);
CREATE INDEX protection_entries_expiry ON hookway_protection.entries(expires_at,namespace,policy_id);
CREATE INDEX protection_policy_entries_expiry ON hookway_protection.entries(namespace,policy_id,expires_at);
CREATE INDEX protection_namespace_entries_expiry ON hookway_protection.entries(namespace,expires_at);
-- +goose Down
DROP TABLE hookway_protection.entries;
DROP TABLE hookway_protection.policies;
