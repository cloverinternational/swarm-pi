package lan

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTLSFixture(t *testing.T, now time.Time, dns []string, ips []net.IP, usages []x509.ExtKeyUsage) (string, string) {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fixture"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		DNSNames:     dns,
		IPAddresses:  ips,
		ExtKeyUsage:  usages,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "listener.crt")
	keyPath := filepath.Join(dir, "listener.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestLoadListenerTLSEvidenceValidatesExactIdentity(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	cert, key := writeTLSFixture(t, now, []string{"gateway.example"}, []net.IP{net.ParseIP("192.0.2.10")}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	evidence, err := LoadListenerTLSEvidence(cert, key, "192.0.2.10:8787", now)
	if err != nil {
		t.Fatalf("LoadListenerTLSEvidence: %v", err)
	}
	if evidence.Identity() != "192.0.2.10" {
		t.Fatalf("identity = %q", evidence.Identity())
	}
	cfg, err := evidence.TLSConfig()
	if err != nil || cfg.MinVersion != 0x0304 || len(cfg.Certificates) != 1 {
		t.Fatalf("TLS config = %+v, err=%v", cfg, err)
	}
}

func TestLoadListenerTLSEvidenceRejectsInvalidMaterialBeforeUse(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	t.Run("wrong SAN", func(t *testing.T) {
		cert, key := writeTLSFixture(t, now, []string{"other.example"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
		if _, err := LoadListenerTLSEvidence(cert, key, "gateway.example:8787", now); err == nil {
			t.Fatal("wrong SAN accepted")
		}
	})
	t.Run("client auth only", func(t *testing.T) {
		cert, key := writeTLSFixture(t, now, []string{"gateway.example"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
		if _, err := LoadListenerTLSEvidence(cert, key, "gateway.example:8787", now); err == nil {
			t.Fatal("certificate without server-auth usage accepted")
		}
	})
	t.Run("expired", func(t *testing.T) {
		cert, key := writeTLSFixture(t, now.Add(-2*time.Hour), []string{"gateway.example"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
		if _, err := LoadListenerTLSEvidence(cert, key, "gateway.example:8787", now); err == nil {
			t.Fatal("expired certificate accepted")
		}
	})
	t.Run("key mismatch", func(t *testing.T) {
		cert, _ := writeTLSFixture(t, now, []string{"gateway.example"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
		_, otherKey := writeTLSFixture(t, now, []string{"gateway.example"}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
		if _, err := LoadListenerTLSEvidence(cert, otherKey, "gateway.example:8787", now); err == nil {
			t.Fatal("mismatched private key accepted")
		}
	})
	t.Run("wildcard advertised identity", func(t *testing.T) {
		cert, key := writeTLSFixture(t, now, nil, []net.IP{net.ParseIP("192.0.2.10")}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
		if _, err := LoadListenerTLSEvidence(cert, key, "0.0.0.0:8787", now); err == nil {
			t.Fatal("wildcard advertised identity accepted")
		}
	})
}
