package acceptance

import "testing"

func TestFreeAddrDoesNotReuseAddressesBeforeStartup(t *testing.T) {
	seen := make(map[string]bool)
	// Replicas allocate HTTP, control and metrics addresses before any process
	// starts listening. Closing a probe must not make its address reusable here.
	for range 1000 {
		addr := freeAddr(t)
		if seen[addr] {
			t.Fatalf("allocated address %s twice before starting listeners", addr)
		}
		seen[addr] = true
	}
}
