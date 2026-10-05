package flowconfig

import (
	"context"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

// Legacy behavior fixtures deliberately bind an explicit named memory store.
// The new policy safety tests separately exercise startup/fingerprint failures.
func protectionTestOptions(t *testing.T, opts RegistryOptions) RegistryOptions {
	t.Helper()
	if opts.ValidateOnly || opts.Protection != nil {
		return opts
	}
	store := &fixtureProtectionStore{Store: protection.NewMemory("tests")}
	t.Cleanup(func() { _ = store.Close() })
	opts.Protection = func(name string) protection.Store {
		if name != "test" {
			return nil
		}
		return store
	}
	return opts
}

type fixtureProtectionStore struct{ protection.Store }

func (s *fixtureProtectionStore) Allow(ctx context.Context, p protection.Policy, key string) (protection.Decision, error) {
	if err := s.EnsurePolicy(ctx, p); err != nil {
		return protection.Decision{}, err
	}
	return s.Store.Allow(ctx, p, key)
}
func (s *fixtureProtectionStore) Reserve(ctx context.Context, p protection.Policy, key string) (protection.Reservation, error) {
	if err := s.EnsurePolicy(ctx, p); err != nil {
		return protection.Reservation{}, err
	}
	return s.Store.Reserve(ctx, p, key)
}
