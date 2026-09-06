package mcp

import "testing"

func TestValidateConfigRejectsDuplicateNames(t *testing.T) {
	cfg := &ConfigFile{
		SchemaVersion: schemaVersion,
		Servers: []ServerConfig{
			{Name: "alpha", Command: "node", Type: "stdio"},
			{Name: "alpha", Command: "node", Type: "stdio"},
		},
	}
	if err := ValidateConfigFile(cfg); err == nil {
		t.Fatalf("expected duplicate name error")
	}
}

func TestValidateConfigRejectsNonLocalHTTP(t *testing.T) {
	cfg := &ConfigFile{
		SchemaVersion: schemaVersion,
		Servers: []ServerConfig{
			{Name: "alpha", Type: "http", URL: "http://example.com/mcp"},
		},
	}
	if err := ValidateConfigFile(cfg); err == nil {
		t.Fatalf("expected http url not allowed error")
	}
}

func TestValidateConfigRejectsSecretEnvOverlap(t *testing.T) {
	cfg := &ConfigFile{
		SchemaVersion: schemaVersion,
		Servers: []ServerConfig{
			{
				Name:      "alpha",
				Type:      "stdio",
				Command:   "node",
				Env:       map[string]string{"API_KEY": "value"},
				SecretEnv: []string{"API_KEY"},
			},
		},
	}
	if err := ValidateConfigFile(cfg); err == nil {
		t.Fatalf("expected env/secret overlap error")
	}
}
