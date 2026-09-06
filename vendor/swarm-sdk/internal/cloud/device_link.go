package cloud

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
)

const (
	deviceLinkStartPath        = "/auth/device/start"
	deviceLinkPollPath         = "/auth/device/poll"
	deviceLinkRequestTimeout   = 15 * time.Second
	deviceLinkMaxStartBytes    = 2 * 1024
	deviceLinkMaxPollBytes     = 1024
	deviceLinkMaxResponseBytes = 32 * 1024
	deviceLinkMaxMetadataLen   = 200
)

// DeviceLinkInfo describes device metadata sent during device link start.
type DeviceLinkInfo struct {
	DeviceID   string
	DeviceName string
	App        string
	AppVersion string
}

// DeviceLinkStartResult contains device link start details.
type DeviceLinkStartResult struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresAt               int64
	IntervalSec             int
}

// DeviceLinkPollResult contains device link poll status and tokens.
type DeviceLinkPollResult struct {
	Status      string
	Error       string
	ExpiresAt   int64
	IntervalSec int
	Tokens      *TokenSet
}

type deviceLinkStartRequest struct {
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	App        string `json:"app,omitempty"`
	AppVersion string `json:"app_version,omitempty"`
}

type deviceLinkStartResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresAt               int64  `json:"expires_at"`
	IntervalSec             int    `json:"interval_sec"`
}

type deviceLinkTokenSet struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresAt    int64  `json:"expires_at"`
}

type deviceLinkPollResponse struct {
	Status           string              `json:"status,omitempty"`
	Error            string              `json:"error,omitempty"`
	ErrorDescription string              `json:"error_description,omitempty"`
	ExpiresAt        int64               `json:"expires_at,omitempty"`
	IntervalSec      int                 `json:"interval_sec,omitempty"`
	Tokens           *deviceLinkTokenSet `json:"tokens,omitempty"`
	TokenSet         *deviceLinkTokenSet `json:"token_set,omitempty"`
}

type deviceLinkHTTPResult struct {
	StatusCode int
	Body       []byte
}

// StartDeviceLink starts a new device link session.
func StartDeviceLink(ctx context.Context, config *CloudConfig, info DeviceLinkInfo) (*DeviceLinkStartResult, error) {
	return NewClient(config, nil, nil).StartDeviceLink(ctx, info)
}

// PollDeviceLink polls device link status and returns tokens when approved.
func PollDeviceLink(ctx context.Context, config *CloudConfig, deviceCode string) (*DeviceLinkPollResult, error) {
	return NewClient(config, nil, nil).PollDeviceLink(ctx, deviceCode)
}

func buildDeviceLinkStartRequest(info DeviceLinkInfo) (*deviceLinkStartRequest, error) {
	deviceID, err := trimAndLimit(info.DeviceID, "device_id")
	if err != nil {
		return nil, err
	}
	deviceName, err := trimAndLimit(info.DeviceName, "device_name")
	if err != nil {
		return nil, err
	}
	app, err := trimAndLimit(info.App, "app")
	if err != nil {
		return nil, err
	}
	appVersion, err := trimAndLimit(info.AppVersion, "app_version")
	if err != nil {
		return nil, err
	}

	return &deviceLinkStartRequest{
		DeviceID:   deviceID,
		DeviceName: deviceName,
		App:        app,
		AppVersion: appVersion,
	}, nil
}

func trimAndLimit(value string, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > deviceLinkMaxMetadataLen {
		return "", fmt.Errorf("%s is too long", field)
	}
	return trimmed, nil
}

