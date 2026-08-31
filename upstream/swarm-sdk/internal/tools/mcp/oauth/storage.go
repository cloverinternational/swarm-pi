package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// TokenStorage defines the interface for persisting OAuth tokens.
type TokenStorage interface {
	// SaveToken saves a token for a server URL.
	SaveToken(serverURL string, token *TokenData) error

	// LoadToken loads a token for a server URL.
	LoadToken(serverURL string) (*TokenData, error)

	// LoadTokenForURL loads a token and validates it matches the server URL.
	// Returns an error if the token exists but is for a different URL.
	LoadTokenForURL(serverURL string) (*TokenData, error)

	// DeleteToken deletes a token for a server URL.
	DeleteToken(serverURL string) error

	// ListTokens returns all stored server URLs.
	ListTokens() ([]string, error)

	// SaveClientInfo saves client registration information.
	SaveClientInfo(serverURL string, clientInfo *ClientInfo) error

	// LoadClientInfo loads client registration information.
	// Returns an error if the client secret has expired.
	LoadClientInfo(serverURL string) (*ClientInfo, error)

	// DeleteClientInfo deletes client registration information.
	DeleteClientInfo(serverURL string) error
}

// FileTokenStorage implements TokenStorage using a JSON file.
type FileTokenStorage struct {
	path string
	mu   sync.RWMutex
}

// tokenStore is the internal structure for the JSON file.
type tokenStore struct {
	Tokens      map[string]*TokenData  `json:"tokens"`
	ClientInfos map[string]*ClientInfo `json:"client_infos,omitempty"`
}

// NewFileTokenStorage creates a new file-based token storage.
// If path is empty, uses ~/.swarm/config/mcp_oauth_tokens.json
func NewFileTokenStorage(path string) (*FileTokenStorage, error) {
	if path == "" {
		path = filepath.Join(paths.Config(), "mcp_oauth_tokens.json")
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	return &FileTokenStorage{path: path}, nil
}

// SaveToken saves a token for a server URL.
func (s *FileTokenStorage) SaveToken(serverURL string, token *TokenData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure ServerURL is set for validation
	token.ServerURL = serverURL

	return atomicfile.WithLock(s.path, func() error {
		store, err := s.loadStore()
		if err != nil {
			return err
		}
		store.Tokens[serverURL] = token
		return s.saveStore(store)
	})
}

// LoadToken loads a token for a server URL.
func (s *FileTokenStorage) LoadToken(serverURL string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	store, err := s.loadStore()
	if err != nil {
		return nil, err
	}

	token, ok := store.Tokens[serverURL]
	if !ok {
		return nil, nil // Not found, but not an error
	}

	return token, nil
}

// LoadTokenForURL loads a token and validates it matches the server URL.
// Returns an error if the token exists but is for a different URL.
func (s *FileTokenStorage) LoadTokenForURL(serverURL string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	store, err := s.loadStore()
	if err != nil {
		return nil, err
	}

	token, ok := store.Tokens[serverURL]
	if !ok {
		return nil, nil // Not found
	}

	// Validate URL matches (security check)
	// If ServerURL is empty, this is an old token from before URL tracking
	// was added - allow it for backward compatibility but log a warning
	if token.ServerURL != "" && token.ServerURL != serverURL {
		return nil, fmt.Errorf("token URL mismatch: stored for %s, requested for %s (token may have been copied or server URL changed)",
			token.ServerURL, serverURL)
	}

	return token, nil
}

// DeleteToken deletes a token for a server URL.
func (s *FileTokenStorage) DeleteToken(serverURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return atomicfile.WithLock(s.path, func() error {
		store, err := s.loadStore()
		if err != nil {
			return err
		}
		delete(store.Tokens, serverURL)
		return s.saveStore(store)
	})
}

// ListTokens returns all stored server URLs.
func (s *FileTokenStorage) ListTokens() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	store, err := s.loadStore()
	if err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(store.Tokens))
	for url := range store.Tokens {
		urls = append(urls, url)
	}

	return urls, nil
}

// SaveClientInfo saves client registration information.
func (s *FileTokenStorage) SaveClientInfo(serverURL string, clientInfo *ClientInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return atomicfile.WithLock(s.path, func() error {
		store, err := s.loadStore()
		if err != nil {
			return err
		}
		if store.ClientInfos == nil {
			store.ClientInfos = make(map[string]*ClientInfo)
		}
		store.ClientInfos[serverURL] = clientInfo
		return s.saveStore(store)
	})
}

