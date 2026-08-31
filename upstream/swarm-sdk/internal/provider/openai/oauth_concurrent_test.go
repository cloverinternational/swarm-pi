package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshAndStoreTokenConcurrentUsesOneRotatingRefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var refreshCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "current-access",
			"refresh_token": "rotated-refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()
	oldURL := oauthTokenURL
	oauthTokenURL = server.URL
	defer func() { oauthTokenURL = oldURL }()

	if err := StoreOAuthToken(&OAuthToken{
		AccessToken:  "expired-access",
		RefreshToken: "original-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
		AccountID:    "account-id",
	}); err != nil {
		t.Fatal(err)
	}

	const callers = 24
	results := make(chan *OAuthToken, callers)
	errors := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := RefreshAndStoreToken(context.Background())
			if err != nil {
				errors <- err
				return
			}
			results <- token
		}()
	}
	wg.Wait()
	close(results)
	close(errors)

	for err := range errors {
		t.Errorf("RefreshAndStoreToken: %v", err)
	}
	for token := range results {
		if token.AccessToken != "current-access" || token.RefreshToken != "rotated-refresh" {
			t.Errorf("token = %+v, want coordinated current token", token)
		}
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh endpoint calls = %d, want 1", got)
	}
}

func TestRefreshSerializationDoesNotOverwriteLaterStoreOrClear(t *testing.T) {
	for _, mutation := range []string{"store", "clear"} {
		t.Run(mutation, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			started := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(started)
				<-release
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "refreshed-access",
					"refresh_token": "rotated-refresh",
					"expires_in":    3600,
				})
			}))
			defer server.Close()
			oldURL := oauthTokenURL
			oauthTokenURL = server.URL
			defer func() { oauthTokenURL = oldURL }()

			if err := StoreOAuthToken(&OAuthToken{
				AccessToken: "expired", RefreshToken: "old-refresh",
				ExpiresAt: time.Now().Add(-time.Hour).Unix(), AccountID: "account",
			}); err != nil {
				t.Fatal(err)
			}
			refreshDone := make(chan error, 1)
			go func() {
				_, err := RefreshAndStoreToken(context.Background())
				refreshDone <- err
			}()
			<-started

			mutationDone := make(chan error, 1)
			go func() {
				if mutation == "clear" {
					mutationDone <- ClearOAuthToken()
					return
				}
				mutationDone <- StoreOAuthToken(&OAuthToken{
					AccessToken: "new-login", RefreshToken: "new-login-refresh",
					ExpiresAt: time.Now().Add(time.Hour).Unix(), AccountID: "new-account",
				})
			}()
			select {
			case err := <-mutationDone:
				t.Fatalf("%s escaped the refresh lock early: %v", mutation, err)
			case <-time.After(25 * time.Millisecond):
			}
			close(release)
			if err := <-refreshDone; err != nil {
				t.Fatalf("refresh: %v", err)
			}
			if err := <-mutationDone; err != nil {
				t.Fatalf("%s: %v", mutation, err)
			}

			token, err := GetStoredOAuthToken()
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "clear" {
				if token != nil {
					t.Fatalf("clear lost race; stale refresh survived: %+v", token)
				}
			} else if token == nil || token.AccessToken != "new-login" || token.RefreshToken != "new-login-refresh" {
				t.Fatalf("store lost race to stale refresh: %+v", token)
			}
		})
	}
}

func TestOAuthConfigAtomicReplacementRemainsValid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := StoreOAuthToken(&OAuthToken{AccessToken: "seed"}); err != nil {
		t.Fatal(err)
	}
	path, err := getOAuthConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				readErr <- nil
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				readErr <- err
				return
			}
			var cfg OAuthConfig
			if err := json.Unmarshal(data, &cfg); err != nil {
				readErr <- err
				return
			}
		}
	}()
	for i := 0; i < 200; i++ {
		if err := StoreOAuthToken(&OAuthToken{AccessToken: strings.Repeat("x", i+1)}); err != nil {
			close(stop)
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-readErr; err != nil {
		t.Fatalf("reader observed partial OAuth JSON: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("OAuth config mode = %o, want 600", info.Mode().Perm())
	}
}
