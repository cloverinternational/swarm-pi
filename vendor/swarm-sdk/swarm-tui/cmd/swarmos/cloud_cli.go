package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/launch"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

type cloudCLIDeps struct {
	stdout            io.Writer
	stderr            io.Writer
	newConfigManager  func() (*cloud.ConfigManager, error)
	newTokenManager   func() (*cloud.TokenManager, error)
	newClient         func(*cloud.CloudConfig, *cloud.TokenManager) cloudCLIClient
	startDeviceLink   func(context.Context, *cloud.CloudConfig, cloud.DeviceLinkInfo) (*cloud.DeviceLinkStartResult, error)
	pollDeviceLink    func(context.Context, *cloud.CloudConfig, string) (*cloud.DeviceLinkPollResult, error)
	loginWithHostedUI func(context.Context, *cloud.CloudConfig, *cloud.TokenManager, cloud.LoginOptions) (*cloud.TokenSet, error)
	openTarget        func(string) error
	hostname          func() (string, error)
	now               func() time.Time
	sleep             func(time.Duration)
}

func defaultCloudCLIDeps() cloudCLIDeps {
	return cloudCLIDeps{
		stdout:           os.Stdout,
		stderr:           os.Stderr,
		newConfigManager: cloud.NewConfigManager,
		newTokenManager:  cloud.NewTokenManager,
		newClient: func(cfg *cloud.CloudConfig, tokenManager *cloud.TokenManager) cloudCLIClient {
			return cloud.NewClient(cfg, tokenManager, nil)
		},
		startDeviceLink:   cloud.StartDeviceLink,
		pollDeviceLink:    cloud.PollDeviceLink,
		loginWithHostedUI: cloud.LoginWithHostedUIOptions,
		openTarget:        launch.Open,
		hostname:          os.Hostname,
		now:               time.Now,
		sleep:             time.Sleep,
	}
}

type cloudCLIClient interface {
	SyncHostedProviderCredential(context.Context, string) error
	CreateCloudTask(context.Context, cloud.CreateCloudTaskRequest) (*cloud.CreateCloudTaskResponse, error)
	CollectCloudSessionTask(context.Context, *cloud.HostedSessionSummary, string) (string, error)
	CreateHostedSession(context.Context, cloud.CreateHostedSessionRequest) (*cloud.HostedSessionSummary, error)
	WaitForHostedSessionStatus(context.Context, string, string) (*cloud.HostedSessionSummary, error)
	GetHostedSession(context.Context, string) (*cloud.HostedSessionSummary, error)
	RunCloudSessionTask(context.Context, *cloud.HostedSessionSummary, string, string) (string, error)
	StopHostedSession(context.Context, string) (*cloud.HostedSessionSummary, error)
}

func runCloudCLI(args []string) error {
	return runCloudCLIWithDeps(args, defaultCloudCLIDeps())
}

func runCloudCLIWithDeps(args []string, deps cloudCLIDeps) error {
	if len(args) == 0 {
		printCloudCLIUsage(deps.stderr)
		return nil
	}

	switch args[0] {
	case "login":
		return runCloudLoginCLI(args[1:], deps)
	case "sync-provider":
		return runCloudSyncProviderCLI(args[1:], deps)
	case "ask":
		return runCloudAskCLI(args[1:], deps)
	case "session":
		return runCloudSessionCLI(args[1:], deps)
	case "-h", "--help", "help":
		printCloudCLIUsage(deps.stdout)
		return nil
	default:
		return fmt.Errorf("unknown cloud subcommand: %s", args[0])
	}
}

func printCloudCLIUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: swarmos cloud <subcommand> [options]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  login            Run device-link or browser login and save cloud tokens")
	fmt.Fprintln(w, "  sync-provider    Sync a local provider OAuth credential to Swarm Cloud")
	fmt.Fprintln(w, "  ask              Run a one-shot non-VM cloud task against a project or repo URL")
	fmt.Fprintln(w, "  session          Start, submit to, or stop an interactive cloud session")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Example:")
	fmt.Fprintln(w, "  swarmos cloud login")
	fmt.Fprintln(w, "  swarmos cloud sync-provider openai")
	fmt.Fprintln(w, "  swarmos cloud ask https://github.com/octocat/Spoon-Knife.git \"Summarize this repo\"")
	fmt.Fprintln(w, "  swarmos cloud session start project_123")
	fmt.Fprintln(w, "  swarmos cloud session submit session_123 \"List the important files\"")
	fmt.Fprintln(w, "  swarmos cloud session stop session_123")
	_, _ = fmt.Fprintln(w, "  swarmos cloud login --no-open --timeout 5m")
}

