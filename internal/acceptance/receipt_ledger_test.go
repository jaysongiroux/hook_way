package acceptance

import "testing"

// This ledger lives in the independent test destination, not in core or replay.
// It survives fixture-owned core termination and distinguishes transport attempts,
// accepted receipts and synthetic idempotent consumer outcomes by provider ID.
type receiptLedger struct {
	Attempts         int
	Receipts         int
	ConsumerOutcomes int
}

func (s *sink) assertLedger(t *testing.T, event string, want receiptLedger) {
	t.Helper()
	s.mu.Lock()
	got := s.ledger[event]
	s.mu.Unlock()
	if got != want {
		t.Fatalf("provider event %s destination %s ledger %+v, want %+v", event, s.srv.URL, got, want)
	}
	t.Logf("receipt reconciliation provider_event=%s destination=%s attempts=%d receipts=%d consumer_outcomes=%d", event, s.srv.URL, got.Attempts, got.Receipts, got.ConsumerOutcomes)
}
