package lan

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestWriteUnavailableIsStableAndPerformsNoNetworkOperation(t *testing.T) {
	w := httptest.NewRecorder()
	WriteUnavailable(w)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if got := w.Body.String(); got != UnavailableBody {
		t.Fatalf("body = %q, want %q", got, UnavailableBody)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	if UnavailableCategory != "authenticated_endpoint_unavailable" {
		t.Fatalf("UnavailableCategory changed shape: %q", UnavailableCategory)
	}
	if !errors.Is(ErrAuthenticatedEndpointUnavailable, ErrAuthenticatedEndpointUnavailable) {
		t.Fatalf("sentinel error identity broken")
	}
	if ErrAuthenticatedEndpointUnavailable.Error() != UnavailableCategory {
		t.Fatalf("error text %q != category %q", ErrAuthenticatedEndpointUnavailable.Error(), UnavailableCategory)
	}
}

func TestNormalizeEndpointURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"http accepted", "http://127.0.0.1:8787/rpc", false},
		{"https accepted", "https://peer.example:8787/rpc", false},
		{"ws accepted", "ws://127.0.0.1:8787/ws", false},
		{"wss accepted", "wss://peer.example:8787/ws", false},
		{"scheme case normalized", "HTTPS://Peer.Example/rpc", false},

		{"file scheme denied", "file:///etc/passwd", true},
		{"unix scheme denied", "unix:///tmp/sock", true},
		{"ftp scheme denied", "ftp://host/file", true},
		{"gopher scheme denied", "gopher://host/", true},
		{"data scheme denied", "data:text/plain,hi", true},
		{"unrecognized scheme denied", "spdy://host/", true},

		{"userinfo rejected", "https://user:pass@host/rpc", true},
		{"userinfo-only rejected", "https://user@host/rpc", true},
		{"fragment rejected", "https://host/rpc#frag", true},
		{"opaque rejected", "mailto:foo@bar.com", true},
		{"missing host rejected", "https:///rpc", true},
		{"wildcard host rejected", "https://*/rpc", true},
		{"zone identifier rejected", "https://[fe80::1%25eth0]/rpc", true},

		{"malformed port letters", "https://host:abc/rpc", true},
		{"malformed port zero", "https://host:0/rpc", true},
		{"malformed port overflow", "https://host:99999999999/rpc", true},

		{"ipv4-mapped ipv6 rejected", "https://[::ffff:127.0.0.1]/rpc", true},
		{"ipv4-mapped ipv6 public rejected", "https://[::ffff:8.8.8.8]/rpc", true},
		{"ordinary ipv6 loopback literal accepted structurally", "https://[::1]/rpc", false},

		{"decimal integer host rejected", "https://2130706433/rpc", true},
		{"hex host rejected", "https://0x7f000001/rpc", true},
		{"octal leading zero host rejected", "https://0177.0.0.1/rpc", true},
		{"octal leading zero last octet rejected", "https://127.0.0.01/rpc", true},
		{"ordinary hostname accepted", "https://swarm-desktop-abcdef.local/rpc", false},

		{"empty rejected", "", true},
		{"whitespace rejected", "   ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := NormalizeEndpointURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeEndpointURL(%q) = %v, %v; want error", tc.raw, u, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeEndpointURL(%q) unexpected error: %v", tc.raw, err)
			}
			if u == nil {
				t.Fatalf("NormalizeEndpointURL(%q) returned nil url with no error", tc.raw)
			}
		})
	}
}

