package account

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileStorage implements RegistryStorage with file-based persistence
type FileStorage struct {
	baseDir string
	mu      sync.RWMutex
}

// NewFileStorage creates a new file storage
func NewFileStorage(baseDir string) *FileStorage {
	return &FileStorage{
		baseDir: baseDir,
	}
}

// StoredAccountData is the on-disk format for accounts and defaults
type StoredAccountData struct {
	Version  string              `json:"version"`
	Accounts []StoredAccountInfo `json:"accounts"`
	Defaults map[string]string   `json:"defaults"`
	SavedAt  time.Time           `json:"saved_at"`
}

// StoredAccountInfo is the on-disk format for an account
type StoredAccountInfo struct {
	Provider    string         `json:"provider"`
	AccountID   string         `json:"account_id"`
	DisplayName string         `json:"display_name"`
	Active      bool           `json:"active"`
	CreatedAt   time.Time      `json:"created_at"`
	LastUsed    time.Time      `json:"last_used"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// LoadAccounts loads accounts from storage
func (fs *FileStorage) LoadAccounts() (map[string]AccountIdentity, map[string]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	accountsPath := fs.getAccountsPath()

	// If file doesn't exist, return empty
	if _, err := os.Stat(accountsPath); os.IsNotExist(err) {
		return make(map[string]AccountIdentity), make(map[string]string), nil
	}

	data, err := os.ReadFile(accountsPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read accounts file: %w", err)
	}

	var stored StoredAccountData
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, nil, fmt.Errorf("failed to parse accounts file: %w", err)
	}

	// Convert to runtime format
	accounts := make(map[string]AccountIdentity)
	for _, accountInfo := range stored.Accounts {
		identity := &StandardAccountIdentity{
			ProviderName: accountInfo.Provider,
			AccountID:    accountInfo.AccountID,
			Name:         accountInfo.DisplayName,
			Active:       accountInfo.Active,
			Created:      accountInfo.CreatedAt,
			Used:         accountInfo.LastUsed,
		}
		key := makeKey(accountInfo.Provider, accountInfo.AccountID)
		accounts[key] = identity
	}

	return accounts, stored.Defaults, nil
}

// SaveAccounts saves accounts to storage
func (fs *FileStorage) SaveAccounts(accounts map[string]AccountIdentity, defaults map[string]string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Convert to storage format
	var accountList []StoredAccountInfo
	for _, identity := range accounts {
		info := StoredAccountInfo{
			Provider:    identity.Provider(),
			AccountID:   identity.ID(),
			DisplayName: identity.DisplayName(),
			Active:      identity.IsActive(),
			CreatedAt:   identity.CreatedAt(),
			LastUsed:    identity.LastUsed(),
		}
		accountList = append(accountList, info)
	}

	stored := StoredAccountData{
		Version:  "1.0",
		Accounts: accountList,
		Defaults: defaults,
		SavedAt:  time.Now(),
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal accounts: %w", err)
	}

	// Write atomically using temp file
	accountsPath := fs.getAccountsPath()
	tmpPath := accountsPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp accounts file: %w", err)
	}

	if err := os.Rename(tmpPath, accountsPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename accounts file: %w", err)
	}

	return nil
}

// LoadToken loads a token from storage
func (fs *FileStorage) LoadToken(accountKey string) (*OAuthToken, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	tokenPath := fs.getTokenPath(accountKey)

	// If file doesn't exist, return nil (not an error)
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read token file: %w", err)
	}

	var token OAuthToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("failed to parse token file: %w", err)
	}

	return &token, nil
}

// SaveToken saves a token to storage
func (fs *FileStorage) SaveToken(accountKey string, token *OAuthToken) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if token == nil {
		return fmt.Errorf("token cannot be nil")
	}

	// Marshal to JSON with restricted permissions
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal token: %w", err)
	}

	// Create provider subdirectory if needed
	tokenPath := fs.getTokenPath(accountKey)
	tokenDir := filepath.Dir(tokenPath)
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		return fmt.Errorf("failed to create token directory: %w", err)
	}

	// Write atomically using temp file
	tmpPath := tokenPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp token file: %w", err)
	}

	if err := os.Rename(tmpPath, tokenPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename token file: %w", err)
	}

	return nil
}

// DeleteToken removes a token from storage
func (fs *FileStorage) DeleteToken(accountKey string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	tokenPath := fs.getTokenPath(accountKey)
	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete token file: %w", err)
	}
	return nil
}

// DeleteAccount removes an account completely
func (fs *FileStorage) DeleteAccount(accountKey string) error {
	return fs.DeleteToken(accountKey)
}

// Path helpers

func (fs *FileStorage) getAccountsPath() string {
	return filepath.Join(fs.baseDir, "accounts.json")
}

func (fs *FileStorage) getTokenPath(accountKey string) string {
	// accountKey is "provider::id"
	// Store as provider/account-{id}.json
	provider := keyProvider(accountKey)
	accountID := keyAccountID(accountKey)
	filename := fmt.Sprintf("account-%s.json", accountID)
	return filepath.Join(fs.baseDir, provider, filename)
}

// RegistryMigration handles migration from old storage format
type RegistryMigration struct {
	oldOpenAIPath    string
	oldAnthropicPath string
	newStoragePath   string
}

// NewRegistryMigration creates a new migration
func NewRegistryMigration(oldOpenAIPath, oldAnthropicPath, newStoragePath string) *RegistryMigration {
	return &RegistryMigration{
		oldOpenAIPath:    oldOpenAIPath,
		oldAnthropicPath: oldAnthropicPath,
		newStoragePath:   newStoragePath,
	}
}

// Migrate performs the migration
func (m *RegistryMigration) Migrate() error {
	// This would be implemented to migrate from old oauth.json files
	// to the new multi-account format
	// For now, return nil as this is a future enhancement
	return nil
}

// Helper to extract account ID from key
func keyAccountID(key string) string {
	for i := 0; i < len(key)-1; i++ {
		if key[i:i+2] == "::" {
			return key[i+2:]
		}
	}
	return ""
}

// BackupRegistry creates a backup of the registry
func (fs *FileStorage) BackupRegistry() (string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	accountsPath := fs.getAccountsPath()
	if _, err := os.Stat(accountsPath); os.IsNotExist(err) {
		return "", nil // No backup needed if file doesn't exist
	}

	// Create backup with timestamp
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	backupPath := accountsPath + "." + timestamp + ".bak"

	// Copy file
	srcFile, err := os.Open(accountsPath)
	if err != nil {
		return "", fmt.Errorf("failed to open accounts file for backup: %w", err)
	}
	defer srcFile.Close()

	backupFile, err := os.Create(backupPath)
	if err != nil {
		return "", fmt.Errorf("failed to create backup file: %w", err)
	}
	defer backupFile.Close()

	if _, err := io.Copy(backupFile, srcFile); err != nil {
		os.Remove(backupPath)
		return "", fmt.Errorf("failed to copy accounts file for backup: %w", err)
	}

	return backupPath, nil
}

// ValidateStorage checks that storage is accessible
func (fs *FileStorage) ValidateStorage() error {
	// Check if base directory is accessible
	if stat, err := os.Stat(fs.baseDir); err != nil {
		return fmt.Errorf("storage directory not accessible: %w", err)
	} else if !stat.IsDir() {
		return fmt.Errorf("storage path is not a directory")
	}

	// Try to write a test file
	testPath := filepath.Join(fs.baseDir, ".write-test")
	if err := os.WriteFile(testPath, []byte("test"), 0600); err != nil {
		return fmt.Errorf("storage directory is not writable: %w", err)
	}
	os.Remove(testPath)

	return nil
}
