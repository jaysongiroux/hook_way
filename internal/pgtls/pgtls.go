// Package pgtls applies a tls block to a PostgreSQL connection. The postgres
// destination and the replay service's store both use it, so the two cannot
// drift.
package pgtls

import (
	"crypto/tls"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

// Apply configures a PostgreSQL connection to use the tls block. With a
// block configured, TLS is mandatory: the plaintext fallbacks pgx adds for
// sslmode=prefer (its default) are removed, so a failed verification never
// downgrades to an unencrypted connection. Each host verifies against its
// own name unless server_name overrides it. An empty block leaves the url's
// sslmode in charge. label names the block in errors.
func Apply(cfg *pgconn.Config, f tlsfiles.Files, label string) error {
	if !f.Configured() {
		return nil
	}
	tlsCfg, err := f.Build(label)
	if err != nil {
		return err
	}
	forHost := func(host string) *tls.Config {
		t := tlsCfg.Clone()
		if t.ServerName == "" {
			t.ServerName = host
		}
		return t
	}
	cfg.TLSConfig = forHost(cfg.Host)
	kept := cfg.Fallbacks[:0]
	for _, fb := range cfg.Fallbacks {
		if fb.TLSConfig == nil {
			continue
		}
		fb.TLSConfig = forHost(fb.Host)
		kept = append(kept, fb)
	}
	cfg.Fallbacks = kept
	return nil
}
