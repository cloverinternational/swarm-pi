package web_fetch

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestToolName(t *testing.T) {
	tool := New()
	if tool.Name() != "web_fetch" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "web_fetch")
	}
}

func TestToolDescription(t *testing.T) {
	tool := New()
	desc := tool.Description()
	if len(desc) < 50 {
		t.Error("Description() should be detailed (at least 50 chars)")
	}
}

func TestToolParameters(t *testing.T) {
	tool := New()
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters() should not return nil")
	}
	schema := params.(map[string]any)
	if schema["type"] != "object" {
		t.Error("Schema type should be 'object'")
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["url"]; !ok {
		t.Error("Schema should have 'url' property")
	}
	if _, ok := props["prompt"]; !ok {
		t.Error("Schema should have 'prompt' property")
	}
	for _, name := range []string{"timeout_seconds", "max_body_bytes", "max_output_chars", "bypass_cache"} {
		if _, ok := props[name]; !ok {
			t.Errorf("Schema should have %q property", name)
		}
	}
}

func TestToolValidate(t *testing.T) {
	tool := New()
	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{"valid https URL", map[string]any{"url": "https://example.com"}, false},
		{"valid http URL", map[string]any{"url": "http://example.com"}, false},
		{"missing url", map[string]any{}, true},
		{"empty url", map[string]any{"url": ""}, true},
		{"url with credentials", map[string]any{"url": "https://user:pass@example.com"}, true},
		{"url without host", map[string]any{"url": "just-a-string"}, true},
		{"url with prompt", map[string]any{"url": "https://example.com", "prompt": "summarize"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.Validate(tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUpgradeToHTTPS(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"http://example.com", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"http://example.com/path?q=1", "https://example.com/path?q=1"},
	}
	for _, tt := range tests {
		got := upgradeToHTTPS(tt.in)
		if got != tt.want {
			t.Errorf("upgradeToHTTPS(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsSameOriginRedirect(t *testing.T) {
	tests := []struct {
		original string
		target   string
		want     bool
	}{
		{"https://example.com", "https://www.example.com/path", true},
		{"https://www.example.com", "https://example.com/path", true},
		{"https://example.com/a", "https://example.com/b", true},
		{"https://example.com", "https://other.com", false},
		{"https://example.com", "http://example.com", false},
	}
	for _, tt := range tests {
		got := isSameOriginRedirect(tt.original, tt.target)
		if got != tt.want {
			t.Errorf("isSameOriginRedirect(%q, %q) = %v, want %v", tt.original, tt.target, got, tt.want)
		}
	}
}

func TestHtmlToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains string
	}{
		{
			"basic text",
			"<html><body><p>Hello world</p></body></html>",
			"Hello world",
		},
		{
			"strips script",
			"<html><body><script>alert('x')</script><p>visible</p></body></html>",
			"visible",
		},
		{
			"heading prefix",
			"<h1>Title</h1>",
			"# Title",
		},
		{
			"link with href",
			`<a href="https://example.com">click here</a>`,
			"https://example.com",
		},
		{
			"image alt text",
			`<img alt="a cat" src="x.jpg">`,
			"a cat",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := htmlToMarkdown(tt.html)
			if !contains(got, tt.contains) {
				t.Errorf("htmlToMarkdown output does not contain %q\nGot: %q", tt.contains, got)
			}
		})
	}

	// Script content must NOT appear in output.
	out := htmlToMarkdown("<script>secretCode()</script><p>visible</p>")
	if contains(out, "secretCode") {
		t.Error("htmlToMarkdown should strip <script> content")
	}
}

func TestCache(t *testing.T) {
	c := newURLCache()
	entry := &cacheEntry{
		content:   "hello",
		bytes:     5,
		code:      200,
		codeText:  "OK",
		fetchedAt: time.Now(),
	}
	c.set("https://example.com", entry)

	got, ok := c.get("https://example.com")
	if !ok {
		t.Fatal("cache.get() should find the entry")
	}
	if got.content != "hello" {
		t.Errorf("cache.get() content = %q, want %q", got.content, "hello")
	}

	// Non-existent key.
	_, ok = c.get("https://missing.example.com")
	if ok {
		t.Error("cache.get() should return false for missing key")
	}
}

func TestToolOptimizationHints(t *testing.T) {
	tool := New()
	hints := tool.OptimizationHints()
	if hints == nil {
		t.Fatal("OptimizationHints() should not return nil")
	}
	if !hints.Cacheable {
		t.Error("web_fetch results should be cacheable")
	}
}

func TestExecuteRedirectCacheBypassStatusAndCaps(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/content", http.StatusFound)
		case "/error":
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("teapot"))
		default:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("0123456789abcdef"))
		}
	}))
	defer server.Close()

	tool := testToolForServer(t, server)
	rawURL := testURL(t, server, "/redirect")
	params := map[string]any{"url": rawURL, "max_body_bytes": float64(12), "max_output_chars": float64(8)}

	first, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	requireMetadata(t, first, "status", "ok")
	requireMetadata(t, first, "truncated", true)
	requireMetadata(t, first, "body_truncated", true)
	requireMetadata(t, first, "output_truncated", true)
	if got := hits.Load(); got != 2 {
		t.Fatalf("redirect request count = %d, want 2", got)
	}

	second, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("cached Execute: %v", err)
	}
	requireMetadata(t, second, "cached", true)
	if got := hits.Load(); got != 2 {
		t.Fatalf("cache hit made network request, count = %d", got)
	}

	params["bypass_cache"] = true
	bypassed, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("bypass Execute: %v", err)
	}
	requireMetadata(t, bypassed, "cached", false)
	if got := hits.Load(); got != 4 {
		t.Fatalf("cache bypass request count = %d, want 4", got)
	}

	httpError, err := tool.Execute(context.Background(), map[string]any{
		"url":              testURL(t, server, "/error"),
		"max_body_bytes":   float64(32),
		"max_output_chars": float64(32),
	})
	if err != nil {
		t.Fatalf("non-2xx Execute: %v", err)
	}
	requireMetadata(t, httpError, "status", "http_error")
	requireMetadata(t, httpError, "ok", false)
	requireMetadata(t, httpError, "code", http.StatusTeapot)
}

