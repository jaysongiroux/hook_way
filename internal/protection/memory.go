package protection

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

type memoryEntry struct {
	key   string
	state State
	index int
}
type expiryHeap []*memoryEntry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].state.ExpiresAt < h[j].state.ExpiresAt }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *expiryHeap) Push(v any)        { e := v.(*memoryEntry); e.index = len(*h); *h = append(*h, e) }
func (h *expiryHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return e
}

type memoryPolicy struct {
	gate     chan struct{}
	policy   Policy
	entries  map[string]*memoryEntry
	expiry   expiryHeap
	pending  *Policy
	updateAt int64
}
type Memory struct {
	mu       sync.Mutex
	policies map[string]*memoryPolicy
	closed   bool
	now      func() time.Time
	batch    int
	timeout  time.Duration
	budget   time.Duration
}

func NewMemory(_ string) *Memory {
	return &Memory{policies: map[string]*memoryPolicy{}, now: time.Now, batch: 100, timeout: 250 * time.Millisecond, budget: 100 * time.Millisecond}
}
func NewMemoryWithConfig(cfg Config) *Memory {
	cfg = cfg.Defaults()
	m := NewMemory(cfg.Namespace)
	m.batch = cfg.Cleanup.BatchSize
	m.timeout = cfg.Timeout
	m.budget = cfg.Cleanup.Budget
	return m
}
func (v *memoryPolicy) lock(ctx context.Context) error {
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	select {
	case v.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrUnavailable
	}
}
func (v *memoryPolicy) Unlock() { <-v.gate }
func (m *Memory) lock(ctx context.Context, v *memoryPolicy) error {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	return v.lock(ctx)
}
func (m *Memory) get(ctx context.Context, p Policy) (*memoryPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrUnavailable
	}
	v := m.policies[p.ID]
	if v == nil {
		return nil, ErrPolicyConflict
	}
	return v, nil
}
func (m *Memory) EnsurePolicy(ctx context.Context, p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrUnavailable
	}
	v := m.policies[p.ID]
	if v == nil {
		if len(m.policies) >= 10000 {
			m.mu.Unlock()
			return ErrCapacity
		}
		m.policies[p.ID] = &memoryPolicy{policy: p, entries: map[string]*memoryEntry{}, gate: make(chan struct{}, 1)}
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if err := m.lock(ctx, v); err != nil {
		return err
	}
	defer v.Unlock()
	return v.check(p)
}
func (v *memoryPolicy) check(p Policy) error {
	if v.pending != nil || v.policy.Fingerprint() != p.Fingerprint() {
		return ErrPolicyConflict
	}
	return nil
}
func (m *Memory) CheckPolicy(ctx context.Context, p Policy) error {
	v, err := m.get(ctx, p)
	if err != nil {
		return err
	}
	if err := m.lock(ctx, v); err != nil {
		return err
	}
	defer v.Unlock()
	return v.check(p)
}
func (m *Memory) PolicyStatus(ctx context.Context, id string) (Status, error) {
	v, err := m.get(ctx, Policy{ID: id})
	if err != nil {
		return Status{}, err
	}
	if err := m.lock(ctx, v); err != nil {
		return Status{}, err
	}
	defer v.Unlock()
	s := Status{Policy: v.policy, Fingerprint: v.policy.Fingerprint(), Updating: v.pending != nil}
	if v.pending != nil {
		s.TargetFingerprint = v.pending.Fingerprint()
	}
	return s, nil
}
func (v *memoryPolicy) clean(now int64, n int) int64 {
	var removed int64
	for n > 0 && len(v.expiry) > 0 && v.expiry[0].state.ExpiresAt <= now {
		e := heap.Pop(&v.expiry).(*memoryEntry)
		delete(v.entries, e.key)
		removed++
		n--
	}
	return removed
}
func (v *memoryPolicy) put(key string, s State) {
	if e := v.entries[key]; e != nil {
		e.state = s
		heap.Fix(&v.expiry, e.index)
	} else {
		e := &memoryEntry{key: key, state: s}
		v.entries[key] = e
		heap.Push(&v.expiry, e)
	}
}
func (m *Memory) Allow(ctx context.Context, p Policy, key string) (Decision, error) {
	if key == "" {
		return Decision{}, ErrMissingKey
	}
	if p.Kind != RateLimit {
		return Decision{}, ErrInvalidPolicy
	}
	v, err := m.get(ctx, p)
	if err != nil {
		return Decision{}, err
	}
	if err := m.lock(ctx, v); err != nil {
		return Decision{}, err
	}
	defer v.Unlock()
	if err = v.check(p); err != nil {
		return Decision{}, err
	}
	now := m.now().UnixMilli()
	v.clean(now, m.batch)
	hash := HashIdentity(key)
	e := v.entries[hash]
	if e == nil && int64(len(v.entries)) >= p.MaxEntries {
		return Decision{}, ErrCapacity
	}
	s := State{}
	if e != nil {
		s = e.state
	}
	s, d := Admit(p, s, e != nil, now)
	s.Version = p.Fingerprint()
	v.put(hash, s)
	return d, nil
}
func (m *Memory) Reserve(ctx context.Context, p Policy, key string) (Reservation, error) {
	if key == "" {
		return Reservation{}, ErrMissingKey
	}
	if p.Kind != ReplayGuard {
		return Reservation{}, ErrInvalidPolicy
	}
	owner, err := NewOwner()
	if err != nil {
		return Reservation{}, err
	}
	v, err := m.get(ctx, p)
	if err != nil {
		return Reservation{}, err
	}
	if err := m.lock(ctx, v); err != nil {
		return Reservation{}, err
	}
	defer v.Unlock()
	if err = v.check(p); err != nil {
		return Reservation{}, err
	}
	now := m.now().UnixMilli()
	v.clean(now, m.batch)
	hash := HashIdentity(key)
	if e := v.entries[hash]; e != nil {
		if e.state.ExpiresAt > now {
			return Reservation{ExpiresAt: time.UnixMilli(e.state.ExpiresAt)}, nil
		}
		heap.Remove(&v.expiry, e.index)
		delete(v.entries, hash)
	}
	if int64(len(v.entries)) >= p.MaxEntries {
		return Reservation{}, ErrCapacity
	}
	s := State{Owner: owner, ExpiresAt: now + p.TTL.Milliseconds(), Version: p.Fingerprint()}
	v.put(hash, s)
	return Reservation{Accepted: true, Owner: owner, ExpiresAt: time.UnixMilli(s.ExpiresAt)}, nil
}
func (m *Memory) Release(ctx context.Context, p Policy, key, owner string) error {
	if key == "" {
		return ErrMissingKey
	}
	v, err := m.get(ctx, p)
	if err != nil {
		return err
	}
	if err := m.lock(ctx, v); err != nil {
		return err
	}
	defer v.Unlock()
	if err = v.check(p); err != nil {
		return err
	}
	hash := HashIdentity(key)
	if e := v.entries[hash]; e != nil && owner != "" && e.state.Owner == owner {
		heap.Remove(&v.expiry, e.index)
		delete(v.entries, hash)
	}
	return nil
}
func (m *Memory) UpdatePolicy(ctx context.Context, p Policy, expected string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	v, err := m.get(ctx, p)
	if err != nil {
		return err
	}
	if err := v.lock(ctx); err != nil {
		return err
	}
	defer v.Unlock()
	if v.policy.Fingerprint() != expected || !v.policy.IsCompatibleUpdate(p) {
		return ErrPolicyConflict
	}
	if v.pending != nil && v.pending.Fingerprint() != p.Fingerprint() {
		return ErrPolicyConflict
	}
	if v.pending == nil {
		v.clean(m.now().UnixMilli(), m.batch)
		if int64(len(v.entries)) > p.MaxEntries {
			return ErrCapacity
		}
		v.pending = &p
		v.updateAt = m.now().UnixMilli()
	}
	n := 0
	for _, e := range v.entries {
		if n%100 == 0 && ctx.Err() != nil {
			return ErrUnavailable
		}
		n++
		if e.state.Version != p.Fingerprint() {
			e.state = Reconfigure(v.policy, p, e.state, v.updateAt)
			e.state.Version = p.Fingerprint()
			heap.Fix(&v.expiry, e.index)
		}
	}
	v.policy = p
	v.pending = nil
	return nil
}
func (m *Memory) GC(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, m.budget)
	defer cancel()
	if err := m.Check(ctx); err != nil {
		return 0, err
	}
	m.mu.Lock()
	values := make([]*memoryPolicy, 0, len(m.policies))
	for _, v := range m.policies {
		values = append(values, v)
	}
	m.mu.Unlock()
	var n int64
	for _, v := range values {
		if ctx.Err() != nil {
			return n, ErrUnavailable
		}
		if err := m.lock(ctx, v); err != nil {
			return n, err
		}
		if v.pending == nil {
			n += v.clean(m.now().UnixMilli(), m.batch-int(n))
		}
		v.Unlock()
		if n >= int64(m.batch) {
			break
		}
	}
	return n, nil
}
func (m *Memory) Check(ctx context.Context) error {
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrUnavailable
	}
	return nil
}
func (m *Memory) Close() error { m.mu.Lock(); defer m.mu.Unlock(); m.closed = true; return nil }
