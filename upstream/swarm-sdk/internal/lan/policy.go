// Package lan provides dependency-light destination-classification,
// outbound-hygiene, and (as of Phase 02) endpoint-provenance and
// policy-bound transport composition for gateway security (ADR-007
// "Gateway authentication and outbound policy", see
// docs/architecture/swarm-attach/adr-007-gateway-security.md). This file
// holds the Phase 01 pure destination-classification and outbound-hygiene
// primitives; endpoint.go defines the non-string-alias AuthenticatedEndpoint
// type and its two provenance-checked constructors, and gateway_policy.go
// defines GatewayPolicy.Transport, the only way to obtain a PeerTransport.
// Every pure function here still takes an injected resolver/dialer seam (or
// is exercised through GatewayPolicy's production wiring of that seam) so
// it can be exercised deterministically, including proving exactly which
// (if any) destinations were attempted.
//
// A production peer HTTP/SSE/WebSocket route in the gateway and
// cmd/swarm-gateway packages returns UnavailableCategory /
// ErrAuthenticatedEndpointUnavailable before a presence URL is parsed as a
// dial target, before any DNS lookup, and before any dial, UNLESS the
// caller has both a configured GatewayPolicy and a genuine
// AuthenticatedEndpoint constructed through endpoint.go's provenance-checked
// constructors -- never from a raw a2a.PeerPresence URL. Testing the
// primitives below (or GatewayPolicy.Transport) with bounded synthetic
// inputs does not create an alternate production constructor or authorize a
// raw URL: NewLocalAuthenticatedEndpoint and NewRemoteAuthenticatedEndpoint
// remain the only ways to construct one, and both require a fully populated
// proof value, never a URL alone.
package lan

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// UnavailableCategory is the exact, stable, machine-checkable string every
// disabled production peer HTTP/SSE/WebSocket route reports. It never
// changes shape based on the underlying reason (offline peer, missing
// provenance, policy denial, ...) so a client cannot distinguish those cases
// from response text alone, and so every route reports identically.
const UnavailableCategory = "authenticated_endpoint_unavailable"

// UnavailableBody is the stable JSON body WriteUnavailable writes.
const UnavailableBody = `{"error":"` + UnavailableCategory + `"}`

// ErrAuthenticatedEndpointUnavailable is the stable denial category every
// production peer HTTP/SSE/WebSocket route returns until Phase 02 supplies
// authenticated endpoint provenance (ADR-007 "Compatibility"). Callers
// return it, or write UnavailableBody via WriteUnavailable, before a
// presence URL is parsed as a dial target, before any DNS lookup, and
// before any dial.
var ErrAuthenticatedEndpointUnavailable = errors.New(UnavailableCategory)

// WriteUnavailable writes the stable Phase 01 denial response for a
// disabled peer HTTP/SSE route. It performs no network operation of its
// own -- no URL parsing, no DNS resolution, no dial -- and callers MUST
// invoke it before doing any of those things, not after.
func WriteUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(UnavailableBody))
}

// Scheme is a normalized, lower-cased endpoint scheme recognized by this
// policy. Every other scheme -- file, unix, ftp, gopher, data, and any
// unrecognized value -- is denied by NormalizeEndpointURL.
type Scheme string

// Recognized endpoint schemes. Remote peers require SchemeHTTPS/SchemeWSS;
// SchemeHTTP/SchemeWS are eligible only for a locally authenticated,
// same-user loopback endpoint (ADR-007 "URL, DNS, dial, and redirect
// policy"). This package does not itself enforce that loopback/remote
// split -- it is a provenance decision Phase 02 owns -- but it does refuse
// every other scheme unconditionally.
const (
	SchemeHTTP  Scheme = "http"
	SchemeHTTPS Scheme = "https"
	SchemeWS    Scheme = "ws"
	SchemeWSS   Scheme = "wss"
)

func (s Scheme) secure() bool { return s == SchemeHTTPS || s == SchemeWSS }

