package cloud

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tlsClientForServers(t *testing.T, servers ...*httptest.Server) *http.Client {
	t.Helper()
	if len(servers) == 0 || servers[0] == nil {
		t.Fatalf("at least one server is required")
	}

	client := servers[0].Client()
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("expected *http.Transport from httptest client, got %T", client.Transport)
	}
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{}
	}
	if tr.TLSClientConfig.RootCAs == nil {
		tr.TLSClientConfig.RootCAs = x509.NewCertPool()
	}
	for _, s := range servers {
		if s == nil {
			continue
		}
		tr.TLSClientConfig.RootCAs.AddCert(s.Certificate())
	}

	return client
}

func TestClient_StartDeviceLink_Success(t *testing.T) {
	var hit int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hit, 1)

		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != deviceLinkStartPath {
			t.Fatalf("expected path %s, got %s", deviceLinkStartPath, r.URL.Path)
		}

		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", got)
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var req deviceLinkStartRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.DeviceID != "dev-1" || req.DeviceName != "my-mac" || req.App != "tui" || req.AppVersion != "0.7.0" {
			t.Fatalf("unexpected request payload: %+v", req)
		}

		resp := deviceLinkStartResponse{
			DeviceCode:              "device-code",
			UserCode:                "user-code",
			VerificationURI:         "https://example.com/verify",
			VerificationURIComplete: "https://example.com/verify?user_code=user-code",
			ExpiresAt:               time.Now().Add(10 * time.Minute).Unix(),
			IntervalSec:             5,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	cfg := &CloudConfig{APIBaseURL: server.URL}
	client := NewClient(cfg, nil, tlsClientForServers(t, server))

	got, err := client.StartDeviceLink(context.Background(), DeviceLinkInfo{
		DeviceID:   "dev-1",
		DeviceName: "my-mac",
		App:        "tui",
		AppVersion: "0.7.0",
	})
	if err != nil {
		t.Fatalf("StartDeviceLink: %v", err)
	}
	if got == nil || got.DeviceCode != "device-code" || got.UserCode != "user-code" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if atomic.LoadInt32(&hit) != 1 {
		t.Fatalf("expected 1 request, got %d", atomic.LoadInt32(&hit))
	}
}