func runCloudLoginCLI(args []string, deps cloudCLIDeps) error {
	fs := flag.NewFlagSet("cloud login", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)

	mode := fs.String("mode", "auto", "Login mode: auto, device, or browser")
	noOpen := fs.Bool("no-open", false, "Do not open the verification URL in the default browser")
	timeout := fs.Duration("timeout", 10*time.Minute, "Maximum time to wait for device approval")
	if err := fs.Parse(args); err != nil {
		return err
	}

	configManager, err := deps.newConfigManager()
	if err != nil {
		return err
	}
	tokenManager, err := deps.newTokenManager()
	if err != nil {
		return err
	}
	cfg, err := configManager.LoadConfig()
	if err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "auto":
		if err := runCloudDeviceLogin(cfg, tokenManager, *noOpen, *timeout, deps); err != nil {
			if !deviceLinkUnavailable(err) {
				return err
			}
			fmt.Fprintln(deps.stdout, "Device-link login unavailable; falling back to browser login.")
			return runCloudBrowserLogin(cfg, tokenManager, *noOpen, *timeout, deps)
		}
		return nil
	case "device":
		return runCloudDeviceLogin(cfg, tokenManager, *noOpen, *timeout, deps)
	case "browser":
		return runCloudBrowserLogin(cfg, tokenManager, *noOpen, *timeout, deps)
	default:
		return fmt.Errorf("unsupported login mode: %s", *mode)
	}
}

func runCloudDeviceLogin(
	cfg *cloud.CloudConfig,
	tokenManager *cloud.TokenManager,
	noOpen bool,
	timeout time.Duration,
	deps cloudCLIDeps,
) error {
	deviceName := "SwarmOS"
	if host, hostErr := deps.hostname(); hostErr == nil && host != "" {
		deviceName = host
	}

	ctx := context.Background()
	start, err := deps.startDeviceLink(ctx, cfg, cloud.DeviceLinkInfo{
		DeviceID:   cfg.DeviceID,
		DeviceName: deviceName,
		App:        "swarmos",
		AppVersion: version.Version,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(deps.stdout, "Open this URL to approve login:\n%s\n\n", start.VerificationURIComplete)
	fmt.Fprintf(deps.stdout, "Code: %s\n", start.UserCode)
	if !noOpen {
		if err := deps.openTarget(start.VerificationURIComplete); err != nil {
			fmt.Fprintf(deps.stderr, "warning: %v\n", err)
		}
	}

	interval := time.Duration(start.IntervalSec) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expiresAt := time.Unix(start.ExpiresAt, 0)
	if timeout > 0 {
		deadline := deps.now().Add(timeout)
		if deadline.Before(expiresAt) {
			expiresAt = deadline
		}
	}

	for deps.now().Before(expiresAt) {
		poll, err := deps.pollDeviceLink(ctx, cfg, start.DeviceCode)
		if err != nil {
			return err
		}
		if poll.ExpiresAt > 0 {
			serverExpiry := time.Unix(poll.ExpiresAt, 0)
			if timeout > 0 {
				deadline := deps.now().Add(timeout)
				if deadline.Before(serverExpiry) {
					serverExpiry = deadline
				}
			}
			expiresAt = serverExpiry
		}
		if poll.IntervalSec > 0 {
			interval = time.Duration(poll.IntervalSec) * time.Second
		}

		switch poll.Status {
		case "approved":
			if poll.Tokens == nil {
				return fmt.Errorf("device link approved but tokens missing")
			}
			if err := tokenManager.SaveTokens(poll.Tokens); err != nil {
				return err
			}
			fmt.Fprintf(deps.stdout, "Login successful. Tokens saved to %s\n", tokenManager.GetTokenPath())
			return nil
		case "expired":
			return fmt.Errorf("device code expired; run login again")
		case "error":
			if poll.Error != "" {
				return fmt.Errorf("device link failed: %s", poll.Error)
			}
			return fmt.Errorf("device link failed")
		}

		deps.sleep(interval)
	}

	return fmt.Errorf("device code expired; run login again")
}

func runCloudBrowserLogin(
	cfg *cloud.CloudConfig,
	tokenManager *cloud.TokenManager,
	noOpen bool,
	timeout time.Duration,
	deps cloudCLIDeps,
) error {
	options := cloud.DefaultLoginOptions()
	options.Timeout = timeout
	options.OpenBrowser = !noOpen
	options.AuthURLCallback = func(authURL string) {
		fmt.Fprintf(deps.stdout, "Open this URL to approve login:\n%s\n\n", authURL)
	}

	if _, err := deps.loginWithHostedUI(context.Background(), cfg, tokenManager, options); err != nil {
		var loginErr *cloud.LoginError
		if errors.As(err, &loginErr) && strings.TrimSpace(loginErr.AuthURL) != "" {
			fmt.Fprintf(deps.stdout, "Open this URL to approve login:\n%s\n\n", loginErr.AuthURL)
		}
		return err
	}

	fmt.Fprintf(deps.stdout, "Login successful. Tokens saved to %s\n", tokenManager.GetTokenPath())
	return nil
}

func deviceLinkUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "device login code not found") ||
		strings.Contains(message, "not_found") ||
		strings.Contains(message, "not found")
}

