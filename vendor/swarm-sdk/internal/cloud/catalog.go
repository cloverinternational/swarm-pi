package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	catalogCacheFileName string = "cloud_catalog.json"
)

var defaultCatalogTTL time.Duration = 10 * time.Minute

const catalogRequestTimeout time.Duration = 15 * time.Second

// CatalogCache stores cached catalog data and metadata.
type CatalogCache struct {
	ETag      string          `json:"etag"`
	UpdatedAt int64           `json:"updated_at"`
	Body      json.RawMessage `json:"body"`
}

// IsFresh returns true if cache is within TTL.
func (c *CatalogCache) IsFresh(ttl time.Duration) bool {
	if c == nil || c.UpdatedAt == 0 {
		return false
	}
	var updated time.Time = time.Unix(c.UpdatedAt, 0)
	var now time.Time = time.Now()
	return now.Sub(updated) < ttl
}

// CatalogCacheManager manages catalog cache storage.
type CatalogCacheManager struct {
	configDir string
}

// NewCatalogCacheManager creates a new cache manager.
func NewCatalogCacheManager() (*CatalogCacheManager, error) {
	var tokenManager *TokenManager
	var err error
	tokenManager, err = NewTokenManager()
	if err != nil {
		return nil, err
	}

	return &CatalogCacheManager{configDir: tokenManager.configDir}, nil
}

// GetCachePath returns the cache file path.
func (cm *CatalogCacheManager) GetCachePath() string {
	return filepath.Join(cm.configDir, catalogCacheFileName)
}

// LoadCache loads catalog cache if present.
func (cm *CatalogCacheManager) LoadCache() (*CatalogCache, error) {
	var path string = cm.GetCachePath()

	var statErr error
	_, statErr = os.Stat(path)
	if os.IsNotExist(statErr) {
		return nil, nil
	}
	if statErr != nil {
		return nil, fmt.Errorf("failed to stat catalog cache: %w", statErr)
	}

	var data []byte
	var err error
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read catalog cache: %w", err)
	}

	var cache CatalogCache
	if err = json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("failed to parse catalog cache: %w", err)
	}

	return &cache, nil
}

// SaveCache persists catalog cache to disk.
func (cm *CatalogCacheManager) SaveCache(cache *CatalogCache) error {
	if cache == nil {
		return fmt.Errorf("cannot save nil cache")
	}

	var data []byte
	var err error
	data, err = json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal catalog cache: %w", err)
	}

	var path string = cm.GetCachePath()
	if err = os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write catalog cache: %w", err)
	}

	return nil
}

// CatalogResult describes a catalog fetch outcome.
type CatalogResult struct {
	FromCache bool
	CachePath string
	Bytes     int
	SourceURL string
}

type catalogHTTPResult struct {
	Body        []byte
	ETag        string
	StatusCode  int
	NotModified bool
}

// FetchCatalog fetches the cloud catalog, using cache and refresh when needed.
func FetchCatalog(ctx context.Context, config *CloudConfig, tokenManager *TokenManager) (*CatalogResult, error) {
	return NewClient(config, tokenManager, nil).FetchCatalog(ctx)
}

func fetchCatalogWithHTTP(ctx context.Context, httpClient *http.Client, config *CloudConfig, tokenManager *TokenManager) (*CatalogResult, error) {
	httpClient = ensureHTTPClient(httpClient)

	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	if tokenManager == nil {
		return nil, fmt.Errorf("token manager is required")
	}

	var cacheManager *CatalogCacheManager
	var err error
	cacheManager, err = NewCatalogCacheManager()
	if err != nil {
		return nil, err
	}

	var cache *CatalogCache
	cache, err = cacheManager.LoadCache()
	if err != nil {
		return nil, err
	}

	if cache != nil && cache.IsFresh(defaultCatalogTTL) {
		return &CatalogResult{
			FromCache: true,
			CachePath: cacheManager.GetCachePath(),
			Bytes:     len(cache.Body),
			SourceURL: config.APIBaseURL,
		}, nil
	}

	token, err := getValidAccessTokenWithHTTP(ctx, httpClient, config, tokenManager)
	if err != nil {
		return nil, err
	}

	var result *catalogHTTPResult
	var sourceURL string = config.APIBaseURL
	result, err = fetchCatalogOnce(ctx, httpClient, config.APIBaseURL, token, cache)
	if result != nil && result.StatusCode == http.StatusUnauthorized {
		_, _ = refreshTokensWithHTTP(ctx, httpClient, config, tokenManager)
		token, err = getValidAccessTokenWithHTTP(ctx, httpClient, config, tokenManager)
		if err == nil {
			result, err = fetchCatalogOnce(ctx, httpClient, config.APIBaseURL, token, cache)
		}
	}

	if err != nil && config.APIFallbackURL != "" {
		sourceURL = config.APIFallbackURL
		result, err = fetchCatalogOnce(ctx, httpClient, config.APIFallbackURL, token, cache)
	}
	if err != nil {
		return nil, err
	}

	var cachePath string = cacheManager.GetCachePath()

	if result.NotModified {
		if cache == nil {
			return nil, fmt.Errorf("catalog cache missing for 304 response")
		}
		cache.UpdatedAt = time.Now().Unix()
		if err = cacheManager.SaveCache(cache); err != nil {
			return nil, err
		}
		return &CatalogResult{
			FromCache: true,
			CachePath: cachePath,
			Bytes:     len(cache.Body),
			SourceURL: sourceURL,
		}, nil
	}

	var updated CatalogCache
	updated.ETag = result.ETag
	updated.UpdatedAt = time.Now().Unix()
	updated.Body = json.RawMessage(result.Body)

	if err = cacheManager.SaveCache(&updated); err != nil {
		return nil, err
	}

	return &CatalogResult{
		FromCache: false,
		CachePath: cachePath,
		Bytes:     len(result.Body),
		SourceURL: sourceURL,
	}, nil
}

func fetchCatalogOnce(ctx context.Context, httpClient *http.Client, baseURL string, token string, cache *CatalogCache) (*catalogHTTPResult, error) {
	httpClient = ensureHTTPClient(httpClient)

	var empty catalogHTTPResult
	var endpoint string
	var err error
	endpoint, err = buildAPIEndpoint(baseURL, "/catalog")
	if err != nil {
		return &empty, err
	}

	var req *http.Request
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &empty, fmt.Errorf("failed to build catalog request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cache != nil && cache.ETag != "" {
		req.Header.Set("If-None-Match", cache.ETag)
	}

	reqCtx, cancel := withTimeout(ctx, catalogRequestTimeout)
	defer cancel()

	req = req.WithContext(reqCtx)

	resp, err := httpClient.Do(req)
	if err != nil {
		return &empty, fmt.Errorf("catalog request failed: %w", err)
	}
	defer resp.Body.Close()

	var result catalogHTTPResult
	result.StatusCode = resp.StatusCode
	result.ETag = resp.Header.Get("ETag")

	if resp.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return &result, nil
	}

	var body []byte
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return &empty, fmt.Errorf("failed to read catalog response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &result, fmt.Errorf("catalog request failed: %s", strings.TrimSpace(string(body)))
	}

	result.Body = body
	return &result, nil
}

func buildAPIEndpoint(base string, path string) (string, error) {
	var baseURL *url.URL
	var err error
	baseURL, err = url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid api base url: %w", err)
	}

	var refURL *url.URL
	refURL, err = url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("invalid api path: %w", err)
	}

	var resolved *url.URL = baseURL.ResolveReference(refURL)
	return resolved.String(), nil
}
