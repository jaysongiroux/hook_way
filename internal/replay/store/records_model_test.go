package store

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestMixedRecordsPaginationAndFilters(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	failure, inspection := testFailure(t, nil), testInspection(t)
	inspection.Metadata.OccurredAt = failure.Metadata.OccurredAt
	mustIngest(t, s, failure)
	mustIngest(t, s, inspection)
	ids := []string{failure.Metadata.ID, inspection.Metadata.ID}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	page, err := s.List(t.Context(), Filter{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids[0] || page.NextCursor == "" {
		t.Fatalf("first: %+v %v", page, err)
	}
	next, err := s.List(t.Context(), Filter{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != ids[1] || next.NextCursor != "" {
		t.Fatalf("next: %+v %v", next, err)
	}
	for _, filter := range []Filter{{Kind: "failure"}, {ReplayMode: "resume"}, {DeliveryStatus: "open"}} {
		page, err := s.List(t.Context(), filter)
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != failure.Metadata.ID {
			t.Fatalf("filter %+v: %+v %v", filter, page, err)
		}
	}
	page, err = s.List(t.Context(), Filter{Since: failure.Metadata.OccurredAt, Until: failure.Metadata.OccurredAt.Add(time.Microsecond)})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("inclusive since: %+v %v", page, err)
	}
	page, err = s.List(t.Context(), Filter{Until: failure.Metadata.OccurredAt})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("exclusive until: %+v %v", page, err)
	}
	if _, err := s.Dismiss(t.Context(), inspection.Metadata.ID, "ops"); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	for _, filter := range []Filter{{Dismissed: &yes}, {Dismissed: &no}} {
		page, err := s.List(t.Context(), filter)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("dismissal %+v: %+v %v", filter, page, err)
		}
	}
	page, err = s.List(t.Context(), Filter{})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("omitted dismissal filter: %+v %v", page, err)
	}
}

func TestRecordNullConstraintsAndFailureOnlyRelations(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f, observation := testFailure(t, nil), testInspection(t)
	mustIngest(t, s, f)
	mustIngest(t, s, observation)
	for _, query := range []string{
		`UPDATE records SET replay_mode=NULL WHERE id=$1`,
		`UPDATE records SET delivery_status=NULL WHERE id=$1`,
		`UPDATE records SET error_class=NULL WHERE id=$1`,
		`UPDATE records SET error_message=NULL WHERE id=$1`,
		`UPDATE records SET attempts=NULL WHERE id=$1`,
		`UPDATE records SET delivery_status='resolved', resolution_source=NULL WHERE id=$1`,
	} {
		if _, err := s.pool.Exec(t.Context(), query, f.Metadata.ID); err == nil {
			t.Fatalf("invalid failure accepted: %s", query)
		} else {
			requireConstraint(t, err)
		}
	}
	for _, query := range []string{
		`UPDATE records SET replay_mode='resume' WHERE id=$1`,
		`UPDATE records SET error_message='' WHERE id=$1`,
		`UPDATE records SET attempts=0 WHERE id=$1`,
		`UPDATE records SET destination_id='' WHERE id=$1`,
		`UPDATE record_artifacts SET snapshot='executable'::bytea WHERE record_id=$1`,
		`INSERT INTO retry_runs(run_id,record_id,status,requested_by,idempotency_key,request_hash,ack_duplicate_risk,created_at) VALUES('01926d3c-0000-7b3a-9c1e-2f4a5b6c7d8e',$1,'queued','ops','key','hash',false,now())`,
		`INSERT INTO delivery_resolutions(record_id,actor,idempotency_key,request_hash,note,resolved_at) VALUES($1,'ops','key','hash','evidence',now())`,
	} {
		if _, err := s.pool.Exec(t.Context(), query, observation.Metadata.ID); err == nil {
			t.Fatalf("invalid inspection accepted: %s", query)
		} else {
			requireConstraint(t, err)
		}
	}
	var nullableFields bool
	if err := s.pool.QueryRow(t.Context(), `SELECT replay_mode IS NULL AND delivery_status IS NULL AND error_class IS NULL AND error_message IS NULL AND attempts IS NULL AND destination_id IS NULL AND resolution_source IS NULL FROM records WHERE id=$1`, observation.Metadata.ID).Scan(&nullableFields); err != nil || !nullableFields {
		t.Fatalf("inspection NULL fields: %v %v", nullableFields, err)
	}
	var snapshotAbsent bool
	if err := s.pool.QueryRow(t.Context(), `SELECT snapshot IS NULL FROM record_artifacts WHERE record_id=$1`, observation.Metadata.ID).Scan(&snapshotAbsent); err != nil || !snapshotAbsent {
		t.Fatalf("inspection snapshot: %v %v", snapshotAbsent, err)
	}
}

