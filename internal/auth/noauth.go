package auth

import "time"

const SchemeNoAuth = "no_auth"

// noAuthVerifier accepts every request without verifying anything. It exists for
// endpoints that intentionally disable authentication. This is unsafe for any
// traffic that carries trust; NewServer logs a warning whenever one is built.
type noAuthVerifier struct{}

func newNoAuthVerifier() noAuthVerifier { return noAuthVerifier{} }

func (noAuthVerifier) Verify(string, []byte, time.Time) error { return nil }