// NormalizeEndpointURL parses raw as a candidate peer endpoint and enforces
// the ADR-007 URL policy: the scheme is case-normalized and restricted to
// http/https/ws/wss; userinfo, fragments, opaque URLs, missing hosts, and
// wildcard hosts are rejected; ports must be well-formed; zone identifiers
// are rejected (Phase 01 has no route policy that explicitly allows one);
// IPv4-mapped IPv6 literals and other ambiguous/non-canonical numeric host
// encodings (decimal-integer, hex, and octal-leading-zero forms) are
// rejected. It performs no DNS resolution and returns a *url.URL only after
// every structural check passes.
func NormalizeEndpointURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("lan: empty endpoint url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("lan: malformed endpoint url: %w", err)
	}
	if u.Opaque != "" {
		return nil, fmt.Errorf("lan: opaque endpoint url rejected")
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return nil, fmt.Errorf("lan: endpoint url fragment rejected")
	}
	if u.User != nil {
		return nil, fmt.Errorf("lan: endpoint url userinfo rejected")
	}
	scheme := Scheme(strings.ToLower(u.Scheme))
	switch scheme {
	case SchemeHTTP, SchemeHTTPS, SchemeWS, SchemeWSS:
	default:
		return nil, fmt.Errorf("lan: unsupported endpoint scheme %q", u.Scheme)
	}
	u.Scheme = string(scheme)

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("lan: endpoint url missing host")
	}
	if host == "*" {
		return nil, fmt.Errorf("lan: wildcard endpoint host rejected")
	}
	if strings.Contains(host, "%") {
		return nil, fmt.Errorf("lan: zone-qualified endpoint host rejected")
	}

	if port := u.Port(); port != "" {
		if _, perr := parsePort(port); perr != nil {
			return nil, fmt.Errorf("lan: malformed endpoint port %q: %w", port, perr)
		}
	}

	if ip := net.ParseIP(host); ip != nil {
		// A literal written with IPv6 colon syntax that nonetheless carries a
		// v4-in-v6 form (e.g. "::ffff:127.0.0.1") is a non-canonical dual
		// encoding of an IPv4 address; ADR-007 requires rejecting it
		// regardless of the address it encodes.
		if strings.Contains(host, ":") && ip.To4() != nil {
			return nil, fmt.Errorf("lan: ipv4-mapped ipv6 endpoint host rejected")
		}
	} else if hasAmbiguousNumericHost(host) {
		return nil, fmt.Errorf("lan: ambiguous numeric endpoint host %q rejected", host)
	}

	return u, nil
}

// parsePort validates a URL port string is well-formed decimal in [1,65535].
func parsePort(port string) (int, error) {
	if port == "" {
		return 0, fmt.Errorf("empty port")
	}
	n := 0
	for _, c := range port {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-numeric port %q", port)
		}
		n = n*10 + int(c-'0')
		if n > 65535 {
			return 0, fmt.Errorf("port out of range %q", port)
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("port zero rejected")
	}
	return n, nil
}

// hasAmbiguousNumericHost reports whether host looks like a non-canonical or
// ambiguous numeric encoding of an IP address that net.ParseIP does not
// already recognize as a canonical literal: a fully decimal-integer form
// (e.g. "2130706433"), a hex form (e.g. "0x7f000001"), or a dotted form with
// an octal-looking leading-zero octet (e.g. "0177.0.0.1", "127.0.0.01").
// Such strings are refused outright rather than allowed to fall through as
// an ordinary DNS hostname, because some resolvers/libc implementations
// interpret them as numeric IP literals -- exactly the ambiguity ADR-007's
// "non-canonical encodings are rejected" targets.
func hasAmbiguousNumericHost(host string) bool {
	if host == "" || net.ParseIP(host) != nil {
		return false
	}
	lower := strings.ToLower(host)
	if strings.HasPrefix(lower, "0x") {
		return isHexDigits(lower[2:])
	}
	if isAllDigits(host) {
		return true // bare decimal-integer IPv4 encoding attempt
	}
	parts := strings.Split(host, ".")
	numericParts := 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		if isAllDigits(p) {
			numericParts++
			if len(p) > 1 && p[0] == '0' {
				return true // octal-leading-zero octet
			}
		}
	}
	return numericParts > 0 && numericParts == len(parts)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isHexDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// AddressClass categorizes a resolved IP address for outbound peer policy.
type AddressClass int

const (
	// AddressInvalid marks a nil, zero-length, or otherwise unparseable
	// address. It is never eligible for any destination.
	AddressInvalid AddressClass = iota
	AddressUnspecified
	AddressLoopback
	AddressLinkLocal
	AddressMulticast
	// AddressMetadata marks a well-known cloud/container metadata-service
	// address (for example 169.254.169.254). It is a strict subset of
	// link-local space but is classified separately and is NEVER eligible
	// for any destination, even one that would otherwise allow link-local.
	AddressMetadata
	AddressPrivate
	AddressPublic
)

