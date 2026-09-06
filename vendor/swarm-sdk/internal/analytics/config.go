package analytics

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

const (
	// Telemetry is OPT-IN and OFF BY DEFAULT. There is intentionally no
	// default collector endpoint or auth token baked into the source: a user
	// who wants to send analytics must explicitly opt in (SWARM_ANALYTICS_ENABLED=1)
	// AND point it at their own collector (SWARM_ANALYTICS_COLLECTOR_URL) with
	// their own credential (SWARM_ANALYTICS_AUTH_TOKEN). With no env vars set,
	// Enabled() is false and nothing is spooled or uploaded.
	DefaultCollectorURL = ""
	DefaultAuthToken    = ""
	DefaultSource       = "swarm-tui"
)

type Config struct {
	CollectorURL         string
	AuthToken            string
	Source               string
	AppVersion           string
	BatchSize            int
	FlushInterval        time.Duration
	SpoolDir             string
	MaxSpoolBytes        int64
	MaxSpoolFiles        int           // delete oldest events beyond this count (0 = no limit)
	MaxSpoolAge          time.Duration // delete events older than this even if undelivered (0 = no limit)
	MachineIDNamespace   string
	DeviceIDNamespace    string
	WorkspaceNamespace   string
	RequestTimeout       time.Duration
	ShutdownFlushTimeout time.Duration
	// OptIn must be explicitly set (SWARM_ANALYTICS_ENABLED) for any telemetry
	// to run. Defaults to false so the open-source build never phones home.
	OptIn    bool
	Disabled bool
}

func ConfigFromEnv() Config {
	spoolDir := paths.In("analytics-spool")
	return Config{
		CollectorURL:         envString("SWARM_ANALYTICS_COLLECTOR_URL", DefaultCollectorURL),
		AuthToken:            envAuthToken(),
		Source:               envString("SWARM_ANALYTICS_SOURCE", DefaultSource),
		AppVersion:           strings.TrimSpace(os.Getenv("SWARM_ANALYTICS_APP_VERSION")),
		BatchSize:            envInt("SWARM_ANALYTICS_BATCH_SIZE", 25),
		FlushInterval:        envDuration("SWARM_ANALYTICS_FLUSH_INTERVAL", 10*time.Second),
		SpoolDir:             envString("SWARM_ANALYTICS_SPOOL_DIR", spoolDir),
		MaxSpoolBytes:        envInt64("SWARM_ANALYTICS_MAX_SPOOL_BYTES", 256<<20),
		MaxSpoolFiles:        envInt("SWARM_ANALYTICS_MAX_SPOOL_FILES", 2000),
		MaxSpoolAge:          envDuration("SWARM_ANALYTICS_MAX_SPOOL_AGE", 7*24*time.Hour),
		MachineIDNamespace:   envString("SWARM_ANALYTICS_MACHINE_NAMESPACE", "swarm.analytics.v1"),
		DeviceIDNamespace:    envString("SWARM_ANALYTICS_DEVICE_NAMESPACE", "swarm.analytics.device.v1"),
		WorkspaceNamespace:   envString("SWARM_ANALYTICS_WORKSPACE_NAMESPACE", "swarm.analytics.workspace.v1"),
		RequestTimeout:       envDuration("SWARM_ANALYTICS_REQUEST_TIMEOUT", 10*time.Second),
		ShutdownFlushTimeout: envDuration("SWARM_ANALYTICS_SHUTDOWN_FLUSH_TIMEOUT", 60*time.Second),
		OptIn:                envBool("SWARM_ANALYTICS_ENABLED", false),
		Disabled:             envBool("SWARM_ANALYTICS_DISABLED", false),
	}
}

// Enabled reports whether telemetry may run. It is deliberately conservative:
// the user must explicitly opt in AND supply their own collector URL and auth
// token. The open-source build ships none of these, so telemetry is inert
// unless a user knowingly configures it. SWARM_ANALYTICS_DISABLED is honored as
// a hard kill switch even if opt-in was set.
func (c Config) Enabled() bool {
	if c.Disabled || !c.OptIn {
		return false
	}
	return strings.TrimSpace(c.CollectorURL) != "" && strings.TrimSpace(c.AuthToken) != ""
}

func (c Config) withDefaults() Config {
	if c.BatchSize <= 0 {
		c.BatchSize = 25
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 10 * time.Second
	}
	if c.MaxSpoolBytes <= 0 {
		c.MaxSpoolBytes = 256 << 20
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 10 * time.Second
	}
	if c.ShutdownFlushTimeout <= 0 {
		c.ShutdownFlushTimeout = 60 * time.Second
	}
	return c
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envAuthToken() string {
	value, ok := os.LookupEnv("SWARM_ANALYTICS_AUTH_TOKEN")
	if !ok {
		return DefaultAuthToken
	}
	return strings.TrimSpace(value)
}

func envInt(key string, fallback int) int {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		switch strings.ToLower(value) {
		case "1", "true", "t", "yes", "y", "on":
			return true
		case "0", "false", "f", "no", "n", "off":
			return false
		}
	}
	return fallback
}
