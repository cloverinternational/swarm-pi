package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-core/pkg/pool"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configmigrate"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/serve"
	tuianalytics "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	attserver "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/server"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/metrics"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/termimage"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tui"
	zone "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/zone"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/update"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
	xterm "github.com/charmbracelet/x/term"
)

// Command-line flags
var (
	// Existing flags
	debugRender         = flag.Bool("debug-render", false, "Launch render sandbox mode for testing TUI rendering")
	debugLineageFixture = flag.Bool("debug-lineage-fixture", false, "Launch deterministic chat fixture for error-lineage visual testing")
	debugMode           = flag.Bool("debug", false, "Enable debug mode with DebugLogs tool for log inspection")
	newUIFlag           = flag.Bool("new-ui", false, "Use new modular chat UI (experimental)")
	helpFlag            = flag.Bool("help", false, "Show help message")
	versionFlag         = flag.Bool("version", false, "Show version and build information")
	noUpdateFlag        = flag.Bool("no-update", false, "Disable auto-update checks")
	a2aFlag             = flag.Bool("a2a", false, "(deprecated, always on) A2A task receiving is enabled by default")
	a2aHandleFlag       = flag.String("a2a-handle", "", "Override the A2A peer handle for this session")
	a2aListenFlag       = flag.String("a2a-listen", "127.0.0.1:0", "Local A2A listen address (default: dynamic port)")
	initialPromptFlag   = flag.String("initial-prompt", "", "Open the interactive TUI and submit this prompt once at startup")
	sessionIDFlag       = flag.String("session-id", "", "Internal recovery session identity")
	autoRecoverFlag     = flag.Bool("auto-recover", false, "Opt in to relaunch interrupted interactive TUI sessions at startup")

	// Headless mode flags
	promptFlag           = flag.String("p", "", "Execute headless with this prompt (enables headless mode)")
	providerFlag         = flag.String("P", "", "Override provider (claudecode, anthropic, openai, gemini, cerebras, openrouter, fireworks, groq, together, deepseek, perplexity)")
	modelFlag            = flag.String("m", "", "Override model ID")
	harnessFlag          = flag.String("harness", "", "Use a harness plan; pass . to opt in to exact local ./harness.yaml discovery")
	harnessAllowYoloFlag = flag.Bool("harness-allow-yolo", false, "Explicitly allow a harness plan with approvalMode: yolo")

	// Provider config flags
	apiKeyFlag    = flag.String("api-key", "", "API key for provider (overrides env/stored; visible in process listings)")
	apiKeyEnvFlag = flag.String("api-key-env", "", "Read the run-scoped API key from this environment variable")
	baseURLFlag   = flag.String("base-url", "", "Custom API endpoint base URL")
	apiTypeFlag   = flag.String("api-type", "", "Custom endpoint protocol: openai or anthropic")
	timeoutFlag   = flag.Duration("timeout", 300*time.Second, "Provider request timeout (default: 300s)")

	// Agent settings flags
	maxTokensFlag      = flag.Int("max-tokens", 0, "Max output tokens (default: 32768)")
	temperatureFlag    = flag.Float64("temperature", -1, "Temperature 0.0-1.0 (default: 0.7)")
	topPFlag           = flag.Float64("top-p", -1, "Top-P sampling (default: 1.0)")
	maxTurnsFlag       = flag.Int("max-turns", 0, "Max agent turns (default: 10)")
	maxWallSecondsFlag = flag.Int("max-wall-seconds", 0, "Hard wall-clock deadline for the entire headless run, in seconds (0 = no deadline)")

	// Tool configuration flags
	toolsFlag         = flag.String("tools", "", "Comma-separated tools to enable (default: all)")
	disableToolsFlag  = flag.String("disable-tools", "", "Comma-separated tools to disable")
	workspaceFlag     = flag.String("workspace", "", "Working directory for tools (default: cwd)")
	allowAllPathsFlag = flag.Bool("allow-all-paths", false, "Allow tools to access any path (DANGEROUS)")
	daemonBackedFlag  = flag.Bool("daemon", false, "Render THE global background daemon instead of running the engine in-process (also: SWARM_DAEMON=1); closing leaves it running for cron")

	// Display flags
	noStreamFlag           = flag.Bool("no-stream", false, "Disable streaming (buffered output)")
	jsonFlag               = flag.Bool("json", false, "Output in JSON format (structured)")
	showThinkingFlag       = flag.Bool("show-thinking", true, "Show extended thinking blocks")
	showFullToolOutputFlag = flag.Bool("show-full-tool-output", false, "Show complete tool output (not truncated)")
	verboseFlag            = flag.Bool("verbose", false, "Verbose logging to stderr")
	rawDebugFlag           = flag.Bool("raw", false, "Show raw JSON requests/responses from provider")
	outputFormatFlag       = flag.String("output-format", "text", "Output format in headless mode: text|json|stream-json")
	includeHookEventsFlag  = flag.Bool("include-hook-events", false, "Include hook lifecycle events in stream-json output")
	debugToStderrFlag      = flag.Bool("debug-to-stderr", false, "Send engine debug tracing to stderr with color")

	// Feature flags
	enableDebugModeFlag          = flag.Bool("enable-debug-mode", false, "Enable DebugLogs tool for log inspection")
	enableCachingFlag            = flag.Bool("enable-caching", true, "Enable prompt caching (default: true)")
	operatingModeFlag            = flag.String("operating-mode", "", "Operating mode (PLAN/ACT/AUTO, default: ACT)")
	systemPromptFlag             = flag.String("system-prompt", "", "Custom system prompt override")
	systemPromptFileFlag         = flag.String("system-prompt-file", "", "Read custom system prompt override from file")
	noGlobalMemoryFlag           = flag.Bool("no-global-memory", false, "Ignore user-level CLAUDE.md and SWARM.md context for this run")
	noProjectMemoryFlag          = flag.Bool("no-project-memory", false, "Ignore project CLAUDE.md, SWARM.md, AGENTS.md, and INDEX.md context for this run")
	noSkillsFlag                 = flag.Bool("no-skills", false, "Disable skill discovery, injection, tools, autogenskills, and plugin skills")
	cleanAgentFlag               = flag.Bool("clean-agent", false, "Run without global/project memory, skills, or hooks")
	requireOutputRepairTurnsFlag = flag.Int("require-output-repair-turns", 3, "Extra turns allowed to repair missing --require-output artifacts")
	requireOutputFlags           stringListFlag

	// Conversation flags
	resumeConversationFlag = flag.String("r", "", "Resume an existing conversation by ID")
	conversationIDFlag     = flag.String("conversation-id", "", "Resume specific conversation")
	newConversationFlag    = flag.Bool("new-conversation", false, "Always start new conversation")
	saveConvFlag           = flag.Bool("no-save-conversation", false, "Don't save conversation (default: save)")

	// Hooks flags
	hooksVerboseFlag = flag.Bool("hooks-verbose", false, "Show verbose hook execution output")
	noHooksFlag      = flag.Bool("no-hooks", false, "Disable all hooks")
	// Code-mode: wrap tools in a run_code JS sandbox so the model can batch many
	// operations per turn. Previously only the TUI honored EnableCodeMode (via
	// config.json); headless -p never wired it, making code-mode unreachable for
	// benchmarks. This flag (and the config fallback below) fix that.
	codeModeFlag = flag.Bool("code-mode", false, "Enable code-mode (wrap tools in a run_code JS sandbox) in headless mode")

	// Approval posture for headless (-p) mode. auto = approve + log (default,
	// backward-compatible); deny = deny mutating/credential/network perms,
	// allow read-only; prompt = fail closed (no TTY in headless).
	approvalModeFlag = flag.String("approval-mode", "auto", "Headless approval posture: auto|deny|prompt (prompt==deny today; no TTY in -p)")
)

func init() {
	flag.Var(&requireOutputFlags, "require-output", "Require a non-empty output file; repeatable")
}

type stringListFlag []string

func (f *stringListFlag) String() string { return strings.Join(*f, ",") }

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("path must not be empty")
	}
	*f = append(*f, value)
	return nil
}

func loadHeadlessSystemPrompt(inlinePrompt, promptFile string) (string, string, error) {
	if strings.TrimSpace(inlinePrompt) != "" && strings.TrimSpace(promptFile) != "" {
		return "", "", fmt.Errorf("--system-prompt and --system-prompt-file are mutually exclusive")
	}
	if strings.TrimSpace(promptFile) == "" {
		return inlinePrompt, "--system-prompt", nil
	}
	data, err := os.ReadFile(promptFile)
	if err != nil {
		return "", "", fmt.Errorf("read --system-prompt-file %q: %w", promptFile, err)
	}
	if len(data) == 0 {
		return "", "", fmt.Errorf("--system-prompt-file %q is empty", promptFile)
	}
	return string(data), "--system-prompt-file", nil
}

func missingRequiredOutputs(workspaceRoot string, paths []string) []string {
	var missing []string
	for _, path := range paths {
		resolved := path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(workspaceRoot, resolved)
		}
		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() || info.Size() == 0 {
			missing = append(missing, path)
		}
	}
	return missing
}

func requiredOutputRepairPrompt(paths []string) string {
	var b strings.Builder
	b.WriteString("The run cannot finish yet because these required output files are missing or empty:\n")
	for _, path := range paths {
		fmt.Fprintf(&b, "- %s\n", path)
	}
	b.WriteString("\nWrite every listed file now. Do not research further and do not merely describe what you will write. Use a file-writing tool, then verify each file is non-empty.")
	return b.String()
}

type headlessIsolation struct {
	noHooks         bool
	noGlobalMemory  bool
	noProjectMemory bool
	noSkills        bool
}

func resolveHeadlessIsolation(clean, noHooks, noGlobalMemory, noProjectMemory, noSkills bool) headlessIsolation {
	return headlessIsolation{
		noHooks:         noHooks || clean,
		noGlobalMemory:  noGlobalMemory || clean,
		noProjectMemory: noProjectMemory || clean,
		noSkills:        noSkills || clean,
	}
}

func resolveEndpointOverride(apiType, apiKey, apiKeyEnv, baseURL, model string) (*chat.ProviderEndpointOverride, error) {
	apiType = strings.ToLower(strings.TrimSpace(apiType))
	apiKey = strings.TrimSpace(apiKey)
	apiKeyEnv = strings.TrimSpace(apiKeyEnv)
	baseURL = strings.TrimSpace(baseURL)
	if apiKey != "" && apiKeyEnv != "" {
		return nil, fmt.Errorf("--api-key and --api-key-env are mutually exclusive")
	}
	if apiKeyEnv != "" {
		value, ok := os.LookupEnv(apiKeyEnv)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("--api-key-env %q is unset or empty", apiKeyEnv)
		}
		apiKey = strings.TrimSpace(value)
	}
	if apiType == "" {
		if apiKey != "" || apiKeyEnv != "" || baseURL != "" {
			return nil, fmt.Errorf("--api-key, --api-key-env, and --base-url require --api-type openai|anthropic")
		}
		return nil, nil
	}
	if apiType != "openai" && apiType != "anthropic" {
		return nil, fmt.Errorf("invalid --api-type %q: expected openai or anthropic", apiType)
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("--api-type requires an explicit --model")
	}
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Hostname() == "" ||
			parsed.User != nil ||
			parsed.RawQuery != "" ||
			parsed.Fragment != "" {
			return nil, fmt.Errorf("--base-url must be a valid http:// or https:// URL")
		}
		if port := parsed.Port(); port != "" {
			value, err := strconv.Atoi(port)
			if err != nil || value < 1 || value > 65535 {
				return nil, fmt.Errorf("--base-url contains an invalid port")
			}
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("--api-type requires --api-key or --api-key-env")
	}
	return &chat.ProviderEndpointOverride{
		APIType: apiType,
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
	}, nil
}

