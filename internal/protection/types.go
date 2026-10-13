// Package protection defines bounded admission policies without native clients.
package protection

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	RateLimit            = "rate_limit"
	ReplayGuard          = "replay_guard"
	TokenBucket          = "token_bucket"
	SlidingWindowCounter = "sliding_window_counter"
	MaxPolicyEntries     = 1000000
	// DefaultRateLimitMaxKeys bounds identities when no capacity is authored.
	DefaultRateLimitMaxKeys = 10000
)

var (
	ErrCapacity       = errors.New("protection capacity exhausted")
	ErrPolicyConflict = errors.New("protection policy missing or conflicting")
	ErrUnavailable    = errors.New("protection store unavailable or operation uncertain")
	ErrMissingKey     = errors.New("protection key is missing")
	ErrInvalidPolicy  = errors.New("invalid protection policy")
)

type Policy struct {
	Kind       string        `json:"kind"`
	ID         string        `json:"id"`
	KeyScope   string        `json:"key_scope"`
	Strategy   string        `json:"strategy"`
	RPM        int64         `json:"rpm"`
	Burst      int64         `json:"burst"`
	Limit      int64         `json:"limit"`
	Window     time.Duration `json:"window"`
	TTL        time.Duration `json:"ttl"`
	MaxEntries int64         `json:"max_entries"`
}

func (p Policy) Validate() error {
	if !validName(p.ID, 255) || !validName(p.KeyScope, 1024) || p.MaxEntries < 1 || p.MaxEntries > MaxPolicyEntries {
		return ErrInvalidPolicy
	}
	switch p.Kind {
	case RateLimit:
		if p.TTL != 0 {
			return ErrInvalidPolicy
		}
		switch p.Strategy {
		case TokenBucket:
			if p.RPM < 1 || p.RPM > 1000000 || p.Burst < 1 || p.Burst > 1000000 || p.Limit != 0 || p.Window != 0 {
				return ErrInvalidPolicy
			}
		case SlidingWindowCounter:
			if p.Limit < 1 || p.Limit > 1000000 || p.Window < time.Millisecond || p.Window > 24*time.Hour || p.Window%time.Millisecond != 0 || p.RPM != 0 || p.Burst != 0 {
				return ErrInvalidPolicy
			}
		default:
			return ErrInvalidPolicy
		}
	case ReplayGuard:
		if p.TTL < time.Millisecond || p.TTL > 30*24*time.Hour || p.TTL%time.Millisecond != 0 || p.Strategy != "" || p.RPM != 0 || p.Burst != 0 || p.Limit != 0 || p.Window != 0 {
			return ErrInvalidPolicy
		}
	default:
		return ErrInvalidPolicy
	}
	return nil
}
func validName(s string, max int) bool {
	return s != "" && len(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}
func (p Policy) Fingerprint() string {
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (p Policy) IsCompatibleUpdate(next Policy) bool {
	return p.Kind == next.Kind && p.ID == next.ID && p.KeyScope == next.KeyScope && p.Strategy == next.Strategy && p.Window == next.Window
}
func HashIdentity(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
func PolicyKey(namespace, id string) string {
	b, _ := json.Marshal([]string{namespace, id})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func NewOwner() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(b), nil
}

type Decision struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
}
type Reservation struct {
	Accepted  bool
	Owner     string
	ExpiresAt time.Time
}
type Status struct {
	Policy            Policy `json:"policy"`
	Fingerprint       string `json:"fingerprint"`
	TargetFingerprint string `json:"target_fingerprint,omitempty"`
	Updating          bool   `json:"updating"`
}
type Statistics struct {
	InUse, Idle  int64
	WaitDuration time.Duration
}
type Statser interface{ Statistics() Statistics }
type Store interface {
	EnsurePolicy(context.Context, Policy) error
	CheckPolicy(context.Context, Policy) error
	PolicyStatus(context.Context, string) (Status, error)
	Allow(context.Context, Policy, string) (Decision, error)
	Reserve(context.Context, Policy, string) (Reservation, error)
	Release(context.Context, Policy, string, string) error
	UpdatePolicy(context.Context, Policy, string) error
	Check(context.Context) error
	GC(context.Context) (int64, error)
	Close() error
}
