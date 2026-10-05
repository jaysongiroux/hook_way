package queue

import (
	"crypto/tls"

	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

// tlsMaterial is the certificate and verification settings shared by every
// TLS-capable destination type.
type tlsMaterial = tlsfiles.Files

// newTLSConfig keeps destination labels and CA path semantics at the adapter
// boundary; certificate loading is shared with other TLS clients.
func newTLSConfig(label string, m tlsMaterial) (*tls.Config, error) {
	return m.Build(label+" tls", tlsfiles.BuildOptions{PreserveCAPath: true})
}
