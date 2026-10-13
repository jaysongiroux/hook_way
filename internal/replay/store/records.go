package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// List page sizes.
const (
	DefaultListLimit = 50
	MaxListLimit     = 500
)

// Record is a stored record: its ingest metadata plus lifecycle state. The
// metadata's Snapshot and Payload describe the artifacts as captured; after a
// dismiss they remain for audit while HasArtifacts turns false.
type Record struct {
	replaywire.RecordMetadata
	DeliveryStatus   string
	DismissedAt      *time.Time
	ParentID         string
	RootID           string
	CapturedAt       time.Time
	ExpiresAt        time.Time
	HasArtifacts     bool
	ResolutionSource string
}

// NewRecord is one validated ingest request. Snapshot and Payload are nil
// for a metadata-only record.
type NewRecord struct {
	Metadata replaywire.RecordMetadata
	Snapshot []byte
	Payload  []byte
	Actor    string
}

// IngestResult says whether Ingest stored a new record.
type IngestResult int

const (
	Created IngestResult = iota + 1
	Duplicate
)

// Run is one retry run.
type Run struct {
	RunID                    string
	RecordID                 string
	Status                   string
	RequestedBy              string
	IdempotencyKey           string
	AcknowledgeDuplicateRisk bool
	CreatedAt                time.Time
	ClaimedAt                *time.Time
	LeaseExpiresAt           *time.Time
	WorkerID                 string
	FinishedAt               *time.Time
	Result                   json.RawMessage // nil until the run finishes
}

// Event is one history entry.
type Event struct {
	Event  string
	Actor  string
	At     time.Time
	Detail json.RawMessage
}

// RecordDetail is a record with its runs (newest first), history (oldest first),
// and child record ids.
type RecordDetail struct {
	Record
	Runs       []Run
	History    []Event
	Children   []string
	Resolution *Resolution
}

// Filter selects records for List. Since is inclusive and Until exclusive,
// both on occurred_at.
type Filter struct {
	Flow           string
	NodeID         string
	DestinationID  string
	DeliveryStatus string
	ReplayMode     string
	Dismissed      *bool
	Kind           string
	Retryable      *bool
	Since          time.Time
	Until          time.Time
	Limit          int
	Cursor         string
}

// Page is one List result. NextCursor is empty on the last page.
type Page struct {
	Items      []Record
	NextCursor string
}

// Payload is a record's downloadable bytes.
type Payload struct {
	Bytes       []byte
	ContentType string
	Filename    string
}

// recordColumns is the column list scanRecord reads, in order.
const capturedRecordColumns = `id, kind, replay_mode, delivery_status, dismissed_at, retryable, not_retryable_reason, payload_unavailable_reason, flow, node_id, node_type, destination_id, destination_type, target, rendered_target, event_id, request_id, trace_id, idempotency_key, received_at, occurred_at, inbound_status, error_class, error_message, attempts, node_config_hash, destination_hash, core_version, core_instance, snapshot_version, snapshot_size, snapshot_sha256, payload_size, payload_sha256, payload_content_type, payload_filename, has_artifacts, parent_id, root_id, captured_at, expires_at, resolution_source`
const recordColumns = `id, kind, COALESCE(replay_mode, ''), COALESCE(delivery_status, ''), dismissed_at, retryable, not_retryable_reason, COALESCE(payload_unavailable_reason, ''), flow, node_id, node_type, COALESCE(destination_id, ''), COALESCE(destination_type, ''), COALESCE(target, ''), COALESCE(rendered_target, ''), event_id, request_id, trace_id, idempotency_key, received_at, occurred_at, inbound_status, COALESCE(error_class, ''), COALESCE(error_message, ''), COALESCE(attempts, 0), node_config_hash, COALESCE(destination_hash, ''), core_version, core_instance, snapshot_version, snapshot_size, snapshot_sha256, payload_size, payload_sha256, payload_content_type, payload_filename, has_artifacts, parent_id, root_id, captured_at, expires_at, COALESCE(resolution_source, '')`

const runColumns = `run_id, record_id, status, requested_by, idempotency_key, ack_duplicate_risk,
	created_at, claimed_at, lease_expires_at, worker_id, finished_at, result`

