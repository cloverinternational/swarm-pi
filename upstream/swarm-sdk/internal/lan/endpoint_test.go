package lan

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAuthenticatedEndpointIsNotAStringAlias(t *testing.T) {
	var e AuthenticatedEndpoint
	if reflect.TypeOf(e).Kind() == reflect.String {
		t.Fatalf("AuthenticatedEndpoint must not be a string alias, got kind %v", reflect.TypeOf(e).Kind())
	}
	if reflect.TypeOf(e).Kind() != reflect.Struct {
		t.Fatalf("AuthenticatedEndpoint must be a struct, got kind %v", reflect.TypeOf(e).Kind())
	}
}

// TestAuthenticatedEndpointHasNoURLOnlyConstructor is a compile-time-shaped
// regression proof: every exported constructor in this package requires a
// fully populated proof value alongside the URL. This test exercises both
// constructors with an empty proof and confirms both fail -- there is no way
// to reach a non-zero AuthenticatedEndpoint from a URL string alone.
func TestAuthenticatedEndpointHasNoURLOnlyConstructor(t *testing.T) {
	now := time.Now()

	if _, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9000", LocalEndpointProof{}, now); err == nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint with zero-value proof must fail (raw-URL-only attempt)")
	} else if !errors.Is(err, ErrEndpointProvenance) {
		t.Fatalf("NewLocalAuthenticatedEndpoint zero-proof error = %v, want ErrEndpointProvenance", err)
	}

	if _, err := NewRemoteAuthenticatedEndpoint("https://peer.example:9000", RemoteAdvertisementProof{}, now); err == nil {
		t.Fatalf("NewRemoteAuthenticatedEndpoint with zero-value proof must fail (raw-URL-only attempt)")
	} else if !errors.Is(err, ErrEndpointProvenance) {
		t.Fatalf("NewRemoteAuthenticatedEndpoint zero-proof error = %v, want ErrEndpointProvenance", err)
	}
}

func validLocalProof(now time.Time) LocalEndpointProof {
	return LocalEndpointProof{
		OwnerVerified:   true,
		PeerIdentity:    "daemon-self",
		DaemonInstance:  "instance-abc123",
		LeaseID:         "lease-1",
		PublicationPath: "/home/user/.swarm/swarms/default/peers/daemon-self.json",
		Signer:          "local-user-authority",
		IssuedAt:        now.Add(-time.Minute),
	}
}

func TestNewLocalAuthenticatedEndpoint_Success(t *testing.T) {
	now := time.Now()
	proof := validLocalProof(now)
	e, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099/", proof, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Provenance() != ProvenanceLocal {
		t.Fatalf("Provenance() = %v, want ProvenanceLocal", e.Provenance())
	}
	if !e.IsLocal() || e.IsRemote() {
		t.Fatalf("IsLocal/IsRemote mismatch for a local endpoint")
	}
	if e.PeerIdentity() != proof.PeerIdentity {
		t.Fatalf("PeerIdentity() = %q, want %q", e.PeerIdentity(), proof.PeerIdentity)
	}
	if e.DaemonInstance() != proof.DaemonInstance {
		t.Fatalf("DaemonInstance() = %q, want %q", e.DaemonInstance(), proof.DaemonInstance)
	}
	if e.SchemaVersion() != EndpointSchemaVersion {
		t.Fatalf("SchemaVersion() = %d, want %d", e.SchemaVersion(), EndpointSchemaVersion)
	}
	if _, ok := e.TLSIdentity(); ok {
		t.Fatalf("a local endpoint must not carry a TLS identity")
	}
	if e.URL().String() != "http://127.0.0.1:9099/" {
		t.Fatalf("URL() = %q, want normalized http://127.0.0.1:9099/", e.URL().String())
	}
	// Mutating the returned URL must never affect the endpoint.
	u := e.URL()
	u.Host = "evil.example:1"
	if e.URL().Host == "evil.example:1" {
		t.Fatalf("URL() leaked a mutable reference to the endpoint's normalized URL")
	}
}

func TestNewLocalAuthenticatedEndpoint_MissingProvenanceFields(t *testing.T) {
	now := time.Now()
	base := validLocalProof(now)

	cases := []struct {
		name   string
		mutate func(p *LocalEndpointProof)
	}{
		{"owner not verified", func(p *LocalEndpointProof) { p.OwnerVerified = false }},
		{"missing peer identity", func(p *LocalEndpointProof) { p.PeerIdentity = "" }},
		{"missing daemon instance", func(p *LocalEndpointProof) { p.DaemonInstance = "" }},
		{"missing lease", func(p *LocalEndpointProof) { p.LeaseID = "" }},
		{"missing publication path", func(p *LocalEndpointProof) { p.PublicationPath = "" }},
		{"missing signer", func(p *LocalEndpointProof) { p.Signer = "" }},
		{"missing issued-at", func(p *LocalEndpointProof) { p.IssuedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proof := base
			tc.mutate(&proof)
			if _, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099", proof, now); err == nil {
				t.Fatalf("expected denial for %s", tc.name)
			} else if !errors.Is(err, ErrEndpointProvenance) {
				t.Fatalf("%s: error = %v, want ErrEndpointProvenance", tc.name, err)
			}
		})
	}
}