func validateEndpointFlagsForMode(headless bool, apiType, apiKey, apiKeyEnv, baseURL string) error {
	if headless {
		return nil
	}
	if strings.TrimSpace(apiType) != "" ||
		strings.TrimSpace(apiKey) != "" ||
		strings.TrimSpace(apiKeyEnv) != "" ||
		strings.TrimSpace(baseURL) != "" {
		return fmt.Errorf("--api-type, --api-key, --api-key-env, and --base-url are only supported with -p")
	}
	return nil
}

// Enable bracketed paste mode for proper paste event handling
func enableBracketedPaste() {
	// Enable bracketed paste mode (ESC [ ? 2004 h)
	fmt.Printf("\x1b[?2004h")
}

// Disable bracketed paste mode
func disableBracketedPaste() {
	// Disable bracketed paste mode (ESC [ ? 2004 l)
	fmt.Printf("\x1b[?2004l")
}

// applyPersistedTelemetryOptIn exports SWARM_ANALYTICS_ENABLED=1 when the user
// enabled telemetry via the in-app settings (persisted in config), unless the
// env var is already explicitly set (explicit env always wins). Telemetry is
// off by default and still requires a user-supplied collector URL + token.
func applyPersistedTelemetryOptIn() {
	if _, ok := os.LookupEnv("SWARM_ANALYTICS_ENABLED"); ok {
		return // explicit env var wins, don't override
	}
	cm, err := commands.NewConfigManager()
	if err != nil {
		return
	}
	cfg, err := cm.LoadConfig()
	if err != nil || cfg == nil {
		return
	}
	if cfg.TelemetryEnabled {
		_ = os.Setenv("SWARM_ANALYTICS_ENABLED", "1")
	}
}

func main() {
	// Migrate legacy ~/.swarmos data before any config or telemetry
	// initialization. The migration is best-effort and idempotent.
	configmigrate.Run()
	// Apply the persisted telemetry opt-in BEFORE any analytics/hook init.
	// DefaultManager() reads SWARM_ANALYTICS_ENABLED via ConfigFromEnv() and is
	// memoized with sync.Once, so the env var must be set first. If the user has
	// not explicitly set the env var but enabled telemetry in settings, export
	// it here. Telemetry still requires a user-supplied collector URL + token.
	applyPersistedTelemetryOptIn()

	defer func() {
		_ = tuianalytics.CloseDefaultManager()
	}()

	// Wire SIGUSR1 -> non-destructive goroutine-stack dump so a wedged session
	// can be diagnosed live without killing it (kill -USR1 <pid>).
	installGoroutineDumpHandler()

	// Check for pending update (rollback protection)
	// If we crashed before clearing the marker, restore the backup
	pendingUpdate, err := update.CheckPendingUpdate()
	if err != nil {
		// Log warning but continue
		fmt.Fprintf(os.Stderr, "Warning: failed to check pending update: %v\n", err)
	} else if pendingUpdate != nil {
		// There was a pending update. Check if the backup exists.
		// If this is a fresh start after update, we'll verify success below.
		// If we crashed and restarted, we might want to rollback.
		// For now, just clear the marker to indicate successful startup.
		fmt.Fprintf(os.Stderr, "Info: clearing pending update marker from %s\n", pendingUpdate.Timestamp.Format("2006-01-02 15:04:05"))
		_ = pendingUpdate.ClearMarker()
	}

	// Start a goroutine to verify successful startup after a delay
	// This clears the marker file to indicate the new binary works
	go func() {
		time.Sleep(5 * time.Second)
		// If we get here, the app started successfully
		// The marker should already be cleared above, but this is a safety check
		if pendingUpdate != nil {
			_ = pendingUpdate.ClearMarker()
		}
	}()

	// Initialize Sentry with enhanced configuration
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              "https://56b1c878e64aac097aedbb5e2edd93bf@o4510268535799808.ingest.us.sentry.io/4510853643304960",
		Debug:            false, // Disable debug in production
		SendDefaultPII:   false, // Don't send PII by default
		EnableTracing:    true,
		TracesSampleRate: 0.1, // Sample 10% of transactions

		// Release tracking
		Release:     version.Version,
		Environment: getEnvironment(),

		// BeforeSend hook for error filtering and enrichment
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			// Filter out context cancellation errors (user-initiated, not real errors)
			if event.Exception != nil && len(event.Exception) > 0 {
				for _, exc := range event.Exception {
					if exc.Type == "ContextCanceledError" ||
						(exc.Value == "context canceled" ||
							strings.Contains(exc.Value, "context canceled")) {
						return nil // Don't send to Sentry
					}
				}
			}

			// Add build information
			if event.Extra == nil {
				event.Extra = make(map[string]interface{})
			}
			event.Extra["build_id"] = version.BuildID
			event.Extra["git_commit"] = version.GitCommit
			event.Extra["build_time"] = version.BuildTime

			return event
		},

		// Ignore known non-error patterns
		IgnoreErrors: []string{
			"context canceled",
			"context deadline exceeded",
			"EOF",
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "sentry.Init: %s\n", err)
	}
	defer sentry.Flush(2 * time.Second)
	defer sentry.Recover()

	// Initialize the global goroutine pool manager
	poolManager := pool.Default()
	defer poolManager.Close()

	// Handle subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "cloud":
			if err := runCloudCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Cloud command failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "conductor":
			if err := runConductorCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Conductor failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "daemon":
			// ADR-002 compatibility-matrix row 10: `swarm daemon [foreground
			// flags]` is the bounded-deprecated legacy spelling for the
			// internal supervisor entry point; interactive users are
			// directed to `swarm daemon start`. runDaemonCLI's behavior is
			// intentionally untouched here (P07.A owns dispatch/warnings
			// only, not daemon-lifecycle logic in daemon_cli.go) — this is
			// the ONLY code path that currently reaches `swarm daemon ...`,
			// so it always runs the legacy foreground operation today and
			// always warns in human mode.
			warnLegacyAlias("swarm daemon [foreground flags]", false)
			if err := runDaemonCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
				os.Exit(1)
			}
			return
		case "attach":
			if err := runAttachCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "attach: %v\n", err)
				os.Exit(1)
			}
			return
		case "session":
			if err := runSessionCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "session: %v\n", err)
				os.Exit(1)
			}
			return
		case "swarm":
			if err := runSwarmCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "swarm: %v\n", err)
				os.Exit(1)
			}
			return
		case "run":
			// Canonical `swarm run <prompt> [run options]` (ADR-002 DOMAIN:
			// "run owns an ephemeral invocation"). Normalizes the positional
			// prompt into the exact same -p flag plumbing the legacy
			// `swarm -p <prompt>` path already uses, then calls the SAME
			// dispatchHeadlessExecution() helper that path calls below —
			// see the `if *promptFlag != ""` branch further down in this
			// function. This guarantees byte-identical behavior beyond
			// invocation syntax (ADR-002 compatibility row 1) and that
			// `swarm run` never starts/selects the persistent daemon
			// implicitly (ADR-002 Decision section) since it reaches
			// runHeadless via the identical code path -p always has.
			runArgs := os.Args[2:]
			prompt, rest, hasPrompt := normalizeRunArgs(runArgs)
			newArgs := rest
			if hasPrompt {
				newArgs = append([]string{"-p", prompt}, rest...)
			}
			if err := validatePromptFlagArgs(flag.CommandLine, newArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			_ = flag.CommandLine.Parse(newArgs) // ExitOnError: exits on parse failure itself
			if strings.TrimSpace(*promptFlag) == "" {
				fmt.Fprintln(os.Stderr, "Error: swarm run requires a prompt: swarm run <prompt> [run options]")
				os.Exit(1)
			}
			if err := validateInitialPromptMode(
				*initialPromptFlag,
				*promptFlag,
				*resumeConversationFlag,
				*conversationIDFlag,
				*harnessFlag,
				*debugRender,
				*debugLineageFixture,
				daemonBackedRequested(*daemonBackedFlag, os.Getenv("SWARM_DAEMON")),
			); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			dispatchHeadlessExecution()
			return
		case "peer":
			// Canonical `swarm peer list|task|control` (ADR-002 DOMAIN:
			// "peer addresses discovered agent or presentation peers").
			// Implemented in peer_cli.go; delegates list/task to the
			// existing swarmList/swarmTask bodies in swarm_cli.go.
			if err := runPeerCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "peer: %v\n", err)
				os.Exit(1)
			}
			return
		case "vault":
			if err := runVaultCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "vault: %v\n", err)
				os.Exit(1)
			}
			return
		case "provider", "providers":
			if err := runProviderCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "provider: %v\n", err)
				os.Exit(1)
			}
			return
		case "model", "models":
			if err := runModelCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "model: %v\n", err)
				os.Exit(1)
			}
			return
		case "service":
			if err := runServiceCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "service: %v\n", err)
				os.Exit(1)
			}
			return
		case "analytics":
			if err := runAnalyticsCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Analytics command failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "conversations", "conv":
			if err := runConversationsCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Conversations command failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "curator":
			if err := runCuratorCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Curator failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "install":
			if err := runInstallCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "audit":
			if err := runAuditCLI(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "audit: %v\n", err)
				os.Exit(1)
			}
			return
		case "harness":
			if err := runHarnessSubcommand(os.Args[2:]); err != nil {
				os.Exit(1)
			}
			return
		case "version", "-version", "--version":
			fmt.Println(version.BannerString())
			os.Exit(0)
		default:
			// ADR-002 Decision section: "Unknown commands and invalid
			// combinations fail with a non-zero status and show the
			// nearest canonical command. No parser may reinterpret an
			// invalid daemon, peer, or attach command as a prompt." A
			// token that looks like a flag (leading "-") is NOT an unknown
			// command — it is the bare invocation form (`swarm -p ...`,
			// `swarm --verbose`, plain `swarm`, etc.) that falls through
			// to normal flag parsing below. Only a non-flag first
			// argument that doesn't match any case above is a genuinely
			// unrecognized command word.
			if !strings.HasPrefix(os.Args[1], "-") {
				fmt.Fprintf(os.Stderr, "Error: unknown command %q\n", os.Args[1])
				if suggestion, ok := nearestCanonicalCommand(os.Args[1]); ok {
					fmt.Fprintf(os.Stderr, "did you mean %q?\n", suggestion)
				}
				os.Exit(1)
			}
		}
	}

	// Parse command-line flags
	if err := validatePromptFlagArgs(flag.CommandLine, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	flag.Parse()

	// Show version if requested
	if *versionFlag {
		fmt.Println(version.BannerString())
		os.Exit(0)
	}

	// Show help if requested
	if *helpFlag {
		printHelp()
		os.Exit(0)
	}

	if err := validateInitialPromptMode(
		*initialPromptFlag,
		*promptFlag,
		*resumeConversationFlag,
		*conversationIDFlag,
		*harnessFlag,
		*debugRender,
		*debugLineageFixture,
		daemonBackedRequested(*daemonBackedFlag, os.Getenv("SWARM_DAEMON")),
	); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Detect headless mode (if -p flag is provided)
	if *promptFlag != "" {
		dispatchHeadlessExecution()
		return
	}
	if err := validateEndpointFlagsForMode(false, *apiTypeFlag, *apiKeyFlag, *apiKeyEnvFlag, *baseURLFlag); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Run TUI mode (existing code)
	if err := validatePeerHandle(strings.TrimSpace(*a2aHandleFlag)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid --a2a-handle: %v\n", err)
		os.Exit(1)
	}
	runTUI()
}

