package termimage

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// FuzzObserveResponse drives the probe state machine with arbitrary, truncated,
// and malformed terminal replies. A terminal that is not Kitty can still emit
// bytes that superficially resemble a graphics acknowledgement (mouse reports,
// paste payloads, another program's APC). Concluding Kitty capability from one
// of those would make the app write placeholder cells no terminal understands:
// space reserved, nothing painted.
func FuzzObserveResponse(f *testing.F) {
	f.Add("\x1b_Gi=31;OK\x1b\\")
	f.Add("\x1b_Gi=34;OK\x1b\\")
	f.Add("\x1b_Gi=31;ENOENT\x1b\\")
	f.Add("i=31;OK")
	f.Add("\x1b_Gi=31;OK")
	f.Add("\x1b_G;OK\x1b\\")
	f.Add("\x1b_Gi=0;OK\x1b\\")
	f.Add("\x1b_Gi=99999999999999999999;OK\x1b\\")
	f.Add("\x1b_Gi=31,i=32;OK\x1b\\")
	f.Add("\x1b_G\x1b\\")
	f.Add("")
	f.Add("\x1b_Gi=+31;OK\x1b\\")
	f.Add("\x1b_Gi=31;ok\x1b\\")
	f.Add("\x1b_Gi=31; OK \x1b\\")
	f.Add("\x1bPtmux;\x1b\x1b_Gi=32;OK\x1b\x1b\\\x1b\\")

	f.Fuzz(func(t *testing.T, reply string) {
		if len(reply) > 8192 {
			t.Skip()
		}
		manager := NewManager()
		defer func() { _ = manager.Release(&bytes.Buffer{}) }()

		done := make(chan struct{})
		go func() {
			defer close(done)
			parsed, ok := ParseResponse(reply)
			if ok {
				// A parsed response must have a usable, non-zero image ID; ID 0
				// is reserved and would alias every source in the manager.
				if parsed.ID == 0 {
					t.Errorf("parsed a response with the reserved ID 0 from %q", reply)
				}
				manager.ObserveResponse(parsed)
			}
			// The raw path the app also uses.
			manager.ObserveResponse(Response{
				ID: parsed.ID, Message: parsed.Message, OK: parsed.OK,
			})
			if got := CapabilityFromResponse(reply); got == Kitty && !isGenuineDirectOK(reply) {
				t.Errorf("concluded Kitty from a malformed reply %q", reply)
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("ObserveResponse hung on %q", reply)
		}

		// Any of the four probe IDs answering OK is a legitimate route to
		// Kitty; anything else must leave the capability unproven.
		if manager.Capability() == Kitty && !isGenuineProbeOK(reply) {
			t.Fatalf("manager reached Kitty capability from %q", reply)
		}
	})
}

// isGenuineProbeOK reports whether the reply is a successful answer to any of
// the four capability probes the manager sends.
func isGenuineProbeOK(reply string) bool {
	for _, id := range []string{"31", "32", "33", "34"} {
		if genuineOKForID(reply, id) {
			return true
		}
	}
	return false
}

// isGenuineDirectOK is an independent, deliberately naive reimplementation of
// "this reply really is a successful answer to our direct capability query".
// Writing it separately from the parser is the point: agreement between two
// independent implementations is the evidence.
func isGenuineDirectOK(reply string) bool { return genuineOKForID(reply, "31") }

func genuineOKForID(reply, want string) bool {
	for _, body := range candidateBodies(reply) {
		options, message, ok := strings.Cut(body, ";")
		if !ok || strings.TrimSpace(message) != "OK" {
			continue
		}
		for _, option := range strings.Split(options, ",") {
			k, v, present := strings.Cut(strings.TrimSpace(option), "=")
			if present && k == "i" && v == want {
				return true
			}
		}
	}
	return false
}

// candidateBodies yields every APC body in the reply plus the unframed reply
// itself, mirroring the two shapes the terminal input layer can hand us.
func candidateBodies(reply string) []string {
	var bodies []string
	s := reply
	for {
		i := strings.Index(s, "\x1b_G")
		if i < 0 {
			break
		}
		s = s[i+3:]
		end := strings.Index(s, "\x1b\\")
		if end < 0 {
			break
		}
		bodies = append(bodies, s[:end])
		s = s[end+2:]
	}
	return append(bodies, strings.TrimSpace(reply))
}
