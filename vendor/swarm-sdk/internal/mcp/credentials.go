package mcp

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-core/core"
)

// CredentialsStore loads and saves MCP credentials.
type CredentialsStore interface {
	Load(ctx context.Context) (*core.Credentials, error)
	Save(ctx context.Context, creds *core.Credentials) error
}

type noopCredentialsStore struct{}

func (s *noopCredentialsStore) Load(ctx context.Context) (*core.Credentials, error) {
	return &core.Credentials{}, nil
}

func (s *noopCredentialsStore) Save(ctx context.Context, creds *core.Credentials) error {
	return nil
}

// ConfigManagerCredentialsStore wraps a core.ConfigManager.
type ConfigManagerCredentialsStore struct {
	Manager core.ConfigManager
}

func (s *ConfigManagerCredentialsStore) Load(ctx context.Context) (*core.Credentials, error) {
	if s == nil || s.Manager == nil {
		return &core.Credentials{}, nil
	}
	return s.Manager.Credentials(), nil
}

func (s *ConfigManagerCredentialsStore) Save(ctx context.Context, creds *core.Credentials) error {
	if s == nil || s.Manager == nil {
		return nil
	}
	if err := s.Manager.SetCredentials(creds); err != nil {
		return err
	}
	return s.Manager.Save(ctx)
}

func credentialRef(server ServerConfig) string {
	if server.CredentialRef != "" {
		return server.CredentialRef
	}
	return server.Name
}

func getCredential(creds *core.Credentials, ref string) *core.MCPCredential {
	if creds == nil || creds.MCP == nil {
		return nil
	}
	cred, ok := creds.MCP[ref]
	if !ok {
		return nil
	}
	copy := cred
	if cred.Headers != nil {
		copy.Headers = copyStringMap(cred.Headers)
	}
	return &copy
}

func hasCredential(store CredentialsStore, server ServerConfig) bool {
	if store == nil {
		return false
	}
	creds, err := store.Load(context.Background())
	if err != nil {
		return false
	}
	cred := getCredential(creds, credentialRef(server))
	if cred == nil {
		return false
	}
	if cred.Token != "" {
		return true
	}
	return len(cred.Headers) > 0
}

func getCredentialHeader(cred *core.MCPCredential, key string) (string, bool) {
	if cred == nil || cred.Headers == nil {
		return "", false
	}
	value, ok := cred.Headers[key]
	return value, ok
}

func ensureCredential(creds *core.Credentials, ref string) core.MCPCredential {
	if creds.MCP == nil {
		creds.MCP = make(map[string]core.MCPCredential)
	}
	cred, ok := creds.MCP[ref]
	if !ok {
		cred = core.MCPCredential{}
	}
	if cred.Headers == nil {
		cred.Headers = make(map[string]string)
	}
	return cred
}
