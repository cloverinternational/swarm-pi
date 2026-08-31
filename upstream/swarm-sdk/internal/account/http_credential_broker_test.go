package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

func TestHostedCredentialBrokerRequestGrantRedeemsIssuedGrant(t *testing.T) {
	t.Parallel()

	var issuedAuthHeader string
	var issuedAttestation string
	var issuedRequest hosted.CredentialGrantRequest
	var redeemedGrantToken string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/issue":
			issuedAuthHeader = r.Header.Get("Authorization")
			issuedAttestation = r.Header.Get("X-Swarm-Guest-Control-Attestation")
			if err := json.NewDecoder(r.Body).Decode(&issuedRequest); err != nil {
				t.Fatalf("decode issue request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"grant": map[string]any{
					"id":         "grant-1",
					"kind":       "model_provider",
					"provider":   "openai",
					"token":      "grant-token-1",
					"scopes":     []string{"responses:create"},
					"issued_at":  time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
					"expires_at": time.Date(2026, 3, 13, 12, 5, 0, 0, time.UTC),
					"metadata": map[string]string{
						"audience": "guest_runtime",
					},
				},
			})
		case "/redeem":
			var body struct {
				GrantToken string `json:"grant_token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode redeem request: %v", err)
			}
			redeemedGrantToken = body.GrantToken
			_ = json.NewEncoder(w).Encode(map[string]any{
				"grant": map[string]any{
					"id":         "grant-1",
					"kind":       "model_provider",
					"provider":   "openai",
					"token":      "provider-secret",
					"scopes":     []string{"responses:create"},
					"issued_at":  time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
					"expires_at": time.Date(2026, 3, 13, 12, 5, 0, 0, time.UTC),
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	broker, err := NewHostedCredentialBroker(HostedCredentialBrokerConfig{
		IssueURL:      server.URL + "/issue",
		RedeemURL:     server.URL + "/redeem",
		SessionToken:  "vm-session-token",
		AttestGuestFn: func(context.Context) (string, error) { return "guest-attestation", nil },
	})
	if err != nil {
		t.Fatalf("NewHostedCredentialBroker error = %v", err)
	}

	grant, err := broker.RequestGrant(context.Background(), hosted.CredentialGrantRequest{
		Kind:      hosted.CredentialKindModelProvider,
		SessionID: "session-1",
		ProjectID: "project-1",
		Provider:  "openai",
		Scopes:    []string{"responses:create"},
		Reason:    "model_call",
	})
	if err != nil {
		t.Fatalf("RequestGrant error = %v", err)
	}

	if issuedAuthHeader != "Bearer vm-session-token" {
		t.Fatalf("Authorization header = %q, want bearer session token", issuedAuthHeader)
	}
	if issuedAttestation != "guest-attestation" {
		t.Fatalf("attestation header = %q", issuedAttestation)
	}
	if issuedRequest.Kind != hosted.CredentialKindModelProvider || issuedRequest.Provider != "openai" {
		t.Fatalf("unexpected issued request: %#v", issuedRequest)
	}
	if redeemedGrantToken != "grant-token-1" {
		t.Fatalf("redeemed grant token = %q", redeemedGrantToken)
	}
	if grant.Token != "provider-secret" {
		t.Fatalf("grant token = %q, want redeemed material", grant.Token)
	}
	if grant.Provider != "openai" {
		t.Fatalf("grant provider = %q", grant.Provider)
	}
}

func TestHostedCredentialBrokerRequiresConfiguredIssueAndRedeemURLs(t *testing.T) {
	t.Parallel()

	if _, err := NewHostedCredentialBroker(HostedCredentialBrokerConfig{}); err == nil {
		t.Fatal("expected constructor error for missing URLs")
	}
}