func TestClassifyAddress(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want AddressClass
	}{
		{"ipv4 loopback", "127.0.0.1", AddressLoopback},
		{"ipv6 loopback", "::1", AddressLoopback},
		{"ipv4 unspecified", "0.0.0.0", AddressUnspecified},
		{"ipv6 unspecified", "::", AddressUnspecified},
		{"ipv4 link-local", "169.254.1.1", AddressLinkLocal},
		{"ipv6 link-local", "fe80::1", AddressLinkLocal},
		{"ipv4 link-local multicast (224.0.0.0/24)", "224.0.0.1", AddressLinkLocal},
		{"ipv6 link-local multicast (ff02::/16)", "ff02::1", AddressLinkLocal},
		{"ipv4 multicast (non-link-local scope)", "239.1.2.3", AddressMulticast},
		{"ipv6 multicast (global scope)", "ff0e::1", AddressMulticast},
		{"aws metadata", "169.254.169.254", AddressMetadata},
		{"ecs metadata", "169.254.170.2", AddressMetadata},
		{"aws ipv6 metadata", "fd00:ec2::254", AddressMetadata},
		{"private class a", "10.1.2.3", AddressPrivate},
		{"private class b", "172.16.0.5", AddressPrivate},
		{"private class c", "192.168.1.1", AddressPrivate},
		{"ipv6 unique-local", "fd12:3456:789a::1", AddressPrivate},
		{"public", "8.8.8.8", AddressPublic},
		{"public ipv6", "2001:4860:4860::8888", AddressPublic},
		{"ipv4-mapped ipv6 loopback classified as loopback", "::ffff:127.0.0.1", AddressLoopback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyAddress(net.ParseIP(tc.ip))
			if got != tc.want {
				t.Fatalf("ClassifyAddress(%s) = %s, want %s", tc.ip, got, tc.want)
			}
		})
	}
	if got := ClassifyAddress(nil); got != AddressInvalid {
		t.Fatalf("ClassifyAddress(nil) = %s, want invalid", got)
	}
	if got := ClassifyAddress(net.IP{}); got != AddressInvalid {
		t.Fatalf("ClassifyAddress(empty) = %s, want invalid", got)
	}
}

