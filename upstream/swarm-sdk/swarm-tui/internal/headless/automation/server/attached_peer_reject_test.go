package server

import (
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/client"
)

// TestAttachedRejectionIsDecodableNotOpaqueReset proves issue #249: a
// connection that AttachedServer refuses (peer-credential mismatch, over
// the connection cap, or shutting down) now surfaces to the SAME
// client.Client used by `swarm swarm attach <handle> state|debug|frame`
// as a normal, decodable "server error N: message" -- not a bare
// "read failed: connection reset by peer" / "broken pipe" that is
// indistinguishable from a crashed peer or a dead socket.
//
// This drives rejectConn directly with the exact category/code
// validatePeerUser uses on a real peer-credential mismatch, which is what
// the original report's cross-identity attach attempts actually hit; it
// does not require a second real OS user to exercise the wire-level
// behavior this fix changed.
func TestAttachedRejectionIsDecodableNotOpaqueReset(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "peer.ctrl")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		rejectConn(conn, attachedPeerUIDMismatchCode, attachedPeerUIDMismatchCategory+": simulated mismatch")
	}()

	c := client.New("unix://" + sock)
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	if _, err := c.GetState(); err == nil {
		t.Fatal("expected an error from a rejected connection")
	} else {
		msg := err.Error()
		if strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") {
			t.Fatalf("client still saw an opaque OS-level reset instead of a decoded server error: %v", err)
		}
		if !strings.Contains(msg, attachedPeerUIDMismatchCategory) {
			t.Fatalf("expected the decoded rejection category in the error, got: %v", err)
		}
	}
}
