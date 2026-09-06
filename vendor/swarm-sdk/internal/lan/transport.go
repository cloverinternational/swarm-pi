package lan

// This file holds the package-private DNS-resolution and dial seams
// ADR-007's "URL, DNS, dial, and redirect policy" describes: "Before any
// remote-peer connection, internal/lan resolves the hostname using its
// injected resolver and validates every DNS result... The policy-bound
// dialer receives the validated address set and checks the actual net.Conn
// remote address before any application bytes are written."
//
// Both the resolver and dialer interfaces, and every function built on
// them, are UNEXPORTED: no other package can reach a resolver/dialer
// through internal/lan directly. Phase 01 wired no production
// implementation to them at all -- every production peer HTTP/SSE/WebSocket
// route stayed disabled (ErrAuthenticatedEndpointUnavailable). Phase 02's
// gateway_policy.go is now the sole production caller: GatewayPolicy's
// loopbackDialer/remoteDialer helpers call resolveAllAnswers and
// dialApprovedPlan directly (same package) to back a policy-bound
// PeerTransport, but only after GatewayPolicy.Transport has already
// validated the calling Principal and the selected AuthenticatedEndpoint --
// see gateway_policy.go and endpoint.go. The deterministic synthetic tests
// in transport_test.go continue to exercise these seams directly, and
// gateway_policy_test.go exercises them indirectly through Transport with
// injected (unexported) resolver/dialer fakes.

import (
	"context"
	"fmt"
	"net"
)

// resolver is the package-private DNS lookup seam. It mirrors the single
// method of *net.Resolver actually needed here so a synthetic test can
// supply deterministic, out-of-order, mixed, empty, or error answer sets
// without touching a real network or DNS server.
type resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// dialer is the package-private connection seam. It mirrors the single
// method of *net.Dialer actually needed here so a synthetic test can supply
// a fake net.Conn -- including one that reports a rebinding remote address
// different from the one requested -- without opening a real socket.
type dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// resolveAllAnswers resolves host with r and validates every answer against
// allowed via ValidateDNSAnswers -- not merely the first preferred address.
// It returns the approved dial plan, or a denial error and a nil plan. No
// caller in this package invokes r before this function has already
// produced a plan, and no caller dials an address absent from the returned
// plan.
func resolveAllAnswers(ctx context.Context, r resolver, host string, allowed func(net.IP) bool) ([]net.IP, error) {
	if r == nil {
		return nil, fmt.Errorf("lan: no resolver configured")
	}
	answers, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("lan: dns resolution error: %w", err)
	}
	ips := make([]net.IP, 0, len(answers))
	for _, a := range answers {
		ips = append(ips, a.IP)
	}
	return ValidateDNSAnswers(ips, nil, allowed)
}

// dialApprovedPlan dials network/port against each address in plan, in
// order, stopping at the first successful connection, then re-validates the
// ACTUAL connected remote address with ValidateDialAddress BEFORE returning
// the connection to the caller (so no caller-visible connection is ever
// handed back for an address outside the plan). It never dials an address
// that is not already a member of plan: the loop bound and address source
// are both plan itself, not any caller-supplied or derived value.
func dialApprovedPlan(ctx context.Context, d dialer, network, port string, plan []net.IP, allowed func(net.IP) bool) (net.Conn, error) {
	if d == nil {
		return nil, fmt.Errorf("lan: no dialer configured")
	}
	if len(plan) == 0 {
		return nil, fmt.Errorf("lan: empty dial plan")
	}
	var lastErr error
	for _, ip := range plan {
		addr := net.JoinHostPort(ip.String(), port)
		conn, err := d.DialContext(ctx, network, addr)
		if err != nil {
			lastErr = err
			continue
		}
		actual, aerr := actualRemoteIP(conn)
		if aerr != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("lan: dial address introspection failed: %w", aerr)
		}
		if verr := ValidateDialAddress(actual, plan, allowed); verr != nil {
			_ = conn.Close()
			return nil, verr
		}
		return conn, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("lan: dial plan exhausted")
	}
	return nil, fmt.Errorf("lan: dial failed: %w", lastErr)
}

// actualRemoteIP extracts the actual connected remote IP from conn so it
// can be re-checked against the approved plan before application bytes are
// written, per ADR-007's "actual net.Conn remote address" re-check.
func actualRemoteIP(conn net.Conn) (net.IP, error) {
	addr := conn.RemoteAddr()
	if addr == nil {
		return nil, fmt.Errorf("lan: connection has no remote address")
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("lan: unparsable remote address %q", addr.String())
	}
	return ip, nil
}
