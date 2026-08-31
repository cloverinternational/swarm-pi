package mcp

import (
	"context"
	"testing"
)

func TestImporterPreviewClaude(t *testing.T) {
	importer := NewImporter(&memoryCredentialsStore{})
	payload := ImportPayload{
		Text: `{"alpha":{"type":"stdio","command":"node","args":["server.js"],"env":{"API_KEY":"sek"}}}`,
	}
	result, err := importer.Preview(context.Background(), "claude", payload)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(result.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result.Servers))
	}
	server := result.Servers[0]
	if server.Config.Name != "alpha" || server.Config.Type != "stdio" || server.Config.Command != "node" {
		t.Fatalf("unexpected server config: %+v", server.Config)
	}
	if len(server.Config.SecretEnv) != 1 || server.Config.SecretEnv[0] != "API_KEY" {
		t.Fatalf("expected secret_env to include API_KEY")
	}
	if len(server.Config.Env) != 0 {
		t.Fatalf("expected env to be empty after import")
	}
	if server.SecretValues["API_KEY"] != "sek" {
		t.Fatalf("expected secret value to be extracted")
	}
}

func TestImporterPreviewCodex(t *testing.T) {
	importer := NewImporter(&memoryCredentialsStore{})
	payload := ImportPayload{
		Text: `
[mcp_servers.github]
type = "http"
url = "https://mcp.example.com"
headers = { Authorization = "Bearer sek" }
`,
	}
	result, err := importer.Preview(context.Background(), "codex", payload)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(result.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result.Servers))
	}
	server := result.Servers[0]
	if server.Config.Name != "github" || server.Config.Type != "http" {
		t.Fatalf("unexpected server config: %+v", server.Config)
	}
	if server.Config.Auth == nil || server.Config.Auth.Mode != "none" {
		t.Fatalf("expected auth mode none for imported http server")
	}
	if len(server.Config.SecretHeaders) != 1 || server.Config.SecretHeaders[0] != "Authorization" {
		t.Fatalf("expected secret_headers to include Authorization")
	}
	if server.SecretValues["Authorization"] != "Bearer sek" {
		t.Fatalf("expected secret header value to be extracted")
	}
}

func TestImporterPreviewJSONCanonical(t *testing.T) {
	importer := NewImporter(&memoryCredentialsStore{})
	payload := ImportPayload{
		Text: `{
  "schema_version": 1,
  "servers": [
    {
      "name": "alpha",
      "type": "http",
      "url": "https://example.com/mcp",
      "auth": {"mode": "none"},
      "headers": {"Authorization": "Bearer abc"}
    }
  ],
  "plugin_overrides": {
    "example": {
      "analytics": {"enabled": false}
    }
  }
}`,
	}
	result, err := importer.Preview(context.Background(), "json", payload)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected warning for plugin_overrides")
	}
	if len(result.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result.Servers))
	}
	server := result.Servers[0]
	if len(server.Config.SecretHeaders) != 1 || server.Config.SecretHeaders[0] != "Authorization" {
		t.Fatalf("expected secret_headers to include Authorization")
	}
	if len(server.Config.Headers) != 0 {
		t.Fatalf("expected headers to be cleared after import")
	}
	if server.SecretValues["Authorization"] != "Bearer abc" {
		t.Fatalf("expected secret header value to be extracted")
	}
}

func TestImporterPreviewTOMLMap(t *testing.T) {
	importer := NewImporter(&memoryCredentialsStore{})
	payload := ImportPayload{
		Text: `
[alpha]
type = "http"
url = "https://example.com/mcp"
headers = { Authorization = "$TOKEN" }
`,
	}
	result, err := importer.Preview(context.Background(), "toml", payload)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(result.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result.Servers))
	}
	server := result.Servers[0]
	if server.Config.Name != "alpha" || server.Config.Type != "http" {
		t.Fatalf("unexpected server config: %+v", server.Config)
	}
	if len(server.Config.SecretHeaders) != 1 || server.Config.SecretHeaders[0] != "Authorization" {
		t.Fatalf("expected secret_headers to include Authorization")
	}
	if _, ok := server.SecretValues["Authorization"]; ok {
		t.Fatalf("expected placeholder header value to be ignored")
	}
	if len(result.RequiredCredentials) != 1 || result.RequiredCredentials[0] != "alpha" {
		t.Fatalf("expected required credentials for alpha")
	}
}
