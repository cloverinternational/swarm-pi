package cloud

import (
	"context"
	"net/http"
	"time"
)

// Client is an injectable Swarm Cloud HTTP client used for deterministic testing and
// for sharing a single HTTP transport across cloud operations.
//
// If HTTP is nil, a default &http.Client{} is used and per-operation timeouts are
// enforced via context.WithTimeout.
type Client struct {
	Config       *CloudConfig
	TokenManager *TokenManager
	HTTP         *http.Client
}

func NewClient(config *CloudConfig, tokenManager *TokenManager, httpClient *http.Client) *Client {
	return &Client{
		Config:       config,
		TokenManager: tokenManager,
		HTTP:         httpClient,
	}
}

func (c *Client) httpClient() *http.Client {
	return ensureHTTPClient(c.HTTP)
}

func ensureHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{}
}

func withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, d)
}

func (c *Client) StartDeviceLink(ctx context.Context, info DeviceLinkInfo) (*DeviceLinkStartResult, error) {
	return startDeviceLinkWithHTTP(ctx, c.httpClient(), c.Config, info)
}

func (c *Client) PollDeviceLink(ctx context.Context, deviceCode string) (*DeviceLinkPollResult, error) {
	return pollDeviceLinkWithHTTP(ctx, c.httpClient(), c.Config, deviceCode)
}

func (c *Client) RefreshTokens(ctx context.Context) (*TokenSet, error) {
	return refreshTokensWithHTTP(ctx, c.httpClient(), c.Config, c.TokenManager)
}

func (c *Client) GetValidTokens(ctx context.Context) (*TokenSet, error) {
	return getValidTokensWithHTTP(ctx, c.httpClient(), c.Config, c.TokenManager)
}

func (c *Client) GetValidAccessToken(ctx context.Context) (string, error) {
	return getValidAccessTokenWithHTTP(ctx, c.httpClient(), c.Config, c.TokenManager)
}

func (c *Client) FetchCatalog(ctx context.Context) (*CatalogResult, error) {
	return fetchCatalogWithHTTP(ctx, c.httpClient(), c.Config, c.TokenManager)
}

func (c *Client) SendTelemetry(ctx context.Context, request *TelemetryRequest) error {
	return sendTelemetryWithHTTP(ctx, c.httpClient(), c.Config, c.TokenManager, request)
}
