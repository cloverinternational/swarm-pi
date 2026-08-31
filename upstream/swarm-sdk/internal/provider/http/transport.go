package http

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// TransportConfig configures the HTTP transport layer for reliable connections.
// These settings are optimized for slow/unstable network conditions.
type TransportConfig struct {
	// DialTimeout is the maximum time to establish a TCP connection.
	// Default: 30s. Increase for slow networks.
	DialTimeout time.Duration

	// TLSHandshakeTimeout is the maximum time for the TLS handshake.
	// Default: 10s. Increase for slow networks.
	TLSHandshakeTimeout time.Duration

	// ResponseHeaderTimeout is the maximum time to receive response headers
	// after the request has been sent.
	// Default: 0 (no timeout). Set this to prevent hanging on slow servers.
	ResponseHeaderTimeout time.Duration

	// IdleConnTimeout is the maximum time an idle connection stays in the pool.
	// Default: 90s. Connections are reused within this window.
	IdleConnTimeout time.Duration

	// MaxIdleConns is the maximum number of idle connections across all hosts.
	// Default: 100. Increase for high-throughput scenarios.
	MaxIdleConns int

	// MaxIdleConnsPerHost is the maximum idle connections per host.
	// Default: 2. Increase to improve connection reuse for a single host.
	MaxIdleConnsPerHost int

	// MaxConnsPerHost limits the total connections per host.
	// Default: 0 (unlimited). Set to prevent overwhelming the server.
	MaxConnsPerHost int

	// TCPKeepAlive specifies the interval for TCP keepalive probes.
	// Default: 30s. Helps detect broken connections on unstable networks.
	TCPKeepAlive time.Duration

	// DisableKeepAlives disables HTTP keep-alives (connection reuse).
	// Default: false. Only disable for debugging connection issues.
	DisableKeepAlives bool

	// DisableCompression disables gzip compression for responses.
	// Default: false. Only disable if you need uncompressed responses.
	DisableCompression bool

	// ExpectContinueTimeout limits time waiting for a 100 Continue response
	// when the request has an "Expect: 100-continue" header.
	// Default: 1s.
	ExpectContinueTimeout time.Duration

	// TLSClientConfig allows custom TLS configuration.
	// Default: nil (uses Go's defaults).
	TLSClientConfig *tls.Config
}

// DefaultTransportConfig returns transport settings optimized for reliability
// on slow/unstable connections. These are AGGRESSIVE defaults for maximum compatibility.
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		// Connection establishment timeouts - VERY generous for unstable networks
		DialTimeout:         120 * time.Second, // 2 minutes to establish connection
		TLSHandshakeTimeout: 60 * time.Second,  // 1 minute for TLS handshake

		// Response timeouts - disabled by default, set per-request
		ResponseHeaderTimeout: 0, // No timeout - rely on request-level timeout

		// Connection pooling - aggressive for API usage
		IdleConnTimeout:     300 * time.Second, // Keep connections alive for 5 minutes
		MaxIdleConns:        200,               // More idle connections
		MaxIdleConnsPerHost: 20,                // More per-host idle connections
		MaxConnsPerHost:     0,                 // No limit on total connections

		// TCP keepalive - VERY frequent to detect dead connections quickly
		TCPKeepAlive: 10 * time.Second, // Probe every 10 seconds

		// HTTP settings
		DisableKeepAlives:     false,           // Enable connection reuse
		DisableCompression:    false,           // Enable gzip
		ExpectContinueTimeout: 2 * time.Second, // Slightly longer for slow servers
		TLSClientConfig:       nil,             // Use defaults
	}
}

// AggressiveRetryTransportConfig returns transport settings optimized for
// MAXIMUM reliability on extremely unstable/slow networks (satellite, mobile, VPN).
func AggressiveRetryTransportConfig() TransportConfig {
	return TransportConfig{
		// EXTREMELY generous timeouts for terrible networks
		DialTimeout:         300 * time.Second, // 5 minutes to connect
		TLSHandshakeTimeout: 120 * time.Second, // 2 minutes for TLS

		// Response timeouts - disabled, rely on request-level
		ResponseHeaderTimeout: 0,

		// Maximum connection pooling - keep everything alive
		IdleConnTimeout:     600 * time.Second, // Keep alive for 10 minutes
		MaxIdleConns:        500,               // Lots of idle connections
		MaxIdleConnsPerHost: 50,                // Lots per-host

		// VERY frequent keepalive - detect dead connections ASAP
		TCPKeepAlive: 5 * time.Second, // Probe every 5 seconds

		DisableKeepAlives:     false,
		DisableCompression:    false,
		ExpectContinueTimeout: 5 * time.Second, // More time for slow servers
	}
}

// UnstableNetworkTransportConfig returns transport settings for the WORST network conditions.
// Use this for satellite internet, very poor mobile, or extremely unreliable connections.
func UnstableNetworkTransportConfig() TransportConfig {
	return TransportConfig{
		// Absurdly generous timeouts - never give up
		DialTimeout:         600 * time.Second, // 10 minutes to connect
		TLSHandshakeTimeout: 300 * time.Second, // 5 minutes for TLS

		// No response timeout - let the request-level context handle it
		ResponseHeaderTimeout: 0,

		// Maximum connection reuse
		IdleConnTimeout:     900 * time.Second, // Keep alive for 15 minutes
		MaxIdleConns:        1000,              // Maximum connections
		MaxIdleConnsPerHost: 100,               // Maximum per-host

		// Ultra-frequent keepalive
		TCPKeepAlive: 3 * time.Second, // Probe every 3 seconds

		DisableKeepAlives:     false,
		DisableCompression:    false,
		ExpectContinueTimeout: 10 * time.Second,
	}
}

