package client

import (
	"os"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// TestResolveCredentialsStaleCredentialsBaseURL recreates the "custom provider
// keeps going back to port 8080" bug:
//
//   - ~/.swarm/config/providers.json (written by the TUI settings editor) says the
//     "local" provider lives at http://localhost:8082/v1
//   - ~/.swarm/config/credentials.json (legacy file, written once when the key was
//     first saved) still holds a stale copy: http://localhost:8080/v1
//
// resolveCredentials finds the API key in credentials.json first and returns
// that file's base_url, so the user's providers.json edit is shadowed.
// Expected: base URL follows the user-edited providers.json (8082).
func TestResolveCredentialsStaleCredentialsBaseURL(t *testing.T) {
	// Isolate the SwarmOS root so resolveCredentials reads only this test's files.
	t.Setenv("SWARM_HOME", t.TempDir())
	t.Setenv("LOCAL_API_KEY", "")

	// Mirror of the user's real ~/.swarm/config/providers.json entry (TUI-edited, current).
	providersJSON := `[
	  {
	    "name": "local",
	    "display_name": "Local",
	    "type": "api_key",
	    "api_type": "openai-compatible",
	    "api_key": "vllm",
	    "base_url": "http://localhost:8082/v1",
	    "available": true,
	    "source": "local"
	  }
	]`
	if err := os.WriteFile(paths.ProvidersFile(), []byte(providersJSON), 0o600); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}

	// Mirror of the user's real ~/.swarm/config/credentials.json entry (stale copy).
	credentialsJSON := `{
	  "providers": {
	    "local": {
	      "api_key": "vllm",
	      "base_url": "http://localhost:8080/v1"
	    }
	  }
	}`
	if err := os.WriteFile(paths.CredentialsFile(), []byte(credentialsJSON), 0o600); err != nil {
		t.Fatalf("write credentials.json: %v", err)
	}

	key, baseURL := resolveCredentials("local")

	if key != "vllm" {
		t.Errorf("resolveCredentials(local) key = %q, want %q", key, "vllm")
	}
	if baseURL != "http://localhost:8082/v1" {
		t.Errorf("resolveCredentials(local) baseURL = %q, want %q (user-edited providers.json must win over stale credentials.json copy)",
			baseURL, "http://localhost:8082/v1")
	}
}