// dispatchHeadlessExecution resolves the effective output format, validates
// flag combinations, and invokes runHeadless (or runHeadlessHarness) for a
// one-shot headless execution. This is the EXACT SAME logic the historical
// `swarm -p <prompt>` path has always run (moved here verbatim from main()
// so it can be shared, byte-for-byte, with the new canonical
// `swarm run <prompt>` dispatch case) — see ADR-002 compatibility-matrix
// row 1 ("swarm -p <prompt>" -> "swarm run <prompt>", no warning, no
// behavior divergence permitted). It assumes *promptFlag has already been
// populated (either directly via -p or via the `run` case's positional-
// prompt-to-flag adapter in normalizeRunArgs) and that flag.Parse()/
// flag.CommandLine.Parse() has already run for this invocation.
//
// This function — and therefore runHeadless, which it calls — never
// references ensureGlobalDaemon or globalDaemonHandle. `swarm run`/
// `swarm -p` must never start or select the persistent daemon implicitly
// (ADR-002 Decision section). cli_contract_test.go statically asserts this
// invariant against runHeadless's source.
func dispatchHeadlessExecution() {
	// Resolve output format: --json is a backward-compat shorthand for --output-format json
	effectiveOutputFormat := *outputFormatFlag
	if *jsonFlag && effectiveOutputFormat == "text" {
		effectiveOutputFormat = "json"
	}
	switch effectiveOutputFormat {
	case "text", "json", "stream-json":
		// valid
	default:
		fmt.Fprintf(os.Stderr, "Error: --output-format must be text, json, or stream-json (got %q)\n", effectiveOutputFormat)
		os.Exit(1)
	}

	// Validate --include-hook-events requires --output-format stream-json
	if *includeHookEventsFlag && effectiveOutputFormat != "stream-json" {
		fmt.Fprintf(os.Stderr, "Error: --include-hook-events requires --output-format stream-json\n")
		os.Exit(1)
	}

	resumeID, err := resolveHeadlessConversationID(*resumeConversationFlag, *conversationIDFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	*conversationIDFlag = resumeID
	if harnessFlagSelected(*harnessFlag) {
		planPath, found := discoverHarnessPath(*harnessFlag)
		if !found {
			fmt.Fprintln(os.Stderr, "Headless execution failed: --harness . requires ./harness.yaml in the current directory")
			os.Exit(1)
		}
		if err := runHeadlessHarness(planPath, *harnessAllowYoloFlag, effectiveOutputFormat, *promptFlag, *conversationIDFlag); err != nil {
			fmt.Fprintf(os.Stderr, "Headless execution failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := runHeadless(effectiveOutputFormat); err != nil {
		fmt.Fprintf(os.Stderr, "Headless execution failed: %v\n", err)
		os.Exit(1)
	}
}

// legacyAliasWarning describes exactly one row of ADR-002's Compatibility
// section matrix: a bounded, still-supported legacy CLI spelling, its
// canonical replacement operation, and the exact stderr warning text (empty
// for rows the ADR marks "None", i.e. no warning is authorized). This table
// is the ONLY place these mappings/warning strings are defined — ADR-002's
// Compatibility section states "another policy may cite it but must not
// define a competing alias meaning, warning rule, compatibility promise, or
// removal gate." Every call site below looks up its row here rather than
// hardcoding warning text inline, so the full matrix stays centralized,
// table-driven, and independently unit-testable (see
// cli_contract_test.go's TestLegacyAliasWarnings).
type legacyAliasWarning struct {
	// Legacy is the exact legacy spelling this row documents, used as the
	// lookup key for warnLegacyAlias and by cli_contract_test.go.
	Legacy string
	// Canonical is the ADR-002 canonical replacement operation.
	Canonical string
	// Warning is the exact, complete stderr line to print once per
	// invocation in human-output mode; empty means this row is a pure
	// compatibility-inventory entry with no authorized warning (ADR-002
	// row 1: "swarm -p <prompt>").
	Warning string
}

// legacyAliasWarnings is the complete ADR-002 Compatibility-section matrix,
// transcribed verbatim (legacy spelling, canonical operation, warning text)
// from docs/architecture/swarm-attach/adr-002-cli-taxonomy.md's
// "## Compatibility" table. Do not add, remove, or reword a row here
// without a corresponding ADR-002 change — this table is intentionally the
// single source of truth for every alias warning in the binary.
var legacyAliasWarnings = []legacyAliasWarning{
	{
		Legacy:    "swarm -p <prompt>",
		Canonical: "swarm run <prompt>",
		// "None; this remains a supported compatibility form in this
		// modernization." — ADR-002 Compatibility table row 1.
		Warning: "",
	},
	{
		Legacy:    "swarm swarm up",
		Canonical: "swarm daemon start",
		Warning:   "warning: 'swarm swarm up' is deprecated; use 'swarm daemon start'",
	},
	{
		Legacy:    "swarm swarm status",
		Canonical: "swarm peer list",
		Warning:   "warning: 'swarm swarm status' is deprecated; use 'swarm peer list'",
	},
	{
		Legacy:    "swarm swarm stop",
		Canonical: "swarm daemon stop",
		Warning:   "warning: 'swarm swarm stop' is deprecated; use 'swarm daemon stop'",
	},
	{
		Legacy:    "swarm swarm list",
		Canonical: "swarm peer list",
		Warning:   "warning: 'swarm swarm list' is deprecated; use 'swarm peer list'",
	},
	{
		Legacy:    "swarm swarm task",
		Canonical: "swarm peer task",
		Warning:   "warning: 'swarm swarm task' is deprecated; use 'swarm peer task'",
	},
	{
		Legacy:    "swarm swarm attach",
		Canonical: "swarm peer control",
		Warning:   "warning: 'swarm swarm attach' is deprecated; use 'swarm peer control'",
	},
	{
		Legacy:    "swarm --daemon",
		Canonical: "swarm attach",
		Warning:   "warning: 'swarm --daemon' is deprecated; use 'swarm attach'",
	},
	{
		Legacy:    "SWARM_DAEMON",
		Canonical: "swarm attach",
		Warning:   "warning: 'SWARM_DAEMON' is deprecated; use 'swarm attach'",
	},
	{
		Legacy:    "swarm daemon [foreground flags]",
		Canonical: "swarm daemon start",
		Warning:   "warning: 'swarm daemon' foreground mode is deprecated; use 'swarm daemon start'",
	},
}

// isMachineOutputArgs reports whether an argument slice requests a
// machine-readable output mode (--json, or --output-format json|stream-json
// in either "--flag value" or "--flag=value" spelling). It is the single
// shared detector legacy-alias call sites use to decide whether
// warnLegacyAlias is permitted to write to stderr, per ADR-002's
// Compatibility section machine-output-mode carve-out. Today only the
// headless (-p/run) command family accepts these flags; the swarm/peer
// command family does not yet expose a machine-output mode, so this
// detector returns false for their argument slices and their warnings
// always fire in what is, for those commands, unconditionally
// human-output mode.
func isMachineOutputArgs(args []string) bool {
	for i, a := range args {
		switch {
		case a == "--json" || a == "-json":
			return true
		case a == "--output-format" || a == "-output-format":
			if i+1 < len(args) {
				switch args[i+1] {
				case "json", "stream-json":
					return true
				}
			}
		case strings.HasPrefix(a, "--output-format="):
			switch strings.TrimPrefix(a, "--output-format=") {
			case "json", "stream-json":
				return true
			}
		case strings.HasPrefix(a, "-output-format="):
			switch strings.TrimPrefix(a, "-output-format=") {
			case "json", "stream-json":
				return true
			}
		}
	}
	return false
}

// canonicalTopLevelCommands lists every top-level command word this binary's
// subcommand switch actually recognizes today (both ADR-002 canonical verbs
// — run, daemon, attach, peer, service — and the other still-supported
// subsystems dispatched from the same switch). nearestCanonicalCommand uses
// this list to suggest a correction for an unrecognized command word, per
// ADR-002's Decision section: "Unknown commands and invalid combinations
// fail with a non-zero status and show the nearest canonical command."
var canonicalTopLevelCommands = []string{
	"run", "daemon", "attach", "peer", "service",
	"swarm", "cloud", "conductor", "vault", "provider", "model",
	"analytics", "conversations", "curator", "install", "audit", "harness", "version",
}

// validatePromptFlagArgs prevents Go's flag parser from silently treating a
// flag-looking token as the value of a bare -p/--p option. Explicit assignment
// (-p=--harness) remains available for a literal flag-looking prompt. Rejecting
// every such token, including misspellings, keeps harness selection fail-closed.
func validatePromptFlagArgs(fs *flag.FlagSet, args []string) error {
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--" || token == "-" || !strings.HasPrefix(token, "-") {
			return nil
		}

		name, hasValue := splitFlagToken(token)
		if name == "" {
			return nil
		}
		registered := fs.Lookup(name)
		if registered == nil {
			return nil // flag.Parse reports the unknown flag itself.
		}

		if (name == "p" || name == "initial-prompt") && !hasValue {
			flagName := "-p"
			if name == "initial-prompt" {
				flagName = "--initial-prompt"
			}
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a prompt value", flagName)
			}
			next := args[i+1]
			if next == "--" || (next != "-" && strings.HasPrefix(next, "-")) {
				return fmt.Errorf("%s requires a prompt; %q is a flag (use %s=<prompt> for a literal flag-looking prompt)", flagName, next, flagName)
			}
			i++
			continue
		}

		if !hasValue && flagConsumesValue(registered) {
			if i+1 >= len(args) {
				return nil // flag.Parse emits the canonical missing-value error.
			}
			i++
		}
	}
	return nil
}

