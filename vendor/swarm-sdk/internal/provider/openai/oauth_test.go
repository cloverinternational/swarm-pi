package openai

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildAPIKeyExchangeFormIncludesDefaultOrganizationID(t *testing.T) {
	t.Parallel()

	token := testJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_123",
			"organizations": []any{
				map[string]any{
					"id":         "org_first",
					"is_default": false,
				},
				map[string]any{
					"id":         "org_default",
					"is_default": true,
				},
			},
		},
	})

	form := buildAPIKeyExchangeForm(token)

	if got := form.Get("organization_id"); got != "org_default" {
		t.Fatalf("organization_id = %q", got)
	}
	if got := form.Get("scope"); got != "model.request" {
		t.Fatalf("scope = %q", got)
	}
	if got := form.Get("requested_token"); got != "openai-api-key" {
		t.Fatalf("requested_token = %q", got)
	}
}

func TestBuildAPIKeyExchangeFormFallsBackToFirstOrganizationID(t *testing.T) {
	t.Parallel()

	token := testJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"organizations": []any{
				map[string]any{"id": "org_first"},
				map[string]any{"id": "org_second"},
			},
		},
	})

	form := buildAPIKeyExchangeForm(token)

	if got := form.Get("organization_id"); got != "org_first" {
		t.Fatalf("organization_id = %q", got)
	}
}

func TestExtractAccountIDFromIDToken(t *testing.T) {
	t.Parallel()

	token := testJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_123",
		},
	})

	if got := ExtractAccountIDFromIDToken(token); got != "acct_123" {
		t.Fatalf("ExtractAccountIDFromIDToken() = %q", got)
	}
}

func testJWT(t *testing.T, payload map[string]any) string {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(payload): %v", err)
	}

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString(raw)
	return strings.Join([]string{header, body, "signature"}, ".")
}