func runCloudSyncProviderCLI(args []string, deps cloudCLIDeps) error {
	fs := flag.NewFlagSet("cloud sync-provider", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	provider := fs.String("provider", "", "Provider to sync (openai, anthropic, gemini)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	selectedProvider := strings.TrimSpace(*provider)
	if selectedProvider == "" && fs.NArg() > 0 {
		selectedProvider = strings.TrimSpace(fs.Arg(0))
	}
	if selectedProvider == "" {
		selectedProvider = defaultCloudProvider()
	}

	client, err := newCloudCLIClient(deps)
	if err != nil {
		return err
	}
	if err := client.SyncHostedProviderCredential(context.Background(), selectedProvider); err != nil {
		return errors.New(reauthInstruction(selectedProvider, err))
	}
	fmt.Fprintf(deps.stdout, "Synced %s provider credential to Swarm Cloud.\n", selectedProvider)
	return nil
}

func runCloudAskCLI(args []string, deps cloudCLIDeps) error {
	fs := flag.NewFlagSet("cloud ask", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	provider := fs.String("provider", defaultCloudProvider(), "Provider to sync before task creation")
	projectName := fs.String("project-name", "", "Optional durable project name when targeting a repo URL")
	defaultBranch := fs.String("branch", "", "Optional default branch when targeting a repo URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: swarmos cloud ask [--provider name] [--project-name name] [--branch branch] <project-id|repo-url> <prompt>")
	}
	target := strings.TrimSpace(fs.Arg(0))
	prompt := strings.TrimSpace(strings.Join(fs.Args()[1:], " "))
	if target == "" || prompt == "" {
		return fmt.Errorf("usage: swarmos cloud ask [--provider name] [--project-name name] [--branch branch] <project-id|repo-url> <prompt>")
	}

	client, err := newCloudCLIClient(deps)
	if err != nil {
		return err
	}
	selectedProvider := strings.TrimSpace(*provider)
	if err := client.SyncHostedProviderCredential(context.Background(), selectedProvider); err != nil {
		return errors.New(reauthInstruction(selectedProvider, err))
	}

	request := cloud.CreateCloudTaskRequest{
		Prompt:        prompt,
		ProjectName:   strings.TrimSpace(*projectName),
		DefaultBranch: strings.TrimSpace(*defaultBranch),
	}
	if looksLikeRepoURL(target) {
		request.RepoURL = target
		if request.ProjectName == "" {
			request.ProjectName = deriveCloudProjectName(target)
		}
	} else {
		request.ProjectID = target
		request.ProjectName = ""
		request.DefaultBranch = ""
	}

	response, err := client.CreateCloudTask(context.Background(), request)
	if err != nil {
		return err
	}
	output, err := client.CollectCloudSessionTask(context.Background(), &response.Session, response.TaskID)
	if err != nil {
		return err
	}
	result := strings.TrimSpace(output)
	if result == "" {
		result = fmt.Sprintf("Cloud task %s completed for session %s", response.TaskID, response.Session.ID)
	}
	fmt.Fprintf(deps.stdout, "%s\n", result)
	return nil
}

func runCloudSessionCLI(args []string, deps cloudCLIDeps) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos cloud session <start|submit|stop> ...")
	}
	switch args[0] {
	case "start":
		return runCloudSessionStartCLI(args[1:], deps)
	case "submit":
		return runCloudSessionSubmitCLI(args[1:], deps)
	case "stop":
		return runCloudSessionStopCLI(args[1:], deps)
	default:
		return fmt.Errorf("unknown cloud session action: %s", args[0])
	}
}

