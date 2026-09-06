package cloud

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// AuthStatus captures the current cloud authentication state.
type AuthStatus string

const (
	AuthStatusUnavailable AuthStatus = "unavailable"
	AuthStatusNotSignedIn AuthStatus = "not signed in"
	AuthStatusExpired     AuthStatus = "expired"
	AuthStatusSignedIn    AuthStatus = "signed in"
)

// CloudIdentity is a UI-friendly view of the signed-in user.
type CloudIdentity struct {
	Status  AuthStatus
	Email   string
	Plan    string
	IsAdmin bool
}

// ResolveIdentity derives UI identity details from stored tokens.
func ResolveIdentity(tokenManager *TokenManager) CloudIdentity {
	var identity CloudIdentity
	identity.Status = AuthStatusUnavailable

	if tokenManager == nil {
		return identity
	}

	var tokens *TokenSet
	var err error
	tokens, err = tokenManager.LoadTokens()
	if err != nil {
		return identity
	}
	if tokens == nil {
		identity.Status = AuthStatusNotSignedIn
		return identity
	}
	if tokens.IsExpired() {
		identity.Status = AuthStatusExpired
		return identity
	}

	identity.Status = AuthStatusSignedIn

	var claims map[string]any
	claims, err = parseJWTClaims(tokens.IDToken)
	if err == nil {
		identity.Email = stringClaim(claims, "email")
	}

	var groups []string
	if err == nil {
		groups = stringSliceClaim(claims, "cognito:groups")
	}
	if len(groups) == 0 && tokens.AccessToken != "" {
		var accessClaims map[string]any
		accessClaims, err = parseJWTClaims(tokens.AccessToken)
		if err == nil {
			groups = stringSliceClaim(accessClaims, "cognito:groups")
		}
	}

	identity.IsAdmin = containsGroup(groups, "Administrators")
	if identity.IsAdmin {
		identity.Plan = "Administrator"
	} else {
		identity.Plan = "Member"
	}

	return identity
}

func parseJWTClaims(token string) (map[string]any, error) {
	var empty map[string]any
	if strings.TrimSpace(token) == "" {
		return empty, fmt.Errorf("token is empty")
	}

	var parts []string = strings.Split(token, ".")
	if len(parts) < 2 {
		return empty, fmt.Errorf("token format invalid")
	}

	var payload []byte
	var err error
	payload, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return empty, fmt.Errorf("failed to decode token payload: %w", err)
	}

	var claims map[string]any
	if err = json.Unmarshal(payload, &claims); err != nil {
		return empty, fmt.Errorf("failed to parse token claims: %w", err)
	}

	return claims, nil
}

func stringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}

	var raw any
	var ok bool
	raw, ok = claims[key]
	if !ok {
		return ""
	}

	var value string
	value, ok = raw.(string)
	if !ok {
		return ""
	}

	return value
}

func stringSliceClaim(claims map[string]any, key string) []string {
	if claims == nil {
		return nil
	}

	var raw any
	var ok bool
	raw, ok = claims[key]
	if !ok {
		return nil
	}

	var groups []string

	var rawStrings []string
	rawStrings, ok = raw.([]string)
	if ok {
		groups = append(groups, rawStrings...)
		return groups
	}

	var rawAny []any
	rawAny, ok = raw.([]any)
	if ok {
		var i int
		for i = 0; i < len(rawAny); i++ {
			var item any = rawAny[i]
			var s string
			s, ok = item.(string)
			if ok && strings.TrimSpace(s) != "" {
				groups = append(groups, s)
			}
		}
		return groups
	}

	var single string
	single, ok = raw.(string)
	if ok && strings.TrimSpace(single) != "" {
		groups = append(groups, single)
	}

	return groups
}

func containsGroup(groups []string, target string) bool {
	if target == "" {
		return false
	}

	var i int
	for i = range groups {
		if groups[i] == target {
			return true
		}
	}
	return false
}