func TestEmptyArtifactAndInspectionUnavailableReason(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	empty := testFailure(t, func(f *NewRecord) { f.Payload = []byte{} })
	mustIngest(t, s, empty)
	payload, err := s.Payload(t.Context(), empty.Metadata.ID, "ops")
	if err != nil || len(payload.Bytes) != 0 {
		t.Fatalf("empty payload: %+v %v", payload, err)
	}
	var present bool
	if err := s.pool.QueryRow(t.Context(), `SELECT payload IS NOT NULL AND octet_length(payload)=0 FROM record_artifacts WHERE record_id=$1`, empty.Metadata.ID).Scan(&present); err != nil || !present {
		t.Fatalf("empty bytes are absent: %v %v", present, err)
	}
	observation := testInspection(t)
	observation.Payload, observation.Metadata.Payload = nil, nil
	observation.Metadata.PayloadUnavailableReason = "too_large"
	mustIngest(t, s, observation)
	detail, err := s.Get(t.Context(), observation.Metadata.ID)
	if err != nil || detail.NotRetryableReason != "inspection_only" || detail.PayloadUnavailableReason != "too_large" || detail.HasArtifacts {
		t.Fatalf("metadata-only inspection: %+v %v", detail, err)
	}
	if _, err := s.Payload(t.Context(), observation.Metadata.ID, "ops"); !errors.Is(err, ErrNoPayload) {
		t.Fatal(err)
	}
	// Dismissal preserves failure state and captured descriptions while removing bytes.
	before := empty.Metadata.Payload
	dismissed, err := s.Dismiss(t.Context(), empty.Metadata.ID, "ops")
	if err != nil || dismissed.DeliveryStatus != StatusOpen || dismissed.DismissedAt == nil || dismissed.Payload.SHA256 != before.SHA256 {
		t.Fatalf("dismissed: %+v %v", dismissed, err)
	}
	if _, err := s.Payload(t.Context(), empty.Metadata.ID, "ops"); !errors.Is(err, ErrGone) {
		t.Fatal(err)
	}
	if _, err := s.Dismiss(t.Context(), empty.Metadata.ID, "ops"); !errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}

}

func requireConstraint(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || (pgErr.Code != "23514" && pgErr.Code != "23503") {
		t.Fatalf("expected check or foreign-key constraint, got %v", err)
	}
}

func TestStatsSeparatesKindDeliveryAndDismissal(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	i := testInspection(t)
	mustIngest(t, s, f)
	mustIngest(t, s, i)
	if _, err := s.Dismiss(t.Context(), f.Metadata.ID, "ops"); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[replaywire.RecordState]int64{
		{Kind: replaywire.KindFailure, DeliveryStatus: StatusOpen, Dismissed: true}: 1,
		{Kind: replaywire.KindInspection}:                                           1,
	}
	if !reflect.DeepEqual(stats.RecordsByState, want) {
		t.Fatalf("record state counts = %+v, want %+v", stats.RecordsByState, want)
	}
}
