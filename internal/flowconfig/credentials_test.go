package flowconfig

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestCredentialProfileBoundsAndReservedHeaders(t *testing.T) {
	ref := SecretRefOptions{Provider: "env", Name: "SAMPLE_KEY"}
	profile := func() CredentialProfile {
		return CredentialProfile{Kind: "static", AllowedOrigins: []string{"https://api.example.org"}, Headers: map[string]CredentialHeader{"X-Key": {SecretRef: ref}}}
	}
	for _, tc := range []struct {
		name string
		edit func(*CredentialProfile)
	}{
		{"too many static headers", func(p *CredentialProfile) {
			for i := 0; i < 32; i++ {
				p.Headers[fmt.Sprintf("X-Extra-%d", i)] = CredentialHeader{SecretRef: ref}
			}
		}},
		{"loop guard override", func(p *CredentialProfile) {
			p.Headers[strings.ToLower(config.LoopHopsHeader)] = CredentialHeader{SecretRef: ref}
		}},
		{"NUL prefix", func(p *CredentialProfile) {
			p.Headers["X-Key"] = CredentialHeader{SecretRef: ref, Prefix: "prefix\x00"}
		}},
		{"oversized prefix", func(p *CredentialProfile) {
			p.Headers["X-Key"] = CredentialHeader{SecretRef: ref, Prefix: strings.Repeat("x", 8193)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := profile()
			tc.edit(&p)
			if p.Validate() == nil {
				t.Fatal("accepted unsafe profile")
			}
		})
	}
	p := profile()
	for i := 0; i < 31; i++ {
		p.Headers[fmt.Sprintf("X-Extra-%d", i)] = CredentialHeader{SecretRef: ref}
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("valid 32-header profile: %v", err)
	}
	for _, key := range []string{strings.Repeat("x", 257), "key\x00", "key\n"} {
		// #nosec G101 -- Synthetic field names and secret references, no credential material.
		p := CredentialProfile{Kind: "token_exchange", AllowedOrigins: []string{"https://api.example.org"}, TokenURL: "https://login.example.org/token", TokenPath: "token", SecretParameters: map[string]SecretRefOptions{key: ref}}
		if p.Validate() == nil {
			t.Fatal("accepted unsafe parameter name")
		}
	}
}
