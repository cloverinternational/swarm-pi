package agent

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

func TestNewCredentialStore_NilRegistry(t *testing.T) {
	cs := NewCredentialStore(nil, nil)
	if cs == nil {
		t.Fatal("expected non-nil store")
	}
	if cs.CredentialCount() != 0 {
		t.Errorf("expected 0 credentials, got %d", cs.CredentialCount())
	}
}

func TestNewCredentialStore_DefaultPolicy(t *testing.T) {
	cs := NewCredentialStore(nil, nil)
	if cs.policy == nil {
		t.Fatal("expected default policy")
	}
	if !cs.policy.RotateOnRateLimit {
		t.Error("default policy should rotate on rate limit")
	}
	if !cs.policy.RotateOnPayment {
		t.Error("default policy should rotate on payment")
	}
}

func TestCredentialStore_GetCredentials_Empty(t *testing.T) {
	cs := NewCredentialStore(nil, nil)
	creds := cs.Credentials("anthropic")
	if creds != nil {
		t.Errorf("expected nil credentials for unknown provider, got %v", creds)
	}
}

func TestCredentialStore_ShouldRotate_RateLimit(t *testing.T) {
	policy := &profiles.RetryPolicy{
		RotateOnRateLimit: true,
		RotateOnPayment:   false,
		RotateOnAuthError: false,
		RotateOnAnyError:  false,
		CooldownSeconds:   30,
	}
	cs := NewCredentialStore(nil, policy)

	// Rate limit error should trigger rotation
	rateLimitErr := sdkerr.Transient("anthropic.rate_limited", "")
	if !cs.ShouldRotate(rateLimitErr) {
		t.Error("expected ShouldRotate=true for rate limit error")
	}

	// Payment error should NOT trigger rotation with this policy
	paymentErr := sdkerr.Permanent("anthropic.payment_required", "payment needed")
	if cs.ShouldRotate(paymentErr) {
		t.Error("expected ShouldRotate=false for payment error when RotateOnPayment=false")
	}
}

func TestCredentialStore_ShouldRotate_Payment(t *testing.T) {
	policy := &profiles.RetryPolicy{
		RotateOnRateLimit: false,
		RotateOnPayment:   true,
		CooldownSeconds:   30,
	}
	cs := NewCredentialStore(nil, policy)

	paymentErr := sdkerr.Permanent("anthropic.payment_required", "payment needed")
	if !cs.ShouldRotate(paymentErr) {
		t.Error("expected ShouldRotate=true for payment error")
	}
}

func TestCredentialStore_ShouldRotate_Auth(t *testing.T) {
	policy := &profiles.RetryPolicy{
		RotateOnAuthError: true,
		CooldownSeconds:   30,
	}
	cs := NewCredentialStore(nil, policy)

	authErr := sdkerr.Permanent("anthropic.unauthorized", "bad token")
	if !cs.ShouldRotate(authErr) {
		t.Error("expected ShouldRotate=true for auth error")
	}
}

func TestCredentialStore_ShouldRotate_AnyError(t *testing.T) {
	policy := &profiles.RetryPolicy{
		RotateOnAnyError: true,
		CooldownSeconds:  30,
	}
	cs := NewCredentialStore(nil, policy)

	genericErr := sdkerr.Permanent("anthropic.some_error", "something failed")
	if !cs.ShouldRotate(genericErr) {
		t.Error("expected ShouldRotate=true for any error when RotateOnAnyError=true")
	}
}

func TestCredentialStore_ShouldRotate_NilPolicy(t *testing.T) {
	cs := &CredentialStore{
		credentials: make(map[string][]Credential),
		policy:      nil,
	}

	err := sdkerr.Transient("anthropic.rate_limited", "")
	if cs.ShouldRotate(err) {
		t.Error("expected ShouldRotate=false when policy is nil")
	}
}

func TestCredentialStore_MarkCooldown(t *testing.T) {
	cs := &CredentialStore{
		credentials: map[string][]Credential{
			"anthropic": {
				{Provider: "anthropic", AccountID: "acct-1", Token: "tok1"},
				{Provider: "anthropic", AccountID: "acct-2", Token: "tok2"},
			},
		},
		cooldown: 30 * time.Second,
		policy:   profiles.DefaultRetryPolicy(),
	}

	// Mark first credential on cooldown
	cs.MarkCooldown("anthropic", "acct-1")

	// Only second credential should be available
	available := cs.Credentials("anthropic")
	if len(available) != 1 {
		t.Fatalf("expected 1 available credential, got %d", len(available))
	}
	if available[0].AccountID != "acct-2" {
		t.Errorf("expected acct-2 to be available, got %s", available[0].AccountID)
	}
}

func TestCredentialStore_CooldownExpiry(t *testing.T) {
	cs := &CredentialStore{
		credentials: map[string][]Credential{
			"anthropic": {
				{
					Provider:   "anthropic",
					AccountID:  "acct-1",
					Token:      "tok1",
					CoolingOff: true,
					CooldownAt: time.Now().Add(-2 * time.Minute), // cooldown started 2 min ago
				},
			},
		},
		cooldown: 1 * time.Minute, // 1 minute cooldown
		policy:   profiles.DefaultRetryPolicy(),
	}

	// Cooldown should have expired, credential should be available
	available := cs.Credentials("anthropic")
	if len(available) != 1 {
		t.Fatalf("expected 1 available credential after cooldown expiry, got %d", len(available))
	}
}

func TestCredentialStore_HasCredentials(t *testing.T) {
	cs := &CredentialStore{
		credentials: map[string][]Credential{
			"anthropic": {
				{Provider: "anthropic", AccountID: "acct-1", Token: "tok1"},
			},
		},
		cooldown: 30 * time.Second,
		policy:   profiles.DefaultRetryPolicy(),
	}

	if !cs.HasCredentials("anthropic") {
		t.Error("expected HasCredentials=true for anthropic")
	}
	if cs.HasCredentials("openai") {
		t.Error("expected HasCredentials=false for openai")
	}
}

func TestCredentialStore_CredentialCount(t *testing.T) {
	cs := &CredentialStore{
		credentials: map[string][]Credential{
			"anthropic": {
				{Provider: "anthropic", AccountID: "acct-1", Token: "tok1"},
				{Provider: "anthropic", AccountID: "acct-2", Token: "tok2"},
			},
			"openai": {
				{Provider: "openai", AccountID: "acct-3", Token: "tok3"},
			},
		},
		cooldown: 30 * time.Second,
		policy:   profiles.DefaultRetryPolicy(),
	}

	if cs.CredentialCount() != 3 {
		t.Errorf("expected 3 credentials, got %d", cs.CredentialCount())
	}
}
