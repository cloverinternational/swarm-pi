package ollama

import (
	"testing"
	"time"
)

func TestDefaultTimeoutIsFifteenMinutes(t *testing.T) {
	if DefaultTimeout != 15*time.Minute {
		t.Fatalf("DefaultTimeout = %s, want %s", DefaultTimeout, 15*time.Minute)
	}

	cfg := Config{}
	cfg.ApplyDefaults()
	if cfg.Timeout != DefaultTimeout {
		t.Fatalf("Timeout = %s, want %s", cfg.Timeout, DefaultTimeout)
	}
}
