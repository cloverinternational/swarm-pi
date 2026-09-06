package anthropic

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DeviceIdentity holds unique identifiers for a Claude Code client instance.
// Each OAuth account should have its own unique DeviceIdentity to prevent
// server-side correlation of multiple accounts to a single "device".
type DeviceIdentity struct {
	// DeviceID is a 64-character hex string (32 bytes) that uniquely identifies
	// this client configuration. Generated once per account on first login.
	// Claude Code stores this in ~/.claude.json under "userID".
	DeviceID string `json:"device_id"`

	// HostID is a UUID-format string representing the "hardware" identity.
	// Can be derived from OS sources or generated uniquely per account.
	// Claude Code uses getMachineId() from OpenTelemetry host detectors.
	HostID string `json:"host_id"`

	// AccountID is the UUID of the Anthropic account from the OAuth token.
	// Extracted from the token's "sub" claim or account metadata.
	AccountID string `json:"account_id"`

	// CreatedAt tracks when this identity was created.
	CreatedAt int64 `json:"created_at"`
}

// deviceIdentityCache caches the current device identity in memory.
// This is regenerated per session (app startup) to simulate ephemeral session IDs.
var (
	deviceIdentityCache   *DeviceIdentity
	deviceIdentityCacheMu sync.RWMutex
	currentSessionID      string
	currentSessionIDMu    sync.RWMutex

	// perAccountIdentityCache caches per-account DeviceIdentities keyed by account ID.
	perAccountIdentityCache   map[string]*DeviceIdentity
	perAccountIdentityCacheMu sync.RWMutex
)

// GenerateDeviceIdentity creates a new unique device identity for an account.
// Each call generates fresh identifiers to ensure uniqueness per account.
func GenerateDeviceIdentity(accountUUID string) *DeviceIdentity {
	return &DeviceIdentity{
		DeviceID:  generateDeviceID(),
		HostID:    generateHostID(),
		AccountID: accountUUID,
		CreatedAt: time.Now().Unix(),
	}
}

// generateDeviceID creates a 64-character hex string (32 bytes).
// This matches Claude Code's crypto.randomBytes(32).toString('hex').
func generateDeviceID() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("%016x%016x%016x%016x",
			time.Now().UnixNano(),
			time.Now().UnixNano()^0xDEADBEEF,
			time.Now().UnixNano()^0xCAFEBABE,
			time.Now().UnixNano()^0x8BADF00D)
	}
	return hex.EncodeToString(bytes)
}

// generateHostID creates a UUID-format identifier.
// For isolation between accounts, we generate a unique UUID per account
// rather than using the real hardware ID (which would correlate accounts).
func generateHostID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			time.Now().Unix()&0xFFFFFFFF,
			(time.Now().UnixNano()>>16)&0xFFFF,
			(time.Now().UnixNano()>>32)&0xFFFF,
			(time.Now().UnixNano()>>48)&0xFFFF,
			time.Now().UnixNano()&0xFFFFFFFFFFFF)
	}

	// Set UUID version 4 bits
	bytes[6] = (bytes[6] & 0x0F) | 0x40
	bytes[8] = (bytes[8] & 0x3F) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

// FormatUserID builds the composite user_id string that Claude Code sends
// in the metadata of every API request.
// Format: "user_<device_id>_account_<account_uuid>_session_<session_uuid>"
func (d *DeviceIdentity) FormatUserID(sessionID string) string {
	if d == nil {
		return ""
	}
	if sessionID == "" {
		sessionID = GetOrCreateSessionID()
	}
	return fmt.Sprintf("user_%s_account_%s_session_%s",
		d.DeviceID, d.AccountID, sessionID)
}

// GetOrCreateSessionID returns the current session ID, creating one if needed.
// Session ID is ephemeral - generated once per application startup.
func GetOrCreateSessionID() string {
	currentSessionIDMu.RLock()
	if currentSessionID != "" {
		defer currentSessionIDMu.RUnlock()
		return currentSessionID
	}
	currentSessionIDMu.RUnlock()

	currentSessionIDMu.Lock()
	defer currentSessionIDMu.Unlock()

	// Double-check after acquiring write lock
	if currentSessionID != "" {
		return currentSessionID
	}

	// Generate new session ID (UUID format)
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		currentSessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())
		return currentSessionID
	}

	// Set UUID version 4 bits
	bytes[6] = (bytes[6] & 0x0F) | 0x40
	bytes[8] = (bytes[8] & 0x3F) | 0x80

	currentSessionID = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
	return currentSessionID
}

