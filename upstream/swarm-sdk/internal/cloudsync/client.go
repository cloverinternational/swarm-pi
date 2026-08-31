package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
)

var ErrNotModified = errors.New("not modified")

const requestTimeout = 20 * time.Second

const (
	payloadFormatJSON      = "json"
	payloadFormatEncrypted = "encrypted"
)

type apiError struct {
	StatusCode int
	Code       string
	Version    int64
	ETag       string
}

type errorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Version int64  `json:"version,omitempty"`
	ETag    string `json:"etag,omitempty"`
}

type signatureInfo struct {
	KeyID     string `json:"key_id"`
	Alg       string `json:"alg"`
	Signature string `json:"signature"`
}

type settingsResponse struct {
	Status        string          `json:"status"`
	PayloadFormat string          `json:"payload_format"`
	Payload       json.RawMessage `json:"payload"`
	Version       int64           `json:"version"`
	ETag          string          `json:"etag"`
	UpdatedAt     int64           `json:"updated_at"`
	UpdatedBy     string          `json:"updated_by"`
	DeviceID      string          `json:"device_id"`
	Signature     *signatureInfo  `json:"signature,omitempty"`
}

type conversationListResponse struct {
	Status    string                 `json:"status"`
	Items     []conversationListItem `json:"items"`
	NextSince int64                  `json:"next_since"`
}

type conversationListItem struct {
	ID        string `json:"id"`
	ETag      string `json:"etag"`
	UpdatedAt int64  `json:"updated_at"`
	DeletedAt int64  `json:"deleted_at"`
}

type conversationResponse struct {
	Status        string          `json:"status"`
	PayloadFormat string          `json:"payload_format"`
	Payload       json.RawMessage `json:"payload"`
	Encryption    *EncryptionInfo `json:"encryption,omitempty"`
	ETag          string          `json:"etag"`
	UpdatedAt     int64           `json:"updated_at"`
	DeletedAt     int64           `json:"deleted_at"`
}

type EncryptionInfo struct {
	Alg     string `json:"alg"`
	Nonce   string `json:"nonce"`
	Version int    `json:"version"`
}

type keysResponse struct {
	Status string          `json:"status"`
	Keys   []publicKeyInfo `json:"keys"`
}

type publicKeyInfo struct {
	KeyID        string `json:"key_id"`
	Alg          string `json:"alg"`
	PublicKeyPEM string `json:"public_key_pem"`
}

type APIClient struct {
	config       *cloud.CloudConfig
	tokenManager *cloud.TokenManager
	httpClient   *http.Client
}

func NewAPIClient(config *cloud.CloudConfig, tokenManager *cloud.TokenManager, httpClient *http.Client) *APIClient {
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}

	return &APIClient{
		config:       config,
		tokenManager: tokenManager,
		httpClient:   client,
	}
}

func (c *APIClient) GetSettingsPersonal(ctx context.Context, etag string) (*settingsResponse, error) {
	resp, body, err := c.doRequest(ctx, http.MethodGet, "/sync/settings/personal", nil, map[string]string{
		"If-None-Match": etag,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return nil, ErrNotModified
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload settingsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) PutSettingsPersonal(ctx context.Context, request any) (*settingsResponse, error) {
	resp, body, err := c.doRequest(ctx, http.MethodPut, "/sync/settings/personal", request, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload settingsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) GetSettingsTeam(ctx context.Context, teamID string, etag string) (*settingsResponse, error) {
	path := fmt.Sprintf("/sync/settings/team/%s", teamID)
	resp, body, err := c.doRequest(ctx, http.MethodGet, path, nil, map[string]string{
		"If-None-Match": etag,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return nil, ErrNotModified
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload settingsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) PutSettingsTeam(ctx context.Context, teamID string, request any) (*settingsResponse, error) {
	path := fmt.Sprintf("/sync/settings/team/%s", teamID)
	resp, body, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload settingsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) GetProfiles(ctx context.Context, etag string) (*settingsResponse, error) {
	resp, body, err := c.doRequest(ctx, http.MethodGet, "/sync/profiles", nil, map[string]string{
		"If-None-Match": etag,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return nil, ErrNotModified
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload settingsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) PutProfiles(ctx context.Context, request any) (*settingsResponse, error) {
	resp, body, err := c.doRequest(ctx, http.MethodPut, "/sync/profiles", request, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload settingsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) ListConversations(ctx context.Context, since int64, limit int) (*conversationListResponse, error) {
	path := fmt.Sprintf("/sync/conversations?since=%d&limit=%d", since, limit)
	resp, body, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload conversationListResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) GetConversation(ctx context.Context, id string) (*conversationResponse, error) {
	path := fmt.Sprintf("/sync/conversations/%s", id)
	resp, body, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload conversationResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) PutConversation(ctx context.Context, id string, request any) (*conversationResponse, error) {
	path := fmt.Sprintf("/sync/conversations/%s", id)
	resp, body, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload conversationResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) DeleteConversation(ctx context.Context, id string) (*conversationResponse, error) {
	path := fmt.Sprintf("/sync/conversations/%s", id)
	resp, body, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload conversationResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) GetKeys(ctx context.Context) (*keysResponse, error) {
	resp, body, err := c.doRequest(ctx, http.MethodGet, "/sync/keys", nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}

	var payload keysResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *APIClient) doRequest(ctx context.Context, method string, path string, payload any, headers map[string]string) (*http.Response, []byte, error) {
	body, err := encodeRequestBody(payload)
	if err != nil {
		return nil, nil, err
	}

	cloudClient := cloud.NewClient(c.config, c.tokenManager, c.httpClient)

	token, err := cloudClient.GetValidAccessToken(ctx)
	if err != nil {
		return nil, nil, err
	}

	resp, bodyBytes, err := c.requestWithToken(ctx, c.config.APIBaseURL, method, path, token, body, headers)
	if err != nil || resp.StatusCode >= http.StatusInternalServerError {
		if c.config.APIFallbackURL != "" {
			resp, bodyBytes, err = c.requestWithToken(ctx, c.config.APIFallbackURL, method, path, token, body, headers)
		}
	}
	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = cloudClient.RefreshTokens(ctx)
		token, err = cloudClient.GetValidAccessToken(ctx)
		if err != nil {
			return resp, bodyBytes, err
		}
		resp, bodyBytes, err = c.requestWithToken(ctx, c.config.APIBaseURL, method, path, token, body, headers)
		if err != nil {
			return resp, bodyBytes, err
		}
	}

	return resp, bodyBytes, nil
}

func (c *APIClient) requestWithToken(ctx context.Context, baseURL string, method string, path string, token string, body []byte, headers map[string]string) (*http.Response, []byte, error) {
	url := strings.TrimRight(baseURL, "/") + path
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if value == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, err
	}

	return resp, respBody, nil
}

func encodeRequestBody(payload any) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	return json.Marshal(payload)
}

func parseAPIError(statusCode int, body []byte) error {
	var payload errorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return &apiError{StatusCode: statusCode, Code: "internal_error"}
	}

	return &apiError{
		StatusCode: statusCode,
		Code:       payload.Message,
		Version:    payload.Version,
		ETag:       payload.ETag,
	}
}

func (e *apiError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("cloud sync error: %s", e.Code)
	}
	return fmt.Sprintf("cloud sync error: status %d", e.StatusCode)
}
