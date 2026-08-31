package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateTelemetryRequest_AllowlistAndLimits(t *testing.T) {
	valid := &TelemetryRequest{
		Events: []TelemetryEvent{
			{
				Name:       "sync_success",
				Ts:         time.Now().Unix(),
				App:        "tui",
				AppVersion: "0.7.0",
				OS:         "mac",
				DeviceID:   "dev-1",
				DurationMS: 10,
				ErrorCode:  "unknown",
			},
		},
	}
	if err := ValidateTelemetryRequest(valid); err != nil {
		t.Fatalf("expected valid telemetry request, got %v", err)
	}

	invalidName := *valid
	invalidName.Events = append([]TelemetryEvent(nil), valid.Events...)
	invalidName.Events[0].Name = "not-allowed"
	if err := ValidateTelemetryRequest(&invalidName); err == nil {
		t.Fatalf("expected invalid event name to fail validation")
	}

	tooMany := &TelemetryRequest{}
	for range maxTelemetryEvents + 1 {
		tooMany.Events = append(tooMany.Events, valid.Events[0])
	}
	if err := ValidateTelemetryRequest(tooMany); err == nil {
		t.Fatalf("expected too many events to fail validation")
	}
}

func TestClient_SendTelemetry_HTTPSRequired(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "access",
		IDToken:      "id",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &CloudConfig{
		APIBaseURL:     "http://127.0.0.1:1234",
		HostedUIURL:    "http://127.0.0.1:1234",
		PublicClientID: "client",
		APIFallbackURL: "",
	}
	client := NewClient(cfg, tm, nil)

	req := &TelemetryRequest{
		Events: []TelemetryEvent{{
			Name:       "sync_success",
			Ts:         time.Now().Unix(),
			App:        "tui",
			AppVersion: "0.7.0",
			OS:         "mac",
			DeviceID:   "dev-1",
			DurationMS: 0,
			ErrorCode:  "unknown",
		}},
	}
	err = client.SendTelemetry(context.Background(), req)
	if err == nil || err.Error() != "telemetry endpoint must use https" {
		t.Fatalf("expected https enforcement error, got %v", err)
	}
}

func TestClient_SendTelemetry_401RefreshesAndRetries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var telemetryHits int32
	var tokenHits int32

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case telemetryEndpointPath:
			atomic.AddInt32(&telemetryHits, 1)
			auth := r.Header.Get("Authorization")
			if atomic.LoadInt32(&telemetryHits) == 1 {
				if auth != "Bearer old-access" {
					t.Fatalf("expected old token on first attempt, got %q", auth)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("unauthorized"))
				return
			}
			if auth != "Bearer new-access" {
				t.Fatalf("expected new token after refresh, got %q", auth)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/oauth2/token":
			atomic.AddInt32(&tokenHits, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "new-access",
				"id_token":     "new-id",
				"token_type":   "Bearer",
				"scope":        "openid",
				"expires_in":   int64(3600),
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "old-access",
		IDToken:      "old-id",
		RefreshToken: "refresh-abc",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &CloudConfig{
		APIBaseURL:     server.URL,
		HostedUIURL:    server.URL,
		PublicClientID: "client-123",
		APIFallbackURL: "",
	}
	client := NewClient(cfg, tm, server.Client())

	req := &TelemetryRequest{
		Events: []TelemetryEvent{{
			Name:       "sync_success",
			Ts:         time.Now().Unix(),
			App:        "tui",
			AppVersion: "0.7.0",
			OS:         "mac",
			DeviceID:   "dev-1",
			DurationMS: 0,
			ErrorCode:  "unknown",
		}},
	}

	if err := client.SendTelemetry(context.Background(), req); err != nil {
		t.Fatalf("SendTelemetry: %v", err)
	}
	if atomic.LoadInt32(&telemetryHits) != 2 {
		t.Fatalf("expected 2 telemetry attempts, got %d", atomic.LoadInt32(&telemetryHits))
	}
	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Fatalf("expected 1 token refresh, got %d", atomic.LoadInt32(&tokenHits))
	}
}

func TestClient_SendTelemetry_UsesFallbackOn5xx(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var primaryHits int32
	var fallbackHits int32

	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != telemetryEndpointPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&primaryHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	t.Cleanup(primary.Close)

	fallback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != telemetryEndpointPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&fallbackHits, 1)
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(fallback.Close)

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "access",
		IDToken:      "id",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &CloudConfig{
		APIBaseURL:     primary.URL,
		APIFallbackURL: fallback.URL,
		HostedUIURL:    primary.URL,
		PublicClientID: "client",
	}
	httpClient := tlsClientForServers(t, primary, fallback)
	client := NewClient(cfg, tm, httpClient)

	req := &TelemetryRequest{
		Events: []TelemetryEvent{{
			Name:       "sync_success",
			Ts:         time.Now().Unix(),
			App:        "tui",
			AppVersion: "0.7.0",
			OS:         "mac",
			DeviceID:   "dev-1",
			DurationMS: 0,
			ErrorCode:  "unknown",
		}},
	}

	if err := client.SendTelemetry(context.Background(), req); err != nil {
		t.Fatalf("SendTelemetry: %v", err)
	}
	if atomic.LoadInt32(&primaryHits) != 1 {
		t.Fatalf("expected 1 primary attempt, got %d", atomic.LoadInt32(&primaryHits))
	}
	if atomic.LoadInt32(&fallbackHits) != 1 {
		t.Fatalf("expected 1 fallback attempt, got %d", atomic.LoadInt32(&fallbackHits))
	}
}