func scanRecord(row pgx.Row) (Record, error) {
	var f Record
	m := &f.RecordMetadata
	var snapVersion int
	var snapSize, paySize int64
	var snapSHA, paySHA, payType, payName string
	var parent *string
	err := row.Scan(&m.ID, &m.Kind, &m.ReplayMode, &f.DeliveryStatus, &f.DismissedAt, &m.Retryable, &m.NotRetryableReason, &m.PayloadUnavailableReason, &m.Flow, &m.NodeID, &m.NodeType,
		&m.DestinationID, &m.DestinationType, &m.Target, &m.RenderedTarget, &m.EventID, &m.RequestID, &m.TraceID, &m.IdempotencyKey,
		&m.ReceivedAt, &m.OccurredAt, &m.InboundStatus, &m.ErrorClass, &m.ErrorMessage, &m.Attempts, &m.NodeConfigHash,
		&m.DestinationHash, &m.CoreVersion, &m.CoreInstance, &snapVersion, &snapSize, &snapSHA,
		&paySize, &paySHA, &payType, &payName, &f.HasArtifacts, &parent, &f.RootID,
		&f.CapturedAt, &f.ExpiresAt, &f.ResolutionSource)
	if err != nil {
		return Record{}, err
	}
	if snapSHA != "" {
		m.Snapshot = &replaywire.ArtifactInfo{Size: snapSize, SHA256: snapSHA, Version: snapVersion}
	}
	if paySHA != "" {
		m.Payload = &replaywire.ArtifactInfo{Size: paySize, SHA256: paySHA, ContentType: payType, Filename: payName}
	}
	if parent != nil {
		f.ParentID = *parent
	}
	m.ReceivedAt, m.OccurredAt = m.ReceivedAt.UTC(), m.OccurredAt.UTC()
	f.CapturedAt, f.ExpiresAt = f.CapturedAt.UTC(), f.ExpiresAt.UTC()
	if f.DismissedAt != nil {
		*f.DismissedAt = f.DismissedAt.UTC()
	}
	return f, nil
}

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	var result []byte
	err := row.Scan(&r.RunID, &r.RecordID, &r.Status, &r.RequestedBy, &r.IdempotencyKey, &r.AcknowledgeDuplicateRisk,
		&r.CreatedAt, &r.ClaimedAt, &r.LeaseExpiresAt, &r.WorkerID, &r.FinishedAt, &result)
	if err != nil {
		return Run{}, err
	}
	r.CreatedAt = r.CreatedAt.UTC()
	for _, t := range []*time.Time{r.ClaimedAt, r.LeaseExpiresAt, r.FinishedAt} {
		if t != nil {
			*t = t.UTC()
		}
	}
	if result != nil {
		r.Result = json.RawMessage(result)
	}
	return r, nil
}

func scanEvent(row pgx.CollectableRow) (Event, error) {
	var e Event
	var detail []byte
	if err := row.Scan(&e.Event, &e.Actor, &e.At, &detail); err != nil {
		return Event{}, err
	}
	e.At, e.Detail = e.At.UTC(), json.RawMessage(detail)
	return e, nil
}

// ingestHash identifies a record's content. Core re-sends the same metadata
// bytes on an upload retry, and the metadata carries both artifact digests.
func ingestHash(m replaywire.RecordMetadata) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func placeholders(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "$" + strconv.Itoa(i+1)
	}
	return strings.Join(p, ", ")
}

var insertRecordSQL = `INSERT INTO records (` + capturedRecordColumns + `, ingest_hash) VALUES (` + placeholders(43) + `)`

// lineage places a record in its family: a root is its own root with no
// parent; a child points at its parent and keeps the root's expiry.
type lineage struct {
	parentID   any // nil for a root
	rootID     string
	capturedAt time.Time
	expiresAt  time.Time
}

// sizesMatch checks artifact bytes against their declared sizes, which
// usage accounting, dismiss, and GC rely on.
func sizesMatch(f NewRecord) error {
	for _, a := range []struct {
		name string
		info *replaywire.ArtifactInfo
		data []byte
	}{{"snapshot", f.Metadata.Snapshot, f.Snapshot}, {"payload", f.Metadata.Payload, f.Payload}} {
		if a.info != nil && int64(len(a.data)) != a.info.Size {
			return fmt.Errorf("store: %s is %d bytes; its metadata declares %d", a.name, len(a.data), a.info.Size)
		}
	}
	return nil
}

