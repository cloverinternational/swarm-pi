package settings

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Cursor (Anysphere) OAuth PKCE login.
//
// This mirrors the `cursor-agent login` flow documented in the Cursor harness
// (deconstruct/cli/closed-source/cursor-cli):
//  1. verifier = base64url(rand32); challenge = base64url(sha256(verifier)); uuid = uuid4
//  2. open {WEBSITE}/loginDeepControl?challenge=&uuid=&mode=login&redirectTarget=cli
//  3. poll GET {API}/auth/poll?uuid=&verifier=  -> 404 pending; 200 {accessToken,refreshToken}
//
// On success the token pair is written to {CURSOR_CONFIG_DIR}/cli-config.json so
// the model-refresh path (package chat: getCursorAccessToken) reuses it exactly
// like a native CLI login.

const (
	cursorWebsiteURLDefault = "https://cursor.com"
	cursorAPIBaseURLDefault = "https://api2.cursor.sh"
)

func cursorWebsiteURL() string {
	if v := strings.TrimSpace(os.Getenv("CURSOR_WEBSITE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return cursorWebsiteURLDefault
}

func cursorAPIBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("CURSOR_API_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return cursorAPIBaseURLDefault
}

func cursorConfigDir() string {
	if v := strings.TrimSpace(os.Getenv("CURSOR_CONFIG_DIR")); v != "" {
		return v
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "cursor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cursor")
}

// cursorPKCEFlow holds the verifier/uuid/url for an in-progress login.
type cursorPKCEFlow struct {
	verifier string
	uuid     string
	authURL  string
}

// cursorToken is the persisted Cursor session.
type cursorToken struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

// base64URL encodes b as URL-safe base64 with padding stripped (RFC 7636).
func base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// newCursorPKCEFlow builds a fresh PKCE challenge and the login deeplink.
func newCursorPKCEFlow() (*cursorPKCEFlow, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("cursor: failed to generate verifier: %w", err)
	}
	verifier := base64URL(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64URL(sum[:])
	id := uuid.NewString()
	authURL := fmt.Sprintf(
		"%s/loginDeepControl?challenge=%s&uuid=%s&mode=login&redirectTarget=cli",
		cursorWebsiteURL(), challenge, id,
	)
	return &cursorPKCEFlow{verifier: verifier, uuid: id, authURL: authURL}, nil
}

// poll repeatedly hits /auth/poll until it returns a token, ctx is cancelled,
// or too many consecutive non-404 errors occur. Backoff: min(1000*1.2^i,10000)ms.
func (f *cursorPKCEFlow) poll(ctx context.Context) (*cursorToken, error) {
	pollURL := fmt.Sprintf("%s/auth/poll?uuid=%s&verifier=%s", cursorAPIBaseURL(), f.uuid, f.verifier)
	client := &http.Client{Timeout: 30 * time.Second}
	consecutiveErrs := 0
	for i := 0; i < 150; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "GET", pollURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			consecutiveErrs++
			if consecutiveErrs >= 3 {
				return nil, fmt.Errorf("cursor: poll failed: %w", err)
			}
			sleepBackoff(ctx, i)
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var tok cursorToken
			if err := json.Unmarshal(body, &tok); err != nil {
				return nil, fmt.Errorf("cursor: invalid poll response: %w", err)
			}
			if tok.AccessToken == "" {
				return nil, fmt.Errorf("cursor: poll returned no accessToken")
			}
			consecutiveErrs = 0
			return &tok, nil
		case http.StatusNotFound:
			// Pending — keep polling.
			resp.Body.Close()
			consecutiveErrs = 0
			sleepBackoff(ctx, i)
			continue
		default:
			resp.Body.Close()
			consecutiveErrs++
			if consecutiveErrs >= 3 {
				return nil, fmt.Errorf("cursor: poll returned status %d", resp.StatusCode)
			}
			sleepBackoff(ctx, i)
		}
	}
	return nil, fmt.Errorf("cursor: login timed out waiting for approval")
}

// sleepBackoff sleeps min(1000*1.2^i, 10000)ms, respecting ctx cancellation.
func sleepBackoff(ctx context.Context, i int) {
	ms := 1000.0
	for k := 0; k < i; k++ {
		ms *= 1.2
		if ms >= 10000 {
			ms = 10000
			break
		}
	}
	t := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// storeCursorToken persists the token pair into {configDir}/cli-config.json,
// preserving any existing fields. This is the same file getCursorAccessToken
// reads, so login and model-refresh stay consistent.
//
// ALSO writes to ~/.swarmos/accounts/cursor/account-cli.json so the token
// survives the VSCode cursor app clobbering cli-config.json on startup.
func storeCursorToken(tok *cursorToken) error {
	dir := cursorConfigDir()
	if dir == "" {
		return fmt.Errorf("cursor: could not resolve config dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cursor: failed to create config dir: %w", err)
	}
	path := filepath.Join(dir, "cli-config.json")

	// Merge into any existing config so we don't clobber unrelated fields.
	merged := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &merged)
	}
	merged["accessToken"] = tok.AccessToken
	if tok.RefreshToken != "" {
		merged["refreshToken"] = tok.RefreshToken
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("cursor: failed to write cli-config.json: %w", err)
	}

	// ALSO store in swarmos account registry so it survives VSCode clobbering.
	storeCursorTokenInAccountRegistry(tok)

	return nil
}

// storeCursorTokenInAccountRegistry writes the token to
// ~/.swarmos/accounts/cursor/account-cli.json in the genericOAuthToken format.
// This is a best-effort write — errors are silently ignored since the primary
// write to cli-config.json has already succeeded.
func storeCursorTokenInAccountRegistry(tok *cursorToken) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	accountsDir := filepath.Join(home, ".swarmos", "accounts", "cursor")
	if err := os.MkdirAll(accountsDir, 0o755); err != nil {
		return
	}
	accountPath := filepath.Join(accountsDir, "account-cli.json")
	account := map[string]any{
		"access_token":  tok.AccessToken,
		"refresh_token": tok.RefreshToken,
		"provider":      "cursor",
		"is_active":     true,
	}
	data, err := json.MarshalIndent(account, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(accountPath, data, 0o600)
}