// ResetSessionID clears the cached session ID.
// Useful for testing or when explicitly starting a new "session".
func ResetSessionID() {
	currentSessionIDMu.Lock()
	defer currentSessionIDMu.Unlock()
	currentSessionID = ""
}

// GetCachedDeviceIdentity returns the cached device identity if available.
func GetCachedDeviceIdentity() *DeviceIdentity {
	deviceIdentityCacheMu.RLock()
	defer deviceIdentityCacheMu.RUnlock()
	return deviceIdentityCache
}

// SetCachedDeviceIdentity sets the cached device identity.
func SetCachedDeviceIdentity(identity *DeviceIdentity) {
	deviceIdentityCacheMu.Lock()
	defer deviceIdentityCacheMu.Unlock()
	deviceIdentityCache = identity
}

// LoadDeviceIdentity loads the device identity from the OAuth config file.
// Returns nil if no identity is stored.
func LoadDeviceIdentity() (*DeviceIdentity, error) {
	config, err := LoadOAuthConfig()
	if err != nil {
		return nil, err
	}
	if config == nil || config.DeviceIdentity == nil {
		return nil, nil
	}
	return config.DeviceIdentity, nil
}

// SaveDeviceIdentity saves the device identity to the OAuth config file.
func SaveDeviceIdentity(identity *DeviceIdentity) error {
	config, err := LoadOAuthConfig()
	if err != nil {
		// Create new config if none exists
		config = &OAuthConfig{}
	}

	config.DeviceIdentity = identity
	return SaveOAuthConfig(config)
}

// GetOrCreateDeviceIdentity returns the existing device identity for the current
// OAuth account, or creates a new one if none exists.
// The accountUUID should be extracted from the OAuth token.
func GetOrCreateDeviceIdentity(accountUUID string) (*DeviceIdentity, error) {
	// First check cache
	if cached := GetCachedDeviceIdentity(); cached != nil {
		return cached, nil
	}

	// Try to load from storage
	identity, err := LoadDeviceIdentity()
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load device identity: %w", err)
	}

	// If no identity exists or account changed, create new one
	if identity == nil || (accountUUID != "" && identity.AccountID != accountUUID) {
		if accountUUID == "" {
			accountUUID = generateAccountUUID()
		}
		identity = GenerateDeviceIdentity(accountUUID)
		if err := SaveDeviceIdentity(identity); err != nil {
			return nil, fmt.Errorf("failed to save device identity: %w", err)
		}
	}

	// Cache for future use
	SetCachedDeviceIdentity(identity)
	return identity, nil
}

// generateAccountUUID creates a placeholder account UUID if none is available.
// This should ideally be extracted from the OAuth token's "sub" claim.
func generateAccountUUID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("account_%d", time.Now().UnixNano())
	}
	bytes[6] = (bytes[6] & 0x0F) | 0x40
	bytes[8] = (bytes[8] & 0x3F) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

// ExtractAccountUUIDFromToken attempts to extract the account UUID from an OAuth token.
// This parses the JWT access token to get the "sub" claim.
func ExtractAccountUUIDFromToken(accessToken string) string {
	if accessToken == "" {
		return ""
	}

	// JWT format: header.payload.signature
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return ""
	}

	// Decode payload (base64url)
	payload := parts[1]
	// Add padding if needed
	if l := len(payload) % 4; l > 0 {
		payload += strings.Repeat("=", 4-l)
	}

	// Try standard base64 first, then raw base64
	decoded, err := decodeBase64(payload)
	if err != nil {
		return ""
	}

	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	return claims.Sub
}

// decodeBase64 decodes a base64url-encoded JWT payload segment.
// JWT uses base64url encoding (RFC 4648 §5) without padding.
func decodeBase64(s string) ([]byte, error) {
	// JWT segments use raw base64url (no padding, URL-safe alphabet).
	// Try RawURLEncoding first (correct for JWT), then fall back to
	// padded variants for robustness.
	if decoded, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}

	// Some implementations include padding — try with padding stripped/added.
	// Normalise to raw URL encoding by stripping any trailing '=' padding.
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

// ClearDeviceIdentity removes the stored device identity.
// This should be called when logging out or switching to a different account.
func ClearDeviceIdentity() error {
	deviceIdentityCacheMu.Lock()
	deviceIdentityCache = nil
	deviceIdentityCacheMu.Unlock()

	config, err := LoadOAuthConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	config.DeviceIdentity = nil
	return SaveOAuthConfig(config)
}

// DeviceIdentityPath returns the path where device identity is stored.
// This is the same as the OAuth config path (~/.swarm/config/oauth/anthropic.json).
func DeviceIdentityPath() (string, error) {
	return GetOAuthConfigPath()
}

