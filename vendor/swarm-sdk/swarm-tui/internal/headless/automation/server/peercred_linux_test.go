//go:build linux

package server

import (
	"net"
	"path/filepath"
	"syscall"
	"testing"
)

func TestValidatePeerUserAcceptsSameEffectiveUser(t *testing.T) {
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "peer.ctrl"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	result := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		result <- validatePeerUser(conn)
	}()

	client, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	if err := <-result; err != nil {
		t.Fatalf("validatePeerUser() = %v, want same-user access", err)
	}
}

func TestValidatePeerUserRejectsNonUnixConnection(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	if err := validatePeerUser(serverConn); err == nil {
		t.Fatal("validatePeerUser() accepted a connection without Unix peer credentials")
	}
}

// TestCheckPeerCredentialUIDAcceptsSameUID exercises the pure uid-comparison
// decision directly with a synthetic credential, proving the same-user
// accept path without depending on validatePeerUser's live SO_PEERCRED
// syscall (which the test above already covers for the happy path via a
// real same-process Unix socket).
func TestCheckPeerCredentialUIDAcceptsSameUID(t *testing.T) {
	cred := &syscall.Ucred{Uid: 1000, Gid: 1000, Pid: 4242}
	if err := checkPeerCredentialUID(cred, 1000); err != nil {
		t.Fatalf("checkPeerCredentialUID() = %v, want nil for a matching uid", err)
	}
}

// TestCheckPeerCredentialUIDRejectsDifferentUID proves a peer presenting a
// different uid than the server's effective uid is rejected. This is the
// cross-user identity case that cannot be exercised through a real Unix
// socket in an unprivileged single-user test environment (there is no
// second OS user available to dial from), so it is proven directly against
// the extracted decision function with a synthetic *syscall.Ucred instead.
func TestCheckPeerCredentialUIDRejectsDifferentUID(t *testing.T) {
	cred := &syscall.Ucred{Uid: 65534, Gid: 65534, Pid: 4242}
	if err := checkPeerCredentialUID(cred, 1000); err == nil {
		t.Fatal("checkPeerCredentialUID() accepted a peer credential with a mismatched uid")
	}
}

// TestCheckPeerCredentialUIDRejectsNilCredential proves an absent/unknown
// credential (which the kernel can return for a peer that disconnected
// mid-handshake, or on a socket type where SO_PEERCRED is unpopulated) fails
// closed instead of being treated as trusted.
func TestCheckPeerCredentialUIDRejectsNilCredential(t *testing.T) {
	if err := checkPeerCredentialUID(nil, 1000); err == nil {
		t.Fatal("checkPeerCredentialUID() accepted a nil peer credential")
	}
}
