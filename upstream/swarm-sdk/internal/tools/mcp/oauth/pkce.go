package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// GenerateCodeVerifier generates a cryptographically random code verifier
// as specified in RFC 7636. The verifier is a high-entropy random string
// between 43 and 128 characters.
func GenerateCodeVerifier() (string, error) {
	// Generate 32 random bytes (gives us 43 base64url characters)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Base64url encode without padding
	verifier := base64.RawURLEncoding.EncodeToString(bytes)
	return verifier, nil
}

// GenerateCodeChallenge generates the code challenge from a verifier
// using the S256 method (SHA-256 hash, then base64url encode).
func GenerateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	return challenge
}

// GenerateState generates a cryptographically random state parameter
// for CSRF protection.
func GenerateState() (string, error) {
	// Generate 16 random bytes (gives us 22 base64url characters)
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	state := base64.RawURLEncoding.EncodeToString(bytes)
	return state, nil
}

// GeneratePKCE generates both the code verifier and challenge.
// Returns (verifier, challenge, error).
func GeneratePKCE() (string, string, error) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return "", "", err
	}

	challenge := GenerateCodeChallenge(verifier)
	return verifier, challenge, nil
}

// GenerateFlowState creates a new FlowState with PKCE parameters.
func GenerateFlowState() (*FlowState, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PKCE: %w", err)
	}

	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	return &FlowState{
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
		State:         state,
	}, nil
}
