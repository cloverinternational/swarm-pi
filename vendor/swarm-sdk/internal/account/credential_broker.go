package account

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

// CredentialBroker is an additive hosted-mode interface for requesting
// short-lived scoped credentials without relying on ambient environment
// variables inside the runtime.
type CredentialBroker interface {
	// RequestGrant requests a scoped credential grant for the current session.
	RequestGrant(ctx context.Context, request hosted.CredentialGrantRequest) (*hosted.CredentialGrant, error)

	// RevokeGrant revokes an issued grant.
	RevokeGrant(ctx context.Context, grantID string) error
}

// CredentialBrokerProvider exposes a hosted credential broker when available.
type CredentialBrokerProvider interface {
	CredentialBroker() CredentialBroker
}
