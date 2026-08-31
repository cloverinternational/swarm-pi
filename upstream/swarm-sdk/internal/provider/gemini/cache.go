package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CachedContent represents a cache object for reusing prompts
type CachedContent struct {
	Name              string     `json:"name"`
	Model             string     `json:"model"`
	DisplayName       string     `json:"displayName,omitempty"`
	SystemInstruction *Content   `json:"systemInstruction,omitempty"`
	Contents          []*Content `json:"contents,omitempty"`
	CreateTime        time.Time  `json:"createTime"`
	UpdateTime        time.Time  `json:"updateTime"`
	ExpireTime        time.Time  `json:"expireTime"`
	TTL               string     `json:"ttl,omitempty"` // Duration string like "300s" or "1h"
}

// CreateCachedContentRequest is the request to create a cache
type CreateCachedContentRequest struct {
	Model             string     `json:"model"`
	DisplayName       string     `json:"displayName,omitempty"`
	SystemInstruction *Content   `json:"systemInstruction,omitempty"`
	Contents          []*Content `json:"contents,omitempty"`
	TTL               string     `json:"ttl,omitempty"` // e.g. "300s", "1h", "24h"
}

// CreateCachedContentResponse is the response from creating a cache
type CreateCachedContentResponse struct {
	Name        string    `json:"name"`
	Model       string    `json:"model"`
	DisplayName string    `json:"displayName,omitempty"`
	CreateTime  time.Time `json:"createTime"`
	UpdateTime  time.Time `json:"updateTime"`
	ExpireTime  time.Time `json:"expireTime"`
}

// ListCachedContentsResponse is the response from listing caches
type ListCachedContentsResponse struct {
	CachedContents []*CachedContent `json:"cachedContents"`
	NextPageToken  string           `json:"nextPageToken,omitempty"`
}

// CreateCache creates a new cached content object
func (p *Provider) CreateCache(ctx context.Context, req CreateCachedContentRequest) (*CachedContent, error) {
	// Get access token
	accessToken, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	// Ensure user is set up
	if err := p.ensureSetup(ctx, accessToken); err != nil {
		return nil, fmt.Errorf("failed to setup user: %w", err)
	}

	// Build URL - use Google AI API endpoint for caches
	// https://generativelanguage.googleapis.com/v1beta/cachedContents
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/cachedContents")

	// Marshal request
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	httpReq.Header.Set("User-Agent", "SwarmOS/1.0 GeminiProvider/1.0")

	// Send request
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cache creation failed: %s - %s", resp.Status, string(respBody))
	}

	// Parse response
	var cache CachedContent
	if err := json.Unmarshal(respBody, &cache); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	p.logger.Info(ctx, fmt.Sprintf("[Gemini] Cache created: %s (expires: %s)", cache.Name, cache.ExpireTime))

	return &cache, nil
}

// GetCache retrieves a cached content object by name
func (p *Provider) Cache(ctx context.Context, name string) (*CachedContent, error) {
	accessToken, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s", name)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get cache failed: %s - %s", resp.Status, string(respBody))
	}

	var cache CachedContent
	if err := json.Unmarshal(respBody, &cache); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &cache, nil
}

// ListCaches lists all cached content objects
func (p *Provider) ListCaches(ctx context.Context, pageSize int, pageToken string) (*ListCachedContentsResponse, error) {
	accessToken, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	url := "https://generativelanguage.googleapis.com/v1beta/cachedContents"
	if pageSize > 0 {
		url += fmt.Sprintf("?pageSize=%d", pageSize)
	}
	if pageToken != "" {
		if pageSize > 0 {
			url += "&"
		} else {
			url += "?"
		}
		url += fmt.Sprintf("pageToken=%s", pageToken)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list caches failed: %s - %s", resp.Status, string(respBody))
	}

	var result ListCachedContentsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// DeleteCache deletes a cached content object
func (p *Provider) DeleteCache(ctx context.Context, name string) error {
	accessToken, err := p.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s", name)

	httpReq, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete cache failed: %s - %s", resp.Status, string(respBody))
	}

	p.logger.Info(ctx, fmt.Sprintf("[Gemini] Cache deleted: %s", name))

	return nil
}

// UpdateCache updates a cached content object (mainly for extending TTL)
func (p *Provider) UpdateCache(ctx context.Context, name string, ttl string) (*CachedContent, error) {
	accessToken, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s?updateMask=ttl", name)

	updateReq := map[string]string{"ttl": ttl}
	body, err := json.Marshal(updateReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "PATCH", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update cache failed: %s - %s", resp.Status, string(respBody))
	}

	var cache CachedContent
	if err := json.Unmarshal(respBody, &cache); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	p.logger.Info(ctx, fmt.Sprintf("[Gemini] Cache updated: %s (new expire: %s)", cache.Name, cache.ExpireTime))

	return &cache, nil
}
