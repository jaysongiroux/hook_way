package conformance

import (
	"context"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/protection"
)

func bindFixtureProtection(t *testing.T, opts flowconfig.RegistryOptions) flowconfig.RegistryOptions {
	t.Helper()
	if opts.Protection != nil || opts.ValidateOnly {
		return opts
	}
	store := &fixtureProtection{Store: protection.NewMemory("tests")}
	t.Cleanup(func() { _ = store.Close() })
	opts.Protection = func(name string) protection.Store {
		if name != "test" {
			return nil
		}
		return store
	}
	return opts
}

type fixtureProtection struct{ protection.Store }

func (s *fixtureProtection) Allow(ctx context.Context, p protection.Policy, key string) (protection.Decision, error) {
	if err := s.EnsurePolicy(ctx, p); err != nil {
		return protection.Decision{}, err
	}
	return s.Store.Allow(ctx, p, key)
}
func (s *fixtureProtection) Reserve(ctx context.Context, p protection.Policy, key string) (protection.Reservation, error) {
	if err := s.EnsurePolicy(ctx, p); err != nil {
		return protection.Reservation{}, err
	}
	return s.Store.Reserve(ctx, p, key)
}