// LoadClientInfo loads client registration information.
// Returns an error if the client secret has expired.
func (s *FileTokenStorage) LoadClientInfo(serverURL string) (*ClientInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	store, err := s.loadStore()
	if err != nil {
		return nil, err
	}

	clientInfo, ok := store.ClientInfos[serverURL]
	if !ok {
		return nil, nil // Not found
	}

	// Check if expired
	if clientInfo.IsExpired() {
		return nil, fmt.Errorf("client secret expired at %s - re-registration required",
			clientInfo.ClientSecretExpiresAt.Format(time.RFC3339))
	}

	return clientInfo, nil
}

// DeleteClientInfo deletes client registration information.
func (s *FileTokenStorage) DeleteClientInfo(serverURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return atomicfile.WithLock(s.path, func() error {
		store, err := s.loadStore()
		if err != nil {
			return err
		}
		delete(store.ClientInfos, serverURL)
		return s.saveStore(store)
	})
}

// loadStore loads the token store from disk.
func (s *FileTokenStorage) loadStore() (*tokenStore, error) {
	store := &tokenStore{
		Tokens:      make(map[string]*TokenData),
		ClientInfos: make(map[string]*ClientInfo),
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil // Empty store
		}
		return nil, fmt.Errorf("failed to read token file: %w", err)
	}

	if err := json.Unmarshal(data, store); err != nil {
		return nil, fmt.Errorf("failed to parse token file: %w", err)
	}

	if store.Tokens == nil {
		store.Tokens = make(map[string]*TokenData)
	}

	if store.ClientInfos == nil {
		store.ClientInfos = make(map[string]*ClientInfo)
	}

	return store, nil
}

// saveStore saves the token store to disk atomically at 0600. Callers hold
// both s.mu and the cross-process advisory lock for s.path.
func (s *FileTokenStorage) saveStore(store *tokenStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}

	// Write with restricted permissions (owner read/write only, the default).
	if err := atomicfile.Write(s.path, data); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}
	return nil
}

// MemoryTokenStorage implements TokenStorage using in-memory storage.
// Useful for testing or ephemeral use cases.
type MemoryTokenStorage struct {
	tokens      map[string]*TokenData
	clientInfos map[string]*ClientInfo
	mu          sync.RWMutex
}

// NewMemoryTokenStorage creates a new in-memory token storage.
func NewMemoryTokenStorage() *MemoryTokenStorage {
	return &MemoryTokenStorage{
		tokens:      make(map[string]*TokenData),
		clientInfos: make(map[string]*ClientInfo),
	}
}

// SaveToken saves a token for a server URL.
func (s *MemoryTokenStorage) SaveToken(serverURL string, token *TokenData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	token.ServerURL = serverURL
	s.tokens[serverURL] = token
	return nil
}

// LoadToken loads a token for a server URL.
func (s *MemoryTokenStorage) LoadToken(serverURL string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokens[serverURL], nil
}

// LoadTokenForURL loads a token and validates it matches the server URL.
func (s *MemoryTokenStorage) LoadTokenForURL(serverURL string) (*TokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	token := s.tokens[serverURL]
	if token == nil {
		return nil, nil
	}

	// Validate URL matches
	if token.ServerURL != "" && token.ServerURL != serverURL {
		return nil, fmt.Errorf("token URL mismatch: stored for %s, requested for %s",
			token.ServerURL, serverURL)
	}

	return token, nil
}

// DeleteToken deletes a token for a server URL.
func (s *MemoryTokenStorage) DeleteToken(serverURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, serverURL)
	return nil
}

// ListTokens returns all stored server URLs.
func (s *MemoryTokenStorage) ListTokens() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	urls := make([]string, 0, len(s.tokens))
	for url := range s.tokens {
		urls = append(urls, url)
	}
	return urls, nil
}

// SaveClientInfo saves client registration information.
func (s *MemoryTokenStorage) SaveClientInfo(serverURL string, clientInfo *ClientInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clientInfos[serverURL] = clientInfo
	return nil
}

// LoadClientInfo loads client registration information.
func (s *MemoryTokenStorage) LoadClientInfo(serverURL string) (*ClientInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clientInfo := s.clientInfos[serverURL]
	if clientInfo == nil {
		return nil, nil
	}

	if clientInfo.IsExpired() {
		return nil, fmt.Errorf("client secret expired at %s",
			clientInfo.ClientSecretExpiresAt.Format(time.RFC3339))
	}

	return clientInfo, nil
}

// DeleteClientInfo deletes client registration information.
func (s *MemoryTokenStorage) DeleteClientInfo(serverURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clientInfos, serverURL)
	return nil
}
