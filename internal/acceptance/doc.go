// Package acceptance runs the real hookway and hookway-replay binaries —
// two replicas of each, one PostgreSQL database — and checks the replay
// design's acceptance scenarios end to end. An in-test round-robin proxy
// stands in for Compose's service DNS. It has no non-test code; run it with
// `make replay-acceptance-test`.
package acceptance
