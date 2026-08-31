package lan

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// fakeResolver is a deterministic, synthetic resolver.LookupIPAddr seam. It
// never touches a real DNS server.
type fakeResolver struct {
	answers map[string][]net.IPAddr
	err     error
	calls   int
}

func (r *fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.answers[host], nil
}

// fakeAddr is a minimal net.Addr for fakeConn.
type fakeAddr struct{ s string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.s }

// fakeConn is a minimal net.Conn whose RemoteAddr is controlled by the test,
// so a rebinding scenario (actual address differs from the requested one)
// can be simulated without a real socket.
type fakeConn struct {
	remote string
	closed bool
}

func (c *fakeConn) Read(b []byte) (int, error)         { return 0, errClosedFakeConn }
func (c *fakeConn) Write(b []byte) (int, error)        { return len(b), nil }
func (c *fakeConn) Close() error                       { c.closed = true; return nil }
func (c *fakeConn) LocalAddr() net.Addr                { return fakeAddr{"198.51.100.1:0"} }
func (c *fakeConn) RemoteAddr() net.Addr               { return fakeAddr{c.remote} }
func (c *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

var errClosedFakeConn = errors.New("lan: fakeConn read after close in test")

// fakeDialer records every address it was asked to dial -- a connection
// counter proving exactly which destinations, if any, were attempted.
type fakeDialer struct {
	attempts []string
	conns    map[string]*fakeConn // addr -> conn to hand back (nil entry -> error)
	errs     map[string]error
}

func (d *fakeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.attempts = append(d.attempts, address)
	if err, ok := d.errs[address]; ok {
		return nil, err
	}
	if c, ok := d.conns[address]; ok {
		return c, nil
	}
	return &fakeConn{remote: address}, nil
}

func TestResolveAllAnswersChecksEveryAnswerNotJustFirst(t *testing.T) {
	allowPublicOnly := func(ip net.IP) bool { return ClassifyAddress(ip) == AddressPublic }

	t.Run("all public answers approved", func(t *testing.T) {
		r := &fakeResolver{answers: map[string][]net.IPAddr{
			"peer.example": {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}},
		}}
		plan, err := resolveAllAnswers(context.Background(), r, "peer.example", allowPublicOnly)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(plan) != 2 {
			t.Fatalf("plan len = %d, want 2", len(plan))
		}
		if r.calls != 1 {
			t.Fatalf("resolver called %d times, want exactly 1", r.calls)
		}
	})

	t.Run("first answer public, second forbidden denies whole destination", func(t *testing.T) {
		r := &fakeResolver{answers: map[string][]net.IPAddr{
			"peer.example": {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}},
		}}
		plan, err := resolveAllAnswers(context.Background(), r, "peer.example", allowPublicOnly)
		if err == nil {
			t.Fatalf("want denial when ANY answer is forbidden, got plan %v", plan)
		}
	})

	t.Run("resolver error denies", func(t *testing.T) {
		r := &fakeResolver{err: errors.New("dns down")}
		_, err := resolveAllAnswers(context.Background(), r, "peer.example", allowPublicOnly)
		if err == nil {
			t.Fatalf("want denial on resolver error")
		}
	})

	t.Run("empty answers deny", func(t *testing.T) {
		r := &fakeResolver{answers: map[string][]net.IPAddr{}}
		_, err := resolveAllAnswers(context.Background(), r, "peer.example", allowPublicOnly)
		if err == nil {
			t.Fatalf("want denial on empty answer set")
		}
	})

	t.Run("nil resolver denies without panicking", func(t *testing.T) {
		_, err := resolveAllAnswers(context.Background(), nil, "peer.example", allowPublicOnly)
		if err == nil {
			t.Fatalf("want denial for nil resolver")
		}
	})
}