func startDeviceLinkWithHTTP(ctx context.Context, httpClient *http.Client, config *CloudConfig, info DeviceLinkInfo) (*DeviceLinkStartResult, error) {
	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	request, err := buildDeviceLinkStartRequest(info)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to encode device link request")
	}
	if len(body) > deviceLinkMaxStartBytes {
		return nil, fmt.Errorf("device link payload too large")
	}

	result, err := doDeviceLinkPost(ctx, httpClient, config.APIBaseURL, deviceLinkStartPath, body)
	if err != nil && config.APIFallbackURL != "" {
		result, err = doDeviceLinkPost(ctx, httpClient, config.APIFallbackURL, deviceLinkStartPath, body)
	}
	if err == nil && config.APIFallbackURL != "" && result != nil && result.StatusCode >= http.StatusInternalServerError {
		if fallback, fallbackErr := doDeviceLinkPost(ctx, httpClient, config.APIFallbackURL, deviceLinkStartPath, body); fallbackErr == nil {
			result = fallback
		}
	}
	if err != nil {
		return nil, err
	}

	if result.StatusCode != http.StatusOK {
		return nil, errors.New(deviceLinkErrorMessage(result.StatusCode, result.Body))
	}

	var payload deviceLinkStartResponse
	if err := decodeStrictJSON(result.Body, &payload); err != nil {
		return nil, fmt.Errorf("invalid device link response")
	}

	if strings.TrimSpace(payload.DeviceCode) == "" {
		return nil, fmt.Errorf("device link response missing device code")
	}
	if strings.TrimSpace(payload.UserCode) == "" {
		return nil, fmt.Errorf("device link response missing user code")
	}
	if strings.TrimSpace(payload.VerificationURI) == "" || strings.TrimSpace(payload.VerificationURIComplete) == "" {
		return nil, fmt.Errorf("device link response missing verification url")
	}
	if payload.ExpiresAt <= 0 {
		return nil, fmt.Errorf("device link response missing expiration")
	}
	if payload.IntervalSec <= 0 {
		return nil, fmt.Errorf("device link response missing poll interval")
	}

	return &DeviceLinkStartResult{
		DeviceCode:              payload.DeviceCode,
		UserCode:                payload.UserCode,
		VerificationURI:         payload.VerificationURI,
		VerificationURIComplete: payload.VerificationURIComplete,
		ExpiresAt:               payload.ExpiresAt,
		IntervalSec:             payload.IntervalSec,
	}, nil
}

func pollDeviceLinkWithHTTP(ctx context.Context, httpClient *http.Client, config *CloudConfig, deviceCode string) (*DeviceLinkPollResult, error) {
	if config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	deviceCode = strings.TrimSpace(deviceCode)
	if deviceCode == "" {
		return nil, fmt.Errorf("device code is required")
	}

	body, err := json.Marshal(map[string]string{"device_code": deviceCode})
	if err != nil {
		return nil, fmt.Errorf("failed to encode device link poll request")
	}
	if len(body) > deviceLinkMaxPollBytes {
		return nil, fmt.Errorf("device link poll payload too large")
	}

	result, err := doDeviceLinkPost(ctx, httpClient, config.APIBaseURL, deviceLinkPollPath, body)
	if err != nil && config.APIFallbackURL != "" {
		result, err = doDeviceLinkPost(ctx, httpClient, config.APIFallbackURL, deviceLinkPollPath, body)
	}
	if err == nil && config.APIFallbackURL != "" && result != nil && result.StatusCode >= http.StatusInternalServerError {
		if fallback, fallbackErr := doDeviceLinkPost(ctx, httpClient, config.APIFallbackURL, deviceLinkPollPath, body); fallbackErr == nil {
			result = fallback
		}
	}
	if err != nil {
		return nil, err
	}

	return parseDeviceLinkPollResponse(result.StatusCode, result.Body)
}

func doDeviceLinkPost(ctx context.Context, httpClient *http.Client, baseURL string, path string, body []byte) (*deviceLinkHTTPResult, error) {
	httpClient = ensureHTTPClient(httpClient)

	endpoint, err := buildAPIEndpoint(baseURL, path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.ToLower(endpoint), "https://") {
		return nil, fmt.Errorf("device link endpoint must use https")
	}

	reqCtx, cancel := withTimeout(ctx, deviceLinkRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build device link request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device link request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := readLimitedBody(resp.Body, deviceLinkMaxResponseBytes)
	if err != nil {
		return nil, err
	}

	return &deviceLinkHTTPResult{StatusCode: resp.StatusCode, Body: bodyBytes}, nil
}

func readLimitedBody(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("invalid response size limit")
	}
	limited := io.LimitReader(reader, limit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read device link response")
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("device link response too large")
	}
	return body, nil
}

