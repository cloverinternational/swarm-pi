package anthropic

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateDeviceID(t *testing.T) {
	id := generateDeviceID()

	// Should be 64 hex characters (32 bytes)
	if len(id) != 64 {
		t.Errorf("expected device ID length 64, got %d", len(id))
	}

	// Should be valid hex
	if _, err := hex.DecodeString(id); err != nil {
		t.Errorf("device ID is not valid hex: %v", err)
	}

	// Should be unique on each call
	id2 := generateDeviceID()
	if id == id2 {
		t.Error("device IDs should be unique")
	}
}

func TestGenerateHostID(t *testing.T) {
	id := generateHostID()

	// Should be UUID format: 8-4-4-4-12
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Errorf("expected 5 UUID parts, got %d", len(parts))
	}

	expectedLengths := []int{8, 4, 4, 4, 12}
	for i, part := range parts {
		if len(part) != expectedLengths[i] {
			t.Errorf("UUID part %d: expected length %d, got %d", i, expectedLengths[i], len(part))
		}
	}

	// Should be unique on each call
	id2 := generateHostID()
	if id == id2 {
		t.Error("host IDs should be unique")
	}
}

func TestGenerateDeviceIdentity(t *testing.T) {
	accountUUID := "test-account-123"
	identity := GenerateDeviceIdentity(accountUUID)

	if identity == nil {
		t.Fatal("expected non-nil identity")
	}

	if len(identity.DeviceID) != 64 {
		t.Errorf("expected device ID length 64, got %d", len(identity.DeviceID))
	}

	if identity.HostID == "" {
		t.Error("host ID should not be empty")
	}

	if identity.AccountID != accountUUID {
		t.Errorf("expected account ID %s, got %s", accountUUID, identity.AccountID)
	}

	if identity.CreatedAt == 0 {
		t.Error("created at should be set")
	}
}

func TestFormatUserID(t *testing.T) {
	identity := &DeviceIdentity{
		DeviceID:  strings.Repeat("a", 64),
		HostID:    "550e8400-e29b-41d4-a716-446655440000",
		AccountID: "test-account-uuid",
	}

	sessionID := "test-session-uuid"
	userID := identity.FormatUserID(sessionID)

	// Format: user_<device_id>_account_<account_uuid>_session_<session_uuid>
	expectedPrefix := "user_" + strings.Repeat("a", 64) + "_account_test-account-uuid_session_test-session-uuid"
	if userID != expectedPrefix {
		t.Errorf("expected user ID %s, got %s", expectedPrefix, userID)
	}

	// Test with empty session ID (should generate one)
	userID2 := identity.FormatUserID("")
	if userID2 == "" {
		t.Error("should generate session ID if not provided")
	}
	if !strings.Contains(userID2, "_session_") {
		t.Error("generated user ID should contain session separator")
	}
}

func TestFormatUserID_NilIdentity(t *testing.T) {
	var identity *DeviceIdentity
	userID := identity.FormatUserID("session-123")
	if userID != "" {
		t.Errorf("expected empty user ID for nil identity, got %s", userID)
	}
}

func TestGetOrCreateSessionID(t *testing.T) {
	// Reset first
	ResetSessionID()

	id1 := GetOrCreateSessionID()
	if id1 == "" {
		t.Error("session ID should not be empty")
	}

	// Should return same ID on subsequent calls
	id2 := GetOrCreateSessionID()
	if id1 != id2 {
		t.Error("session ID should be consistent within session")
	}

	// After reset, should generate new ID
	ResetSessionID()
	id3 := GetOrCreateSessionID()
	if id3 == id1 {
		t.Error("session ID should change after reset")
	}
}

func TestDeviceIdentityCache(t *testing.T) {
	// Clear cache
	SetCachedDeviceIdentity(nil)

	// Get should return nil
	if cached := GetCachedDeviceIdentity(); cached != nil {
		t.Error("expected nil cache initially")
	}

	// Set cache
	identity := &DeviceIdentity{
		DeviceID:  strings.Repeat("b", 64),
		HostID:    "test-host-id",
		AccountID: "test-account",
	}
	SetCachedDeviceIdentity(identity)

	// Get should return cached value
	cached := GetCachedDeviceIdentity()
	if cached == nil {
		t.Fatal("expected non-nil cached identity")
	}
	if cached.DeviceID != identity.DeviceID {
		t.Errorf("expected device ID %s, got %s", identity.DeviceID, cached.DeviceID)
	}

	// Clear cache
	SetCachedDeviceIdentity(nil)
	if cached := GetCachedDeviceIdentity(); cached != nil {
		t.Error("expected nil cache after clearing")
	}
}

func TestExtractAccountUUIDFromToken(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantEmpty bool
		wantSub   string
	}{
		{
			name:      "empty token",
			token:     "",
			wantEmpty: true,
		},
		{
			name:      "invalid token format",
			token:     "not-a-jwt",
			wantEmpty: true,
		},
		{
			name:      "token with no sub claim",
			token:     "header.eyJlbWFpbCI6InRlc3RAdGVzdC5jb20ifQ.signature",
			wantEmpty: true,
		},
		{
			// payload: {"sub":"user-uuid-1234","iat":1700000000}
			// base64url(payload) = eyJzdWIiOiJ1c2VyLXV1aWQtMTIzNCIsImlhdCI6MTcwMDAwMDAwMH0
			name:    "valid JWT with sub claim",
			token:   "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyLXV1aWQtMTIzNCIsImlhdCI6MTcwMDAwMDAwMH0.signature",
			wantSub: "user-uuid-1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractAccountUUIDFromToken(tt.token)
			if tt.wantEmpty && result != "" {
				t.Errorf("expected empty result, got %s", result)
			}
			if tt.wantSub != "" && result != tt.wantSub {
				t.Errorf("expected sub %q, got %q", tt.wantSub, result)
			}
		})
	}
}