func TestClient_StartDeviceLink_StrictJSONRejectsUnknownFields(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"device_code":"d","user_code":"u","verification_uri":"https://x","verification_uri_complete":"https://x?u","expires_at":1,"interval_sec":1,"extra":true}`)
	}))
	t.Cleanup(server.Close)

	cfg := &CloudConfig{APIBaseURL: server.URL}
	client := NewClient(cfg, nil, tlsClientForServers(t, server))

	_, err := client.StartDeviceLink(context.Background(), DeviceLinkInfo{DeviceID: "dev-1"})
	if err == nil || err.Error() != "invalid device link response" {
		t.Fatalf("expected strict json error, got %v", err)
	}
}

func TestClient_StartDeviceLink_HTTPSRequired(t *testing.T) {
	cfg := &CloudConfig{APIBaseURL: "http://example.invalid"}
	client := NewClient(cfg, nil, nil)
	_, err := client.StartDeviceLink(context.Background(), DeviceLinkInfo{DeviceID: "dev-1"})
	if err == nil || err.Error() != "device link endpoint must use https" {
		t.Fatalf("expected https enforcement error, got %v", err)
	}
}

func TestParseDeviceLinkPollResponse_Statuses(t *testing.T) {
	now := time.Now().Add(5 * time.Minute).Unix()

	approvedBody := []byte(`{"status":"approved","tokens":{"access_token":"a","refresh_token":"r","id_token":"i","token_type":"Bearer","expires_at":` + itoa(now) + `}}`)
	approved, err := parseDeviceLinkPollResponse(http.StatusOK, approvedBody)
	if err != nil {
		t.Fatalf("approved parse: %v", err)
	}
	if approved.Status != "approved" || approved.Tokens == nil || approved.Tokens.AccessToken != "a" {
		t.Fatalf("unexpected approved result: %+v", approved)
	}

	pendingBody := []byte(`{"status":"pending","expires_at":123,"interval_sec":7}`)
	pending, err := parseDeviceLinkPollResponse(http.StatusAccepted, pendingBody)
	if err != nil {
		t.Fatalf("pending parse: %v", err)
	}
	if pending.Status != "pending" || pending.IntervalSec != 7 || pending.ExpiresAt != 123 {
		t.Fatalf("unexpected pending result: %+v", pending)
	}

	expiredBody := []byte(`{"status":"expired","error":"expired","expires_at":456}`)
	expired, err := parseDeviceLinkPollResponse(http.StatusGone, expiredBody)
	if err != nil {
		t.Fatalf("expired parse: %v", err)
	}
	if expired.Status != "expired" || expired.ExpiresAt != 456 {
		t.Fatalf("unexpected expired result: %+v", expired)
	}
	if !strings.Contains(strings.ToLower(expired.Error), "expired") {
		t.Fatalf("expected expired error message, got %q", expired.Error)
	}

	rateLimitedBody := []byte(`{"status":"rate_limited","interval_sec":9}`)
	limited, err := parseDeviceLinkPollResponse(http.StatusTooManyRequests, rateLimitedBody)
	if err != nil {
		t.Fatalf("rate limited parse: %v", err)
	}
	if limited.Status != "rate_limited" {
		t.Fatalf("unexpected rate limited status: %+v", limited)
	}
	if !strings.Contains(limited.Error, "Wait 9 seconds") {
		t.Fatalf("expected rate limit message to include interval, got %q", limited.Error)
	}
}

func TestClient_PollDeviceLink_Statuses(t *testing.T) {
	now := time.Now().Add(10 * time.Minute).Unix()

	type tc struct {
		name       string
		statusCode int
		body       string
		check      func(*testing.T, *DeviceLinkPollResult, error)
	}
	tests := []tc{
		{
			name:       "pending",
			statusCode: http.StatusAccepted,
			body:       `{"status":"pending","expires_at":123,"interval_sec":7}`,
			check: func(t *testing.T, got *DeviceLinkPollResult, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if got == nil || got.Status != "pending" || got.ExpiresAt != 123 || got.IntervalSec != 7 {
					t.Fatalf("unexpected pending result: %+v", got)
				}
			},
		},
		{
			name:       "approved",
			statusCode: http.StatusOK,
			body:       `{"status":"approved","tokens":{"access_token":"a","refresh_token":"r","id_token":"i","token_type":"Bearer","expires_at":` + itoa(now) + `}}`,
			check: func(t *testing.T, got *DeviceLinkPollResult, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if got == nil || got.Status != "approved" || got.Tokens == nil || got.Tokens.AccessToken != "a" {
					t.Fatalf("unexpected approved result: %+v", got)
				}
			},
		},
		{
			name:       "expired",
			statusCode: http.StatusGone,
			body:       `{"status":"expired","error":"expired","expires_at":456}`,
			check: func(t *testing.T, got *DeviceLinkPollResult, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if got == nil || got.Status != "expired" || got.ExpiresAt != 456 || got.Error == "" {
					t.Fatalf("unexpected expired result: %+v", got)
				}
			},
		},
		{
			name:       "rate_limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{"status":"rate_limited","interval_sec":9}`,
			check: func(t *testing.T, got *DeviceLinkPollResult, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if got == nil || got.Status != "rate_limited" || !strings.Contains(got.Error, "Wait 9 seconds") {
					t.Fatalf("unexpected rate_limited result: %+v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hit int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hit, 1)
				if r.Method != http.MethodPost {
					t.Fatalf("expected POST, got %s", r.Method)
				}
				if r.URL.Path != deviceLinkPollPath {
					t.Fatalf("expected path %s, got %s", deviceLinkPollPath, r.URL.Path)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Fatalf("expected Content-Type application/json, got %q", got)
				}

				bodyBytes, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				var req map[string]string
				if err := json.Unmarshal(bodyBytes, &req); err != nil {
					t.Fatalf("unmarshal request: %v", err)
				}
				if req["device_code"] != "device-code" {
					t.Fatalf("expected device_code device-code, got %q", req["device_code"])
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(server.Close)

			cfg := &CloudConfig{APIBaseURL: server.URL}
			client := NewClient(cfg, nil, tlsClientForServers(t, server))

			got, err := client.PollDeviceLink(context.Background(), "device-code")
			tt.check(t, got, err)

			if atomic.LoadInt32(&hit) != 1 {
				t.Fatalf("expected 1 request, got %d", atomic.LoadInt32(&hit))
			}
		})
	}
}

