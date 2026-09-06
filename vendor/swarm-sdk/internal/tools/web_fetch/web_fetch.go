// Package web_fetch provides a bounded, SSRF-resistant web content fetch tool.
package web_fetch

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	defaultTimeout          = 60 * time.Second
	maxTimeout              = 60 * time.Second
	defaultMaxBodyBytes     = 10 * 1024 * 1024
	maxAllowedBodyBytes     = 10 * 1024 * 1024
	defaultMaxOutputChars   = 100_000
	maxAllowedOutputChars   = 100_000
	maxURLLength            = 2000
	maxRedirects            = 10
	userAgent               = "SwarmCode-WebFetch/1.0 (+https://swarmcode.ai)"
	truncationMetadataValue = "limit_exceeded"
)

type lookupIPFunc func(context.Context, string) ([]net.IPAddr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

// Tool implements the web_fetch tool.
type Tool struct {
	httpClient  *http.Client
	lookupIP    lookupIPFunc
	dialContext dialContextFunc
}

type fetchOptions struct {
	timeout        time.Duration
	maxBodyBytes   int64
	maxOutputChars int
	bypassCache    bool
}

var _ tools.Tool = &Tool{}

// New creates a web_fetch tool with conservative resource and network defaults.
func New() *Tool {
	tool := &Tool{
		lookupIP:    net.DefaultResolver.LookupIPAddr,
		dialContext: (&net.Dialer{}).DialContext,
	}
	tool.httpClient = &http.Client{
		Timeout: defaultTimeout,
		Transport: &http.Transport{
			// Proxies can resolve a different address after local validation, so
			// web_fetch deliberately connects directly.
			Proxy:       nil,
			DialContext: tool.secureDialContext,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return tool
}

// Name returns the tool's stable catalog name.
func (t *Tool) Name() string { return "web_fetch" }

// Description describes web_fetch behavior and safety limits.
func (t *Tool) Description() string {
	return `Fetches public HTTP(S) content and returns readable text.
The tool converts HTML to markdown-style text, follows safe same-origin redirects,
caches successful results for 15 minutes, and reports HTTP/truncation status in
machine-readable metadata. Private, loopback, link-local, multicast, and
otherwise unsafe destinations are blocked, including after DNS resolution and
on redirects. timeout_seconds, max_body_bytes, and max_output_chars can lower
the safe defaults; bypass_cache forces a fresh request.`
}

// Parameters returns the JSON schema accepted by Execute.
func (t *Tool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "A fully formed public http:// or https:// URL. HTTP is upgraded to HTTPS.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Optional intent recorded in result metadata.",
			},
			"timeout_seconds": map[string]any{
				"type":        "number",
				"minimum":     1,
				"maximum":     int(maxTimeout / time.Second),
				"description": "Request timeout in seconds; may only lower the safe default.",
			},
			"max_body_bytes": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxAllowedBodyBytes,
				"description": "Maximum response bytes read; may only lower the safe default.",
			},
			"max_output_chars": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxAllowedOutputChars,
				"description": "Maximum characters returned; may only lower the safe default.",
			},
			"bypass_cache": map[string]any{
				"type":        "boolean",
				"description": "Skip a cached value and refresh it from the network.",
			},
		},
		"required": []string{"url"},
	}
}

// Validate checks URL syntax and bounded execution controls.
func (t *Tool) Validate(params map[string]any) error {
	rawURL, ok := params["url"].(string)
	if !ok || rawURL == "" {
		return fmt.Errorf("url parameter is required and must be a non-empty string")
	}
	if len(rawURL) > maxURLLength {
		return fmt.Errorf("url exceeds maximum length of %d characters", maxURLLength)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid URL %q: scheme must be http or https", rawURL)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("invalid URL %q: missing host", rawURL)
	}
	if parsed.User != nil {
		return fmt.Errorf("URLs with embedded credentials are not allowed")
	}
	if _, err := optionsFromParams(params); err != nil {
		return err
	}
	return nil
}

func optionsFromParams(params map[string]any) (fetchOptions, error) {
	options := fetchOptions{
		timeout:        defaultTimeout,
		maxBodyBytes:   defaultMaxBodyBytes,
		maxOutputChars: defaultMaxOutputChars,
	}
	if value, ok := params["timeout_seconds"]; ok {
		seconds, parseErr := boundedNumber(value, "timeout_seconds", 1, float64(maxTimeout/time.Second))
		if parseErr != nil {
			return options, parseErr
		}
		options.timeout = time.Duration(seconds * float64(time.Second))
	}
	if value, ok := params["max_body_bytes"]; ok {
		number, parseErr := boundedInteger(value, "max_body_bytes", maxAllowedBodyBytes)
		if parseErr != nil {
			return options, parseErr
		}
		options.maxBodyBytes = int64(number)
	}
	if value, ok := params["max_output_chars"]; ok {
		number, parseErr := boundedInteger(value, "max_output_chars", maxAllowedOutputChars)
		if parseErr != nil {
			return options, parseErr
		}
		options.maxOutputChars = number
	}
	if value, ok := params["bypass_cache"]; ok {
		options.bypassCache, ok = value.(bool)
		if !ok {
			return options, fmt.Errorf("bypass_cache must be a boolean")
		}
	}
	return options, nil
}