func TestExecuteBlocksUnsafeDestinationsAndRedirects(t *testing.T) {
	tool := New()
	for _, rawURL := range []string{
		"https://127.0.0.1/",
		"https://[::1]/",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/",
	} {
		if _, err := tool.Execute(context.Background(), map[string]any{"url": rawURL}); err == nil ||
			!strings.Contains(err.Error(), "blocked destination") {
			t.Errorf("Execute(%q) error = %v, want blocked destination", rawURL, err)
		}
	}

	tool = New()
	tool.lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IPAddr{{IP: ip}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	tool.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Status:     "302 Found",
			Header:     http.Header{"Location": []string{"http://169.254.169.254/secret"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})
	if _, err := tool.Execute(context.Background(), map[string]any{"url": "https://public.example/"}); err == nil ||
		!strings.Contains(err.Error(), "blocked destination") {
		t.Fatalf("unsafe redirect error = %v, want blocked destination", err)
	}
}

func TestExecuteFailsClosedOnDNSRebinding(t *testing.T) {
	tool := New()
	var lookups atomic.Int32
	tool.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		if lookups.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	tool.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("must not dial")
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"url": "https://rebind.example/"}); err == nil ||
		!strings.Contains(err.Error(), "blocked destination") {
		t.Fatalf("DNS rebinding error = %v, want blocked destination", err)
	}
}

func TestSpecialPurposeRangesBlockedInValidationAndDial(t *testing.T) {
	blocked := []string{
		"0.1.2.3", "100.64.0.1", "100.127.255.254", "192.0.2.1",
		"198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"2001:db8::1", "3fff::1", "5f00::1", "100::1",
	}
	for _, rawIP := range blocked {
		t.Run(rawIP, func(t *testing.T) {
			ip := net.ParseIP(rawIP)
			if isPublicIP(ip) {
				t.Fatalf("isPublicIP(%s) = true", rawIP)
			}
			tool := New()
			var lookups atomic.Int32
			tool.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
				if lookups.Add(1) == 1 {
					return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
				}
				return []net.IPAddr{{IP: ip}}, nil
			}
			tool.dialContext = func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("must not dial a special-purpose address")
			}
			if _, err := tool.Execute(context.Background(), map[string]any{
				"url": "https://special-range.example/",
			}); err == nil || !strings.Contains(err.Error(), "blocked destination") {
				t.Fatalf("secure dial accepted %s: %v", rawIP, err)
			}
		})
	}

	for _, rawIP := range []string{"8.8.8.8", "100.63.255.255", "100.128.0.1", "2606:4700:4700::1111"} {
		if !isPublicIP(net.ParseIP(rawIP)) {
			t.Errorf("public boundary address %s was blocked", rawIP)
		}
	}
}

func TestExecuteTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()
	tool := testToolForServer(t, server)
	start := time.Now()
	_, err := tool.Execute(context.Background(), map[string]any{
		"url":             testURL(t, server, "/"),
		"timeout_seconds": float64(1),
		"bypass_cache":    true,
	})
	if err == nil || time.Since(start) > 1400*time.Millisecond {
		t.Fatalf("timeout error = %v after %s", err, time.Since(start))
	}
}

func TestHTMLConversionLimitIsMachineDetectable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<main>" + strings.Repeat("word ", maxMarkdownLength) + "</main>"))
	}))
	defer server.Close()

	tool := testToolForServer(t, server)
	result, err := tool.Execute(context.Background(), map[string]any{
		"url":          testURL(t, server, "/large-html"),
		"bypass_cache": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	requireMetadata(t, result, "output_truncated", true)
	requireMetadata(t, result, "truncated", true)
	requireMetadata(t, result, "truncation_reason", truncationMetadataValue)
}

func TestValidateRejectsOutOfBoundsControls(t *testing.T) {
	tool := New()
	for _, params := range []map[string]any{
		{"url": "https://example.com", "timeout_seconds": float64(0)},
		{"url": "https://example.com", "timeout_seconds": float64(maxTimeout/time.Second + 1)},
		{"url": "https://example.com", "max_body_bytes": float64(maxAllowedBodyBytes + 1)},
		{"url": "https://example.com", "max_output_chars": float64(maxAllowedOutputChars + 1)},
		{"url": "https://example.com", "bypass_cache": "yes"},
	} {
		if err := tool.Validate(params); err == nil {
			t.Errorf("Validate(%v) unexpectedly succeeded", params)
		}
	}
}

func testToolForServer(t *testing.T, server *httptest.Server) *Tool {
	t.Helper()
	tool := New()
	address := server.Listener.Addr().String()
	tool.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	tool.httpClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // test server certificate
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
	return tool
}

func testURL(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return "https://public.example:" + port + path
}

func requireMetadata(t *testing.T, result *tools.ToolResult, key string, want any) {
	t.Helper()
	if got := result.Metadata[key]; got != want {
		t.Fatalf("metadata[%q] = %#v, want %#v (all: %#v)", key, got, want, result.Metadata)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// ─── helpers ─────────────────────────────────────────────────────────────────

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
