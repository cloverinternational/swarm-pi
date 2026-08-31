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
	telemetryEndpointPath   = "/telemetry"
	defaultTelemetryTimeout = 10 * time.Second
	maxTelemetryEvents      = 25
	maxTelemetryBytes       = 16 * 1024
)

var telemetryEventAllowlist = map[string]struct{}{
	"sync_success":  {},
	"sync_failure":  {},
	"login_success": {},
	"login_failure": {},
	"catalog_fetch": {},
}

var telemetryAppAllowlist = map[string]struct{}{
	"ide": {},
	"tui": {},
}

var telemetryOSAllowlist = map[string]struct{}{
	"mac":     {},
	"linux":   {},
	"windows": {},
}

var telemetryErrorAllowlist = map[string]struct{}{
	"network":    {},
	"auth":       {},
	"validation": {},
	"unknown":    {},
}

// TelemetryEvent describes an anonymized telemetry event.
type TelemetryEvent struct {
	Name       string `json:"name"`
	Ts         int64  `json:"ts"`
	App        string `json:"app"`
	AppVersion string `json:"app_version"`
	OS         string `json:"os"`
	DeviceID   string `json:"device_id"`
	DurationMS int64  `json:"duration_ms"`
	ErrorCode  string `json:"error_code"`
}

// TelemetryRequest is the payload for /telemetry.
type TelemetryRequest struct {
	Events []TelemetryEvent `json:"events"`
}

type telemetryHTTPResult struct {
	StatusCode int
	Body       []byte
}

// ValidateTelemetryRequest enforces the telemetry schema client-side.
func ValidateTelemetryRequest(request *TelemetryRequest) error {
	if request == nil || len(request.Events) == 0 {
		return errors.New("telemetry events required")
	}
	if len(request.Events) > maxTelemetryEvents {
		return errors.New("telemetry events limit exceeded")
	}

	for _, event := range request.Events {
		if _, ok := telemetryEventAllowlist[event.Name]; !ok {
			return errors.New("invalid telemetry event name")
		}
		if event.Ts <= 0 {
			return errors.New("invalid telemetry timestamp")
		}
		if _, ok := telemetryAppAllowlist[event.App]; !ok {
			return errors.New("invalid telemetry app")
		}
		if strings.TrimSpace(event.AppVersion) == "" {
			return errors.New("invalid telemetry app version")
		}
		if _, ok := telemetryOSAllowlist[event.OS]; !ok {
			return errors.New("invalid telemetry os")
		}
		if strings.TrimSpace(event.DeviceID) == "" {
			return errors.New("invalid telemetry device id")
		}
		if event.DurationMS < 0 {
			return errors.New("invalid telemetry duration")
		}
		if _, ok := telemetryErrorAllowlist[event.ErrorCode]; !ok {
			return errors.New("invalid telemetry error code")
		}
	}

	return nil
}

// SendTelemetry sends telemetry events to the Swarm Cloud API.
func SendTelemetry(ctx context.Context, config *CloudConfig, tokenManager *TokenManager, request *TelemetryRequest) error {
	return NewClient(config, tokenManager, nil).SendTelemetry(ctx, request)
}

func sendTelemetryWithHTTP(ctx context.Context, httpClient *http.Client, config *CloudConfig, tokenManager *TokenManager, request *TelemetryRequest) error {
	httpClient = ensureHTTPClient(httpClient)

	if config == nil {
		return fmt.Errorf("cloud config is required")
	}
	if tokenManager == nil {
		return fmt.Errorf("token manager is required")
	}
	if err := ValidateTelemetryRequest(request); err != nil {
		return err
	}

	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to encode telemetry payload")
	}
	if len(body) > maxTelemetryBytes {
		return fmt.Errorf("telemetry payload too large")
	}

	token, err := getValidAccessTokenWithHTTP(ctx, httpClient, config, tokenManager)
	if err != nil {
		return err
	}

	result, err := sendTelemetryOnce(ctx, httpClient, config.APIBaseURL, token, body)
	if result != nil && result.StatusCode == http.StatusUnauthorized {
		_, _ = refreshTokensWithHTTP(ctx, httpClient, config, tokenManager)
		token, err = getValidAccessTokenWithHTTP(ctx, httpClient, config, tokenManager)
		if err == nil {
			result, err = sendTelemetryOnce(ctx, httpClient, config.APIBaseURL, token, body)
		}
	}

	if err != nil && config.APIFallbackURL != "" {
		result, err = sendTelemetryOnce(ctx, httpClient, config.APIFallbackURL, token, body)
		if result != nil && result.StatusCode == http.StatusUnauthorized {
			_, _ = refreshTokensWithHTTP(ctx, httpClient, config, tokenManager)
			token, err = getValidAccessTokenWithHTTP(ctx, httpClient, config, tokenManager)
			if err == nil {
				result, err = sendTelemetryOnce(ctx, httpClient, config.APIFallbackURL, token, body)
			}
		}
	}

	return err
}

func sendTelemetryOnce(ctx context.Context, httpClient *http.Client, baseURL string, token string, body []byte) (*telemetryHTTPResult, error) {
	httpClient = ensureHTTPClient(httpClient)

	endpoint, err := buildAPIEndpoint(baseURL, telemetryEndpointPath)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.ToLower(endpoint), "https://") {
		return nil, fmt.Errorf("telemetry endpoint must use https")
	}

	reqCtx, cancel := withTimeout(ctx, defaultTelemetryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telemetry request failed: %w", err)
	}
	defer resp.Body.Close()

	result := &telemetryHTTPResult{StatusCode: resp.StatusCode}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("telemetry response read failed: %w", err)
	}
	result.Body = bodyBytes

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(bodyBytes))
		if message == "" {
			message = "telemetry request failed"
		}
		return result, errors.New(message)
	}

	return result, nil
}