func TestAddressAllowedRemote(t *testing.T) {
	openLAN := RemotePolicy{AllowedPrivateCIDRs: []net.IPNet{*mustParseCIDR(t, "192.168.0.0/16")}}
	closedLAN := RemotePolicy{}

	cases := []struct {
		name   string
		ip     string
		policy RemotePolicy
		want   bool
	}{
		{"public always allowed", "8.8.8.8", closedLAN, true},
		{"loopback never allowed remote", "127.0.0.1", openLAN, false},
		{"unspecified never allowed remote", "0.0.0.0", openLAN, false},
		{"link-local never allowed remote", "169.254.1.1", openLAN, false},
		{"multicast never allowed remote", "224.0.0.1", openLAN, false},
		{"metadata never allowed remote even with LAN scope", "169.254.169.254", openLAN, false},
		{"private outside scope denied", "10.0.0.1", openLAN, false},
		{"private inside scope allowed", "192.168.1.5", openLAN, true},
		{"private denied when scope closed", "192.168.1.5", closedLAN, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AddressAllowedRemote(net.ParseIP(tc.ip), tc.policy)
			if got != tc.want {
				t.Fatalf("AddressAllowedRemote(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestAddressAllowedLoopbackPeer(t *testing.T) {
	if !AddressAllowedLoopbackPeer(net.ParseIP("127.0.0.1")) {
		t.Fatalf("127.0.0.1 should be an eligible loopback peer address")
	}
	if !AddressAllowedLoopbackPeer(net.ParseIP("::1")) {
		t.Fatalf("::1 should be an eligible loopback peer address")
	}
	denied := []string{"10.0.0.1", "8.8.8.8", "169.254.1.1", "169.254.169.254", "0.0.0.0", "224.0.0.1"}
	for _, ip := range denied {
		if AddressAllowedLoopbackPeer(net.ParseIP(ip)) {
			t.Fatalf("%s must not gain loopback peer privilege", ip)
		}
	}
}

func mustParseCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return n
}

func TestValidateDNSAnswers(t *testing.T) {
	allowPublicOnly := func(ip net.IP) bool { return ClassifyAddress(ip) == AddressPublic }

	t.Run("resolver error denies", func(t *testing.T) {
		_, err := ValidateDNSAnswers([]net.IP{net.ParseIP("8.8.8.8")}, errors.New("boom"), allowPublicOnly)
		if err == nil {
			t.Fatalf("want error on resolver error")
		}
	})

	t.Run("empty answer set denies", func(t *testing.T) {
		_, err := ValidateDNSAnswers(nil, nil, allowPublicOnly)
		if err == nil {
			t.Fatalf("want error on empty answers")
		}
	})

	t.Run("excessive answer count denies", func(t *testing.T) {
		var addrs []net.IP
		for i := 0; i < MaxDNSAnswers+1; i++ {
			addrs = append(addrs, net.ParseIP("8.8.8.8"))
		}
		_, err := ValidateDNSAnswers(addrs, nil, allowPublicOnly)
		if err == nil {
			t.Fatalf("want error on excessive answer count")
		}
	})

	t.Run("all allowed returns plan", func(t *testing.T) {
		addrs := []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1")}
		plan, err := ValidateDNSAnswers(addrs, nil, allowPublicOnly)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(plan) != 2 {
			t.Fatalf("plan len = %d, want 2", len(plan))
		}
	})

	t.Run("mixed public and private denies entire destination", func(t *testing.T) {
		addrs := []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("10.0.0.1")}
		_, err := ValidateDNSAnswers(addrs, nil, allowPublicOnly)
		if err == nil {
			t.Fatalf("want denial for mixed public/private answer set")
		}
	})

	t.Run("mixed public and loopback (rebinding shape) denies entire destination", func(t *testing.T) {
		addrs := []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")}
		_, err := ValidateDNSAnswers(addrs, nil, allowPublicOnly)
		if err == nil {
			t.Fatalf("want denial for mixed public/loopback answer set")
		}
	})

	t.Run("single forbidden answer denies", func(t *testing.T) {
		_, err := ValidateDNSAnswers([]net.IP{net.ParseIP("169.254.169.254")}, nil, allowPublicOnly)
		if err == nil {
			t.Fatalf("want denial for metadata answer")
		}
	})

	t.Run("invalid address in set denies", func(t *testing.T) {
		_, err := ValidateDNSAnswers([]net.IP{nil}, nil, allowPublicOnly)
		if err == nil {
			t.Fatalf("want denial for invalid address")
		}
	})
}

func TestValidateDialAddress(t *testing.T) {
	allowPublicOnly := func(ip net.IP) bool { return ClassifyAddress(ip) == AddressPublic }
	plan := []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1")}

	if err := ValidateDialAddress(net.ParseIP("8.8.8.8"), plan, allowPublicOnly); err != nil {
		t.Fatalf("planned address should validate: %v", err)
	}
	if err := ValidateDialAddress(net.ParseIP("9.9.9.9"), plan, allowPublicOnly); err == nil {
		t.Fatalf("rebinding to an address outside the plan must be denied")
	}
	if err := ValidateDialAddress(nil, plan, allowPublicOnly); err == nil {
		t.Fatalf("nil actual address must be denied")
	}

	// A plan entry whose classification changed between resolution and
	// connect (TOCTOU) must still be denied even though it is plan-member.
	tocTouPlan := []net.IP{net.ParseIP("127.0.0.1")}
	if err := ValidateDialAddress(net.ParseIP("127.0.0.1"), tocTouPlan, allowPublicOnly); err == nil {
		t.Fatalf("plan-member address that no longer satisfies allowed() must be denied")
	}
}

func TestValidateRedirect(t *testing.T) {
	remoteOnly := map[Scheme]bool{SchemeHTTPS: true, SchemeWSS: true}
	must := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", raw, err)
		}
		return u
	}

	if err := ValidateRedirect(must("https://peer/rpc"), must("https://peer/rpc2"), remoteOnly); err != nil {
		t.Fatalf("same-class redirect should be allowed: %v", err)
	}
	if err := ValidateRedirect(must("https://peer/rpc"), must("http://peer/rpc2"), remoteOnly); err == nil {
		t.Fatalf("scheme not in allowlist must be denied")
	}
	if err := ValidateRedirect(must("https://peer/rpc"), must("http://attacker/rpc"), map[Scheme]bool{SchemeHTTPS: true, SchemeHTTP: true}); err == nil {
		t.Fatalf("downgrade from https to http must be denied even if http is otherwise allowed")
	}
	if err := ValidateRedirect(nil, nil, remoteOnly); err == nil {
		t.Fatalf("nil redirect target must be denied")
	}
}

func TestListenerAddressGrant(t *testing.T) {
	cases := []struct {
		addr        string
		wantLoop    bool
		wantErr     bool
		description string
	}{
		{"127.0.0.1:8787", true, false, "ipv4 loopback literal"},
		{"[::1]:8787", true, false, "ipv6 loopback literal"},
		{"localhost:8787", true, false, "localhost hostname"},
		{"LOCALHOST:8787", true, false, "localhost case-insensitive"},
		{":8787", false, false, "bare port wildcard"},
		{"0.0.0.0:8787", false, false, "ipv4 unspecified"},
		{"[::]:8787", false, false, "ipv6 unspecified"},
		{"192.168.1.5:8787", false, false, "lan address"},
		{"example.com:8787", false, true, "ambiguous hostname"},
	}
	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			loop, err := ListenerAddressGrant(tc.addr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ListenerAddressGrant(%q) = %v, %v; want error", tc.addr, loop, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ListenerAddressGrant(%q) unexpected error: %v", tc.addr, err)
			}
			if loop != tc.wantLoop {
				t.Fatalf("ListenerAddressGrant(%q) = %v, want %v", tc.addr, loop, tc.wantLoop)
			}
		})
	}
}

