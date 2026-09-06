// cmd/custom-provider-probe — probe for custom openai-compatible provider URL construction.
//
// Reproduces BUG-1: trailing slash in base_url causes double-slash in request path,
// which vLLM (and other FastAPI-based servers) return as HTTP 404.
//
// # Root cause
//
// When a user configures a custom provider with base_url ending in "/":
//
//	"base_url": "https://host:port/v1/"
//
// provider/openai/provider.go New() did NOT strip the trailing slash, so:
//
//	p.baseURL + "/chat/completions"
//	→ "https://host:port/v1/" + "/chat/completions"
//	→ "https://host:port/v1//chat/completions"   ← double slash → 404
//
// # Known bugs this probe exposes
//
//   - BUG-1 (FIXED): provider/openai/provider.go New() did not call
//     strings.TrimRight(config.BaseURL, "/"). Now fixed. The probe confirms
//     the fix holds.
//
// # Usage
//
//	cd swarm-sdk && go run ./cmd/custom-provider-probe [base_url] [api_key] [model]
//
// Exit 0 = all checks passed (bug is fixed).
// Non-zero = one or more checks failed (bug still present).
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

var failures []string

func check(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("  ✓  %s\n", name)
	} else {
		fmt.Fprintf(os.Stderr, "  ✗  FAIL %s: %s\n", name, detail)
		failures = append(failures, name)
	}
}

func main() {
	baseURL := "https://144.31.231.22:8999/v1"
	apiKey := "OlYkRmPqooRYWfGRe5lJEqstepLZJAHXkxlI0K9XhO0sa02dkEuhAKOHJDnqDS90"
	model := "kimi-k2.6-ega-s4orig"
	if len(os.Args) > 1 {
		baseURL = os.Args[1]
	}
	if len(os.Args) > 2 {
		apiKey = os.Args[2]
	}
	if len(os.Args) > 3 {
		model = os.Args[3]
	}

	// Normalize: strip any trailing slash for our clean baseline
	cleanURL := strings.TrimRight(baseURL, "/")
	slashURL := cleanURL + "/"

	fmt.Printf("custom-provider-probe\n")
	fmt.Printf("  server   : %s\n", cleanURL)
	fmt.Printf("  model    : %s\n\n", model)

	// insecure HTTP client for probing self-signed cert on bare IP
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 — probe only
		},
	}

	// ── Stage 1: raw HTTP — double slash must 404 ────────────────────────────
	fmt.Println("Stage 1: raw HTTP — trailing slash produces double-slash path")
	doubleSlashURL := slashURL + "/chat/completions" // → .../v1//chat/completions
	status, body := rawPost(httpClient, doubleSlashURL, apiKey, model)
	check(
		"BUG-1 reproduction: double-slash path returns 404",
		status == 404,
		fmt.Sprintf("expected 404, got %d body=%s", status, body),
	)

	// ── Stage 2: raw HTTP — clean URL must succeed ───────────────────────────
	fmt.Println("\nStage 2: raw HTTP — clean path (single slash) succeeds")
	cleanSlashURL := cleanURL + "/chat/completions" // → .../v1/chat/completions
	status, body = rawPost(httpClient, cleanSlashURL, apiKey, model)
	check(
		"clean path returns 200",
		status == 200,
		fmt.Sprintf("expected 200, got %d body=%s", status, truncate(body, 120)),
	)

	// ── Stage 3: SDK with trailing slash — must work after fix ───────────────
	fmt.Println("\nStage 3: openai.New() with trailing slash (BUG-1 fix validation)")
	p, err := openai.New(openai.Config{
		APIKey:        apiKey,
		BaseURL:       slashURL, // intentionally supply trailing slash
		Name:          "probe-target",
		TransportMode: "default",
		Timeout:       10,
	})
	if err != nil {
		check("SDK init with trailing slash", false, err.Error())
	} else {
		maxTok := 5
		chatCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := p.Chat(chatCtx, provider.ChatRequest{
			Model: model,
			Messages: []*conversation.Message{
				{Role: conversation.RoleUser, Content: "say ok"},
			},
			MaxTokens: &maxTok,
		})
		check(
			"BUG-1 fixed: SDK with trailing slash base_url returns response",
			err == nil && resp != nil,
			fmt.Sprintf("err=%v", err),
		)
		if err == nil && resp != nil && resp.Usage != nil {
			fmt.Printf("    → %d output token(s)\n", resp.Usage.Output)
		}
	}

	// ── Result ────────────────────────────────────────────────────────────────
	fmt.Println()
	if len(failures) == 0 {
		fmt.Println("PASS — all checks passed")
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "FAIL — %d check(s) failed: %s\n", len(failures), strings.Join(failures, ", "))
	os.Exit(1)
}

func rawPost(client *http.Client, url, apiKey, model string) (int, string) {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "say ok"}},
		"max_tokens": 5,
		"stream":     false,
	})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