func validateInitialPromptMode(
	initialPrompt string,
	headlessPrompt string,
	resumeConversation string,
	conversationID string,
	harness string,
	debugRender bool,
	debugFixture bool,
	daemonBacked bool,
) error {
	if strings.TrimSpace(initialPrompt) == "" {
		return nil
	}
	conflicts := []struct {
		active bool
		flag   string
	}{
		{active: strings.TrimSpace(headlessPrompt) != "", flag: "-p"},
		{active: strings.TrimSpace(resumeConversation) != "", flag: "-r"},
		{active: strings.TrimSpace(conversationID) != "", flag: "--conversation-id"},
		{active: strings.TrimSpace(harness) != "", flag: "--harness"},
		{active: debugRender, flag: "--debug-render"},
		{active: debugFixture, flag: "--debug-lineage-fixture"},
		{active: daemonBacked, flag: "--daemon"},
	}
	for _, conflict := range conflicts {
		if conflict.active {
			return fmt.Errorf("--initial-prompt cannot be combined with %s", conflict.flag)
		}
	}
	return nil
}

func daemonBackedRequested(flagEnabled bool, environmentValue string) bool {
	return flagEnabled || strings.TrimSpace(environmentValue) != ""
}

func splitFlagToken(token string) (name string, hasValue bool) {
	trimmed := strings.TrimLeft(token, "-")
	if trimmed == "" {
		return "", false
	}
	if idx := strings.IndexByte(trimmed, '='); idx >= 0 {
		return trimmed[:idx], true
	}
	return trimmed, false
}

func flagConsumesValue(f *flag.Flag) bool {
	type boolFlag interface {
		IsBoolFlag() bool
	}
	b, ok := f.Value.(boolFlag)
	return !ok || !b.IsBoolFlag()
}

func resolveHeadlessConversationID(resumeID, conversationID string) (string, error) {
	resumeID = strings.TrimSpace(resumeID)
	conversationID = strings.TrimSpace(conversationID)
	if resumeID != "" && conversationID != "" && resumeID != conversationID {
		return "", fmt.Errorf("conflicting conversation IDs: -r %q does not match --conversation-id %q", resumeID, conversationID)
	}
	if conversationID != "" {
		return conversationID, nil
	}
	return resumeID, nil
}

// getEnvironment determines the current environment
func getEnvironment() string {
	if env := os.Getenv("SWARMOS_ENV"); env != "" {
		return env
	}
	if version.Version == "v0.0.0-dev" || strings.Contains(version.Version, "dev") {
		return "development"
	}
	return "production"
}

const cliHelpText = `Swarm — interactive and headless AI agents

USAGE
  swarm                                      Open the interactive TUI
  swarm --initial-prompt <text>              Open the TUI and submit one initial prompt
  swarm -p <prompt> [options]                Run one non-interactive task
  swarm <command> [args]                     Run a service, cloud, or peer command

START HERE
  swarm -p "Fix the failing tests" --workspace .
  swarm -p "$(cat task.md)" --clean-agent --system-prompt-file agent.md
  swarm -p "Write the report" --require-output report.md --max-turns 12
  swarm -p "Research HBM" --output-format stream-json > run.ndjson

RELIABLE HEADLESS RUNS
  -p <text>                                  Prompt to execute
  --workspace <path>                         Working root for tools and relative outputs
  --system-prompt-file <path>                Load a run-specific system prompt from a file
  --system-prompt <text>                     Set a run-specific system prompt inline
  --clean-agent                              Disable global/project memory, skills, and hooks
  --require-output <path>                    Require a non-empty artifact; repeatable
  --require-output-repair-turns <n>          Extra turns to repair missing artifacts (default 3)
  --max-turns <n>                            Turn limit; 0 uses agent default/unlimited
  --operating-mode <mode>                    ACT, AUTO, or PLAN (default ACT)

  A missing required output triggers a write-only repair pass. The command fails
  if the file is still missing or empty. Relative paths resolve under --workspace.

MEMORY, SKILLS, AND HOOKS
  --no-global-memory                         Ignore user CLAUDE.md and SWARM.md
  --no-project-memory                        Ignore project CLAUDE/SWARM/AGENTS/INDEX files
  --no-skills                                Disable skill discovery, injection, and skill tools
  --no-hooks                                 Disable all builtin and custom hooks

  --no-hooks also removes autogenskills enforcement and SkillManage, but ordinary
  skills remain available unless --no-skills is set. --clean-agent sets all four
  isolation flags above.

OUTPUT AND AUTOMATION
  --output-format <format>                   text | json | stream-json (default text)
      text                                   Final human-readable response
      json                                   One final result object; no tool transcript
      stream-json                            NDJSON messages, tool calls/results, and result
  --include-hook-events                      Add hook lifecycle events to stream-json
  --show-thinking                            Include extended-thinking blocks (default true)
  --verbose                                  Diagnostic logs to stderr
  --raw                                      Raw provider requests/responses to stderr
  --debug-to-stderr                          Colored engine trace to stderr

  For evidence capture, use stream-json, which emits tool results as received.
  Keep stdout for NDJSON and redirect stderr separately.

TOOLS AND APPROVALS
  --tools <a,b,...>                          Enable only the named tools
  --disable-tools <a,b,...>                  Remove named tools from the default set
  --approval-mode <mode>                     auto | deny | prompt (default auto)
  --allow-all-paths                          Remove workspace path boundaries (dangerous)

  Headless prompt has no TTY and currently behaves like deny. Unknown approval
  modes warn and fall back to auto; use deny for fail-closed automation.

PROVIDER AND MODEL
  -P <provider>                              Override provider
  -m <model>                                 Override model ID
  --api-type <type>                          Endpoint protocol: openai | anthropic
  --api-key-env <name>                       Read run-scoped key from an environment variable
  --api-key <key>                            Run-scoped key; visible in process listings
  --base-url <url>                           Run-scoped API base URL
  --max-tokens <n>                           Maximum output tokens per turn
  --temperature <0..1>                       Sampling temperature (headless default 0)

  --api-key, --api-key-env, and --base-url require --api-type and an explicit
  --model; --api-type also requires one of the two key sources.
  Prefer --api-key-env so the secret is not exposed through argv. The API type
  selects the wire protocol; it is not inferred from the URL.

CONVERSATIONS
  --conversation-id <id>                     Continue an existing conversation

COMMON RECIPES
  # Clean, citation-grade research with guaranteed artifacts
  swarm -p "$(cat research.md)" --workspace ./run --clean-agent \
    --system-prompt-file research-agent.md --max-turns 18 \
    --require-output report.md --require-output evidence/sources.md \
    --output-format stream-json > run.ndjson 2> run.log

  # Continue an expensive run
  swarm -p "Continue and write the remaining files" --conversation-id <id>

  # Restrict a run to a small tool surface
  swarm -p "Analyze this repository" --tools Read,grep,websearch,apply_patch

  # Custom OpenAI-compatible endpoint
  swarm -p "Say hello" --api-type openai --base-url http://localhost:8000/v1 \
    --api-key-env API_KEY --model my-model

  # Custom Anthropic Messages endpoint
  swarm -p "Say hello" --api-type anthropic --base-url https://gateway.example/api \
    --api-key-env API_KEY --model claude-compatible-model

PEERS, DAEMONS, AND MOBILE
  swarm swarm list                           List live TUI and daemon peers
  swarm swarm status                         Show compact peer status
  swarm swarm task <handle> <prompt>         Send a task to a peer
  swarm swarm up                             Start or find the global daemon
  swarm swarm stop [handle]                  Stop a daemon
  swarm swarm attach <handle> [command]      Inspect or control a live TUI
  swarm attach <handle>                      Attach to a daemon session
  swarm --daemon                             Open the global daemon client; daemon stays running
  swarm daemon --gateway                     Run daemon plus LAN gateway on :8787
  swarm service install|status|uninstall     Manage startup service

CLOUD AND MAINTENANCE
  swarm cloud login                          Authenticate without opening the TUI
  swarm cloud sync-provider                  Sync a provider credential
  swarm cloud ask                            Run a one-shot cloud task
  swarm cloud session start|submit|stop      Manage an interactive cloud session
  swarm analytics status|flush|backfill      Manage analytics spool
  swarm conversations retitle               Backfill local conversation titles
  swarm install [-d|--list|<version>]        Install or update Swarm

OTHER
  --initial-prompt <text>                    Submit one prompt after interactive startup
  --version                                  Show version information
  --no-update                                Skip startup update checks
  --a2a-handle <name>                        Override this peer's display name
  --a2a-listen <addr>                        A2A listen address
  --debug, --new-ui                          TUI diagnostics/experimental UI
  --debug-render, --debug-lineage-fixture    TUI visual test fixtures

COMPATIBILITY FLAGS
  --json                                     Alias for --output-format json

  These flags are accepted by the current CLI but are not applied by the -p
  execution path: --timeout, --top-p, --no-stream, --show-full-tool-output,
  --new-conversation, --no-save-conversation, --enable-caching,
  --enable-debug-mode, and --hooks-verbose unless combined with --verbose or
  --debug-to-stderr.

Run "swarm daemon --help" for daemon-specific options.
`

