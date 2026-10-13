package protection

import (
	"math"
	"time"
)

// State is adapter-owned persistence, with millisecond backend timestamps.
type State struct {
	Version     string `json:"version"`
	Tokens      int64  `json:"tokens"` // credits: 60000 per token, RPM per millisecond
	At          int64  `json:"at"`
	Current     int64  `json:"current"`
	Previous    int64  `json:"previous"`
	WindowStart int64  `json:"window_start"`
	ExpiresAt   int64  `json:"expires_at"`
	Owner       string `json:"owner"`
}

func Advance(p Policy, s State, now int64) State {
	now = max(now, s.At)
	if p.Strategy == TokenBucket {
		capacity := p.Burst * 60000
		delta := min(now-s.At, (capacity+p.RPM-1)/p.RPM)
		s.Tokens = min(capacity, s.Tokens+delta*p.RPM)
	}
	if p.Strategy == SlidingWindowCounter {
		window := p.Window.Milliseconds()
		start := now - now%window
		if start > s.WindowStart {
			if start-s.WindowStart == window {
				s.Previous = s.Current
			} else {
				s.Previous = 0
			}
			s.Current = 0
			s.WindowStart = start
		}
	}
	s.At = now
	return s
}
func Expiry(p Policy, s State) int64 {
	if p.Strategy == TokenBucket {
		return s.At + (p.Burst*60000+p.RPM-1)/p.RPM
	}
	return s.WindowStart + 2*p.Window.Milliseconds()
}
func Admit(p Policy, s State, exists bool, now int64) (State, Decision) {
	if !exists {
		s = State{Tokens: p.Burst * 60000, At: now, WindowStart: now - now%max(int64(1), p.Window.Milliseconds())}
	}
	s = Advance(p, s, now)
	d := Decision{}
	if p.Strategy == TokenBucket {
		if s.Tokens >= 60000 {
			s.Tokens -= 60000
			d.Allowed = true
		}
		d.Remaining = s.Tokens / 60000
		if !d.Allowed {
			d.RetryAfter = time.Duration((60000-s.Tokens+p.RPM-1)/p.RPM) * time.Millisecond
		}
	} else {
		window := p.Window.Milliseconds()
		weight := 1 - float64(s.At-s.WindowStart)/float64(window)
		used := float64(s.Current) + float64(s.Previous)*weight
		if used+1 <= float64(p.Limit) {
			s.Current++
			d.Allowed = true
			used++
		}
		d.Remaining = max(0, int64(math.Floor(float64(p.Limit)-used)))
		if !d.Allowed {
			d.RetryAfter = time.Duration(s.WindowStart+2*window-s.At) * time.Millisecond
		}
	}
	s.ExpiresAt = Expiry(p, s)
	return s, d
}
func Reconfigure(old, next Policy, s State, now int64) State {
	if old.Kind == ReplayGuard {
		return s
	}
	s = Advance(old, s, now)
	s.Tokens = min(s.Tokens, next.Burst*60000)
	s.ExpiresAt = Expiry(next, s)
	return s
}
