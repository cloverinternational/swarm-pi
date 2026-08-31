package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	collectURL  string
	artifactURL string
	authToken   string
	httpClient  *http.Client
}

func NewClient(cfg Config) *Client {
	collectURL := normalizeCollectURL(cfg.CollectorURL)
	return &Client{
		collectURL:  collectURL,
		artifactURL: normalizeArtifactURL(collectURL),
		authToken:   cfg.AuthToken,
		httpClient:  &http.Client{Timeout: cfg.RequestTimeout},
	}
}

func normalizeCollectURL(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	if strings.HasSuffix(url, "/v1/collect") {
		return url
	}
	return strings.TrimRight(url, "/") + "/v1/collect"
}

func normalizeArtifactURL(collectURL string) string {
	collectURL = strings.TrimSpace(collectURL)
	if collectURL == "" {
		return ""
	}
	if before, ok := strings.CutSuffix(collectURL, "/v1/collect"); ok {
		return before + "/v1/artifacts"
	}
	return strings.TrimRight(collectURL, "/") + "/v1/artifacts"
}

func (c *Client) Send(ctx context.Context, batch BatchRequest) error {
	return c.sendJSON(ctx, c.collectURL, batch, "batch")
}

func (c *Client) SendArtifacts(ctx context.Context, batch ArtifactBatchRequest) error {
	return c.sendJSON(ctx, c.artifactURL, batch, "artifact batch")
}

func (c *Client) sendJSON(ctx context.Context, url string, payload any, label string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", label, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.authToken) != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("collector returned %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}