// insertRecord stores a record and its artifacts.
func insertRecord(ctx context.Context, tx pgx.Tx, f NewRecord, hash string, l lineage) error {
	if err := sizesMatch(f); err != nil {
		return err
	}
	m := f.Metadata
	var snapVersion int
	var snapSize, paySize int64
	var snapSHA, paySHA, payType, payName string
	if a := m.Snapshot; a != nil {
		snapVersion, snapSize, snapSHA = a.Version, a.Size, a.SHA256
	}
	if a := m.Payload; a != nil {
		paySize, paySHA, payType, payName = a.Size, a.SHA256, a.ContentType, a.Filename
	}
	hasArtifacts := m.Snapshot != nil || m.Payload != nil
	var deliveryStatus any
	if m.Kind == replaywire.KindFailure {
		deliveryStatus = StatusOpen
	}
	failureField := func(value any) any {
		if m.Kind == replaywire.KindInspection {
			return nil
		}
		return value
	}

	if _, err := tx.Exec(ctx, insertRecordSQL,
		m.ID, m.Kind, nullableText(m.ReplayMode), deliveryStatus, nil, m.Retryable, m.NotRetryableReason, nullableText(m.PayloadUnavailableReason), m.Flow, m.NodeID, m.NodeType,
		failureField(m.DestinationID), failureField(m.DestinationType), failureField(m.Target), failureField(m.RenderedTarget), m.EventID, m.RequestID, m.TraceID, m.IdempotencyKey,
		m.ReceivedAt, m.OccurredAt, m.InboundStatus, failureField(m.ErrorClass), failureField(m.ErrorMessage), failureField(m.Attempts), m.NodeConfigHash,
		failureField(m.DestinationHash), m.CoreVersion, m.CoreInstance, snapVersion, snapSize, snapSHA,
		paySize, paySHA, payType, payName, hasArtifacts, l.parentID, l.rootID,
		l.capturedAt, l.expiresAt, nil, hash); err != nil {
		return err
	}
	if !hasArtifacts {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO record_artifacts (record_id, record_kind, snapshot, payload) VALUES ($1, $2, $3, $4)`, m.ID, m.Kind, f.Snapshot, f.Payload)
	return err
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func appendEvent(ctx context.Context, tx pgx.Tx, recordID, event, actor string, at time.Time, detail map[string]any) error {
	body := []byte("{}")
	if len(detail) > 0 {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		body = b
	}
	_, err := tx.Exec(ctx, `INSERT INTO record_events (record_id, event, actor, at, detail) VALUES ($1, $2, $3, $4, $5)`,
		recordID, event, actor, at, body)
	return err
}

func validateRecord(f NewRecord) error {
	if err := f.Metadata.Validate(); err != nil {
		return err
	}
	if err := replaywire.VerifyArtifact("snapshot", f.Metadata.Snapshot, f.Snapshot != nil, f.Snapshot); err != nil {
		return err
	}
	if err := replaywire.VerifyArtifact("payload", f.Metadata.Payload, f.Payload != nil, f.Payload); err != nil {
		return err
	}

	return nil
}

// Ingest stores a validated record in one transaction with its artifacts, a
// captured history event, and the usage update. Ingests serialize on the
// usage row, which makes the capacity check exact. A re-sent record with the
// same content is a Duplicate and changes nothing.
func (s *Store) Ingest(ctx context.Context, f NewRecord) (IngestResult, error) {
	if err := validateRecord(f); err != nil {
		return 0, err
	}

	hash, err := ingestHash(f.Metadata)
	if err != nil {
		return 0, err
	}
	size := int64(len(f.Snapshot) + len(f.Payload))
	var result IngestResult
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var records, bytes int64
		if err := tx.QueryRow(ctx, `SELECT records, bytes FROM usage FOR UPDATE`).Scan(&records, &bytes); err != nil {
			return err
		}
		var existing string
		switch err := tx.QueryRow(ctx, `SELECT ingest_hash FROM records WHERE id = $1`, f.Metadata.ID).Scan(&existing); {
		case err == nil && existing == hash:
			result = Duplicate
			return nil
		case err == nil:
			return ErrConflict
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if records+1 > s.limits.MaxRecords || bytes+size > s.limits.MaxBytes {
			return ErrCapacity
		}
		now := s.now().UTC()
		if err := insertRecord(ctx, tx, f, hash, lineage{rootID: f.Metadata.ID, capturedAt: now, expiresAt: now.Add(s.limits.TTL)}); err != nil {
			return err
		}
		if err := appendEvent(ctx, tx, f.Metadata.ID, EventCaptured, f.Actor, now, nil); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE usage SET records = records + 1, bytes = bytes + $1`, size); err != nil {
			return err
		}
		result = Created
		return nil
	})
	if err != nil {
		return 0, err
	}
	return result, nil
}

