package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRefreshAccessTokenSendsJSONWithoutScope pins the refresh request to the
// codex-rs contract: Content-Type application/json, body containing exactly
// client_id/grant_type/refresh_token, and no scope parameter.
func TestRefreshAccessTokenSendsJSONWithoutScope(t *testing.T) {
	var gotContentType string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id_token":"","access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer server.Close()

	oldURL := oauthTokenURL
	oauthTokenURL = server.URL
	defer func() { oauthTokenURL = oldURL }()

	refreshed, err := RefreshAccessToken(context.Background(), &OAuthToken{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
	})
	if err != nil {
		t.Fatalf("RefreshAccessToken: %v", err)
	}

	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["grant_type"] != "refresh_token" {
		t.Errorf("grant_type = %v", gotBody["grant_type"])
	}
	if gotBody["client_id"] != codexClientID {
		t.Errorf("client_id = %v", gotBody["client_id"])
	}
	if gotBody["refresh_token"] != "old-refresh" {
		t.Errorf("refresh_token = %v", gotBody["refresh_token"])
	}
	if _, hasScope := gotBody["scope"]; hasScope {
		t.Error("refresh body must not contain scope (codex-rs parity)")
	}
	if len(gotBody) != 3 {
		t.Errorf("refresh body has %d keys, want exactly 3: %v", len(gotBody), gotBody)
	}

	if refreshed.AccessToken != "new-access" {
		t.Errorf("AccessToken = %q", refreshed.AccessToken)
	}
	if refreshed.RefreshToken != "new-refresh" {
		t.Errorf("rotated RefreshToken = %q", refreshed.RefreshToken)
	}
}