func TestBuildOutboundHeadersDropsEverythingNotAllowlisted(t *testing.T) {
	in := make(http.Header)
	in.Set("Authorization", "Bearer sentinel-secret")
	in.Set("Proxy-Authorization", "sentinel-secret")
	in.Set("Cookie", "session=sentinel-secret")
	in.Set("Set-Cookie", "session=sentinel-secret")
	in.Set("X-Csrf-Token", "sentinel-secret")
	in.Set("Host", "attacker.example")
	in.Set("X-Forwarded-For", "sentinel-secret")
	in.Set("X-Forwarded-Host", "sentinel-secret")
	in.Set("X-Forwarded-Proto", "sentinel-secret")
	in.Set("Forwarded", "sentinel-secret")
	in.Set("Connection", "X-Sentinel-Custom")
	in.Set("X-Sentinel-Custom", "sentinel-secret")
	in.Set("Upgrade", "websocket")
	in.Set("Content-Type", "application/json")
	in.Set("Accept", "application/json")

	out := BuildOutboundHeaders(in)

	for name := range ForbiddenInboundHeaders {
		if v := out.Get(name); v != "" {
			t.Fatalf("forbidden header %q leaked into outbound headers with value %q", name, v)
		}
	}
	for name := range hopByHopHeaders {
		if v := out.Get(name); v != "" {
			t.Fatalf("hop-by-hop header %q leaked into outbound headers with value %q", name, v)
		}
	}
	if v := out.Get("X-Sentinel-Custom"); v != "" {
		t.Fatalf("Connection-nominated header leaked into outbound headers: %q", v)
	}
	if got := out.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := out.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q, want application/json", got)
	}
	if len(out) != 2 {
		t.Fatalf("outbound header set has %d keys, want exactly 2 (Content-Type, Accept): %v", len(out), out)
	}

	// out must never be the same underlying map as in.
	out.Set("Content-Type", "text/plain")
	if in.Get("Content-Type") == "text/plain" {
		t.Fatalf("BuildOutboundHeaders aliased the inbound header map")
	}
}

func TestIsHopByHopHeader(t *testing.T) {
	if !IsHopByHopHeader("Connection", "") {
		t.Fatalf("Connection must be hop-by-hop")
	}
	if !IsHopByHopHeader("Upgrade", "") {
		t.Fatalf("Upgrade must be hop-by-hop")
	}
	if !IsHopByHopHeader("X-Custom", "x-custom, x-other") {
		t.Fatalf("Connection-nominated header must be treated as hop-by-hop")
	}
	if IsHopByHopHeader("Content-Type", "") {
		t.Fatalf("Content-Type must not be hop-by-hop")
	}
}

func TestBuildOutboundQueryStartsEmpty(t *testing.T) {
	q := BuildOutboundQuery()
	if len(q) != 0 {
		t.Fatalf("BuildOutboundQuery() = %v, want empty", q)
	}
	if q.Encode() != "" {
		t.Fatalf("BuildOutboundQuery().Encode() = %q, want empty RawQuery", q.Encode())
	}
}

func TestRejectedInboundQueryFields(t *testing.T) {
	in := url.Values{
		"peer":          {"swarm-desktop-1"},
		"token":         {"sentinel-secret"},
		"authorization": {"sentinel-secret"},
		"access_token":  {"sentinel-secret"},
		"unexpected":    {"1"},
	}
	in["peer"] = append(in["peer"], "second-value") // duplicate recognized field

	rejected := RejectedInboundQueryFields(in)

	want := map[string]bool{
		"authorization": true,
		"access_token":  true,
		"unexpected":    true,
		"peer":          true, // duplicated, so rejected even though recognized
	}
	if len(rejected) != len(want) {
		t.Fatalf("rejected = %v, want set %v", rejected, want)
	}
	for _, k := range rejected {
		if !want[k] {
			t.Fatalf("unexpected rejected field %q", k)
		}
	}

	// token, presented exactly once, is a recognized gateway-only field and
	// must be consumed locally, not "rejected".
	for _, k := range rejected {
		if k == "token" {
			t.Fatalf("single-valued gateway-only field %q must not be rejected", k)
		}
	}
}
