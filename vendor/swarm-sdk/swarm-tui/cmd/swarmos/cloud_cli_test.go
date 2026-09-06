package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
)

type stubCloudCLIClient struct {
	syncProvider  func(context.Context, string) error
	createTask    func(context.Context, cloud.CreateCloudTaskRequest) (*cloud.CreateCloudTaskResponse, error)
	collectTask   func(context.Context, *cloud.HostedSessionSummary, string) (string, error)
	createSession func(context.Context, cloud.CreateHostedSessionRequest) (*cloud.HostedSessionSummary, error)
	waitSession   func(context.Context, string, string) (*cloud.HostedSessionSummary, error)
	getSession    func(context.Context, string) (*cloud.HostedSessionSummary, error)
	runTask       func(context.Context, *cloud.HostedSessionSummary, string, string) (string, error)
	stopSession   func(context.Context, string) (*cloud.HostedSessionSummary, error)
}

func (s stubCloudCLIClient) SyncHostedProviderCredential(ctx context.Context, provider string) error {
	if s.syncProvider != nil {
		return s.syncProvider(ctx, provider)
	}
	return nil
}

func (s stubCloudCLIClient) CreateCloudTask(ctx context.Context, req cloud.CreateCloudTaskRequest) (*cloud.CreateCloudTaskResponse, error) {
	if s.createTask == nil {
		return nil, errors.New("unexpected CreateCloudTask")
	}
	return s.createTask(ctx, req)
}

func (s stubCloudCLIClient) CollectCloudSessionTask(ctx context.Context, session *cloud.HostedSessionSummary, taskID string) (string, error) {
	if s.collectTask == nil {
		return "", errors.New("unexpected CollectCloudSessionTask")
	}
	return s.collectTask(ctx, session, taskID)
}

func (s stubCloudCLIClient) CreateHostedSession(ctx context.Context, req cloud.CreateHostedSessionRequest) (*cloud.HostedSessionSummary, error) {
	if s.createSession == nil {
		return nil, errors.New("unexpected CreateHostedSession")
	}
	return s.createSession(ctx, req)
}

func (s stubCloudCLIClient) WaitForHostedSessionStatus(ctx context.Context, sessionID string, expected string) (*cloud.HostedSessionSummary, error) {
	if s.waitSession == nil {
		return nil, errors.New("unexpected WaitForHostedSessionStatus")
	}
	return s.waitSession(ctx, sessionID, expected)
}

func (s stubCloudCLIClient) GetHostedSession(ctx context.Context, sessionID string) (*cloud.HostedSessionSummary, error) {
	if s.getSession == nil {
		return nil, errors.New("unexpected GetHostedSession")
	}
	return s.getSession(ctx, sessionID)
}

func (s stubCloudCLIClient) RunCloudSessionTask(ctx context.Context, session *cloud.HostedSessionSummary, taskID string, prompt string) (string, error) {
	if s.runTask == nil {
		return "", errors.New("unexpected RunCloudSessionTask")
	}
	return s.runTask(ctx, session, taskID, prompt)
}

func (s stubCloudCLIClient) StopHostedSession(ctx context.Context, sessionID string) (*cloud.HostedSessionSummary, error) {
	if s.stopSession == nil {
		return nil, errors.New("unexpected StopHostedSession")
	}
	return s.stopSession(ctx, sessionID)
}

func TestRunCloudCLIWithDepsLoginSavesTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", filepath.Join(home, ".swarm"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var opened string
	var polled int

	err := runCloudCLIWithDeps([]string{"login", "--no-open"}, cloudCLIDeps{
		stdout:           &stdout,
		stderr:           &stderr,
		newConfigManager: cloud.NewConfigManager,
		newTokenManager:  cloud.NewTokenManager,
		startDeviceLink: func(_ context.Context, _ *cloud.CloudConfig, info cloud.DeviceLinkInfo) (*cloud.DeviceLinkStartResult, error) {
			if info.App != "swarmos" {
				t.Fatalf("expected swarmos app, got %q", info.App)
			}
			return &cloud.DeviceLinkStartResult{
				DeviceCode:              "device-code",
				UserCode:                "USER-CODE",
				VerificationURI:         "https://auth.example/verify",
				VerificationURIComplete: "https://auth.example/verify?code=USER-CODE",
				ExpiresAt:               time.Now().Add(5 * time.Minute).Unix(),
				IntervalSec:             1,
			}, nil
		},
		pollDeviceLink: func(_ context.Context, _ *cloud.CloudConfig, deviceCode string) (*cloud.DeviceLinkPollResult, error) {
			polled++
			if deviceCode != "device-code" {
				t.Fatalf("unexpected device code %q", deviceCode)
			}
			return &cloud.DeviceLinkPollResult{
				Status: "approved",
				Tokens: &cloud.TokenSet{
					AccessToken:  "access-token",
					IDToken:      "id-token",
					RefreshToken: "refresh-token",
					TokenType:    "Bearer",
					ExpiresAt:    time.Now().Add(time.Hour).Unix(),
				},
			}, nil
		},
		openTarget: func(target string) error {
			opened = target
			return nil
		},
		hostname: func() (string, error) {
			return "test-host", nil
		},
		loginWithHostedUI: func(context.Context, *cloud.CloudConfig, *cloud.TokenManager, cloud.LoginOptions) (*cloud.TokenSet, error) {
			t.Fatal("hosted ui fallback should not be used")
			return nil, nil
		},
		now:   time.Now,
		sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("runCloudCLIWithDeps: %v", err)
	}
	if polled != 1 {
		t.Fatalf("expected 1 poll, got %d", polled)
	}
	if opened != "" {
		t.Fatalf("expected browser open to be skipped, got %q", opened)
	}

	tokenPath := filepath.Join(home, ".swarm", "cloud_tokens.json")
	data, readErr := os.ReadFile(tokenPath)
	if readErr != nil {
		t.Fatalf("read tokens: %v", readErr)
	}
	if !strings.Contains(string(data), "\"refresh_token\": \"refresh-token\"") {
		t.Fatalf("expected refresh token in saved file, got %s", string(data))
	}
	if !strings.Contains(stdout.String(), "Login successful.") {
		t.Fatalf("expected success output, got %q", stdout.String())
	}
}

