package analytics

import (
	"os"
	"testing"
)

func TestConfigFromEnvDefaultsToDisabledOptIn(t *testing.T) {
	t.Setenv("SWARM_ANALYTICS_COLLECTOR_URL", "")
	t.Setenv("SWARM_ANALYTICS_SOURCE", "")
	t.Setenv("SWARM_ANALYTICS_DISABLED", "")
	t.Setenv("SWARM_ANALYTICS_ENABLED", "")
	unsetEnv(t, "SWARM_ANALYTICS_AUTH_TOKEN")

	cfg := ConfigFromEnv()

	if cfg.CollectorURL != "" {
		t.Fatalf("CollectorURL = %q, want empty default (no baked-in endpoint)", cfg.CollectorURL)
	}
	if cfg.Source != DefaultSource {
		t.Fatalf("Source = %q, want %q", cfg.Source, DefaultSource)
	}
	if cfg.AuthToken != "" {
		t.Fatalf("AuthToken = %q, want empty default (no baked-in token)", cfg.AuthToken)
	}
	if cfg.OptIn {
		t.Fatal("OptIn should default to false")
	}
	if cfg.Enabled() {
		t.Fatal("telemetry must be OFF by default (opt-in not set, no collector URL/token)")
	}
}

func TestConfigEnabledRequiresOptInUrlAndToken(t *testing.T) {
	t.Setenv("SWARM_ANALYTICS_DISABLED", "")
	// Opt-in alone is not enough without a collector URL + token.
	t.Setenv("SWARM_ANALYTICS_ENABLED", "1")
	t.Setenv("SWARM_ANALYTICS_COLLECTOR_URL", "")
	t.Setenv("SWARM_ANALYTICS_AUTH_TOKEN", "")
	if ConfigFromEnv().Enabled() {
		t.Fatal("opt-in without collector URL/token must stay disabled")
	}

	// URL + token but no opt-in is still disabled.
	t.Setenv("SWARM_ANALYTICS_ENABLED", "")
	t.Setenv("SWARM_ANALYTICS_COLLECTOR_URL", "https://example.test/collect")
	t.Setenv("SWARM_ANALYTICS_AUTH_TOKEN", "tok")
	if ConfigFromEnv().Enabled() {
		t.Fatal("collector URL/token without opt-in must stay disabled")
	}

	// All three present -> enabled.
	t.Setenv("SWARM_ANALYTICS_ENABLED", "1")
	cfg := ConfigFromEnv()
	if !cfg.Enabled() {
		t.Fatal("opt-in + collector URL + token should enable telemetry")
	}

	// Hard kill switch wins even with full opt-in config.
	t.Setenv("SWARM_ANALYTICS_DISABLED", "true")
	if ConfigFromEnv().Enabled() {
		t.Fatal("SWARM_ANALYTICS_DISABLED must override opt-in")
	}
}

func TestConfigFromEnvCanOverrideAuthToken(t *testing.T) {
	t.Setenv("SWARM_ANALYTICS_AUTH_TOKEN", "custom-token")

	cfg := ConfigFromEnv()

	if cfg.AuthToken != "custom-token" {
		t.Fatalf("AuthToken = %q, want custom-token", cfg.AuthToken)
	}
}

func TestConfigFromEnvCanDisableDefaultAuthToken(t *testing.T) {
	t.Setenv("SWARM_ANALYTICS_AUTH_TOKEN", "")

	cfg := ConfigFromEnv()

	if cfg.AuthToken != "" {
		t.Fatalf("AuthToken = %q, want empty override", cfg.AuthToken)
	}
}

func TestConfigFromEnvCanDisableDefaultAnalytics(t *testing.T) {
	t.Setenv("SWARM_ANALYTICS_DISABLED", "true")

	cfg := ConfigFromEnv()

	if cfg.Enabled() {
		t.Fatal("ConfigFromEnv should honor SWARM_ANALYTICS_DISABLED")
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	previous, hadPrevious := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if hadPrevious {
			_ = os.Setenv(key, previous)
			return
		}
		_ = os.Unsetenv(key)
	})
}