func parseDeviceLinkPollResponse(statusCode int, body []byte) (*DeviceLinkPollResult, error) {
	if len(body) == 0 {
		return &DeviceLinkPollResult{Status: "error", Error: deviceLinkErrorMessage(statusCode, nil)}, nil
	}

	var payload deviceLinkPollResponse
	if err := decodeStrictJSON(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid device link response")
	}

	tokensPayload := payload.Tokens
	if tokensPayload == nil {
		tokensPayload = payload.TokenSet
	}

	switch statusCode {
	case http.StatusOK:
		if payload.Status == "" {
			payload.Status = "approved"
		}
		if payload.Status != "approved" {
			return &DeviceLinkPollResult{
				Status: "error",
				Error:  deviceLinkErrorMessage(statusCode, body),
			}, nil
		}
		if tokensPayload == nil {
			return &DeviceLinkPollResult{
				Status: "error",
				Error:  "device login approved but tokens were missing",
			}, nil
		}
		tokens, err := buildDeviceLinkTokenSet(tokensPayload)
		if err != nil {
			return &DeviceLinkPollResult{Status: "error", Error: err.Error()}, nil
		}
		return &DeviceLinkPollResult{Status: "approved", Tokens: tokens}, nil
	case http.StatusAccepted:
		status := payload.Status
		if status == "" {
			status = "pending"
		}
		if status != "pending" {
			return &DeviceLinkPollResult{
				Status: "error",
				Error:  deviceLinkErrorMessage(statusCode, body),
			}, nil
		}
		return &DeviceLinkPollResult{
			Status:      "pending",
			ExpiresAt:   payload.ExpiresAt,
			IntervalSec: payload.IntervalSec,
		}, nil
	case http.StatusGone:
		status := payload.Status
		if status == "" {
			status = "expired"
		}
		return &DeviceLinkPollResult{
			Status:    status,
			Error:     deviceLinkErrorMessage(statusCode, body),
			ExpiresAt: payload.ExpiresAt,
		}, nil
	case http.StatusTooManyRequests:
		status := payload.Status
		if status == "" {
			status = "rate_limited"
		}
		return &DeviceLinkPollResult{
			Status:      status,
			Error:       deviceLinkRateLimitMessage(payload.IntervalSec),
			ExpiresAt:   payload.ExpiresAt,
			IntervalSec: payload.IntervalSec,
		}, nil
	default:
		return &DeviceLinkPollResult{
			Status: "error",
			Error:  deviceLinkErrorMessage(statusCode, body),
		}, nil
	}
}

func buildDeviceLinkTokenSet(tokens *deviceLinkTokenSet) (*TokenSet, error) {
	if tokens == nil {
		return nil, fmt.Errorf("token set is required")
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return nil, fmt.Errorf("access token missing in device link response")
	}
	if strings.TrimSpace(tokens.RefreshToken) == "" {
		return nil, fmt.Errorf("refresh token missing in device link response")
	}
	if strings.TrimSpace(tokens.IDToken) == "" {
		return nil, fmt.Errorf("id token missing in device link response")
	}
	if strings.TrimSpace(tokens.TokenType) == "" {
		return nil, fmt.Errorf("token type missing in device link response")
	}
	if tokens.ExpiresAt <= 0 {
		return nil, fmt.Errorf("token expiration missing in device link response")
	}

	return &TokenSet{
		AccessToken:  tokens.AccessToken,
		IDToken:      tokens.IDToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    tokens.TokenType,
		ExpiresAt:    tokens.ExpiresAt,
	}, nil
}

func deviceLinkErrorMessage(statusCode int, body []byte) string {
	var payload deviceLinkPollResponse
	if len(body) > 0 && decodeStrictJSON(body, &payload) == nil {
		message := mapDeviceLinkError(payload.Error, payload.ErrorDescription, statusCode)
		if message != "" {
			return message
		}
	}

	return mapDeviceLinkError("", "", statusCode)
}

func mapDeviceLinkError(code string, description string, statusCode int) string {
	normalized := strings.ToLower(strings.TrimSpace(code))
	switch normalized {
	case "invalid_request":
		return "Device login request was rejected. Please try again."
	case "invalid_device_code":
		return "Device login code is invalid. Restart device login."
	case "expired":
		return "Device login expired. Start device login again."
	case "rate_limited":
		return "Device login polling is too frequent. Wait a few seconds and retry."
	case "denied":
		return "Device login was denied."
	case "canceled":
		return "Device login was canceled."
	case "already_consumed":
		return "Device login already completed. Start device login again."
	case "already_confirmed":
		return "Device login already confirmed. Start device login again."
	case "not_authorized":
		return "Device login unauthorized. Sign in and try again."
	}

	if strings.TrimSpace(description) != "" {
		return description
	}
	if strings.TrimSpace(code) != "" {
		return code
	}

	switch statusCode {
	case http.StatusBadRequest:
		return "Device login request was rejected."
	case http.StatusUnauthorized:
		return "Device login unauthorized."
	case http.StatusForbidden:
		return "Device login was denied."
	case http.StatusNotFound:
		return "Device login code not found."
	case http.StatusGone:
		return "Device login expired."
	case http.StatusTooManyRequests:
		return "Device login polling is too frequent."
	default:
		return "Device login failed. Please try again."
	}
}

func deviceLinkRateLimitMessage(intervalSec int) string {
	if intervalSec > 0 {
		return fmt.Sprintf("Device login polling is too frequent. Wait %d seconds and retry.", intervalSec)
	}
	return "Device login polling is too frequent. Wait a few seconds and retry."
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected data")
	}
	return nil
}