// newUUID generates a random UUID v4 string.
func newUUID() string {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic("newUUID: crypto/rand unavailable: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func wallDeadlineFrom(maxWallSeconds int, start time.Time) (time.Time, bool) {
	if maxWallSeconds <= 0 {
		return time.Time{}, false
	}
	return start.Add(time.Duration(maxWallSeconds) * time.Second), true
}

// runHeadless executes headless mode using the same SDK as TUI
func runHeadless(outputFormat string) error {
	ctx := context.Background()
	if dl, ok := wallDeadlineFrom(*maxWallSecondsFlag, time.Now()); ok {
		var cancelWall context.CancelFunc
		ctx, cancelWall = context.WithDeadline(ctx, dl)
		defer cancelWall()
	}
	isolation := resolveHeadlessIsolation(
		*cleanAgentFlag,
		*noHooksFlag,
		*noGlobalMemoryFlag,
		*noProjectMemoryFlag,
		*noSkillsFlag,
	)
	customSystemPrompt, customSystemPromptSource, err := loadHeadlessSystemPrompt(*systemPromptFlag, *systemPromptFileFlag)
	if err != nil {
		return err
	}
	endpointOverride, err := resolveEndpointOverride(*apiTypeFlag, *apiKeyFlag, *apiKeyEnvFlag, *baseURLFlag, *modelFlag)
	if err != nil {
		return err
	}
	if *maxTurnsFlag < 0 {
		return fmt.Errorf("--max-turns must be >= 0")
	}
	if *requireOutputRepairTurnsFlag < 0 {
		return fmt.Errorf("--require-output-repair-turns must be >= 0")
	}

	// Load provider/model from saved config (same as TUI)
	providerName := *providerFlag
	modelName := *modelFlag
	if endpointOverride != nil && strings.TrimSpace(providerName) == "" {
		providerName = endpointOverride.APIType
	}

	// Record whether the caller explicitly passed BOTH -P and -m. When so, the
	// agent's fallback chain must honor that selection verbatim rather than
	// resolving from the active profile (which would override -P/-m with the
	// profile's primary provider).
	explicitProviderModel := endpointOverride != nil ||
		(strings.TrimSpace(*providerFlag) != "" && strings.TrimSpace(*modelFlag) != "")

	// Code-mode is enabled by the --code-mode flag OR the saved config
	// (enableCodeMode). Resolved here so the headless SDK actually receives it.
	enableCodeMode := *codeModeFlag

	configMgr, err := commands.NewConfigManager()
	if err == nil {
		if savedConfig, err := configMgr.LoadConfig(); err == nil && savedConfig != nil {
			if providerName == "" {
				providerName = savedConfig.CurrentProvider
			}
			if modelName == "" {
				modelName = savedConfig.CurrentModel
			}
			if savedConfig.EnableCodeMode {
				enableCodeMode = true
			}
		}
	}

	// Defaults
	if providerName == "" {
		providerName = "claudecode"
	}
	if modelName == "" {
		modelName = "claude-sonnet-4-20250514"
	}

	// Determine workspace root
	workspaceRoot := *workspaceFlag
	if workspaceRoot == "" {
		workspaceRoot, _ = os.Getwd()
	}

	// Create SDK (SAME as TUI does in app.go)
	// Load permission config (SAME as TUI does)
	permConfig, _ := chat.LoadPermissionConfig()
	if permConfig == nil {
		permConfig = chat.NewPermissionConfigWithDefaults()
	}

	// Create a headless-friendly approval broker. Its posture is controlled by
	// --approval-mode (default auto). Previously this broker unconditionally
	// auto-approved EVERYTHING while ignoring config and permission level — a
	// silent consent defect. auto now logs each approval; deny/prompt refuse
	// mutating/credential/network permissions.
	approvalMode, modeOK := chat.ParseApprovalMode(*approvalModeFlag)
	if !modeOK {
		fmt.Fprintf(os.Stderr, "[PERMISSIONS] unknown --approval-mode %q; falling back to auto\n", *approvalModeFlag)
	}
	headlessBroker := chat.NewHeadlessApprovalBrokerWithMode(approvalMode)

	// Determine the headless sampling temperature. For deterministic benchmark
	// re-runs, headless `-p` defaults to greedy decoding (temperature=0.0) so
	// the same (model, question, index) cell yields the same answer. The user
	// can override with `--temperature X`; the flag default is -1, so any value
	// >= 0 means it was explicitly supplied. We always force the temperature in
	// headless mode (ForceTemperature=true) so 0.0 is actually sent rather than
	// treated as "unset" (which would fall back to the provider's nondeterministic
	// default). Provider translators that reject non-default sampling params
	// (e.g. Anthropic Opus 4.7) still drop it via their existing RejectsSamplingParams logic.
	headlessTemperature := 0.0
	if *temperatureFlag >= 0 {
		headlessTemperature = *temperatureFlag
	}

	sdk, err := chat.NewSDKIntegrationWithOptions(providerName, modelName, chat.SDKIntegrationOptions{
		DebugMode: *debugMode,
		// Wire the documented --max-tokens flag (was registered + in --help but
		// never read). 0/unset falls through to the SDK default (31999).
		MaxTokens:        *maxTokensFlag,
		EndpointOverride: endpointOverride,
		RawDebug:         *rawDebugFlag,
		VerboseDebug:     *verboseFlag,
		PermissionConfig: permConfig,
		ApprovalBroker:   headlessBroker,
		// Honor --no-hooks HERE, at construction, so builtin hooks (task-
		// enforcement, protected-branch, autogenskills, plan-mode, task-nudge)
		// are never wired into the agent. Previously this flag was only checked
		// AFTER the SDK had already built + attached the hooks manager, so the
		// builtin hooks still fired in --no-hooks runs.
		NoHooks: isolation.noHooks,
		// Honor --allow-all-paths (DANGEROUS): removes the workspace boundary so
		// file tools can touch any path. Previously advertised but inert.
		AllowAllPaths: *allowAllPathsFlag,
		// Headless (-p) has no UI to answer an interactive fallback-exhausted
		// profile picker; this propagates isHeadless=true to the agent so its
		// onExhausted handler returns immediately instead of deadlocking.
		HeadlessMode: true,
		// Keep tools, context loading, and output contracts rooted at the
		// workspace supplied by the caller rather than the process cwd.
		WorkspaceRoot: workspaceRoot,
		// Suppress user-level prompt memory for clean, task-specific runs.
		DisableGlobalMemory:  isolation.noGlobalMemory,
		DisableProjectMemory: isolation.noProjectMemory,
		DisableSkills:        isolation.noSkills,
		// Honor explicit -P/-m verbatim (single-entry chain, no profile fallback).
		ExplicitProviderModel: explicitProviderModel,
		// Pin sampling temperature for deterministic benchmark re-runs (default 0.0).
		Temperature:      headlessTemperature,
		ForceTemperature: true,
		// Wire code-mode into headless (was previously TUI-only, so code-mode was
		// silently unreachable in `-p`/benchmark runs).
		EnableCodeMode: enableCodeMode,
	})
	if err != nil {
		return fmt.Errorf("SDK init failed (provider=%s, model=%s): %w", providerName, modelName, err)
	}

	// Non-interactive vault auto-load: if an age identity + team/two-person vault
	// (or a no-password transparent store) exist for this workspace, make the
	// credentials available to the agent WITHOUT prompting. Sensitive (two-person)
	// credentials still require the approval flow; this only makes them resolvable.
	if home, herr := os.UserHomeDir(); herr == nil {
		if provider, ok := vault.AutoLoadProvider(vault.DefaultAutoLoadPaths(home, workspaceRoot, workspaceRoot)); ok {
			sdk.SetVaultProvider(provider)
		}
	}

	// Create colored, timestamped log writer for stderr output.
	headlessLog := newLogWriter(os.Stderr, *verboseFlag, *debugToStderrFlag)

	// Check proxy settings
	proxySettings := chat.GetProxySettings()
	if proxySettings != nil {
		defaultProxy := proxySettings.GetDefaultProxy()
		if defaultProxy != nil && defaultProxy.Enabled {
			headlessLog.Debug(catProxy, "ENABLED: %s (%s)", defaultProxy.DisplayName, defaultProxy.Name)
			headlessLog.Debug(catProxy, "Base URL: %s", defaultProxy.BaseURL)
			if defaultProxy.Description != "" {
				headlessLog.Debug(catProxy, "Description: %s", defaultProxy.Description)
			}
			// Note: Actual provider URL is determined during SDK initialization
			headlessLog.Debug(catProxy, "Requests will be routed through proxy")
		} else {
			headlessLog.Debug(catProxy, "DISABLED (direct connection)")
		}
	}

	// Initialize hooks manager — reuse the SDK's existing manager (which already
	// has autogenskills budget enforcement + lifecycle hooks wired in) unless
	// disabled. Previously this created a brand-new HooksManager, which silently
	// discarded the autogenskills hooks registered during SDK initialization.
	var hooksManager *chat.HooksManager
	if !isolation.noHooks {
		hooksManager = sdk.GetHooksManager()
		if hooksManager == nil {
			// Fallback: create a new one if SDK didn't initialize one
			hooksManager = chat.NewHooksManager(sdk.Logger(), sdk.Tracer(), workspaceRoot)
			sdk.SetHooksManager(hooksManager)
		}

		// Hooks are already registered by NewHooksManager.registerBuiltinHooks()
		// No need to register them again here

		if *verboseFlag || *hooksVerboseFlag {
			stats := hooksManager.GetStats()
			headlessLog.Debug(catHooks, "Initialized with %d hooks (%d enabled)", stats.TotalHooks, stats.EnabledHooks)
		}
		// 1. Project-level .claude/settings.json
		// 2. User-level ~/.claude/settings.json
		// 3. SwarmOS native ~/.swarmos/hooks.json
		hooksConfig, err := chat.LoadHooksConfigWithProject(workspaceRoot)
		if *verboseFlag || *hooksVerboseFlag {
			if err != nil {
				headlessLog.Debug(catHooks, "Failed to load hooks: %v", err)
			} else if hooksConfig == nil {
				headlessLog.Debug(catHooks, "No hooks found")
			} else {
				headlessLog.Debug(catHooks, "Loaded %d hooks from project + user + system configs", len(hooksConfig.CustomHooks))
			}
		}
		if err == nil && hooksConfig != nil {
			// Register enabled custom hooks
			enabledHooks := hooksConfig.GetEnabledHooks()
			if *verboseFlag || *hooksVerboseFlag {
				headlessLog.Debug(catHooks, "Found %d enabled hooks", len(enabledHooks))
			}
			for _, hookConfig := range enabledHooks {
				shellHook, err := hookConfig.ToShellHook()
				if err != nil {
					if *verboseFlag || *hooksVerboseFlag {
						headlessLog.Debug(catHooks, "Failed to create hook %s: %v", hookConfig.Name, err)
					}
					continue
				}
				if err := hooksManager.RegisterCustomHook(shellHook); err != nil {
					if *verboseFlag || *hooksVerboseFlag {
						headlessLog.Debug(catHooks, "Failed to register hook %s: %v", hookConfig.Name, err)
					}
					continue
				}
				if *verboseFlag || *hooksVerboseFlag {
					headlessLog.Debug(catHooks, "Registered hook: %s (patterns: %v)", hookConfig.Name, hookConfig.EventPatterns)
				}
			}
		}

		// Set file read registrar for nested INDEX.md discovery
		hooksManager.SetFileReadRegistrar(sdk.ContextOrchestrator())

		if *verboseFlag || *hooksVerboseFlag {
			stats := hooksManager.GetStats()
			headlessLog.Debug(catHooks, "Initialized with %d hooks (%d enabled)", stats.TotalHooks, stats.EnabledHooks)
		}
	} else if *verboseFlag {
		headlessLog.Debug(catHooks, "Disabled via --no-hooks flag")
	}

	modeID := strings.ToLower(strings.TrimSpace(*operatingModeFlag))
	if modeID == "" {
		modeID = "act"
	}
	switch modeID {
	case "act", "auto", "plan":
	default:
		return fmt.Errorf("invalid --operating-mode %q: expected ACT, AUTO, or PLAN", *operatingModeFlag)
	}
	sdk.SetOperatingModeForExecution(modeID)

	// Create or resume conversation
	var convID string
	if *conversationIDFlag != "" {
		resumed, err := sdk.ResumeConversation(ctx, *conversationIDFlag)
		if err != nil {
			return fmt.Errorf("failed to resume conversation %q: %w", *conversationIDFlag, err)
		}
		convID = resumed.ID
		headlessLog.Debug(catSystem, "Resumed conversation %s (%d messages)", convID, len(resumed.Messages))
		sdk.SetActiveConversation(convID)
	} else {
		// Headless (`swarm -p`) conversations are tagged conversation.HeadlessTag
		// so they are excluded from the browsable TUI conversation picker and the
		// agent-facing HistorySearch tool. They are still persisted on disk and
		// resumable via `swarm -p --id <id>`.
		created, err := sdk.CreateHeadlessConversation(ctx, modeID)
		if err != nil {
			return fmt.Errorf("failed to create conversation: %w", err)
		}
		convID = created.ID
	}

	// Apply the caller-owned base prompt before context injection so project
	// context is appended deterministically instead of being silently erased.
	if customSystemPrompt != "" {
		sdk.SetSystemPrompt(customSystemPrompt)
		contribution := "headless_" + strings.TrimPrefix(customSystemPromptSource, "--")
		sdk.RecordStartupPromptContribution(contribution, len(customSystemPrompt))
		headlessLog.Debug(catSystem, "Custom system prompt loaded from %s (%d chars)", customSystemPromptSource, len(customSystemPrompt))
	}

	// Inject persistent memory (dream contract) + context files into the system
	// prompt before the agent runs. The TUI app does this at startup (see
	// app_init.go); headless used to skip it, leaving the agent with no memory
	// access. Without this call the rich-index path never reaches the agent.
	if loader := sdk.GetContextLoader(); loader != nil {
		base := sdk.AgentSystemPrompt()
		if err := sdk.LoadAndInjectContext(loader, base); err != nil {
			headlessLog.Debug(catContext, "LoadAndInjectContext failed: %v", err)
		} else {
			headlessLog.Debug(catContext, "memory + context injected into system prompt")
		}
	}

	// Emit session start event to fire hooks (plan mode guidance, etc.)
	if hooksManager != nil {
		sessionStartResults := hooksManager.EmitSessionStart(ctx, convID)
		if *verboseFlag || *hooksVerboseFlag {
			if len(sessionStartResults) > 0 {
				headlessLog.Debug(catHooks, "Session start hook fired %d results", len(sessionStartResults))
			}
		}
	}

	// Show startup info: tools and MCP servers (always show in headless mode)
	toolList := sdk.GetToolRegistry().List()
	mcpManager := sdk.GetMCPManager()
	connectedMCPs := 0
	totalMCPTools := 0
	totalMCPServers := 0
	if mcpManager != nil {
		mcpServers := mcpManager.GetServers()
		totalMCPServers = len(mcpServers)
		for _, server := range mcpServers {
			if server.Connected {
				connectedMCPs++
				totalMCPTools += len(server.Tools)
			}
		}
	}
	headlessLog.Banner([]string{
		"┌─────────────────────────────────────────────────────────────────────────────┐",
		"│ SwarmOS Headless Mode                                                       │",
		"├─────────────────────────────────────────────────────────────────────────────┤",
		fmt.Sprintf("│ Tools: %-4d  (builtin: %-4d  MCP: %-4d)                                     │",
			len(toolList), len(toolList)-totalMCPTools, totalMCPTools),
		fmt.Sprintf("│ MCP Servers: %-4d connected / %-4d configured                               │",
			connectedMCPs, totalMCPServers),
		"└─────────────────────────────────────────────────────────────────────────────┘",
	})
	// Wait a moment for async MCP connections to complete/fail
	time.Sleep(1 * time.Second)

	// Reconnect any enabled servers that aren't connected yet
	if mcpManager != nil {
		mcpManager.ReconnectAllServers(context.Background())
		// Wait a bit more for reconnections to complete
		time.Sleep(500 * time.Millisecond)
	}

	// Apply the --tools / --disable-tools registry filter AFTER MCP servers have
	// connected + registered their tools. These flags were historically declared
	// and advertised in --help but had ZERO read sites, so passing them silently
	// did nothing. Filtering here (rather than immediately after SDK construction)
	// is deliberate: MCP tools register into the same registry during the connect
	// + ReconnectAllServers phase above, so an earlier one-shot sweep would let
	// MCP tools slip past the allowlist. --tools is an allowlist (only those
	// survive), --disable-tools is a denylist. Unknown names are a hard error so
	// a typo can't leave the agent over-provisioned.
	if applyErr := applyToolFilters(sdk.GetToolRegistry(), *toolsFlag, *disableToolsFlag, headlessLog); applyErr != nil {
		return applyErr
	}
	// Re-snapshot the (possibly filtered) tool list so the debug listing below
	// reflects what the agent will actually see.
	toolList = sdk.GetToolRegistry().List()

	os.Stderr.Sync() // Ensure output is flushed

	// Verbose debug: show all registered tools
	headlessLog.Debug(catDebug, "Registered Tools (%d):", len(toolList))
	for i, toolName := range toolList {
		headlessLog.Debug(catDebug, "  %3d. %s", i+1, toolName)
	}

	// Verbose debug: show MCP server status and errors
	if mcpManager != nil {
		mcpServers := mcpManager.GetServers()
		headlessLog.Debug(catDebug, "MCP Servers (%d):", len(mcpServers))
		for _, server := range mcpServers {
			status := "❌ NOT CONNECTED"
			if server.Connected {
				status = fmt.Sprintf("✅ CONNECTED (%d tools)", len(server.Tools))
			}
			autoConnect := server.Config.ShouldAutoConnect()
			enabled := server.Config.Enabled
			headlessLog.Debug(catDebug, "  • %s [%s] %s (enabled=%v autoConnect=%v)",
				server.Config.Name, server.Config.GetType(), status, enabled, autoConnect)
			if server.Error != "" {
				headlessLog.Debug(catDebug, "    ERROR: %s", server.Error)
			}
			if server.LastError != nil {
				headlessLog.Debug(catDebug, "    LAST ERROR: %v", server.LastError)
			}
			if !server.ErrorTimestamp.IsZero() {
				headlessLog.Debug(catDebug, "    Error Time: %s", server.ErrorTimestamp.Format("15:04:05"))
			}
			if server.ConnectionAttempts > 0 {
				headlessLog.Debug(catDebug, "    Attempts: %d, Last: %s", server.ConnectionAttempts, server.LastAttempt.Format("15:04:05"))
			}
		}

		// Always surface enabled-but-failed MCP servers so users can diagnose
		// without needing --debug.
		for _, server := range mcpServers {
			if !server.Connected && server.Config.Enabled {
				errMsg := server.Error
				if errMsg == "" && server.LastError != nil {
					errMsg = server.LastError.Error()
				}
				if errMsg == "" {
					errMsg = "connection failed (no error detail)"
				}
				fmt.Fprintf(os.Stderr, "[MCP] ⚠ %s (%s): %s\n",
					server.Config.Name, server.Config.GetType(), errMsg)
			}
		}
	}

	// NOTE: UserPromptSubmit hooks (including the built-in task-nudge) are fired
	// inside sdk.ExecuteMessage 	 sdk_integration_execution.go.  There is no manual
	// hook call here; calling it twice would inject the nudge twice.
	userMessage := *promptFlag

	// Emit message.after_receive event for hooks (e.g., plan-following detection)
	if hooksManager != nil {
		hooksManager.EmitMessageAfterReceive(ctx, convID, userMessage)
	}

	// Install stdout guard for stream-json mode: any stray non-JSON write to
	// stdout would break the NDJSON parser on the consumer side.
	var stdoutWriter io.Writer = os.Stdout
	if outputFormat == "stream-json" {
		stdoutWriter = newStdoutGuard(os.Stdout, os.Stderr)
	}

	// Create headless printer for structured output.
	printer := newHeadlessPrinter(outputFormat, modelName, *verboseFlag, *showThinkingFlag, *includeHookEventsFlag, *debugToStderrFlag, stdoutWriter, headlessLog)

	// Emit init event for stream-json mode (contains session metadata).
	if printer.isStreamJSON() {
		printer.emitInit(toolList)
	}

	// Channel for streaming updates
	updateChan := make(chan agent.IntermediateUpdate, 100)

	// Execute in goroutine
	errChan := make(chan error, 1)
	go func() {
		_, err := sdk.ExecuteMessageWithMaxTurns(ctx, convID, userMessage, modelName, updateChan, nil, *maxTurnsFlag)
		missing := missingRequiredOutputs(workspaceRoot, requireOutputFlags)
		if err == nil && len(missing) > 0 && *requireOutputRepairTurnsFlag > 0 {
			repairPrompt := requiredOutputRepairPrompt(missing)
			headlessLog.Info(catSystem, "Required outputs missing; starting repair turn for %s", strings.Join(missing, ", "))
			_, err = sdk.ExecuteMessageWithMaxTurns(ctx, convID, repairPrompt, modelName, updateChan, nil, *requireOutputRepairTurnsFlag)
			missing = missingRequiredOutputs(workspaceRoot, requireOutputFlags)
		}
		if err == nil && len(missing) > 0 {
			err = fmt.Errorf("required output files are missing or empty after repair: %s", strings.Join(missing, ", "))
		}
		close(updateChan)
		errChan <- err
	}()

	// Engine tracing: execution started.
	if *debugToStderrFlag {
		headlessLog.Engine("execution started: model=%s prompt=%q", modelName, userMessage)
	}

	// Track last token update so we can print an accurate final summary AND
	// drive the same model_metrics.json file the TUI's usage tab reads.
	// Stream start time is captured here (not in the agent) because the
	// headless harness is what owns the wall clock for this turn.
	var lastTokenUpdate *agent.TokenCountUpdate
	streamStart := time.Now()

	// Print streaming updates
	for update := range updateChan {
		printer.handleUpdate(update)
		if tu, ok := update.(agent.TokenCountUpdate); ok {
			lastTokenUpdate = &tu
		}
	}

	// Wait for execution to complete
	execErr := <-errChan

	// Emit the final result event through the printer.
	printer.emitResult(printer.resultText, execErr)

	// Emit agent stopped event to fire verification protocol hook
	if hooksManager != nil {
		finishReason := "end_turn"
		if execErr != nil {
			finishReason = "error"
		}
		hookResults := hooksManager.EmitAgentStopped(ctx, convID, finishReason, userMessage, printer.resultText)
		if *verboseFlag || *hooksVerboseFlag {
			if len(hookResults) > 0 {
				headlessLog.Debug(catHooks, "Agent stopped hook fired %d results", len(hookResults))
				for _, hr := range hookResults {
					if hr.Output != "" {
						headlessLog.Debug(catHook, "%s", hr.Output)
					}
				}
			}
		}
	}

	// Persist token counts to model_metrics.json so the TUI's usage tab
	// reflects headless-mode runs too. Without this, `-p` invocations were
	// invisible to the lifetime totals (separate from the time.Since-zero
	// bug fixed in metrics/persistence.go).
	if lastTokenUpdate != nil && lastTokenUpdate.InputTokens+lastTokenUpdate.OutputTokens > 0 {
		store := metrics.NewMetricsStore(getHeadlessMetricsPath())
		if n := store.RepairCorruptedDurations(); n > 0 {
			headlessLog.Debug(catMetrics, "repaired %d corrupted duration entries", n)
		}
		store.RecordTurnTokens(modelName, modelName,
			lastTokenUpdate.InputTokens, lastTokenUpdate.OutputTokens,
			time.Since(streamStart))
		if err := store.SaveToDisk(); err != nil {
			headlessLog.Debug(catMetrics, "save failed: %v", err)
		} else {
			headlessLog.Debug(catMetrics, "recorded turn tokens: model=%s in=%d out=%d",
				modelName, lastTokenUpdate.InputTokens, lastTokenUpdate.OutputTokens)
		}
	}

	// Print final token summary + cache stats if verbose.
	// Use the authoritative InputTokens from the last TokenCountUpdate instead of
	// the old cacheCreation+cacheRead sum (which was always 0 on cache misses).
	cacheCreation, cacheRead := sdk.GetTotalCacheStats()
	if lastTokenUpdate != nil {
		if lastTokenUpdate.ContextWindow > 0 {
			headlessLog.Debug(catTokens, "final: input=%d output=%d window=%d pct=%.1f%%",
				lastTokenUpdate.InputTokens, lastTokenUpdate.OutputTokens,
				lastTokenUpdate.ContextWindow, lastTokenUpdate.PctUsed)
		} else {
			headlessLog.Debug(catTokens, "final: input=%d output=%d",
				lastTokenUpdate.InputTokens, lastTokenUpdate.OutputTokens)
		}
	}
	headlessLog.Debug(catTokens, "cache creation: %d, read: %d", cacheCreation, cacheRead)
	if cacheRead > 0 {
		headlessLog.Debug(catTokens, "Cache HIT! Saved %d input tokens", cacheRead)
	} else if cacheCreation > 0 {
		headlessLog.Debug(catTokens, "Cache CREATED: %d tokens cached for future requests", cacheCreation)
	}

	return execErr
}

// getHeadlessMetricsPath mirrors the TUI's getMetricsPath helper so headless
// `-p` invocations land in the same model_metrics.json the usage tab reads.
// Kept here (not imported from internal/chat) because the headless path
// deliberately avoids constructing a full *App.
func getHeadlessMetricsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	dir := configDir + string(os.PathSeparator) + "swarm-tui" + string(os.PathSeparator) + "metrics"
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// runTUI executes TUI mode (existing code)
func runTUI() {
	if *autoRecoverFlag {
		if records, err := listSessionRecords(); err == nil {
			for _, record := range records {
				if record.State == sessionStateInterrupted && record.Intent == sessionIntentRunning && record.ID != strings.TrimSpace(*sessionIDFlag) {
					_ = resumeSession(record)
				}
			}
		}
	}
	// Initialize bubblezone but disable it: zone.Mark() calls in components become
	// no-ops (return string unchanged), and we never call zone.Scan() so no per-frame
	// zone scanning overhead.
	zone.NewGlobal()
	zone.SetEnabled(false)

	// Start pprof HTTP server if SWARMOS_PPROF is set (e.g., "6060")
	if pprofPort := os.Getenv("SWARMOS_PPROF"); pprofPort != "" {
		go func() {
			addr := "localhost:" + pprofPort
			fmt.Fprintf(os.Stderr, "pprof server starting on http://%s/debug/pprof/\n", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				fmt.Fprintf(os.Stderr, "pprof server error: %v\n", err)
			}
		}()
	}

	// Initialize performance optimizations
	optConfig := tui.DefaultOptimizationConfig()
	ctx := context.Background()
	poolManager, err := tui.InitializeOptimizations(ctx, optConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to initialize optimizations: %v\n", err)
	}
	// Defer cleanup
	if poolManager != nil {
		defer func() {
			if err := poolManager.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to close pool manager: %v\n", err)
			}
		}()
	}

	// Daemon-backed mode (opt-in via --daemon or SWARM_DAEMON): instead of running
	// the engine in-process, ensure THE global background daemon and render it as a
	// thin client. Detaching leaves the daemon — and its conversations/agents —
	// running for cron jobs. This is the same path that becomes the bare-`swarm`
	// default once the full TUI renders over serve (AG-TUI).
	if daemonBackedRequested(*daemonBackedFlag, os.Getenv("SWARM_DAEMON")) {
		// ADR-002 compatibility-matrix rows 8/9: both `swarm --daemon` and the
		// `SWARM_DAEMON` environment selection are bounded deprecated aliases
		// for "Daemon-backed `swarm attach`". Warn once, stderr-only, on
		// whichever selection mechanism actually fired — never both, and
		// never in machine-output mode (the TUI has none today, so this is
		// unconditionally human-mode).
		if *daemonBackedFlag {
			warnLegacyAlias("swarm --daemon", false)
		} else if strings.TrimSpace(os.Getenv("SWARM_DAEMON")) != "" {
			warnLegacyAlias("SWARM_DAEMON", false)
		}
		ws := strings.TrimSpace(*workspaceFlag)
		if ws == "" {
			ws, _ = os.Getwd()
		}
		if err := runDaemonBackedTUI(ws); err != nil {
			fmt.Fprintf(os.Stderr, "daemon-backed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// The global background daemon is ensured AFTER the tea.Program exists
	// (see the autoEnsureGlobalDaemon call below app.SetProgram) so that
	// startup failures surface as a visible TUI banner via App.NotifyAsync
	// instead of dying silently in the debug log.

	// Enable bracketed paste mode for proper paste event handling
	enableBracketedPaste()
	defer disableBracketedPaste() // Ensure it's disabled when we exit

	// Initialize auto-updater (unless disabled via --no-update flag)
	var updaterInstance *update.Updater
	if !*noUpdateFlag {
		// Stable binaries are published from the public distribution repository.
		// TODO: Make this configurable via flag or config file
		updaterInstance, _ = update.NewUpdater("")
	}

	// Resolve peer handle for swarm presence + control socket.
	// Always computed — used even when --a2a is not set, so the control
	// plane is discoverable by default.
	peerHandle := computePeerHandle()
	sessionID := strings.TrimSpace(*sessionIDFlag)
	if sessionID == "" {
		sessionID = newSessionID()
	}
	sessionRecord := sessionRecordForLaunch(
		strings.TrimSpace(*workspaceFlag),
		strings.TrimSpace(*workspaceFlag),
		strings.TrimSpace(*conversationIDFlag),
		peerHandle,
		sessionID,
	)
	if sessionRecord.Workspace == "" {
		sessionRecord.Workspace, _ = os.Getwd()
		sessionRecord.Args = sessionArgs(sessionRecord.Workspace, sessionRecord.Conversation, sessionID)
	}
	_ = saveSessionRecord(sessionRecord)
	defer func() {
		sessionRecord.State = sessionStateStopped
		sessionRecord.Intent = sessionIntentStopped
		sessionRecord.Events = append(sessionRecord.Events, "interactive process exited normally")
		_ = saveSessionRecord(sessionRecord)
	}()

	// Create app - either debug render mode or normal mode
	imageManager := termimage.NewManager()
	if !xterm.IsTerminal(os.Stdout.Fd()) {
		imageManager.SetCapability(termimage.Unsupported)
	}
	var model tea.Model
	if *debugRender {
		model = chat.NewDebugRenderApp()
	} else {
		if *debugMode {
		}
		if *newUIFlag {
		}
		appOptions := currentAppOptions(updaterInstance, peerHandle)
		if harnessFlagSelected(*harnessFlag) {
			harnessPath, found := discoverHarnessPath(*harnessFlag)
			if !found {
				fmt.Fprintln(os.Stderr, "Error: --harness . requires ./harness.yaml in the current directory")
				return
			}
			appOptions.HarnessPath = harnessPath
			appOptions.HarnessAllowYolo = *harnessAllowYoloFlag
		}
		appOptions.ImageManager = imageManager
		model = chat.NewAsyncAppWithOptions(appOptions)
	}
	var liveApp atomic.Pointer[chat.App]
	if app, ok := model.(*chat.App); ok {
		liveApp.Store(app)
	}

	imageWriter := termimage.NewWriter(os.Stdout, imageManager)
	imageOutput := io.Writer(imageWriter)
	// Some embedded/degraded PTYs report 0x0. Hide the output's Fd identity in
	// that case so Bubble Tea retains the explicit 80x24 first-frame fallback.
	if w, h, err := xterm.GetSize(os.Stdout.Fd()); err == nil && (w <= 0 || h <= 0) {
		imageOutput = struct{ io.Writer }{imageOutput}
	}

	programOptions := []tea.ProgramOption{
		tea.WithWindowSize(80, 24), // Positive first-frame fallback; real resize events replace it.
		tea.WithFPS(60),            // 60fps renderer — smartLayer makes unchanged frames O(1); gives ≤16ms keystroke latency
		tea.WithOutput(imageOutput),
	}
	p := tea.NewProgram(model, programOptions...)
	if app, ok := model.(*chat.App); ok {
		app.SetProgram(p)
	}

	// ── Global background daemon (default ON, 100% of TUI launches) ─────────
	// Ensure THE machine-wide daemon exists: it serves the LAN gateway on
	// :8787, runs cron/background work, and keeps running after this UI
	// closes. autoEnsureGlobalDaemon retries with backoff, replaces wedged
	// daemons, restarts idle daemons left on an outdated binary, and reports
	// the outcome as a visible banner (warning on failure). Never blocks TUI
	// startup. Opt out with SWARM_NO_AUTO_DAEMON=1 (the systemd unit sets
	// this — the service already guarantees the daemon).
	if os.Getenv("SWARM_NO_AUTO_DAEMON") == "" {
		notify := func(level, message string) {
		}
		if liveApp.Load() != nil {
			notify = func(level, message string) {
				if app := liveApp.Load(); app != nil {
					app.NotifyAsync(level, message)
				}
			}
		}
		ws := strings.TrimSpace(*workspaceFlag)
		if ws == "" {
			ws, _ = os.Getwd()
		}
		go autoEnsureGlobalDaemon(ws, notify)
	}

	// ── Control plane (always on) ──────────────────────────────────────────
	// Every TUI session registers itself in the swarm and starts a Unix-socket
	// control server. This lets any external tool (swarmos swarm attach,
	// conductor, scripts) read the live screen, inject keystrokes, and stream
	// SDK events — no flags required.
	//
	// Socket lives next to the peer presence file:
	//  ~/.swarmos/swarms/default/peers/<handle>.ctrl
	ctrlSockDir := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers")
	_ = os.MkdirAll(ctrlSockDir, 0755)
	ctrlSock := filepath.Join(ctrlSockDir, peerHandle+".ctrl")

	ctrlSrv := attserver.NewAttached(p, model, 220, 50, nil, ctrlSock)
	if app := liveApp.Load(); app != nil {
		app.SetModelPromotedCallback(func(next *chat.App) {
			liveApp.Store(next)
			ctrlSrv.SetModel(next)
		})
	}
	ctrlCtx, ctrlCancel := context.WithCancel(context.Background())
	defer ctrlCancel()

	// Bind synchronously before publishing the path. A freshly advertised
	// socket must already be dialable; otherwise attach clients can race the
	// server goroutine's Listen call. A bind failure is non-fatal to the TUI and
	// deliberately leaves the peer presence without a bad control_socket. Both
	// steps join the same per-handle a2a.WithPeerHandleTransaction (Phase 01
	// CONTRACT.md) as conditional cleanup and any replacement instance's own
	// startup, so a concurrent same-handle transaction can never observe a
	// bound-but-unpublished (or published-but-unbound) control socket.
	if err := listenAndPublishControlSocket(peerHandle, ctrlSrv, func(tx *a2a.PeerHandleTransaction) error {
		return patchControlSocket(tx, peerHandle, ctrlSock)
	}); err != nil {
		ctrlSrv.Stop()
	} else {
		sessionRecord.ControlSocket = ctrlSock
		sessionRecord.Handle = peerHandle
		_ = saveSessionRecord(sessionRecord)
		defer ctrlSrv.Stop()
		go func() {
			if err := ctrlSrv.Start(ctrlCtx); err != nil {
			}
		}()
	}
	defer func() {
		if leaveOwnedPeer(peerHandle) {
		}
	}()

	// ── Rich native attach path (serve.Mux) ──────────────────────────────────
	// SDK construction now happens behind the first frame. Register an
	// idempotent readiness callback so native attach endpoints appear as soon as
	// the client exists, without doing listen or presence I/O on Update.
	var serveMu sync.Mutex
	var serveServer *http.Server
	var serveOnce sync.Once
	startNativeServe := func() {
		serveOnce.Do(func() {
			app := liveApp.Load()
			if app == nil {
				return
			}
			srv := mountNativeServe(app, peerHandle)
			if srv != nil {
				serveMu.Lock()
				serveServer = srv
				serveMu.Unlock()
			}
		})
	}
	if app := liveApp.Load(); app != nil {
		app.SetRuntimeReadyCallback(startNativeServe)
		if app.SDKClient() != nil {
			go startNativeServe()
		}
	}
	defer func() {
		serveMu.Lock()
		defer serveMu.Unlock()
		if serveServer != nil {
			_ = serveServer.Close()
		}
	}()

	_, runErr := p.Run()
	if err := imageWriter.Flush(); err != nil {
	}
	if err := imageManager.Release(os.Stdout); err != nil {
	}
	if runErr != nil {
		sentry.CaptureException(runErr)
		sentry.Flush(2 * time.Second)
		fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
		os.Exit(1)
	}

}

func mountNativeServe(app *chat.App, peerHandle string) *http.Server {
	cl := app.SDKClient()
	if cl == nil {
		return nil
	}

	mux := serve.NewMux(cl)
	httpMux := http.NewServeMux()
	httpMux.Handle("/rpc", mux.HTTPHandler())
	httpMux.Handle("/sse", mux.SSEHandler())
	httpMux.Handle("/ws", mux.WebSocketHandler())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil
	}

	serveURL := "http://" + ln.Addr().String()
	srv := &http.Server{Handler: httpMux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		}
	}()
	if existing, err := a2a.GetPeer(a2a.DefaultSwarmName, peerHandle); err == nil && existing != nil {
		existing.ServeURL = serveURL
		_ = a2a.JoinSwarm(a2a.DefaultSwarmName, *existing)
	}
	return srv
}

func currentAppOptions(updaterInstance *update.Updater, peerHandle string) chat.AppOptions {
	// Resolve workspace: explicit flag wins; fall back to cwd so the hub
	// always starts and peer discovery works without --workspace.
	workspaceRoot := strings.TrimSpace(*workspaceFlag)
	if workspaceRoot == "" {
		workspaceRoot, _ = os.Getwd()
	}
	// Workspace/worktree binding can touch git metadata, so the async runtime
	// constructor resolves this initial same-root pair after the shell is visible.
	projectRoot := workspaceRoot
	return chat.AppOptions{
		DebugMode:             *debugMode,
		UseNewUI:              *newUIFlag,
		DebugLineageFixture:   *debugLineageFixture,
		Updater:               updaterInstance,
		A2AEnabled:            true, // always on — every TUI session receives tasks
		A2AHandle:             peerHandle,
		A2AListenAddress:      *a2aListenFlag,
		WorkspaceRoot:         workspaceRoot,
		ProjectRoot:           projectRoot,
		TrackWorkspaceSession: true,
		ResumeConversationID:  strings.TrimSpace(*resumeConversationFlag),
		InitialPrompt:         strings.TrimSpace(*initialPromptFlag),
	}
}

func validatePeerHandle(handle string) error {
	if handle == "" {
		return nil
	}
	if len(handle) > 64 {
		return fmt.Errorf("must be at most 64 characters")
	}
	if handle == "." || handle == ".." {
		return fmt.Errorf("must not be %q", handle)
	}
	for _, character := range handle {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("contains unsupported character %q", character)
	}
	return nil
}

// computePeerHandle derives a peer handle for this TUI session.
// Priority: --a2a-handle flag > hostname-based auto-name with a random suffix.
func computePeerHandle() string {
	if h := strings.TrimSpace(*a2aHandleFlag); h != "" {
		return h
	}
	host, _ := os.Hostname()
	host = strings.ToLower(strings.TrimSpace(host))
	if idx := strings.Index(host, "."); idx > 0 {
		host = host[:idx]
	}
	if host == "" {
		host = "swarm"
	}
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), newPeerHandleSuffix())
}