func TestClient_PollDeviceLink_StrictJSONRejectsUnknownFields(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"pending","interval_sec":1,"expires_at":1,"extra":true}`)
	}))
	t.Cleanup(server.Close)

	cfg := &CloudConfig{APIBaseURL: server.URL}
	client := NewClient(cfg, nil, tlsClientForServers(t, server))

	_, err := client.PollDeviceLink(context.Background(), "device-code")
	if err == nil || err.Error() != "invalid device link response" {
		t.Fatalf("expected strict json error, got %v", err)
	}
}

func TestClient_PollDeviceLink_HTTPSRequired(t *testing.T) {
	cfg := &CloudConfig{APIBaseURL: "http://example.invalid"}
	client := NewClient(cfg, nil, nil)
	_, err := client.PollDeviceLink(context.Background(), "device-code")
	if err == nil || err.Error() != "device link endpoint must use https" {
		t.Fatalf("expected https enforcement error, got %v", err)
	}
}

func TestClient_StartDeviceLink_UsesFallbackOn5xx(t *testing.T) {
	var primaryHits int32
	var fallbackHits int32

	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != deviceLinkStartPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&primaryHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "server error")
	}))
	t.Cleanup(primary.Close)

	fallback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != deviceLinkStartPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&fallbackHits, 1)
		w.Header().Set("Content-Type", "application/json")
		resp := deviceLinkStartResponse{
			DeviceCode:              "device-code",
			UserCode:                "user-code",
			VerificationURI:         "https://example.com/verify",
			VerificationURIComplete: "https://example.com/verify?user_code=user-code",
			ExpiresAt:               time.Now().Add(10 * time.Minute).Unix(),
			IntervalSec:             5,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(fallback.Close)

	httpClient := tlsClientForServers(t, primary, fallback)
	cfg := &CloudConfig{
		APIBaseURL:     primary.URL,
		APIFallbackURL: fallback.URL,
	}
	client := NewClient(cfg, nil, httpClient)

	got, err := client.StartDeviceLink(context.Background(), DeviceLinkInfo{DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("StartDeviceLink: %v", err)
	}
	if got == nil || got.DeviceCode == "" {
		t.Fatalf("expected successful result from fallback, got %+v", got)
	}
	if atomic.LoadInt32(&primaryHits) != 1 {
		t.Fatalf("expected primary hit once, got %d", atomic.LoadInt32(&primaryHits))
	}
	if atomic.LoadInt32(&fallbackHits) != 1 {
		t.Fatalf("expected fallback hit once, got %d", atomic.LoadInt32(&fallbackHits))
	}
}

func TestClient_PollDeviceLink_UsesFallbackOn5xx(t *testing.T) {
	var primaryHits int32
	var fallbackHits int32

	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != deviceLinkPollPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&primaryHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "server error")
	}))
	t.Cleanup(primary.Close)

	fallback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != deviceLinkPollPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&fallbackHits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"pending","expires_at":123,"interval_sec":5}`)
	}))
	t.Cleanup(fallback.Close)

	httpClient := tlsClientForServers(t, primary, fallback)
	cfg := &CloudConfig{
		APIBaseURL:     primary.URL,
		APIFallbackURL: fallback.URL,
	}
	client := NewClient(cfg, nil, httpClient)

	got, err := client.PollDeviceLink(context.Background(), "device-code")
	if err != nil {
		t.Fatalf("PollDeviceLink: %v", err)
	}
	if got == nil || got.Status != "pending" {
		t.Fatalf("expected pending from fallback, got %+v", got)
	}
	if atomic.LoadInt32(&primaryHits) != 1 {
		t.Fatalf("expected primary hit once, got %d", atomic.LoadInt32(&primaryHits))
	}
	if atomic.LoadInt32(&fallbackHits) != 1 {
		t.Fatalf("expected fallback hit once, got %d", atomic.LoadInt32(&fallbackHits))
	}
}

func itoa(v int64) string {
	// Avoid fmt for tiny helper used in test JSON construction.
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [32]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + (v % 10))
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