func boundedNumber(value any, name string, minimum, maximum float64) (float64, error) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	default:
		return 0, fmt.Errorf("%s must be a number between %s and %s", name,
			strconv.FormatFloat(minimum, 'f', -1, 64), strconv.FormatFloat(maximum, 'f', -1, 64))
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number < minimum || number > maximum {
		return 0, fmt.Errorf("%s must be a number between %s and %s", name,
			strconv.FormatFloat(minimum, 'f', -1, 64), strconv.FormatFloat(maximum, 'f', -1, 64))
	}
	return number, nil
}

func boundedInteger(value any, name string, maximum int) (int, error) {
	number, err := boundedNumber(value, name, 1, float64(maximum))
	if err != nil {
		return 0, err
	}
	integer := int(number)
	if number != float64(integer) {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return integer, nil
}

// Execute fetches the URL and returns content plus machine-readable metadata.
func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if err := t.Validate(params); err != nil {
		return nil, err
	}
	options, _ := optionsFromParams(params)
	rawURL := params["url"].(string)
	prompt, _ := params["prompt"].(string)
	fetchURL := upgradeToHTTPS(rawURL)
	cacheKey := fmt.Sprintf("%s|body=%d|output=%d", fetchURL, options.maxBodyBytes, options.maxOutputChars)
	started := time.Now()

	if !options.bypassCache {
		if cached, ok := globalCache.get(cacheKey); ok {
			return buildResult(rawURL, prompt, cached, true, started), nil
		}
	}

	requestContext, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()
	content, entry, redirectURL, err := t.fetchURL(requestContext, fetchURL, options)
	if err != nil {
		return nil, err
	}
	if redirectURL != "" {
		result := tools.NewXMLResult(tools.NewXML("result").
			Attr("url", rawURL).
			Attr("status", "redirect").
			Attr("redirect_url", redirectURL).
			Field("message", "Cross-origin redirect not followed; fetch redirect_url explicitly."))
		result.WithDuration(time.Since(started).Milliseconds())
		result.WithMetadata("url", rawURL)
		result.WithMetadata("status", "redirect")
		result.WithMetadata("ok", false)
		result.WithMetadata("redirect_url", redirectURL)
		return result, nil
	}
	entry.content = content
	if entry.code >= 200 && entry.code < 300 {
		globalCache.set(cacheKey, entry)
	}
	return buildResult(rawURL, prompt, entry, false, started), nil
}

func buildResult(rawURL, prompt string, entry *cacheEntry, cached bool, started time.Time) *tools.ToolResult {
	status := "ok"
	ok := entry.code >= 200 && entry.code < 300
	if !ok {
		status = "http_error"
	}
	truncated := entry.bodyTruncated || entry.outputTruncated
	result := tools.NewXMLResult(tools.NewXML("result").
		Attr("url", rawURL).
		Attr("status", status).
		Attr("ok", strconv.FormatBool(ok)).
		Attr("cached", strconv.FormatBool(cached)).
		Attr("truncated", strconv.FormatBool(truncated)).
		Field("content", entry.content))
	result.WithDuration(time.Since(started).Milliseconds())
	result.WithMetadata("url", rawURL)
	result.WithMetadata("status", status)
	result.WithMetadata("ok", ok)
	result.WithMetadata("code", entry.code)
	result.WithMetadata("codeText", entry.codeText)
	result.WithMetadata("bytes", entry.bytes)
	result.WithMetadata("cached", cached)
	result.WithMetadata("truncated", truncated)
	result.WithMetadata("body_truncated", entry.bodyTruncated)
	result.WithMetadata("output_truncated", entry.outputTruncated)
	if truncated {
		result.WithMetadata("truncation_reason", truncationMetadataValue)
	}
	if prompt != "" {
		result.WithMetadata("prompt", prompt)
	}
	result.Content = []tools.ContentBlock{tools.TextContent(entry.content)}
	return result
}

func (t *Tool) fetchURL(ctx context.Context, rawURL string, options fetchOptions) (string, *cacheEntry, string, error) {
	return t.fetchRecursive(ctx, rawURL, options, 0)
}