// EnsureDeviceIdentityForToken ensures a device identity exists for the given token.
// This is called after a successful OAuth login to generate identity if needed.
func EnsureDeviceIdentityForToken(token *OAuthToken) (*DeviceIdentity, error) {
	if token == nil {
		return nil, fmt.Errorf("token is nil")
	}

	// Try to extract account UUID from token
	accountUUID := ExtractAccountUUIDFromToken(token.AccessToken)

	return GetOrCreateDeviceIdentity(accountUUID)
}

// GetCurrentDeviceIdentity is a convenience function that loads the current
// device identity from storage and returns it, creating one if needed.
// This should be called at provider initialization time.
func GetCurrentDeviceIdentity() (*DeviceIdentity, error) {
	// Check cache first
	if cached := GetCachedDeviceIdentity(); cached != nil {
		return cached, nil
	}

	// Load from storage
	identity, err := LoadDeviceIdentity()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	if identity != nil {
		SetCachedDeviceIdentity(identity)
		return identity, nil
	}

	// No identity exists - will be created when token is available
	return nil, nil
}

// MigrateDeviceIdentityFromClaudeJSON attempts to read device ID from existing
// Claude Code installation (~/.claude.json) for users migrating from Claude Code.
// Returns nil if no Claude Code config exists.
func MigrateDeviceIdentityFromClaudeJSON() (*DeviceIdentity, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	claudeConfigPath := filepath.Join(homeDir, ".claude.json")
	data, err := os.ReadFile(claudeConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No Claude Code config, not an error
		}
		return nil, err
	}

	var config struct {
		UserID string `json:"userID"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse .claude.json: %w", err)
	}

	if config.UserID == "" {
		return nil, nil
	}

	// Create identity with the migrated device ID
	return &DeviceIdentity{
		DeviceID:  config.UserID,
		HostID:    generateHostID(), // Generate new host ID for isolation
		AccountID: "",               // Will be set when OAuth token is available
		CreatedAt: time.Now().Unix(),
	}, nil
}

// ============================================================================
// Per-account DeviceIdentity — multi-account support
// ============================================================================

// GetOrCreateDeviceIdentityForAccount returns the stable DeviceIdentity for the
// given account ID, creating and persisting a fresh one if none exists.
//
// This is used instead of GetOrCreateDeviceIdentity when multiple OAuth accounts
// are active simultaneously so each account carries a distinct device_id / host_id
// in its API request metadata. Using a shared identity across accounts would allow
// server-side correlation.
func GetOrCreateDeviceIdentityForAccount(accountID string) (*DeviceIdentity, error) {
	if accountID == "" {
		// Fall back to the global single-slot identity for anonymous sessions.
		return GetOrCreateDeviceIdentity("")
	}

	// Check in-memory cache first.
	perAccountIdentityCacheMu.RLock()
	if perAccountIdentityCache != nil {
		if identity, ok := perAccountIdentityCache[accountID]; ok {
			perAccountIdentityCacheMu.RUnlock()
			return identity, nil
		}
	}
	perAccountIdentityCacheMu.RUnlock()

	// Load from persisted storage.
	config, err := LoadOAuthConfig()
	if err != nil {
		config = &OAuthConfig{}
	}

	if config.AccountIdentities != nil {
		if identity, ok := config.AccountIdentities[accountID]; ok {
			cachePerAccountIdentity(accountID, identity)
			return identity, nil
		}
	}

	// None found — generate a new unique identity for this account.
	identity := GenerateDeviceIdentity(accountID)

	// Persist it.
	if config.AccountIdentities == nil {
		config.AccountIdentities = make(map[string]*DeviceIdentity)
	}
	config.AccountIdentities[accountID] = identity
	if saveErr := SaveOAuthConfig(config); saveErr != nil {
		return nil, fmt.Errorf("failed to save per-account device identity: %w", saveErr)
	}

	cachePerAccountIdentity(accountID, identity)
	return identity, nil
}

// cachePerAccountIdentity stores an identity in the in-memory per-account cache.
func cachePerAccountIdentity(accountID string, identity *DeviceIdentity) {
	perAccountIdentityCacheMu.Lock()
	defer perAccountIdentityCacheMu.Unlock()
	if perAccountIdentityCache == nil {
		perAccountIdentityCache = make(map[string]*DeviceIdentity)
	}
	perAccountIdentityCache[accountID] = identity
}

// ClearPerAccountIdentityCache clears the in-memory per-account identity cache.
// Useful for testing or when logging out all accounts.
func ClearPerAccountIdentityCache() {
	perAccountIdentityCacheMu.Lock()
	defer perAccountIdentityCacheMu.Unlock()
	perAccountIdentityCache = nil
}