func TestNewLocalAuthenticatedEndpoint_PlainHTTPRequiresLoopbackLiteral(t *testing.T) {
	now := time.Now()
	proof := validLocalProof(now)

	if _, err := NewLocalAuthenticatedEndpoint("http://93.184.216.34:9099", proof, now); err == nil {
		t.Fatalf("expected denial for plain-http non-loopback host")
	} else if !errors.Is(err, ErrEndpointScheme) {
		t.Fatalf("error = %v, want ErrEndpointScheme", err)
	}

	if _, err := NewLocalAuthenticatedEndpoint("http://localhost:9099", proof, now); err != nil {
		t.Fatalf("unexpected denial for the \"localhost\" loopback literal: %v", err)
	}
	if _, err := NewLocalAuthenticatedEndpoint("ws://[::1]:9099", proof, now); err != nil {
		t.Fatalf("unexpected denial for the ::1 loopback literal: %v", err)
	}
}

func TestNewLocalAuthenticatedEndpoint_ExpiredLease(t *testing.T) {
	now := time.Now()
	proof := validLocalProof(now)
	proof.ExpiresAt = now.Add(-time.Second)

	if _, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099", proof, now); err == nil {
		t.Fatalf("expected denial for an expired local presence lease")
	} else if !errors.Is(err, ErrEndpointExpired) {
		t.Fatalf("error = %v, want ErrEndpointExpired", err)
	}
}

func TestNewLocalAuthenticatedEndpoint_MalformedURLStillDenied(t *testing.T) {
	now := time.Now()
	proof := validLocalProof(now)
	if _, err := NewLocalAuthenticatedEndpoint("://not-a-url", proof, now); err == nil {
		t.Fatalf("expected denial for a malformed endpoint URL")
	}
}

func validRemoteProof(now time.Time) RemoteAdvertisementProof {
	return RemoteAdvertisementProof{
		PeerIdentity:   "peer-remote-1",
		DaemonInstance: "remote-instance-xyz",
		Route:          "lan-iface-eth0",
		Signer:         "swarm-lan-signer-1",
		IssuedAt:       now.Add(-time.Minute),
		ExpiresAt:      now.Add(time.Hour),
		Nonce:          "nonce-1",
		ReplayChecked:  true,
		TLS: RemoteTLSIdentity{
			ServerName: "peer.example",
			SPKI:       SPKIFingerprint{1, 2, 3, 4},
		},
	}
}