func newPeerHandleSuffix() string {
	var entropy [6]byte
	if _, err := rand.Read(entropy[:]); err == nil {
		return fmt.Sprintf("%x", entropy[:])
	}
	// crypto/rand failures are exceptional; retain process/time uniqueness
	// rather than falling back to the old second-resolution suffix.
	return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
}

type controlSocketListener interface {
	Listen() error
}

// controlSocketStopper is implemented by production control-socket listeners
// (*attserver.AttachedServer) so listenAndPublishControlSocket can call an
// identity-checked Stop before releasing the registry transaction when
// publication fails after a successful bind. Test doubles that only exercise
// the bind-failure path need not implement it.
type controlSocketStopper interface {
	Stop()
}

// listenAndPublishControlSocket enforces the control-plane readiness order:
// bind first, publish second — and, per Phase 01 CONTRACT.md, joins BOTH
// steps to the SAME per-handle a2a.WithPeerHandleTransaction as conditional
// cleanup and any concurrent replacement instance's own startup, so a
// same-handle transaction elsewhere can never observe a socket that is bound
// but not yet published (or published but not yet bound). publish receives
// the already-locked transaction and must use ONLY tx.Get()/tx.Publish() for
// this handle — never JoinSwarm, LeaveSwarm, UpdateStatus, or
// RemovePeerIfInstance, all of which reacquire the same non-reentrant
// per-handle lock and would deadlock the calling goroutine against itself.
// The publisher is never called after a bind error. Any publish error after
// a successful bind calls the identity-checked Stop (when server implements
// controlSocketStopper) before this function — and therefore
// WithPeerHandleTransaction — returns, so the per-handle lock is never
// released while a bound-but-abandoned socket is still on disk.
func listenAndPublishControlSocket(handle string, server controlSocketListener, publish func(tx *a2a.PeerHandleTransaction) error) error {
	return a2a.WithPeerHandleTransaction(a2a.DefaultSwarmName, handle, func(tx *a2a.PeerHandleTransaction) error {
		if err := server.Listen(); err != nil {
			return err
		}
		if err := publish(tx); err != nil {
			if stopper, ok := server.(controlSocketStopper); ok {
				stopper.Stop()
			}
			return fmt.Errorf("publish control socket: %w", err)
		}
		return nil
	})
}

