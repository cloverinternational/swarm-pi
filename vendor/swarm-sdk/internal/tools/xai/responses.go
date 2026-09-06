// Package xai provides Swarm tools backed by xAI's Responses API.
//
// xAI's Responses API (/v1/responses) exposes server-side agentic tools that
// Grok executes natively — including privileged access to X (Twitter) data and
// real-time web search with page-browsing. These are fundamentally different from
// Chat Completions tools: xAI runs them server-side, not the agent.
//
// Auth: reads the SuperGrok OAuth bearer from ~/.swarm/config/oauth/xai.json (written
// by the /auth xai flow) or falls back to XAI_API_KEY env var.
package xai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	xaioauth "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/xai"
)

const (
	defaultBaseURL        = "https://api.x.ai/v1"
	defaultResponsesModel = "grok-4.3"
	defaultTimeout        = 120 * time.Second
	maxResultSize         = 100_000 // chars — matches Hermes
)

// resolveBearer returns the best available xAI bearer token.
//
// Priority:
//  1. Stored SuperGrok OAuth access token (~/.swarm/config/oauth/xai.json)
//  2. XAI_API_KEY environment variable
func resolveBearer() (apiKey, baseURL string, isOAuth bool, err error) {
	// 1. Try SuperGrok OAuth store.
	if token, terr := xaioauth.GetStoredOAuthToken(); terr == nil && token != nil {
		if token.IsExpired() {
			// Attempt silent refresh.
			refreshed, rerr := xaioauth.RefreshToken(context.Background(), token.RefreshToken)
			if rerr == nil && refreshed != nil {
				return refreshed.AccessToken, defaultBaseURL, true, nil
			}
			// Refresh failed — fall through to env var.
		} else {
			return token.AccessToken, defaultBaseURL, true, nil
		}
	}

	// 2. XAI_API_KEY env var.
	if key := strings.TrimSpace(os.Getenv("XAI_API_KEY")); key != "" {
		return key, defaultBaseURL, false, nil
	}

	return "", "", false, fmt.Errorf("no xAI credentials found — run /auth xai or set XAI_API_KEY")
}

// HasCredentials returns true when at least one credential source is available.
// Cheap: no HTTP calls, no token refresh.
func HasCredentials() bool {
	if token, err := xaioauth.GetStoredOAuthToken(); err == nil && token != nil {
		if !token.IsExpired() {
			return true
		}
		// Expired but has a refresh token → still credentialed.
		if token.RefreshToken != "" {
			return true
		}
	}
	return strings.TrimSpace(os.Getenv("XAI_API_KEY")) != ""
}

// ─── Responses API types ─────────────────────────────────────────────────────

// responsesRequest is the payload for POST /v1/responses.
type responsesRequest struct {
	Model string           `json:"model"`
	Input []responsesInput `json:"input"`
	Tools []any            `json:"tools"`
	Store bool             `json:"store"`
}

type responsesInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// responsesReply is a partial representation of the Responses API reply.
type responsesReply struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type        string `json:"type"`
			Text        string `json:"text,omitempty"`
			Annotations []struct {
				Type       string `json:"type"`
				URL        string `json:"url,omitempty"`
				Title      string `json:"title,omitempty"`
				StartIndex *int   `json:"start_index,omitempty"`
				EndIndex   *int   `json:"end_index,omitempty"`
			} `json:"annotations,omitempty"`
		} `json:"content,omitempty"`
	} `json:"output,omitempty"`
	Citations []string `json:"citations,omitempty"`
	Error     *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// Citation is a structured reference returned by the Responses API.
type Citation struct {
	URL        string `json:"url"`
	Title      string `json:"title,omitempty"`
	StartIndex *int   `json:"start_index,omitempty"`
	EndIndex   *int   `json:"end_index,omitempty"`
}

// ResponsesResult holds the parsed output of a Responses API call.
type ResponsesResult struct {
	Answer          string     `json:"answer"`
	Citations       []string   `json:"citations,omitempty"`
	InlineCitations []Citation `json:"inline_citations,omitempty"`
}

// callResponses executes a POST /v1/responses request and returns the parsed result.
func callResponses(ctx context.Context, prompt string, toolDef any) (*ResponsesResult, error) {
	apiKey, baseURL, _, err := resolveBearer()
	if err != nil {
		return nil, err
	}

	payload := responsesRequest{
		Model: defaultResponsesModel,
		Input: []responsesInput{{Role: "user", Content: prompt}},
		Tools: []any{toolDef},
		Store: false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("xAI responses: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("xAI responses: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SwarmCode-TUI/1.0 (+https://swarmcode.ai)")

	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xAI responses: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))

	if resp.StatusCode >= 400 {
		// Try to surface a useful error message from the JSON body.
		var errBody struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if jerr := json.Unmarshal(raw, &errBody); jerr == nil && errBody.Error.Message != "" {
			return nil, fmt.Errorf("xAI responses HTTP %d: %s", resp.StatusCode, errBody.Error.Message)
		}
		return nil, fmt.Errorf("xAI responses HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))[:min(300, len(raw))])
	}

	var reply responsesReply
	if jerr := json.Unmarshal(raw, &reply); jerr != nil {
		return nil, fmt.Errorf("xAI responses: parse reply: %w", jerr)
	}

	if reply.Error != nil && reply.Error.Message != "" {
		return nil, fmt.Errorf("xAI returned error: %s", reply.Error.Message)
	}

	// Extract text answer from output blocks.
	var textParts []string
	var inlineCitations []Citation
	for _, item := range reply.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				textParts = append(textParts, content.Text)
			}
			for _, ann := range content.Annotations {
				if ann.Type == "url_citation" && ann.URL != "" {
					inlineCitations = append(inlineCitations, Citation{
						URL:        ann.URL,
						Title:      ann.Title,
						StartIndex: ann.StartIndex,
						EndIndex:   ann.EndIndex,
					})
				}
			}
		}
	}

	answer := strings.Join(textParts, "\n\n")
	if len(answer) > maxResultSize {
		answer = answer[:maxResultSize] + "\n\n[truncated]"
	}

	return &ResponsesResult{
		Answer:          answer,
		Citations:       reply.Citations,
		InlineCitations: inlineCitations,
	}, nil
}