func TestRunCloudCLIWithDepsUnknownSubcommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runCloudCLIWithDeps([]string{"bogus"}, cloudCLIDeps{
		stdout: &stdout,
		stderr: &stderr,
	})
	if err == nil || err.Error() != "unknown cloud subcommand: bogus" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCloudCLIWithDepsLoginFallsBackToBrowser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var browserMode bool

	err := runCloudCLIWithDeps([]string{"login", "--mode", "auto", "--no-open"}, cloudCLIDeps{
		stdout:           &stdout,
		stderr:           &stderr,
		newConfigManager: cloud.NewConfigManager,
		newTokenManager:  cloud.NewTokenManager,
		startDeviceLink: func(context.Context, *cloud.CloudConfig, cloud.DeviceLinkInfo) (*cloud.DeviceLinkStartResult, error) {
			return nil, errors.New("Device login code not found.")
		},
		pollDeviceLink: func(context.Context, *cloud.CloudConfig, string) (*cloud.DeviceLinkPollResult, error) {
			t.Fatal("device poll should not be used after fallback")
			return nil, nil
		},
		loginWithHostedUI: func(_ context.Context, _ *cloud.CloudConfig, tokenManager *cloud.TokenManager, options cloud.LoginOptions) (*cloud.TokenSet, error) {
			browserMode = true
			if options.OpenBrowser {
				t.Fatalf("expected browser launch disabled")
			}
			if options.AuthURLCallback == nil {
				t.Fatalf("expected auth url callback")
			}
			options.AuthURLCallback("https://auth.example/browser")
			tokens := &cloud.TokenSet{
				AccessToken:  "browser-access",
				IDToken:      "browser-id",
				RefreshToken: "browser-refresh",
				TokenType:    "Bearer",
				ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			}
			if err := tokenManager.SaveTokens(tokens); err != nil {
				t.Fatalf("SaveTokens: %v", err)
			}
			return tokens, nil
		},
		openTarget: func(string) error {
			t.Fatal("openTarget should not be used in --no-open mode")
			return nil
		},
		hostname: func() (string, error) {
			return "test-host", nil
		},
		now:   time.Now,
		sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("runCloudCLIWithDeps: %v", err)
	}
	if !browserMode {
		t.Fatalf("expected browser fallback to run")
	}
	if !strings.Contains(stdout.String(), "Device-link login unavailable; falling back to browser login.") {
		t.Fatalf("expected fallback notice, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "https://auth.example/browser") {
		t.Fatalf("expected browser auth url in output, got %q", stdout.String())
	}
}

func TestRunCloudCLIWithDepsSyncProvider(t *testing.T) {
	var stdout bytes.Buffer
	var synced string

	err := runCloudCLIWithDeps([]string{"sync-provider", "gemini"}, cloudCLIDeps{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		newClient: func(*cloud.CloudConfig, *cloud.TokenManager) cloudCLIClient {
			return stubCloudCLIClient{
				syncProvider: func(_ context.Context, provider string) error {
					synced = provider
					return nil
				},
			}
		},
		newConfigManager: cloud.NewConfigManager,
		newTokenManager:  cloud.NewTokenManager,
	})
	if err != nil {
		t.Fatalf("runCloudCLIWithDeps(sync-provider): %v", err)
	}
	if synced != "gemini" {
		t.Fatalf("expected gemini provider, got %q", synced)
	}
	if !strings.Contains(stdout.String(), "Synced gemini provider credential") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunCloudCLIWithDepsAskRepoURL(t *testing.T) {
	var stdout bytes.Buffer
	var gotRequest cloud.CreateCloudTaskRequest

	err := runCloudCLIWithDeps([]string{"ask", "https://github.com/octocat/Spoon-Knife.git", "Summarize", "this", "repo"}, cloudCLIDeps{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		newClient: func(*cloud.CloudConfig, *cloud.TokenManager) cloudCLIClient {
			return stubCloudCLIClient{
				syncProvider: func(_ context.Context, provider string) error {
					if provider != "openai" {
						t.Fatalf("expected default provider openai, got %q", provider)
					}
					return nil
				},
				createTask: func(_ context.Context, req cloud.CreateCloudTaskRequest) (*cloud.CreateCloudTaskResponse, error) {
					gotRequest = req
					return &cloud.CreateCloudTaskResponse{
						TaskID: "task-123",
						Session: cloud.HostedSessionSummary{
							ID: "session-123",
						},
					}, nil
				},
				collectTask: func(_ context.Context, session *cloud.HostedSessionSummary, taskID string) (string, error) {
					if session.ID != "session-123" || taskID != "task-123" {
						t.Fatalf("unexpected session/task: %+v %s", session, taskID)
					}
					return "Repo summary", nil
				},
			}
		},
		newConfigManager: cloud.NewConfigManager,
		newTokenManager:  cloud.NewTokenManager,
	})
	if err != nil {
		t.Fatalf("runCloudCLIWithDeps(ask): %v", err)
	}
	if gotRequest.RepoURL != "https://github.com/octocat/Spoon-Knife.git" {
		t.Fatalf("unexpected repo url: %+v", gotRequest)
	}
	if gotRequest.ProjectName != "Spoon-Knife" {
		t.Fatalf("expected derived project name, got %+v", gotRequest)
	}
	if gotRequest.Prompt != "Summarize this repo" {
		t.Fatalf("unexpected prompt: %+v", gotRequest)
	}
	if strings.TrimSpace(stdout.String()) != "Repo summary" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunCloudCLIWithDepsCloudSessionLifecycle(t *testing.T) {
	var stdout bytes.Buffer
	var created cloud.CreateHostedSessionRequest

	client := stubCloudCLIClient{
		syncProvider: func(_ context.Context, provider string) error {
			if provider != "openai" {
				t.Fatalf("expected openai provider, got %q", provider)
			}
			return nil
		},
		createSession: func(_ context.Context, req cloud.CreateHostedSessionRequest) (*cloud.HostedSessionSummary, error) {
			created = req
			return &cloud.HostedSessionSummary{ID: "session-1", Status: "starting"}, nil
		},
		waitSession: func(_ context.Context, sessionID string, expected string) (*cloud.HostedSessionSummary, error) {
			if sessionID != "session-1" || expected != "running" {
				t.Fatalf("unexpected wait args: %s %s", sessionID, expected)
			}
			return &cloud.HostedSessionSummary{ID: "session-1", Status: "running", Runtime: cloud.SessionRuntimeCloud}, nil
		},
		getSession: func(_ context.Context, sessionID string) (*cloud.HostedSessionSummary, error) {
			if sessionID != "session-1" {
				t.Fatalf("unexpected session id: %s", sessionID)
			}
			return &cloud.HostedSessionSummary{ID: "session-1", Runtime: cloud.SessionRuntimeCloud}, nil
		},
		runTask: func(_ context.Context, session *cloud.HostedSessionSummary, taskID string, prompt string) (string, error) {
			if session.ID != "session-1" || taskID == "" || prompt != "List files" {
				t.Fatalf("unexpected run args: %+v %q %q", session, taskID, prompt)
			}
			return "Task output", nil
		},
		stopSession: func(_ context.Context, sessionID string) (*cloud.HostedSessionSummary, error) {
			if sessionID != "session-1" {
				t.Fatalf("unexpected stop id: %s", sessionID)
			}
			return &cloud.HostedSessionSummary{ID: "session-1", Status: "stopped"}, nil
		},
	}

	deps := cloudCLIDeps{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		newClient: func(*cloud.CloudConfig, *cloud.TokenManager) cloudCLIClient {
			return client
		},
		newConfigManager: cloud.NewConfigManager,
		newTokenManager:  cloud.NewTokenManager,
	}

	if err := runCloudCLIWithDeps([]string{"session", "start", "project-1"}, deps); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if created.ProjectID != "project-1" || created.Runtime != cloud.SessionRuntimeCloud {
		t.Fatalf("unexpected session create request: %+v", created)
	}
	if !strings.Contains(stdout.String(), "Cloud session session-1 is running.") {
		t.Fatalf("unexpected start stdout: %q", stdout.String())
	}

	stdout.Reset()
	if err := runCloudCLIWithDeps([]string{"session", "submit", "session-1", "List", "files"}, deps); err != nil {
		t.Fatalf("session submit: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "Task output" {
		t.Fatalf("unexpected submit stdout: %q", stdout.String())
	}

	stdout.Reset()
	if err := runCloudCLIWithDeps([]string{"session", "stop", "session-1"}, deps); err != nil {
		t.Fatalf("session stop: %v", err)
	}
	if !strings.Contains(stdout.String(), "Stopped session session-1 (stopped).") {
		t.Fatalf("unexpected stop stdout: %q", stdout.String())
	}
}