func (c AddressClass) String() string {
	switch c {
	case AddressUnspecified:
		return "unspecified"
	case AddressLoopback:
		return "loopback"
	case AddressLinkLocal:
		return "link-local"
	case AddressMulticast:
		return "multicast"
	case AddressMetadata:
		return "metadata"
	case AddressPrivate:
		return "private"
	case AddressPublic:
		return "public"
	default:
		return "invalid"
	}
}

// metadataRanges are well-known cloud/container metadata-service addresses
// that must never be treated as an ordinary link-local or private
// destination, per ADR-007 ("configured cloud/container metadata
// ranges"). This is the built-in minimum set; a deployment-specific set is a
// later composition concern, not a Phase 01 primitive.
var metadataRanges = []net.IPNet{
	mustCIDR("169.254.169.254/32"), // AWS/GCP/Azure/OpenStack metadata
	mustCIDR("169.254.170.2/32"),   // AWS ECS task metadata
	mustCIDR("fd00:ec2::254/128"),  // AWS IPv6 metadata
}

func mustCIDR(s string) net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("lan: invalid built-in cidr " + s + ": " + err.Error())
	}
	return *n
}

// ClassifyAddress reports ip's AddressClass. An empty, zero-length, or
// unparseable ip is AddressInvalid. Go's net.IP loopback/private/multicast
// predicates already normalize IPv4-mapped-IPv6 (::ffff:a.b.c.d) forms to
// their embedded IPv4 classification, so no separate handling is needed
// here for an address that has already survived DNS-answer decoding (the
// URL-literal case is rejected earlier by NormalizeEndpointURL).
func ClassifyAddress(ip net.IP) AddressClass {
	if len(ip) == 0 {
		return AddressInvalid
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return AddressInvalid
	}
	for _, r := range metadataRanges {
		if r.Contains(ip16) {
			return AddressMetadata
		}
	}
	switch {
	case ip16.IsUnspecified():
		return AddressUnspecified
	case ip16.IsLoopback():
		return AddressLoopback
	case ip16.IsLinkLocalUnicast(), ip16.IsLinkLocalMulticast():
		return AddressLinkLocal
	case ip16.IsMulticast():
		return AddressMulticast
	case ip16.IsPrivate():
		return AddressPrivate
	default:
		return AddressPublic
	}
}

// RemotePolicy is the closed, explicitly configured allow-list a private
// address must satisfy to be eligible for a remote peer destination.
// ADR-007: "Private LAN addresses are not automatically trusted: they must
// match the authenticated advertisement's interface/route proof and
// configured LAN scope." Phase 01 exposes only the closed allow-list
// primitive; binding it to interface/route proof is a later composition
// concern.
type RemotePolicy struct {
	AllowedPrivateCIDRs []net.IPNet
}