func TestNewRemoteAuthenticatedEndpoint_Success(t *testing.T) {
	now := time.Now()
	proof := validRemoteProof(now)
	e, err := NewRemoteAuthenticatedEndpoint("https://peer.example:9443/", proof, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Provenance() != ProvenanceRemote {
		t.Fatalf("Provenance() = %v, want ProvenanceRemote", e.Provenance())
	}
	if !e.IsRemote() || e.IsLocal() {
		t.Fatalf("IsRemote/IsLocal mismatch for a remote endpoint")
	}
	tlsID, ok := e.TLSIdentity()
	if !ok {
		t.Fatalf("a remote endpoint must carry a TLS identity")
	}
	if tlsID.ServerName != proof.TLS.ServerName || tlsID.SPKI != proof.TLS.SPKI {
		t.Fatalf("TLSIdentity() = %+v, want %+v", tlsID, proof.TLS)
	}
	if e.RouteProof() != proof.Route {
		t.Fatalf("RouteProof() = %q, want %q", e.RouteProof(), proof.Route)
	}
	if e.ExpiresAt() != proof.ExpiresAt {
		t.Fatalf("ExpiresAt() = %v, want %v", e.ExpiresAt(), proof.ExpiresAt)
	}
}

func TestNewRemoteAuthenticatedEndpoint_MissingProvenanceFields(t *testing.T) {
	now := time.Now()
	base := validRemoteProof(now)

	cases := []struct {
		name    string
		mutate  func(p *RemoteAdvertisementProof)
		wantErr error
	}{
		{"missing peer identity", func(p *RemoteAdvertisementProof) { p.PeerIdentity = "" }, ErrEndpointProvenance},
		{"missing daemon instance", func(p *RemoteAdvertisementProof) { p.DaemonInstance = "" }, ErrEndpointProvenance},
		{"missing route proof", func(p *RemoteAdvertisementProof) { p.Route = "" }, ErrEndpointProvenance},
		{"missing signer", func(p *RemoteAdvertisementProof) { p.Signer = "" }, ErrEndpointProvenance},
		{"missing issued-at", func(p *RemoteAdvertisementProof) { p.IssuedAt = time.Time{} }, ErrEndpointProvenance},
		{"missing expiry", func(p *RemoteAdvertisementProof) { p.ExpiresAt = time.Time{} }, ErrEndpointProvenance},
		{"missing nonce", func(p *RemoteAdvertisementProof) { p.Nonce = "" }, ErrEndpointProvenance},
		{"replay not checked", func(p *RemoteAdvertisementProof) { p.ReplayChecked = false }, ErrEndpointProvenance},
		{"missing tls server name", func(p *RemoteAdvertisementProof) { p.TLS.ServerName = "" }, ErrEndpointTLSIdentity},
		{"missing tls spki", func(p *RemoteAdvertisementProof) { p.TLS.SPKI = SPKIFingerprint{} }, ErrEndpointTLSIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proof := base
			tc.mutate(&proof)
			if _, err := NewRemoteAuthenticatedEndpoint("https://peer.example:9443", proof, now); err == nil {
				t.Fatalf("expected denial for %s", tc.name)
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: error = %v, want %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestNewRemoteAuthenticatedEndpoint_RequiresHTTPSOrWSS(t *testing.T) {
	now := time.Now()
	proof := validRemoteProof(now)

	for _, raw := range []string{"http://peer.example:9443", "ws://peer.example:9443"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := NewRemoteAuthenticatedEndpoint(raw, proof, now); err == nil {
				t.Fatalf("expected denial for plaintext remote scheme %q", raw)
			} else if !errors.Is(err, ErrEndpointScheme) {
				t.Fatalf("error = %v, want ErrEndpointScheme", err)
			}
		})
	}

	if _, err := NewRemoteAuthenticatedEndpoint("wss://peer.example:9443", proof, now); err != nil {
		t.Fatalf("unexpected denial for wss: %v", err)
	}
}

func TestNewRemoteAuthenticatedEndpoint_ServerNameMismatch(t *testing.T) {
	now := time.Now()
	proof := validRemoteProof(now)
	if _, err := NewRemoteAuthenticatedEndpoint("https://different-host.example:9443", proof, now); err == nil {
		t.Fatalf("expected denial when endpoint host does not match the signed TLS server name")
	} else if !errors.Is(err, ErrEndpointTLSIdentity) {
		t.Fatalf("error = %v, want ErrEndpointTLSIdentity", err)
	}
}

func TestNewRemoteAuthenticatedEndpoint_Expired(t *testing.T) {
	now := time.Now()
	proof := validRemoteProof(now)
	proof.ExpiresAt = now.Add(-time.Second)
	if _, err := NewRemoteAuthenticatedEndpoint("https://peer.example:9443", proof, now); err == nil {
		t.Fatalf("expected denial for an expired remote advertisement")
	} else if !errors.Is(err, ErrEndpointExpired) {
		t.Fatalf("error = %v, want ErrEndpointExpired", err)
	}
}

func TestNewRemoteAuthenticatedEndpoint_IssuedInFuture(t *testing.T) {
	now := time.Now()
	proof := validRemoteProof(now)
	proof.IssuedAt = now.Add(time.Hour)
	if _, err := NewRemoteAuthenticatedEndpoint("https://peer.example:9443", proof, now); err == nil {
		t.Fatalf("expected denial for an advertisement issued in the future")
	} else if !errors.Is(err, ErrEndpointProvenance) {
		t.Fatalf("error = %v, want ErrEndpointProvenance", err)
	}
}

func TestNewRemoteAuthenticatedEndpoint_MalformedURLStillDenied(t *testing.T) {
	now := time.Now()
	proof := validRemoteProof(now)
	if _, err := NewRemoteAuthenticatedEndpoint("://not-a-url", proof, now); err == nil {
		t.Fatalf("expected denial for a malformed endpoint URL")
	}
}

func TestAuthenticatedEndpointExpiredMethod(t *testing.T) {
	now := time.Now()
	local, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099", validLocalProof(now), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if local.Expired(now) {
		t.Fatalf("a local endpoint with no expiry must never report Expired")
	}

	proof := validLocalProof(now)
	proof.ExpiresAt = now.Add(time.Minute)
	local2, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099", proof, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if local2.Expired(now) {
		t.Fatalf("endpoint must not report Expired before its expiry")
	}
	if !local2.Expired(now.Add(2 * time.Minute)) {
		t.Fatalf("endpoint must report Expired after its expiry")
	}
}
