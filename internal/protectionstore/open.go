// Package protectionstore contains native shared protection adapters.
package protectionstore

import (
	"context"
	"errors"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

func Open(ctx context.Context, cfg protection.Config) (protection.Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.Defaults()
	ctx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()
	var s protection.Store
	var err error
	switch {
	case cfg.Memory != nil:
		s = protection.NewMemoryWithConfig(cfg)
	case cfg.Redis != nil:
		s, err = OpenRedis(ctx, cfg)
	case cfg.Postgres != nil:
		s, err = OpenPostgres(ctx, cfg)
	}
	if err != nil {
		return nil, err
	}
	if err = s.Check(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}
func bounded(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
func unavailable(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, protection.ErrCapacity) || errors.Is(err, protection.ErrPolicyConflict) || errors.Is(err, protection.ErrInvalidPolicy) || errors.Is(err, protection.ErrMissingKey) {
		return err
	}
	return protection.ErrUnavailable
}
