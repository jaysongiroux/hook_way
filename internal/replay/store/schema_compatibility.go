package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The initial generic-record baseline reused schema version 1; the number alone
// cannot distinguish it from the earlier failure-shaped database. Keep this
// shape contract aligned with migrations, without reading captured data.
var recordSchemaColumns = []struct{ table, columns string }{
	{"records", `id uuid kind text replay_mode text delivery_status text dismissed_at timestamptz
		retryable bool not_retryable_reason text payload_unavailable_reason text flow text node_id text node_type text
		destination_id text destination_type text target text rendered_target text event_id text request_id text trace_id text
		idempotency_key text received_at timestamptz occurred_at timestamptz inbound_status int4 error_class text error_message text
		attempts int4 node_config_hash text destination_hash text core_version text core_instance text snapshot_version int4
		snapshot_size int8 snapshot_sha256 text payload_size int8 payload_sha256 text payload_content_type text payload_filename text
		has_artifacts bool ingest_hash text parent_id uuid root_id uuid captured_at timestamptz expires_at timestamptz resolution_source text`},
	{"record_artifacts", `record_id uuid record_kind text snapshot bytea payload bytea`},
	{"retry_runs", `run_id uuid record_id uuid record_kind text status text requested_by text idempotency_key text request_hash text
		ack_duplicate_risk bool created_at timestamptz claimed_at timestamptz lease_expires_at timestamptz worker_id text finished_at timestamptz result jsonb`},
	{"record_events", `id int8 record_id uuid event text actor text at timestamptz detail jsonb`},
	{"delivery_resolutions", `record_id uuid record_kind text actor text idempotency_key text request_hash text note text reference text resolved_at timestamptz`},
	{"usage", `id bool records int8 bytes int8`},
}

var recordSchemaConstraints = []string{"records_kind_fields", "records_publish_fields", "records_artifact_metadata", "records_retry_capture", "records_resolution", "records_dismissal"}
var recordSchemaIndexes = []string{"retry_runs_one_active", "retry_runs_idempotency", "delivery_resolutions_idempotency"}

// One metadata query avoids a round trip per table, constraint or index on every
// readiness probe. The names/types come from the same explicit shape contract;
// no process cache can hide a changed schema after startup.
const recordSchemaQuery = `WITH
		wanted_tables AS (SELECT * FROM unnest($1::text[]) WITH ORDINALITY AS t(name, position)),
		wanted_columns AS (SELECT * FROM unnest($2::text[], $3::text[], $4::text[]) AS c(table_name, name, type))
	SELECT to_regclass('failures') IS NOT NULL OR to_regclass('failure_artifacts') IS NOT NULL OR to_regclass('failure_events') IS NOT NULL,
		ARRAY(SELECT name FROM wanted_tables WHERE to_regclass(name) IS NOT NULL ORDER BY position),
		ARRAY(SELECT t.name FROM wanted_tables t WHERE
			NOT EXISTS(SELECT 1 FROM pg_class WHERE oid=to_regclass(t.name) AND relkind='r') OR
			EXISTS(SELECT 1 FROM wanted_columns c LEFT JOIN pg_attribute a
				ON a.attrelid=to_regclass(c.table_name) AND a.attname=c.name AND a.attnum>0 AND NOT a.attisdropped
				WHERE c.table_name=t.name AND (a.attname IS NULL OR a.atttypid IS DISTINCT FROM to_regtype(c.type)::oid))
			ORDER BY t.position),
		ARRAY(SELECT name FROM unnest($5::text[]) WITH ORDINALITY AS c(name, position) WHERE
			NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('records') AND conname=c.name AND contype='c' AND convalidated)
			ORDER BY position),
		ARRAY(SELECT name FROM unnest($6::text[]) WITH ORDINALITY AS i(name, position) WHERE
			NOT EXISTS(SELECT 1 FROM pg_index WHERE indexrelid=to_regclass(i.name) AND indisunique AND indisvalid AND indisready)
			ORDER BY position)`

type recordSchemaCheck struct {
	legacy                                                           bool
	presentTables, incompatibleTables, missingChecks, missingIndexes []string
}

func readRecordSchema(ctx context.Context, db schemaReader) (recordSchemaCheck, error) {
	var tables, columnTables, names, types []string
	for _, shape := range recordSchemaColumns {
		tables = append(tables, shape.table)
		fields := strings.Fields(shape.columns)
		for i := 0; i < len(fields); i += 2 {
			columnTables, names, types = append(columnTables, shape.table), append(names, fields[i]), append(types, fields[i+1])
		}
	}
	var check recordSchemaCheck
	err := db.QueryRow(ctx, recordSchemaQuery, tables, columnTables, names, types, recordSchemaConstraints, recordSchemaIndexes).
		Scan(&check.legacy, &check.presentTables, &check.incompatibleTables, &check.missingChecks, &check.missingIndexes)
	return check, err
}

func incompatibleSchema(reason string) error {
	return fmt.Errorf("store: unsupported replay schema (%s); preserve this database and its matching release/configuration; use a fresh replay database for the generic-record baseline, or restore a verified matching backup; no automatic legacy conversion or schema downgrade", reason)
}

// checkSchema uses a single read-only snapshot so a concurrent initial migration
// cannot appear as version 0 with newly committed tables. It never creates a
// version table or repairs an unsupported database.
func (s *Store) checkSchema(ctx context.Context, allowEmpty bool) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("store: schema compatibility: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	version, err := schemaVersion(ctx, tx)
	if err != nil {
		return err
	}
	latest := LatestSchemaVersion()
	if version > latest {
		return incompatibleSchema(fmt.Sprintf("database version %d is ahead of binary version %d", version, latest))
	}
	check, err := readRecordSchema(ctx, tx)
	if err != nil {
		return fmt.Errorf("store: schema compatibility: %w", err)
	}
	if check.legacy {
		return incompatibleSchema("legacy failure-shaped tables exist")
	}
	if version < latest {
		if version == 0 {
			if len(check.presentTables) > 0 {
				return incompatibleSchema("unversioned or incomplete " + check.presentTables[0] + " table")
			}
			if allowEmpty {
				return tx.Commit(ctx)
			}
		}
		return fmt.Errorf("store: database schema version %d is behind %d; run hookway-replay migrate", version, latest)
	}
	if len(check.incompatibleTables) > 0 {
		return incompatibleSchema("missing or incompatible generic-record columns in " + check.incompatibleTables[0])
	}
	// These named constraints/indexes distinguish the baseline's inspection,
	// resolution and active-run/idempotency invariants from a superficially
	// similar table set. This is a compatibility check, not a tamper attestation.
	if len(check.missingChecks) > 0 {
		return incompatibleSchema("missing or unvalidated constraint " + check.missingChecks[0])
	}
	if len(check.missingIndexes) > 0 {
		return incompatibleSchema("missing or invalid index " + check.missingIndexes[0])
	}
	return tx.Commit(ctx)
}