// Get returns one record with its runs, history, and children, read from one
// snapshot of the database.
func (s *Store) Get(ctx context.Context, id string) (RecordDetail, error) {
	if !validID(id) {
		return RecordDetail{}, ErrNotFound
	}
	var d RecordDetail
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		f, err := scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM records WHERE id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		d.Record = f
		resolution, err := scanResolution(tx.QueryRow(ctx, `SELECT `+resolutionColumns+` FROM delivery_resolutions WHERE record_id = $1`, id))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			d.Resolution = &resolution
		}
		rows, err := tx.Query(ctx, `SELECT `+runColumns+` FROM retry_runs WHERE record_id = $1 ORDER BY created_at DESC, run_id DESC`, id)
		if err != nil {
			return err
		}
		if d.Runs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) { return scanRun(r) }); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT event, actor, at, detail FROM record_events WHERE record_id = $1 ORDER BY id`, id)
		if err != nil {
			return err
		}
		if d.History, err = pgx.CollectRows(rows, scanEvent); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT id::text FROM records WHERE parent_id = $1 ORDER BY occurred_at, id`, id)
		if err != nil {
			return err
		}
		d.Children, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		return RecordDetail{}, err
	}
	return d, nil
}

// List returns records newest first by (occurred_at, id), with an opaque
// keyset cursor for the next page.
func (s *Store) List(ctx context.Context, f Filter) (Page, error) {
	var where []string
	var args []any
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	for _, eq := range []struct{ column, value string }{
		{"flow", f.Flow}, {"node_id", f.NodeID}, {"destination_id", f.DestinationID}, {"delivery_status", f.DeliveryStatus}, {"kind", f.Kind}, {"replay_mode", f.ReplayMode},
	} {
		if eq.value != "" {
			add(eq.column+" = $%d", eq.value)
		}
	}
	if f.Dismissed != nil {
		add("(dismissed_at IS NOT NULL) = $%d", *f.Dismissed)
	}
	if f.Retryable != nil {
		add("retryable = $%d", *f.Retryable)
	}
	if !f.Since.IsZero() {
		add("occurred_at >= $%d", f.Since)
	}
	if !f.Until.IsZero() {
		add("occurred_at < $%d", f.Until)
	}
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return Page{}, ErrBadCursor
		}
		args = append(args, at, id)
		where = append(where, fmt.Sprintf("(occurred_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	limit = min(limit, MaxListLimit)
	query := `SELECT ` + recordColumns + ` FROM records`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(` ORDER BY occurred_at DESC, id DESC LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return Page{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Record, error) { return scanRecord(r) })
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		at := last.OccurredAt
		page.NextCursor = encodeCursor(at, last.ID)
	}
	return page, nil
}

// encodeCursor packs the last row's (occurred_at, id). occurred_at is stored at
// microsecond precision, so microseconds round-trip exactly.
func encodeCursor(occurredAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(occurredAt.UnixMicro(), 10) + "|" + id))
}

// decodeCursor reverses encodeCursor. A cursor is client input; anything
// malformed is an error, never a partial value.
func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	micros, id, found := strings.Cut(string(raw), "|")
	if !found || !validID(id) {
		return time.Time{}, "", ErrBadCursor
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	at := time.UnixMicro(n).UTC()
	if at.Before(minCursorTime) || at.After(maxCursorTime) {
		return time.Time{}, "", ErrBadCursor
	}
	return at, id, nil
}

// Cursor timestamps must fall in years 1 through 9999, well inside what
// PostgreSQL's timestamptz represents, so a crafted cursor is rejected here
// rather than failing the query.
var (
	minCursorTime = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	maxCursorTime = time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
)

// Payload returns a record's payload after checking it against the size and
// digest recorded at ingest, and logs the download to history. A mismatch is
// ErrCorrupt and is not logged as a download.
func (s *Store) Payload(ctx context.Context, id, actor string) (Payload, error) {
	if !validID(id) {
		return Payload{}, ErrNotFound
	}
	var p Payload
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var digest string
		var dismissedAt *time.Time
		var size int64
		var has bool
		var expiresAt time.Time
		err := tx.QueryRow(ctx, `SELECT dismissed_at, has_artifacts, payload_size, payload_sha256, payload_content_type, payload_filename, expires_at
			FROM records WHERE id = $1 FOR SHARE`, id).Scan(&dismissedAt, &has, &size, &digest, &p.ContentType, &p.Filename, &expiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !expiresAt.After(s.now().UTC()) {
			return ErrGone
		}
		if !has {
			if dismissedAt != nil {
				return ErrGone
			}
			return ErrNoPayload
		}
		if err := tx.QueryRow(ctx, `SELECT payload FROM record_artifacts WHERE record_id = $1 AND payload IS NOT NULL`, id).Scan(&p.Bytes); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrGone
			}
			return err
		}
		sum := sha256.Sum256(p.Bytes)
		if int64(len(p.Bytes)) != size || hex.EncodeToString(sum[:]) != digest {
			return ErrCorrupt
		}
		return appendEvent(ctx, tx, id, EventPayloadDownloaded, actor, s.now().UTC(), map[string]any{"size": size})
	})
	if err != nil {
		return Payload{}, err
	}
	return p, nil
}
