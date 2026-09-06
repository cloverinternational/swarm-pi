package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

type HostedCredentialBrokerConfig struct {
	IssueURL      string
	RedeemURL     string
	RevokeURL     string
	SessionToken  string
	HTTPClient    *http.Client
	AttestGuestFn func(ctx context.Context) (string, error)
}

type HostedCredentialBroker struct {
	issueURL      string
	redeemURL     string
	revokeURL     string
	sessionToken  string
	httpClient    *http.Client
	attestGuestFn func(ctx context.Context) (string, error)
}

func NewHostedCredentialBroker(config HostedCredentialBrokerConfig) (*HostedCredentialBroker, error) {
	if strings.TrimSpace(config.IssueURL) == "" {
		return nil, fmt.Errorf("issue url is required")
	}
	if strings.TrimSpace(config.RedeemURL) == "" {
		return nil, fmt.Errorf("redeem url is required")
	}
	if strings.TrimSpace(config.SessionToken) == "" {
		return nil, fmt.Errorf("session token is required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &HostedCredentialBroker{
		issueURL:      strings.TrimSpace(config.IssueURL),
		redeemURL:     strings.TrimSpace(config.RedeemURL),
		revokeURL:     strings.TrimSpace(config.RevokeURL),
		sessionToken:  strings.TrimSpace(config.SessionToken),
		httpClient:    client,
		attestGuestFn: config.AttestGuestFn,
	}, nil
}

func (b *HostedCredentialBroker) RequestGrant(ctx context.Context, request hosted.CredentialGrantRequest) (*hosted.CredentialGrant, error) {
	issued := struct {
		Grant hosted.CredentialGrant `json:"grant"`
	}{}
	if err := b.postJSON(ctx, b.issueURL, request, &issued); err != nil {
		return nil, err
	}
	if strings.TrimSpace(issued.Grant.Token) == "" {
		return nil, fmt.Errorf("issued grant token is empty")
	}

	redeemed := struct {
		Grant hosted.CredentialGrant `json:"grant"`
	}{}
	if err := b.postJSON(ctx, b.redeemURL, map[string]string{
		"grant_token": issued.Grant.Token,
	}, &redeemed); err != nil {
		return nil, err
	}
	if strings.TrimSpace(redeemed.Grant.Token) == "" {
		return nil, fmt.Errorf("redeemed grant token is empty")
	}
	if redeemed.Grant.ID == "" {
		redeemed.Grant.ID = issued.Grant.ID
	}
	if redeemed.Grant.Kind == "" {
		redeemed.Grant.Kind = issued.Grant.Kind
	}
	if redeemed.Grant.Provider == "" {
		redeemed.Grant.Provider = issued.Grant.Provider
	}
	if len(redeemed.Grant.Scopes) == 0 {
		redeemed.Grant.Scopes = append([]string{}, issued.Grant.Scopes...)
	}
	if redeemed.Grant.IssuedAt.IsZero() {
		redeemed.Grant.IssuedAt = issued.Grant.IssuedAt
	}
	if redeemed.Grant.ExpiresAt.IsZero() {
		redeemed.Grant.ExpiresAt = issued.Grant.ExpiresAt
	}
	if redeemed.Grant.Metadata == nil && issued.Grant.Metadata != nil {
		redeemed.Grant.Metadata = mapsClone(issued.Grant.Metadata)
	}
	return &redeemed.Grant, nil
}

func (b *HostedCredentialBroker) RevokeGrant(ctx context.Context, grantID string) error {
	if strings.TrimSpace(b.revokeURL) == "" {
		return fmt.Errorf("revoke url is not configured")
	}
	return b.postJSON(ctx, b.revokeURL, map[string]string{"grant_id": strings.TrimSpace(grantID)}, nil)
}

func (b *HostedCredentialBroker) postJSON(ctx context.Context, endpoint string, body any, target any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.sessionToken)
	if b.attestGuestFn != nil {
		attestation, err := b.attestGuestFn(ctx)
		if err != nil {
			return err
		}
		if strings.TrimSpace(attestation) != "" {
			req.Header.Set("X-Swarm-Guest-Control-Attestation", attestation)
		}
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("credential broker request failed with status %d", resp.StatusCode)
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func mapsClone(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	cloned := make(map[string]string, len(input))
	maps.Copy(cloned, input)
	return cloned
}