func (t *Tool) fetchRecursive(ctx context.Context, rawURL string, options fetchOptions, depth int) (string, *cacheEntry, string, error) {
	if depth > maxRedirects {
		return "", nil, "", fmt.Errorf("too many redirects (exceeded %d)", maxRedirects)
	}
	if err := t.validateDestination(ctx, rawURL); err != nil {
		return "", nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Accept", "text/markdown, text/html, */*")
	req.Header.Set("User-Agent", userAgent)
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if isRedirect(resp.StatusCode) {
		location := resp.Header.Get("Location")
		if location == "" {
			return "", nil, "", fmt.Errorf("redirect with no Location header (status %d)", resp.StatusCode)
		}
		redirect, err := url.Parse(location)
		if err != nil {
			return "", nil, "", fmt.Errorf("invalid redirect URL %q: %w", location, err)
		}
		base, _ := url.Parse(rawURL)
		target := base.ResolveReference(redirect).String()
		if err := t.validateDestination(ctx, target); err != nil {
			return "", nil, "", fmt.Errorf("unsafe redirect: %w", err)
		}
		if isSameOriginRedirect(rawURL, target) {
			return t.fetchRecursive(ctx, target, options, depth+1)
		}
		return "", nil, target, nil
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, options.maxBodyBytes+1))
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to read response body: %w", err)
	}
	bodyTruncated := int64(len(bodyBytes)) > options.maxBodyBytes
	if bodyTruncated {
		bodyBytes = bodyBytes[:options.maxBodyBytes]
	}
	contentType := resp.Header.Get("Content-Type")
	processed := string(bodyBytes)
	conversionTruncated := false
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		processed, conversionTruncated = htmlToMarkdownWithTruncation(processed)
	}
	processed, outputLimitTruncated := truncateCharacters(processed, options.maxOutputChars)
	outputTruncated := conversionTruncated || outputLimitTruncated
	entry := &cacheEntry{
		content:         processed,
		bytes:           len(bodyBytes),
		code:            resp.StatusCode,
		codeText:        resp.Status,
		contentType:     contentType,
		fetchedAt:       time.Now(),
		bodyTruncated:   bodyTruncated,
		outputTruncated: outputTruncated,
	}
	return processed, entry, "", nil
}

func truncateCharacters(content string, maximum int) (string, bool) {
	runes := []rune(content)
	if len(runes) <= maximum {
		return content, false
	}
	return string(runes[:maximum]), true
}

func (t *Tool) validateDestination(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("blocked destination: invalid HTTP(S) URL")
	}
	addresses, err := t.lookupIP(ctx, parsed.Hostname())
	if err != nil {
		return fmt.Errorf("blocked destination: DNS lookup failed for %q: %w", parsed.Hostname(), err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("blocked destination: DNS returned no addresses for %q", parsed.Hostname())
	}
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			return fmt.Errorf("blocked destination: %q resolves to non-public address %s", parsed.Hostname(), address.IP)
		}
	}
	return nil
}

var blockedSpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

func isPublicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range blockedSpecialPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func (t *Tool) secureDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("blocked destination: invalid network address %q", address)
	}
	addresses, err := t.lookupIP(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("blocked destination: DNS lookup failed for %q", host)
	}
	for _, resolved := range addresses {
		if !isPublicIP(resolved.IP) {
			return nil, fmt.Errorf("blocked destination: %q resolves to non-public address %s", host, resolved.IP)
		}
	}
	// Dial the already validated address, not the hostname. This closes the
	// resolution-to-connect DNS rebinding window.
	return t.dialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
}

func upgradeToHTTPS(rawURL string) string {
	if strings.HasPrefix(rawURL, "http://") {
		return "https://" + rawURL[7:]
	}
	return rawURL
}

func isRedirect(code int) bool {
	return code == http.StatusMovedPermanently ||
		code == http.StatusFound ||
		code == http.StatusSeeOther ||
		code == http.StatusTemporaryRedirect ||
		code == http.StatusPermanentRedirect
}

func isSameOriginRedirect(original, target string) bool {
	source, sourceErr := url.Parse(original)
	destination, destinationErr := url.Parse(target)
	if sourceErr != nil || destinationErr != nil ||
		source.Scheme != destination.Scheme ||
		source.Port() != destination.Port() ||
		destination.User != nil {
		return false
	}
	stripWWW := func(host string) string { return strings.TrimPrefix(strings.ToLower(host), "www.") }
	return stripWWW(source.Hostname()) == stripWWW(destination.Hostname())
}

// IsIdempotent reports false because remote content can change.
func (t *Tool) IsIdempotent() bool { return false }

// RequiresPermission declares the network capability enforced by the binder.
func (t *Tool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionNetworkAccess}
}

// SupportedContentTypes reports text output.
func (t *Tool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints advertises cache and scheduling behavior.
func (t *Tool) OptimizationHints() *tools.OptimizationHints {
	return &tools.OptimizationHints{
		PreferSequential:  false,
		EstimatedDuration: 5 * time.Second,
		CanBatch:          false,
		Priority:          50,
		Cacheable:         true,
		CacheTTL:          cacheTTL,
	}
}
