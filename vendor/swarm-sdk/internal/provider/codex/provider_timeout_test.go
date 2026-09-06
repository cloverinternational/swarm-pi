package codex

import (
	"testing"
	"time"
)

func TestCreateResilientHTTPClientCodexDefaultTimeout(t *testing.T) {
	for _, transportMode := range []string{"", "resilient", "default"} {
		t.Run(transportMode, func(t *testing.T) {
			client := createResilientHTTPClientCodex(Config{TransportMode: transportMode})
			if client.Timeout != defaultRequestTimeout {
				t.Fatalf("Timeout = %s, want %s", client.Timeout, defaultRequestTimeout)
			}
		})
	}
}

func TestCreateResilientHTTPClientCodexTimeoutOverrides(t *testing.T) {
	const configuredTimeout = 37 * time.Second

	client := createResilientHTTPClientCodex(Config{Timeout: int(configuredTimeout / time.Second)})
	if client.Timeout != configuredTimeout {
		t.Fatalf("configured Timeout = %s, want %s", client.Timeout, configuredTimeout)
	}

	client = createResilientHTTPClientCodex(Config{Timeout: -1})
	if client.Timeout != 0 {
		t.Fatalf("disabled Timeout = %s, want 0", client.Timeout)
	}
}