func TestDialApprovedPlanNeverDialsOutsidePlanAndRevalidatesActualAddress(t *testing.T) {
	allowPublicOnly := func(ip net.IP) bool { return ClassifyAddress(ip) == AddressPublic }

	t.Run("dials only planned address and succeeds", func(t *testing.T) {
		plan := []net.IP{net.ParseIP("8.8.8.8")}
		d := &fakeDialer{}
		conn, err := dialApprovedPlan(context.Background(), d, "tcp", "443", plan, allowPublicOnly)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer conn.Close()
		if len(d.attempts) != 1 || d.attempts[0] != "8.8.8.8:443" {
			t.Fatalf("dial attempts = %v, want exactly [8.8.8.8:443]", d.attempts)
		}
	})

	t.Run("empty plan dials nothing", func(t *testing.T) {
		d := &fakeDialer{}
		_, err := dialApprovedPlan(context.Background(), d, "tcp", "443", nil, allowPublicOnly)
		if err == nil {
			t.Fatalf("want error for empty plan")
		}
		if len(d.attempts) != 0 {
			t.Fatalf("dial attempts = %v, want zero outbound attempts for empty plan", d.attempts)
		}
	})

	t.Run("nil dialer denies without any attempt tracking possible", func(t *testing.T) {
		plan := []net.IP{net.ParseIP("8.8.8.8")}
		_, err := dialApprovedPlan(context.Background(), nil, "tcp", "443", plan, allowPublicOnly)
		if err == nil {
			t.Fatalf("want error for nil dialer")
		}
	})

	t.Run("rebinding: actual remote address outside plan is rejected and connection closed", func(t *testing.T) {
		plan := []net.IP{net.ParseIP("8.8.8.8")}
		rebound := &fakeConn{remote: "203.0.113.9:443"} // NOT the planned address
		d := &fakeDialer{conns: map[string]*fakeConn{"8.8.8.8:443": rebound}}

		conn, err := dialApprovedPlan(context.Background(), d, "tcp", "443", plan, allowPublicOnly)
		if err == nil {
			conn.Close()
			t.Fatalf("want denial when actual dialed address is not in the approved plan")
		}
		if !rebound.closed {
			t.Fatalf("rebinding connection must be closed on denial, not left open")
		}
		if len(d.attempts) != 1 {
			t.Fatalf("dial attempts = %v, want exactly one attempt (no retry storm)", d.attempts)
		}
	})

	t.Run("actual address reclassified as forbidden between resolve and connect is rejected", func(t *testing.T) {
		plan := []net.IP{net.ParseIP("8.8.8.8")}
		// The dialer hands back a connection whose actual remote address IS
		// the planned literal, but allowed() now rejects it -- e.g. policy
		// revoked mid-flight. Still must deny (defense in depth beyond plan
		// membership).
		conn := &fakeConn{remote: "8.8.8.8:443"}
		d := &fakeDialer{conns: map[string]*fakeConn{"8.8.8.8:443": conn}}
		denyEverything := func(net.IP) bool { return false }

		_, err := dialApprovedPlan(context.Background(), d, "tcp", "443", plan, denyEverything)
		if err == nil {
			t.Fatalf("want denial when allowed() rejects the actual address")
		}
		if !conn.closed {
			t.Fatalf("connection must be closed on post-dial policy denial")
		}
	})

	t.Run("first plan address fails to dial, second succeeds, still bounded to plan", func(t *testing.T) {
		plan := []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1")}
		d := &fakeDialer{errs: map[string]error{"8.8.8.8:443": errors.New("connection refused")}}

		conn, err := dialApprovedPlan(context.Background(), d, "tcp", "443", plan, allowPublicOnly)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer conn.Close()
		if len(d.attempts) != 2 {
			t.Fatalf("dial attempts = %v, want exactly 2 (both plan members, in plan order)", d.attempts)
		}
		if d.attempts[0] != "8.8.8.8:443" || d.attempts[1] != "1.1.1.1:443" {
			t.Fatalf("dial attempts out of plan order: %v", d.attempts)
		}
	})

	t.Run("every plan address fails to dial denies with zero successful connections", func(t *testing.T) {
		plan := []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1")}
		d := &fakeDialer{errs: map[string]error{
			"8.8.8.8:443": errors.New("refused"),
			"1.1.1.1:443": errors.New("refused"),
		}}
		_, err := dialApprovedPlan(context.Background(), d, "tcp", "443", plan, allowPublicOnly)
		if err == nil {
			t.Fatalf("want error when every planned dial fails")
		}
		if len(d.attempts) != 2 {
			t.Fatalf("dial attempts = %v, want exactly 2", d.attempts)
		}
	})
}

func TestActualRemoteIP(t *testing.T) {
	conn := &fakeConn{remote: "8.8.8.8:443"}
	ip, err := actualRemoteIP(conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip.String() != "8.8.8.8" {
		t.Fatalf("actualRemoteIP = %s, want 8.8.8.8", ip)
	}

	noAddr := &fakeConn{remote: ""}
	if _, err := actualRemoteIP(noAddr); err == nil {
		t.Fatalf("want error for empty remote address")
	}
}
