package auth

import (
	"math"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	failedPeerBurst   = 10
	failedPeerRate    = 10.0 / 60
	failedGlobalBurst = 200
	failedGlobalRate  = 100.0
	maxFailedPeers    = 4096
	failedPeerIdle    = 15 * time.Minute
)

type failureBucket struct {
	tokens        float64
	updated, seen time.Time
}
type failureLimiter struct {
	mu               sync.Mutex
	peers            map[string]*failureBucket
	global, overflow failureBucket
	now              func() time.Time
	nextSweep        time.Time
}

func newFailureLimiter() *failureLimiter {
	return &failureLimiter{peers: make(map[string]*failureBucket), now: time.Now}
}

func (b *failureBucket) refill(now time.Time, burst, rate float64) {
	if b.updated.IsZero() {
		b.tokens = burst
	} else if elapsed := now.Sub(b.updated).Seconds(); elapsed > 0 {
		b.tokens = math.Min(burst, b.tokens+elapsed*rate)
	}
	b.updated, b.seen = now, now
}

// reject consumes only a failed attempt. Peer identity comes exclusively from
// the transport RemoteAddr; ephemeral source ports and forwarded headers do not
// let the caller create independent allowances. Overflow shares one bucket.
func (l *failureLimiter) reject(remote string) time.Duration {
	peer, _, err := net.SplitHostPort(remote)
	if err != nil {
		peer = remote
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !now.Before(l.nextSweep) {
		for key, bucket := range l.peers {
			if now.Sub(bucket.seen) >= failedPeerIdle {
				delete(l.peers, key)
			}
		}
		l.nextSweep = now.Add(time.Minute)
	}
	bucket := l.peers[peer]
	if bucket == nil {
		if len(l.peers) < maxFailedPeers {
			bucket = &failureBucket{}
			l.peers[peer] = bucket
		} else {
			bucket = &l.overflow
		}
	}
	bucket.refill(now, failedPeerBurst, failedPeerRate)
	l.global.refill(now, failedGlobalBurst, failedGlobalRate)
	wait := math.Max((1-bucket.tokens)/failedPeerRate, (1-l.global.tokens)/failedGlobalRate)
	if wait > 0 {
		return time.Duration(math.Ceil(wait * float64(time.Second)))
	}
	bucket.tokens--
	l.global.tokens--
	return 0
}

func retryAfter(wait time.Duration) string {
	return strconv.Itoa(max(1, int(math.Ceil(wait.Seconds()))))
}