func leaveOwnedPeer(handle string) bool {
	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil || peer == nil || peer.PID != os.Getpid() {
		return false
	}
	return a2a.LeaveSwarm(a2a.DefaultSwarmName, handle) == nil
}

// patchControlSocket adds (or updates) the control_socket field in this peer's
// swarm presence file without overwriting fields the A2A runtime already wrote.
// If no entry exists, it creates a minimal presence entry for discovery. tx is
// the already-locked per-handle transaction from listenAndPublishControlSocket
// (or a caller-opened a2a.WithPeerHandleTransaction in tests); this function
// reads and writes ONLY through tx.Get()/tx.Publish() and must never call
// JoinSwarm/GetPeer/LeaveSwarm directly — those reacquire the same
// non-reentrant per-handle lock the transaction already holds.
func patchControlSocket(tx *a2a.PeerHandleTransaction, handle, sockPath string) error {
	existing, err := tx.Get()
	if err != nil {
		return fmt.Errorf("read peer %q before control socket publication: %w", handle, err)
	}
	now := time.Now().UTC()
	if existing == nil {
		if err := tx.Publish(a2a.PeerPresence{
			Handle:        handle,
			PID:           os.Getpid(),
			Status:        "active",
			ControlSocket: sockPath,
			Type:          a2a.PeerTypeLocal,
			StartedAt:     now,
			LastSeenAt:    now,
		}); err != nil {
			return err
		}
		return nil
	}
	if existing.PID != 0 && existing.PID != os.Getpid() {
		return fmt.Errorf("peer %q is owned by pid %d", handle, existing.PID)
	}
	existing.PID = os.Getpid()
	existing.ControlSocket = sockPath
	existing.LastSeenAt = now
	if err := tx.Publish(*existing); err != nil {
		return err
	}
	return nil
}

