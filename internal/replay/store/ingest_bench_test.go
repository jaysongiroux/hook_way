package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// BenchmarkIngest measures stored records per second at each artifact size
// against a real PostgreSQL (snapshot and payload each of the given size).
func BenchmarkIngest(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, Options{URL: replaytest.Database(b), MaxConns: 8,
		Limits: Limits{TTL: time.Hour, MaxRecords: 1 << 40, MaxBytes: 1 << 50}})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	for _, parallel := range []bool{false, true} {
		for _, size := range []int{1 << 10, 256 << 10, 4 << 20, 16 << 20} {
			benchIngest(b, s, size, parallel)
		}
	}
}

// benchIngest ingests records of size; parallel runs GOMAXPROCS ingesters at
// once, which shows how far the usage-row lock serializes them.
func benchIngest(b *testing.B, s *Store, size int, parallel bool) {
	ctx := context.Background()
	art := make([]byte, size)
	if _, err := rand.Read(art); err != nil {
		b.Fatal(err)
	}
	sum := sha256.Sum256(art)
	digest := hex.EncodeToString(sum[:])
	name := fmt.Sprintf("%dKiB", size>>10)
	if parallel {
		name += "/parallel"
	}
	var seq atomic.Int64
	b.Run(name, func(b *testing.B) {
		b.SetBytes(int64(2 * size))
		ingest := func() {
			i := seq.Add(1)
			at := time.Now().UTC()
			m := replaywire.RecordMetadata{ID: uuid.Must(uuid.NewV7()).String(), Kind: replaywire.KindFailure, ReplayMode: replaywire.ReplayResume,
				Flow: "orders", NodeID: "out", NodeType: "publish", EventID: fmt.Sprintf("evt-%d", i),
				ReceivedAt: at, OccurredAt: at, InboundStatus: 202, ErrorClass: replaywire.ErrorClass5xx, Attempts: 1,
				Retryable: true, Snapshot: &replaywire.ArtifactInfo{Size: int64(size), SHA256: digest, Version: 1},
				Payload: &replaywire.ArtifactInfo{Size: int64(size), SHA256: digest}}
			if _, err := s.Ingest(ctx, NewRecord{Metadata: m, Snapshot: art, Payload: art, Actor: "bench"}); err != nil {
				b.Error(err)
			}
		}
		if !parallel {
			for range b.N {
				ingest()
			}
			return
		}
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				ingest()
			}
		})
	})
}