func (p RemotePolicy) allowsPrivate(ip net.IP) bool {
	for _, n := range p.AllowedPrivateCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// AddressAllowedRemote reports whether ip is eligible as a remote peer
// destination under policy p. Loopback, unspecified, link-local, multicast,
// and metadata are never eligible for a remote peer regardless of p.
// Private is eligible only when p explicitly allows it. Public is always
// eligible.
func AddressAllowedRemote(ip net.IP, p RemotePolicy) bool {
	switch ClassifyAddress(ip) {
	case AddressPublic:
		return true
	case AddressPrivate:
		return p.allowsPrivate(ip)
	default:
		return false
	}
}

// AddressAllowedLoopbackPeer reports whether ip is eligible as a locally
// authenticated, same-user loopback peer destination. Only AddressLoopback
// is eligible: ADR-007 "A remote advertisement can never gain loopback
// privilege by naming localhost, using a DNS alias, selecting the gateway's
// own address, or changing address family."
func AddressAllowedLoopbackPeer(ip net.IP) bool {
	return ClassifyAddress(ip) == AddressLoopback
}

// MaxDNSAnswers bounds the number of DNS answers a single resolution may
// return before the whole destination is denied as an excessive answer set.
const MaxDNSAnswers = 32

// ValidateDNSAnswers checks every answer in addrs against allowed, per
// ADR-007: "internal/lan resolves the hostname using its injected resolver
// and validates every DNS result, not merely the first preferred address.
// If any answer is forbidden or outside the authenticated route/policy, the
// entire destination is denied." An empty answer set, a resolver error
// (err), an excessive answer count, an invalid address, or any single
// forbidden/off-policy answer denies the ENTIRE destination -- including
// when other answers in the same set would individually be allowed (mixed
// answers are denied, not filtered).
func ValidateDNSAnswers(addrs []net.IP, err error, allowed func(net.IP) bool) ([]net.IP, error) {
	if err != nil {
		return nil, fmt.Errorf("lan: dns resolution error: %w", err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("lan: dns answer set empty")
	}
	if len(addrs) > MaxDNSAnswers {
		return nil, fmt.Errorf("lan: dns answer set excessive (%d > %d)", len(addrs), MaxDNSAnswers)
	}
	plan := make([]net.IP, 0, len(addrs))
	for _, ip := range addrs {
		if ip == nil || ClassifyAddress(ip) == AddressInvalid {
			return nil, fmt.Errorf("lan: dns answer invalid address")
		}
		if !allowed(ip) {
			return nil, fmt.Errorf("lan: dns answer %s forbidden or off-policy (class %s)", ip, ClassifyAddress(ip))
		}
		plan = append(plan, ip)
	}
	return plan, nil
}

// ValidateDialAddress checks the ACTUAL net.Conn remote address dialed
// against the planned/approved address set produced by ValidateDNSAnswers,
// per ADR-007: "The policy-bound dialer receives the validated address set
// and checks the actual net.Conn remote address before any application
// bytes are written. It rejects an address that was not in the plan,
// changed classification, [or] lost route authorization." Re-applying
// allowed (not just plan membership) catches a TOCTOU classification change
// between resolution and connect.
func ValidateDialAddress(actual net.IP, plan []net.IP, allowed func(net.IP) bool) error {
	if actual == nil {
		return fmt.Errorf("lan: dial address missing")
	}
	found := false
	for _, ip := range plan {
		if ip.Equal(actual) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("lan: dial address %s not in approved plan (rebinding)", actual)
	}
	if !allowed(actual) {
		return fmt.Errorf("lan: dial address %s no longer authorized (class %s)", actual, ClassifyAddress(actual))
	}
	return nil
}

// ValidateRedirect checks a proposed redirect target per ADR-007:
// "Redirects are disabled by default for peer RPC, SSE, and WebSocket. If a
// specific compatibility contract enables HTTP redirects, each hop is
// bounded, parsed from scratch, restricted to the endpoint's permitted
// local http or remote https class". Callers parse `to` from scratch with
// NormalizeEndpointURL before calling this, then still re-resolve and
// re-dial it through a new policy plan; this function only enforces the
// scheme-class and downgrade rules, not DNS/dial.
func ValidateRedirect(from, to *url.URL, allowedSchemes map[Scheme]bool) error {
	if to == nil {
		return fmt.Errorf("lan: redirect target missing")
	}
	scheme := Scheme(strings.ToLower(to.Scheme))
	if !allowedSchemes[scheme] {
		return fmt.Errorf("lan: redirect scheme %q not permitted", to.Scheme)
	}
	if from != nil {
		fromScheme := Scheme(strings.ToLower(from.Scheme))
		if fromScheme.secure() && !scheme.secure() {
			return fmt.Errorf("lan: redirect downgrade from %q to %q denied", from.Scheme, to.Scheme)
		}
	}
	return nil
}

// ListenerAddressGrant reports whether listen address addr (as passed to
// net.Listen, e.g. "127.0.0.1:8787", ":8787", "0.0.0.0:8787", "[::]:8787",
// "localhost:8787") is loopback-only per ADR-007: "Loopback is the default
// and the only implicit listener grant... Wildcard addresses, unspecified
// IPv4/IPv6, interface-derived LAN addresses, and hostnames resolving
// outside loopback count as non-loopback." A non-literal, non-"localhost"
// hostname is ambiguous without a resolution step this pure primitive does
// not perform, so it is reported as an error rather than silently accepted
// or rejected.
func ListenerAddressGrant(addr string) (loopback bool, err error) {
	host, _, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		if strings.HasPrefix(addr, ":") {
			host = "" // bare ":port" wildcard shorthand
		} else {
			return false, fmt.Errorf("lan: malformed listener address %q: %w", addr, splitErr)
		}
	}
	if host == "" {
		return false, nil // wildcard bind
	}
	if strings.EqualFold(host, "localhost") {
		return true, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, fmt.Errorf("lan: ambiguous listener hostname %q requires explicit resolution", host)
	}
	if ip.IsUnspecified() {
		return false, nil
	}
	return ip.IsLoopback(), nil
}

// AllowedOutboundRequestHeaders is the closed allowlist of header names a
// policy-bound peer request may set BY EXPLICIT VALUE. Nothing else is ever
// copied from an inbound gateway request onto an outbound peer request; see
// BuildOutboundHeaders.
var AllowedOutboundRequestHeaders = map[string]bool{
	"Content-Type": true,
	"Accept":       true,
}

// ForbiddenInboundHeaders names inbound headers that must never cross the
// gateway boundary onto an outbound peer request under any circumstance.
// It exists for defense-in-depth assertions in tests; the primary control
// is that BuildOutboundHeaders only ever copies from
// AllowedOutboundRequestHeaders, an allowlist that does not include any of
// these.
var ForbiddenInboundHeaders = map[string]bool{
	"Authorization":       true,
	"Proxy-Authorization": true,
	"Cookie":              true,
	"Set-Cookie":          true,
	"X-Csrf-Token":        true,
	"Host":                true,
	"X-Forwarded-For":     true,
	"X-Forwarded-Host":    true,
	"X-Forwarded-Proto":   true,
	"Forwarded":           true,
}

// hopByHopHeaders are connection-scoped headers RFC 7230 §6.1 requires a
// proxy never forward.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// ConnectionNominatedHeaders returns the canonicalized header names an
// inbound Connection header value nominates as additionally hop-by-hop for
// that specific request (RFC 7230 §6.1).
func ConnectionNominatedHeaders(connection string) map[string]bool {
	out := make(map[string]bool)
	for _, tok := range strings.Split(connection, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		out[http.CanonicalHeaderKey(tok)] = true
	}
	return out
}

// IsHopByHopHeader reports whether name is a standard hop-by-hop header, or
// was nominated by the given inbound Connection header value.
func IsHopByHopHeader(name string, connection string) bool {
	canon := http.CanonicalHeaderKey(name)
	if hopByHopHeaders[canon] {
		return true
	}
	return ConnectionNominatedHeaders(connection)[canon]
}

// BuildOutboundHeaders returns a fresh, empty-based http.Header for an
// outbound peer request -- never the same map as in, and never populated by
// iterating in. It copies a value from in only when its canonical name is
// present in the closed AllowedOutboundRequestHeaders allowlist; every
// other inbound header -- including every ForbiddenInboundHeaders entry and
// every hop-by-hop or Connection-nominated header -- is dropped
// unconditionally, per ADR-007: "It never copies the inbound Authorization,
// Proxy-Authorization, Cookie, Set-Cookie, CSRF headers, query credentials,
// client Host, forwarding headers, or hop-by-hop headers."
func BuildOutboundHeaders(in http.Header) http.Header {
	out := make(http.Header, len(AllowedOutboundRequestHeaders))
	for name := range AllowedOutboundRequestHeaders {
		if v := in.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	return out
}

// PeerQueryFields is the closed, typed allowlist of outbound peer-contract
// query field names a typed peer method may add after authentication and
// route authorization (ADR-007: "a typed peer method may add only its
// closed allowlist of peer-contract query fields from validated typed
// values"). It contains no credential-shaped field: query credentials are
// always rejected. It is intentionally empty in Phase 01 because no
// production peer route is enabled yet (every route returns
// ErrAuthenticatedEndpointUnavailable); Phase 02 peer methods extend this
// allowlist with validated typed values, never with raw inbound query
// bytes.
var PeerQueryFields = map[string]bool{}

// GatewayOnlyQueryFields names inbound query fields the gateway itself
// consumes locally (peer selection, legacy query-token compatibility) and
// which are therefore never forwarded to a peer under any circumstance.
var GatewayOnlyQueryFields = map[string]bool{
	"peer":  true,
	"token": true,
}

// BuildOutboundQuery returns a fresh, empty url.Values for an outbound peer
// request. Per ADR-007: "The outbound URL begins with an empty RawQuery and
// ForceQuery false; no inbound query key or byte, credential-shaped or
// otherwise, is copied." The returned value can only be populated
// field-by-field, by the caller, from already-validated typed values
// against the closed PeerQueryFields allowlist -- this function never reads
// an inbound request.
func BuildOutboundQuery() url.Values {
	return url.Values{}
}

// RejectedInboundQueryFields reports, in sorted order, every inbound query
// key that must be refused rather than forwarded: any key that is not a
// recognized GatewayOnlyQueryFields entry (consumed locally) or a
// recognized PeerQueryFields entry, and any recognized key presented more
// than once (duplicate). Per ADR-007: "Unknown or duplicate inbound gateway
// query fields are rejected or consumed locally, never forwarded."
func RejectedInboundQueryFields(in url.Values) []string {
	var rejected []string
	for key, values := range in {
		if GatewayOnlyQueryFields[key] || PeerQueryFields[key] {
			if len(values) > 1 {
				rejected = append(rejected, key)
			}
			continue
		}
		rejected = append(rejected, key)
	}
	sort.Strings(rejected)
	return rejected
}