// toolRegistryFilter is the minimal registry surface applyToolFilters needs.
// Defining it locally avoids importing internal/tools here and makes the
// filter logic unit-testable with a fake.
type toolRegistryFilter interface {
	List() []string
	Unregister(name string) error
}

// parseCSVSet splits a comma-separated flag value into a trimmed, de-duplicated
// set of non-empty names.
func parseCSVSet(v string) map[string]bool {
	out := make(map[string]bool)
	for _, part := range strings.Split(v, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// applyToolFilters honors the --tools (allowlist) and --disable-tools (denylist)
// flags by mutating the constructed tool registry in place. These flags were
// historically advertised in --help but had no read sites, so passing them did
// nothing — a truthfulness defect that could leave an operator believing they
// had sandboxed the agent's tools when they had not.
//
// Semantics:
//   - --tools "A,B": only A and B survive; every other tool is unregistered.
//   - --disable-tools "C": C is removed (applied after the allowlist).
//   - Unknown names in either flag are a hard error (a typo must not silently
//     leave the agent over-provisioned).
//
// A nil logger is tolerated (no debug output).
func applyToolFilters(reg toolRegistryFilter, toolsCSV, disableCSV string, log *logWriter) error {
	if reg == nil {
		return nil
	}
	toolsCSV = strings.TrimSpace(toolsCSV)
	disableCSV = strings.TrimSpace(disableCSV)
	if toolsCSV == "" && disableCSV == "" {
		return nil // nothing requested; leave the full registry intact
	}

	// Snapshot current names into a lookup set for validation.
	present := make(map[string]bool)
	for _, name := range reg.List() {
		present[name] = true
	}

	allow := parseCSVSet(toolsCSV)
	disable := parseCSVSet(disableCSV)

	// Validate: every explicitly-named tool must exist. This catches typos.
	var unknown []string
	for name := range allow {
		if !present[name] {
			unknown = append(unknown, name)
		}
	}
	for name := range disable {
		if !present[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown tool name(s) in --tools/--disable-tools: %s (available: %s)",
			strings.Join(unknown, ", "), strings.Join(reg.List(), ", "))
	}

	removed := 0
	// Apply the allowlist first: drop anything not in it.
	if len(allow) > 0 {
		for _, name := range reg.List() {
			if !allow[name] {
				if err := reg.Unregister(name); err == nil {
					removed++
				}
			}
		}
	}
	// Apply the denylist second: drop explicitly-disabled tools that survived.
	for name := range disable {
		if err := reg.Unregister(name); err == nil {
			removed++
		}
	}

	if log != nil {
		if len(allow) > 0 {
			log.Debug(catDebug, "--tools allowlist active: kept %d tool(s), removed %d",
				len(reg.List()), removed)
		} else {
			log.Debug(catDebug, "--disable-tools active: removed %d tool(s), %d remain",
				removed, len(reg.List()))
		}
	}
	return nil
}