// FastTransportConfig returns transport settings optimized for low-latency
// on fast, stable networks.
func FastTransportConfig() TransportConfig {
	return TransportConfig{
		// Tighter timeouts for fast networks
		DialTimeout:           10 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, // Expect headers quickly

		// Standard pooling
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 5,

		// Less frequent keepalive
		TCPKeepAlive: 60 * time.Second,

		DisableKeepAlives:     false,
		DisableCompression:    false,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NewTransport creates a custom http.Transport with the given configuration.
// This transport is optimized for reliability on slow/unstable connections.
func NewTransport(config TransportConfig) *http.Transport {
	// Create custom dialer with timeout and keepalive
	dialer := &net.Dialer{
		Timeout:   config.DialTimeout,
		KeepAlive: config.TCPKeepAlive,
		// DualStack is deprecated but we keep it for backwards compatibility
	}

	return &http.Transport{
		// Connection establishment
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,

		// Connection pooling
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConnsPerHost,
		MaxConnsPerHost:     config.MaxConnsPerHost,
		IdleConnTimeout:     config.IdleConnTimeout,

		// HTTP behavior
		DisableKeepAlives:     config.DisableKeepAlives,
		DisableCompression:    config.DisableCompression,
		ExpectContinueTimeout: config.ExpectContinueTimeout,

		// TLS configuration
		TLSClientConfig: config.TLSClientConfig,

		// Force HTTP/2 when available
		ForceAttemptHTTP2: true,
	}
}

// NewDefaultTransport creates a transport with default reliability settings.
func NewDefaultTransport() *http.Transport {
	return NewTransport(DefaultTransportConfig())
}

// NewAggressiveRetryTransport creates a transport optimized for flaky networks.
func NewAggressiveRetryTransport() *http.Transport {
	return NewTransport(AggressiveRetryTransportConfig())
}

// NewFastTransport creates a transport optimized for fast, stable networks.
func NewFastTransport() *http.Transport {
	return NewTransport(FastTransportConfig())
}

// NewUnstableNetworkTransport creates a transport for the worst network conditions.
func NewUnstableNetworkTransport() *http.Transport {
	return NewTransport(UnstableNetworkTransportConfig())
}

// ResilientClientConfig configures a resilient HTTP client with connection retry.
type ResilientClientConfig struct {
	// Transport configuration
	TransportConfig TransportConfig

	// ConnectionRetries is the number of times to retry connection failures
	// (dial timeout, TLS handshake failure, DNS errors).
	// Default: 3
	ConnectionRetries int

	// ConnectionRetryDelay is the initial delay between connection retries.
	// Uses exponential backoff: delay, delay*2, delay*4, etc.
	// Default: 1s
	ConnectionRetryDelay time.Duration

	// MaxConnectionRetryDelay caps the maximum delay between retries.
	// Default: 30s
	MaxConnectionRetryDelay time.Duration

	// RequestTimeout is the overall timeout for a single request attempt.
	// 0 means no timeout. -1 means use transport defaults.
	// Default: 0 (no timeout)
	RequestTimeout time.Duration
}

// DefaultResilientClientConfig returns sensible defaults for unstable networks.
func DefaultResilientClientConfig() ResilientClientConfig {
	return ResilientClientConfig{
		TransportConfig:         DefaultTransportConfig(),
		ConnectionRetries:       5,                // More retries for unstable networks
		ConnectionRetryDelay:    3 * time.Second,  // Longer initial delay
		MaxConnectionRetryDelay: 60 * time.Second, // Longer max delay
		RequestTimeout:          0,                // No timeout - rely on context
	}
}

// AggressiveResilientClientConfig returns very aggressive settings for terrible networks.
func AggressiveResilientClientConfig() ResilientClientConfig {
	return ResilientClientConfig{
		TransportConfig:         AggressiveRetryTransportConfig(),
		ConnectionRetries:       10,                // Many retries
		ConnectionRetryDelay:    5 * time.Second,   // Longer initial delay
		MaxConnectionRetryDelay: 120 * time.Second, // 2 minute max delay
		RequestTimeout:          0,                 // No timeout
	}
}

// UnstableNetworkClientConfig returns settings for the worst possible networks.
func UnstableNetworkClientConfig() ResilientClientConfig {
	return ResilientClientConfig{
		TransportConfig:         UnstableNetworkTransportConfig(),
		ConnectionRetries:       20,                // Never give up
		ConnectionRetryDelay:    10 * time.Second,  // Long initial delay
		MaxConnectionRetryDelay: 300 * time.Second, // 5 minute max delay
		RequestTimeout:          0,                 // No timeout
	}
}

// NewResilientClient creates an http.Client optimized for reliability.
// It uses a custom transport with connection pooling and keepalive.
func NewResilientClient(config ResilientClientConfig) *http.Client {
	transport := NewTransport(config.TransportConfig)

	var timeout time.Duration
	if config.RequestTimeout > 0 {
		timeout = config.RequestTimeout
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}
