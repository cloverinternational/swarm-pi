package mcp

import (
	"fmt"
	"maps"
	"net/url"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/core"
)

type credentialError struct {
	Ref string
	Key string
}

func (e credentialError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("missing credentials for %s", e.Ref)
	}
	return fmt.Sprintf("missing credential %s for %s", e.Key, e.Ref)
}

func missingCredentialError(ref, key string) error {
	return credentialError{Ref: ref, Key: key}
}

func isCredentialError(err error) bool {
	_, ok := err.(credentialError)
	return ok
}

func normalizeServerConfig(server ServerConfig) ServerConfig {
	cfg := server
	if cfg.Type == "" {
		if cfg.Command != "" {
			cfg.Type = "stdio"
		} else if cfg.URL != "" {
			cfg.Type = "http"
		}
	}
	if cfg.TimeoutSec == 0 {
		cfg.TimeoutSec = 30
	}
	if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	return cfg
}

func normalizeToolsConfig(tools *ToolsConfig) *ToolsConfig {
	if tools == nil {
		return &ToolsConfig{Mode: "all"}
	}
	copy := *tools
	if copy.Mode == "" {
		copy.Mode = "all"
	}
	return &copy
}

func resolveAuthConfig(server ServerConfig) AuthConfig {
	if server.Auth != nil {
		return *server.Auth
	}
	if server.Type == "http" || server.Type == "sse" {
		return AuthConfig{Mode: "bearer", Header: "Authorization", Prefix: "Bearer "}
	}
	return AuthConfig{Mode: "none"}
}

func applySecretEnv(runtime *RuntimeServer, cfg ServerConfig, cred *core.MCPCredential) error {
	if len(cfg.SecretEnv) == 0 {
		return nil
	}
	ref := credentialRef(cfg)
	if cred == nil {
		return missingCredentialError(ref, "")
	}
	for _, key := range cfg.SecretEnv {
		value, ok := getCredentialHeader(cred, key)
		if !ok {
			return missingCredentialError(ref, key)
		}
		if _, exists := runtime.Env[key]; exists {
			return fmt.Errorf("env key already set: %s", key)
		}
		runtime.Env[key] = value
	}
	return nil
}

func applySecretHeaders(runtime *RuntimeServer, cfg ServerConfig, cred *core.MCPCredential) error {
	if len(cfg.SecretHeaders) == 0 {
		return nil
	}
	ref := credentialRef(cfg)
	if cred == nil {
		return missingCredentialError(ref, "")
	}
	for _, key := range cfg.SecretHeaders {
		value, ok := getCredentialHeader(cred, key)
		if !ok {
			return missingCredentialError(ref, key)
		}
		if _, exists := runtime.Headers[key]; exists {
			return fmt.Errorf("header already set: %s", key)
		}
		runtime.Headers[key] = value
	}
	return nil
}

func applyAuth(runtime *RuntimeServer, cfg ServerConfig, auth AuthConfig, cred *core.MCPCredential) error {
	mode := strings.ToLower(auth.Mode)
	if mode == "" {
		mode = "none"
	}
	if mode == "none" {
		return nil
	}
	ref := credentialRef(cfg)
	credential := cred
	if credential == nil || credential.Token == "" {
		return missingCredentialError(ref, "token")
	}

	switch mode {
	case "bearer", "header":
		header := auth.Header
		if header == "" {
			header = "Authorization"
		}
		if _, exists := runtime.Headers[header]; exists {
			return nil
		}
		prefix := auth.Prefix
		if prefix == "" && mode == "bearer" {
			prefix = "Bearer "
		}
		runtime.Headers[header] = prefix + credential.Token
	case "env":
		if cfg.Type != "stdio" {
			return fmt.Errorf("auth env mode requires stdio transport")
		}
		if auth.Env == "" {
			return fmt.Errorf("auth env requires env key")
		}
		if _, exists := runtime.Env[auth.Env]; exists {
			return nil
		}
		runtime.Env[auth.Env] = credential.Token
	case "query":
		if cfg.Type == "stdio" {
			return fmt.Errorf("auth query mode requires http transport")
		}
		if auth.Query == "" {
			return fmt.Errorf("auth query requires query key")
		}
		parsed, err := url.Parse(runtime.ResolvedURL)
		if err != nil {
			return err
		}
		values := parsed.Query()
		if values.Get(auth.Query) == "" {
			values.Set(auth.Query, credential.Token)
			parsed.RawQuery = values.Encode()
			runtime.ResolvedURL = parsed.String()
		}
	default:
		return fmt.Errorf("invalid auth mode: %s", auth.Mode)
	}
	return nil
}

func copyStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	copy := make(map[string]string, len(input))
	maps.Copy(copy, input)
	return copy
}

func copyStringSlice(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	return append([]string{}, input...)
}
