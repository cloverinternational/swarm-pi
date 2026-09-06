package lan

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

var ErrListenerTLS = errors.New("lan: invalid listener TLS identity")

// ListenerTLSEvidence is opaque proof that one exact certificate/key pair was
// loaded and validated for one advertised listener DNS name or IP. Its fields
// are intentionally unexported so gateway callers cannot replace validation
// with a TLSConfigured boolean assertion.
type ListenerTLSEvidence struct {
	certificate tls.Certificate
	leaf        *x509.Certificate
	identity    string
}

// LoadListenerTLSEvidence loads the exact PEM certificate/key pair and proves:
// the private key matches the leaf certificate, the leaf is currently valid,
// it explicitly permits TLS server authentication, and its SAN covers the
// advertised listener hostname or IP.
func LoadListenerTLSEvidence(certPath, keyPath, advertisedListener string, now time.Time) (*ListenerTLSEvidence, error) {
	identity, err := listenerIdentity(advertisedListener)
	if err != nil {
		return nil, err
	}
	certPEM, err := readRegularNoSymlink(certPath, false)
	if err != nil {
		return nil, fmt.Errorf("%w: certificate unavailable", ErrListenerTLS)
	}
	keyPEM, err := readRegularNoSymlink(keyPath, true)
	if err != nil {
		return nil, fmt.Errorf("%w: private key unavailable", ErrListenerTLS)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%w: certificate/key mismatch or malformed PEM", ErrListenerTLS)
	}
	if len(pair.Certificate) == 0 {
		return nil, fmt.Errorf("%w: empty certificate chain", ErrListenerTLS)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("%w: malformed leaf certificate", ErrListenerTLS)
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("%w: certificate is not currently valid", ErrListenerTLS)
	}
	if !allowsServerAuth(leaf.ExtKeyUsage) {
		return nil, fmt.Errorf("%w: certificate does not permit server authentication", ErrListenerTLS)
	}
	if err := leaf.VerifyHostname(identity); err != nil {
		return nil, fmt.Errorf("%w: advertised listener is not present in certificate SAN", ErrListenerTLS)
	}
	pair.Leaf = leaf
	return &ListenerTLSEvidence{certificate: pair, leaf: leaf, identity: identity}, nil
}

// ValidFor rechecks the immutable advertised identity and certificate
// validity. The loaded keypair itself cannot be swapped after evidence creation.
func (e *ListenerTLSEvidence) ValidFor(advertisedListener string, now time.Time) error {
	if e == nil || e.leaf == nil {
		return ErrListenerTLS
	}
	identity, err := listenerIdentity(advertisedListener)
	if err != nil || identity != e.identity {
		return fmt.Errorf("%w: listener identity differs from validated evidence", ErrListenerTLS)
	}
	if now.Before(e.leaf.NotBefore) || !now.Before(e.leaf.NotAfter) {
		return fmt.Errorf("%w: certificate is not currently valid", ErrListenerTLS)
	}
	return nil
}

// Identity returns the non-secret advertised DNS name or canonical IP covered
// by this evidence.
func (e *ListenerTLSEvidence) Identity() string {
	if e == nil {
		return ""
	}
	return e.identity
}

// TLSConfig returns a fresh server configuration containing only the exact
// validated certificate/key pair. TLS 1.3 is mandatory for non-loopback
// authenticated control.
func (e *ListenerTLSEvidence) TLSConfig() (*tls.Config, error) {
	if e == nil || e.leaf == nil {
		return nil, ErrListenerTLS
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{e.certificate},
	}, nil
}

func listenerIdentity(listener string) (string, error) {
	host := listener
	if split, _, err := net.SplitHostPort(listener); err == nil {
		host = split
	}
	host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(host), "["), "]")
	if host == "" {
		return "", fmt.Errorf("%w: advertised listener identity is empty", ErrListenerTLS)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() {
			return "", fmt.Errorf("%w: wildcard address cannot be advertised", ErrListenerTLS)
		}
		return ip.String(), nil
	}
	if strings.ContainsAny(host, "/\\%") {
		return "", fmt.Errorf("%w: malformed advertised listener identity", ErrListenerTLS)
	}
	return strings.ToLower(host), nil
}

func allowsServerAuth(usages []x509.ExtKeyUsage) bool {
	for _, usage := range usages {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func readRegularNoSymlink(path string, ownerOnly bool) ([]byte, error) {
	if path == "" {
		return nil, ErrListenerTLS
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, ErrListenerTLS
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrListenerTLS
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, ErrListenerTLS
	}
	if ownerOnly && (opened.Mode().Perm() != 0o600 || !ownedByCurrentUser(opened)) {
		return nil, ErrListenerTLS
	}
	const maxTLSFileBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxTLSFileBytes+1))
	if err != nil {
		return nil, ErrListenerTLS
	}
	if len(data) > maxTLSFileBytes {
		return nil, ErrListenerTLS
	}
	return data, nil
}
