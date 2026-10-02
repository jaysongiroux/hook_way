package metrics

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestAdmissionSharedRegistryAndRetirement(t *testing.T) {
	m := New()
	old := m.NewAdmission(3)
	next := m.NewAdmission(5)
	unlimited := m.NewAdmission(0)
	check := func(capacity, bounded, unbounded, servers float64) {
		t.Helper()
		for _, value := range []struct{ got, want float64 }{
			{testutil.ToFloat64(m.admissionCapacity), capacity},
			{testutil.ToFloat64(m.admitted.WithLabelValues("bounded")), bounded},
			{testutil.ToFloat64(m.admitted.WithLabelValues("unlimited")), unbounded},
			{testutil.ToFloat64(m.admissionServers.WithLabelValues("bounded")), servers},
		} {
			if value.got != value.want {
				t.Fatalf("gauge = %v, want %v", value.got, value.want)
			}
		}
	}
	if !old.Enter() || !next.Enter() || !unlimited.Enter() {
		t.Fatal("open owner rejected work")
	}
	check(8, 2, 1, 2)
	old.Close()
	old.Close()
	check(8, 2, 1, 2) // Retain old capacity while it drains.
	if old.Enter() {
		t.Fatal("retired owner accepted new work")
	}
	old.Leave()
	check(5, 1, 1, 1)
	next.Leave()
	next.Close()
	unlimited.Close()
	check(0, 0, 1, 0)
	unlimited.Leave()
	check(0, 0, 0, 0)
	if got := testutil.ToFloat64(m.admissionServers.WithLabelValues("unlimited")); got != 0 {
		t.Fatal(got)
	}
}

func TestAdmissionConcurrentCloseAndLeave(t *testing.T) {
	m := New()
	a := m.NewAdmission(64)
	for range 64 {
		if !a.Enter() {
			t.Fatal("enter")
		}
	}
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() { defer wg.Done(); a.Close(); a.Leave(); a.Close() }()
	}
	wg.Wait()
	if testutil.ToFloat64(m.admissionCapacity) != 0 || testutil.ToFloat64(m.admitted.WithLabelValues("bounded")) != 0 || testutil.ToFloat64(m.admissionServers.WithLabelValues("bounded")) != 0 {
		t.Fatal("retirement leaked or double-subtracted gauges")
	}
}