func runCloudSessionStartCLI(args []string, deps cloudCLIDeps) error {
	fs := flag.NewFlagSet("cloud session start", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	provider := fs.String("provider", defaultCloudProvider(), "Provider to sync before session start")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: swarmos cloud session start [--provider name] <project-id>")
	}
	projectID := strings.TrimSpace(fs.Arg(0))
	client, err := newCloudCLIClient(deps)
	if err != nil {
		return err
	}
	selectedProvider := strings.TrimSpace(*provider)
	if err := client.SyncHostedProviderCredential(context.Background(), selectedProvider); err != nil {
		return errors.New(reauthInstruction(selectedProvider, err))
	}
	session, err := client.CreateHostedSession(context.Background(), cloud.CreateHostedSessionRequest{
		ProjectID: projectID,
		Runtime:   cloud.SessionRuntimeCloud,
	})
	if err != nil {
		return err
	}
	if session.Status != "running" {
		session, err = client.WaitForHostedSessionStatus(context.Background(), session.ID, "running")
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(deps.stdout, "Cloud session %s is running.\n", session.ID)
	return nil
}

func runCloudSessionSubmitCLI(args []string, deps cloudCLIDeps) error {
	fs := flag.NewFlagSet("cloud session submit", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: swarmos cloud session submit <session-id> <prompt>")
	}
	sessionID := strings.TrimSpace(fs.Arg(0))
	prompt := strings.TrimSpace(strings.Join(fs.Args()[1:], " "))
	client, err := newCloudCLIClient(deps)
	if err != nil {
		return err
	}
	session, err := client.GetHostedSession(context.Background(), sessionID)
	if err != nil {
		return err
	}
	if session.Runtime != cloud.SessionRuntimeCloud {
		return fmt.Errorf("session %s is not a cloud session", sessionID)
	}
	taskID := fmt.Sprintf("cloud-%d", time.Now().UTC().UnixNano())
	output, err := client.RunCloudSessionTask(context.Background(), session, taskID, prompt)
	if err != nil {
		return err
	}
	result := strings.TrimSpace(output)
	if result == "" {
		result = fmt.Sprintf("Cloud session task %s completed", taskID)
	}
	fmt.Fprintf(deps.stdout, "%s\n", result)
	return nil
}

func runCloudSessionStopCLI(args []string, deps cloudCLIDeps) error {
	fs := flag.NewFlagSet("cloud session stop", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: swarmos cloud session stop <session-id>")
	}
	client, err := newCloudCLIClient(deps)
	if err != nil {
		return err
	}
	session, err := client.StopHostedSession(context.Background(), strings.TrimSpace(fs.Arg(0)))
	if err != nil {
		return err
	}
	fmt.Fprintf(deps.stdout, "Stopped session %s (%s).\n", session.ID, session.Status)
	return nil
}

func newCloudCLIClient(deps cloudCLIDeps) (cloudCLIClient, error) {
	configManager, err := deps.newConfigManager()
	if err != nil {
		return nil, err
	}
	tokenManager, err := deps.newTokenManager()
	if err != nil {
		return nil, err
	}
	cfg, err := configManager.LoadConfig()
	if err != nil {
		return nil, err
	}
	return deps.newClient(cfg, tokenManager), nil
}

func defaultCloudProvider() string {
	provider := strings.TrimSpace(os.Getenv("SWARM_CLOUD_PROVIDER"))
	if provider == "" {
		return "openai"
	}
	return provider
}

func reauthInstruction(provider string, err error) string {
	return fmt.Sprintf("%v; re-auth locally, then run swarmos cloud sync-provider %s", err, provider)
}

func looksLikeRepoURL(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.Contains(trimmed, "://") || strings.Contains(trimmed, "github.com:") || strings.HasSuffix(trimmed, ".git")
}

func deriveCloudProjectName(repoURL string) string {
	trimmed := strings.TrimSpace(repoURL)
	trimmed = strings.TrimSuffix(trimmed, ".git")
	segments := strings.Split(trimmed, "/")
	if len(segments) == 0 {
		return "Cloud Repo"
	}
	name := segments[len(segments)-1]
	if name == "" {
		return "Cloud Repo"
	}
	return name
}
