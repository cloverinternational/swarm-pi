//go:build cloud_integration

package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

func TestCloudIntegration_HostedSession_Smoke(t *testing.T) {
	realHome := os.Getenv("HOME")
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	if err := stageHostedProviderOAuthFiles(tempHome, realHome); err != nil {
		t.Fatalf("stageHostedProviderOAuthFiles: %v", err)
	}
	if err := stageCloudTokensFile(tempHome, realHome); err != nil {
		t.Fatalf("stageCloudTokensFile: %v", err)
	}

	repoURL := strings.TrimSpace(os.Getenv("SWARM_CLOUD_SMOKE_PROJECT_REPO_URL"))
	if repoURL == "" {
		t.Skip("SWARM_CLOUD_SMOKE_PROJECT_REPO_URL not set; skipping hosted session smoke test")
	}

	defaultBranch := firstNonEmptyString(
		strings.TrimSpace(os.Getenv("SWARM_CLOUD_SMOKE_PROJECT_BRANCH")),
		"main",
	)
	readPrompt := firstNonEmptyString(
		strings.TrimSpace(os.Getenv("SWARM_CLOUD_SMOKE_READ_PROMPT")),
		"Inspect the repository, list the top-level files, and summarize what this project is without making any file changes.",
	)
	approvalPrompt := firstNonEmptyString(
		strings.TrimSpace(os.Getenv("SWARM_CLOUD_SMOKE_APPROVAL_PROMPT")),
		"Use the shell tool to write the text smoke-approved to /workspace/.swarm/smoke-approval.txt, then explain what changed.",
	)

	cfg := integrationConfig()
	tokenManager, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	seedIntegrationTokens(t, cfg, tokenManager)

	client := NewClient(cfg, tokenManager, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	accessToken, err := client.GetValidAccessToken(ctx)
	if err != nil {
		t.Fatalf("GetValidAccessToken: %v", err)
	}
	provider := firstNonEmptyString(
		strings.TrimSpace(os.Getenv("SWARM_CLOUD_SMOKE_PROVIDER")),
		"openai",
	)
	if err := client.SyncHostedProviderCredential(ctx, provider); err != nil {
		t.Fatalf("SyncHostedProviderCredential(%s): %v", provider, err)
	}

	projectName := fmt.Sprintf("Hosted Smoke %d", time.Now().UTC().Unix())
	project, err := createHostedProject(ctx, client.httpClient(), cfg.APIBaseURL, accessToken, projectName, repoURL, defaultBranch)
	if err != nil {
		t.Fatalf("createHostedProject: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_ = deleteHostedProject(cleanupCtx, client.httpClient(), cfg.APIBaseURL, accessToken, project.ID)
	})

	session, err := createHostedSession(ctx, client.httpClient(), cfg.APIBaseURL, accessToken, project.ID)
	if err != nil {
		t.Fatalf("createHostedSession: %v", err)
	}

	t.Cleanup(func() {
		if session == nil || strings.TrimSpace(session.ID) == "" {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_, _ = stopHostedSession(cleanupCtx, client.httpClient(), cfg.APIBaseURL, accessToken, session.ID)
	})

	session, err = waitForHostedSessionStatus(ctx, client.httpClient(), cfg.APIBaseURL, accessToken, session.ID, "running")
	if err != nil {
		t.Fatalf("waitForHostedSessionStatus(running): %v", err)
	}

	conn, err := dialHostedSession(ctx, session.WebsocketURL, accessToken, session.ProtocolVersion, session.ResumeToken.Value, 0)
	if err != nil {
		t.Fatalf("dialHostedSession: %v", err)
	}

	var lastSequence int64
	attachEvent, err := waitForHostedEvent(conn, 30*time.Second, func(event *hosted.EventEnvelope) bool {
		return event.Event.Type == hosted.EventTypeSessionAttach
	})
	if err != nil {
		_ = conn.Close()
		t.Fatalf("waitForHostedEvent(session_attach): %v", err)
	}
	lastSequence = attachEvent.Sequence

	if err := submitHostedTask(conn, "smoke-read", readPrompt); err != nil {
		_ = conn.Close()
		t.Fatalf("submitHostedTask(smoke-read): %v", err)
	}
	readComplete, err := waitForHostedTaskCompletion(conn, "smoke-read", true)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("waitForHostedEvent(read complete): %v", err)
	}
	lastSequence = maxSequence(lastSequence, readComplete.Sequence)
	_ = conn.Close()

	replayAfter := lastSequence - 1
	if replayAfter < 0 {
		replayAfter = 0
	}
	conn, err = dialHostedSession(ctx, session.WebsocketURL, accessToken, session.ProtocolVersion, session.ResumeToken.Value, replayAfter)
	if err != nil {
		t.Fatalf("dialHostedSession(reconnect): %v", err)
	}
	defer conn.Close()

	replayed, err := waitForHostedEvent(conn, 30*time.Second, func(event *hosted.EventEnvelope) bool {
		return event.Sequence == lastSequence
	})
	if err != nil {
		t.Fatalf("waitForHostedEvent(replay): %v", err)
	}
	lastSequence = maxSequence(lastSequence, replayed.Sequence)

	if err := submitHostedTask(conn, "smoke-approval", approvalPrompt); err != nil {
		t.Fatalf("submitHostedTask(smoke-approval): %v", err)
	}
	approvalRequest, err := waitForHostedEvent(conn, 2*time.Minute, func(event *hosted.EventEnvelope) bool {
		return event.Event.Type == hosted.EventTypeApprovalRequest &&
			event.Interaction != nil &&
			strings.TrimSpace(event.Interaction.ID) != ""
	})
	if err != nil {
		t.Fatalf("waitForHostedEvent(approval request): %v", err)
	}
	lastSequence = maxSequence(lastSequence, approvalRequest.Sequence)

	if err := respondHostedApproval(conn, approvalRequest.Interaction.ID, "approve", "once"); err != nil {
		t.Fatalf("respondHostedApproval: %v", err)
	}
	approvalComplete, err := waitForHostedTaskCompletion(conn, "smoke-approval", true)
	if err != nil {
		t.Fatalf("waitForHostedEvent(approval complete): %v", err)
	}
	lastSequence = maxSequence(lastSequence, approvalComplete.Sequence)

	if _, err := stopHostedSession(ctx, client.httpClient(), cfg.APIBaseURL, accessToken, session.ID); err != nil {
		t.Fatalf("stopHostedSession: %v", err)
	}
	finalSession, err := waitForHostedSessionStatus(ctx, client.httpClient(), cfg.APIBaseURL, accessToken, session.ID, "stopped")
	if err != nil {
		t.Fatalf("waitForHostedSessionStatus(stopped): %v", err)
	}
	if finalSession.Metrics["snapshot_restore_ms"] <= 0 {
		t.Fatalf("expected snapshot_restore_ms metric, got %+v", finalSession.Metrics)
	}
	if finalSession.Metrics["session_tti_ms"] <= 0 {
		t.Fatalf("expected session_tti_ms metric, got %+v", finalSession.Metrics)
	}

	events, err := listHostedSessionEvents(ctx, client.httpClient(), cfg.APIBaseURL, accessToken, session.ID)
	if err != nil {
		t.Fatalf("listHostedSessionEvents: %v", err)
	}
	if !containsHostedEventType(events, hosted.EventTypeSessionAttach) {
		t.Fatalf("expected session_attach event in audit trail, got %d events", len(events))
	}
	if !containsHostedEventType(events, hosted.EventTypeSessionResume) {
		t.Fatalf("expected session_resume event in audit trail, got %d events", len(events))
	}
	if !containsHostedEventType(events, hosted.EventTypeApprovalRequest) {
		t.Fatalf("expected approval_request event in audit trail, got %d events", len(events))
	}
	if !containsHostedEventType(events, hosted.EventTypeApprovalResponse) {
		t.Fatalf("expected approval_response event in audit trail, got %d events", len(events))
	}
	if !containsHostedLifecycleComplete(events, "smoke-read") || !containsHostedLifecycleComplete(events, "smoke-approval") {
		t.Fatalf("expected lifecycle_complete events for smoke tasks, got %d events", len(events))
	}
}

func createHostedProject(
	ctx context.Context,
	httpClient *http.Client,
	apiBaseURL string,
	accessToken string,
	name string,
	repoURL string,
	defaultBranch string,
) (*HostedProjectRecord, error) {
	response, err := authorizedJSONRequest(ctx, httpClient, http.MethodPost, strings.TrimRight(apiBaseURL, "/")+"/v1/projects", accessToken, map[string]string{
		"name":           name,
		"repo_url":       repoURL,
		"default_branch": defaultBranch,
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var payload hostedProjectCreateResponse
	if err := decodeJSONResponse(response, &payload); err != nil {
		return nil, err
	}
	return &payload.Project, nil
}

func deleteHostedProject(
	ctx context.Context,
	httpClient *http.Client,
	apiBaseURL string,
	accessToken string,
	projectID string,
) error {
	response, err := authorizedJSONRequest(ctx, httpClient, http.MethodDelete, strings.TrimRight(apiBaseURL, "/")+"/v1/projects/"+projectID, accessToken, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("delete project returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func createHostedSession(
	ctx context.Context,
	httpClient *http.Client,
	apiBaseURL string,
	accessToken string,
	projectID string,
) (*HostedSessionSummary, error) {
	response, err := authorizedJSONRequest(ctx, httpClient, http.MethodPost, strings.TrimRight(apiBaseURL, "/")+"/v1/sessions", accessToken, map[string]string{
		"project_id": projectID,
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("create session returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload hostedSessionResponse
	if err := decodeJSONResponse(response, &payload); err != nil {
		return nil, err
	}
	return &payload.Session, nil
}

func getHostedSession(
	ctx context.Context,
	httpClient *http.Client,
	apiBaseURL string,
	accessToken string,
	sessionID string,
) (*HostedSessionSummary, error) {
	response, err := authorizedJSONRequest(ctx, httpClient, http.MethodGet, strings.TrimRight(apiBaseURL, "/")+"/v1/sessions/"+sessionID, accessToken, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var payload hostedSessionResponse
	if err := decodeJSONResponse(response, &payload); err != nil {
		return nil, err
	}
	return &payload.Session, nil
}

func stopHostedSession(
	ctx context.Context,
	httpClient *http.Client,
	apiBaseURL string,
	accessToken string,
	sessionID string,
) (*HostedSessionSummary, error) {
	response, err := authorizedJSONRequest(ctx, httpClient, http.MethodPost, strings.TrimRight(apiBaseURL, "/")+"/v1/sessions/"+sessionID+"/stop", accessToken, map[string]string{})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("stop session returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload hostedSessionResponse
	if err := decodeJSONResponse(response, &payload); err != nil {
		return nil, err
	}
	return &payload.Session, nil
}

func waitForHostedSessionStatus(
	ctx context.Context,
	httpClient *http.Client,
	apiBaseURL string,
	accessToken string,
	sessionID string,
	expected string,
) (*HostedSessionSummary, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		session, err := getHostedSession(ctx, httpClient, apiBaseURL, accessToken, sessionID)
		if err != nil {
			return nil, err
		}
		if session.Status == expected {
			return session, nil
		}
		if session.Status == "failed" || session.Status == "stopped" {
			return nil, fmt.Errorf("session reached %q before %q (error=%q)", session.Status, expected, session.Error)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func listHostedSessionEvents(
	ctx context.Context,
	httpClient *http.Client,
	apiBaseURL string,
	accessToken string,
	sessionID string,
) ([]hosted.EventEnvelope, error) {
	response, err := authorizedJSONRequest(ctx, httpClient, http.MethodGet, strings.TrimRight(apiBaseURL, "/")+"/v1/sessions/"+sessionID+"/events", accessToken, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var payload HostedSessionEventsResponse
	if err := decodeJSONResponse(response, &payload); err != nil {
		return nil, err
	}
	return payload.Events, nil
}

func dialHostedSession(
	ctx context.Context,
	websocketURL string,
	accessToken string,
	protocolVersion string,
	resumeToken string,
	replayAfterSequence int64,
) (*websocket.Conn, error) {
	parsedURL, err := url.Parse(websocketURL)
	if err != nil {
		return nil, err
	}
	switch parsedURL.Scheme {
	case "https":
		parsedURL.Scheme = "wss"
	case "http":
		parsedURL.Scheme = "ws"
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+accessToken)
	headers.Set("X-Swarm-Protocol-Version", protocolVersion)
	headers.Set("X-Swarm-Resume-Token", resumeToken)
	if replayAfterSequence > 0 {
		headers.Set("X-Swarm-Replay-Sequence", fmt.Sprintf("%d", replayAfterSequence))
	}

	conn, response, err := websocket.DefaultDialer.DialContext(ctx, parsedURL.String(), headers)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			return nil, fmt.Errorf("websocket dial returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, err
	}
	return conn, nil
}

func waitForHostedEvent(
	conn *websocket.Conn,
	timeout time.Duration,
	predicate func(*hosted.EventEnvelope) bool,
) (*hosted.EventEnvelope, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var envelope hosted.EventEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("decode hosted event: %w", err)
		}
		if predicate(&envelope) {
			return &envelope, nil
		}
	}
	return nil, fmt.Errorf("timed out waiting for hosted event")
}

func waitForHostedTaskCompletion(
	conn *websocket.Conn,
	taskID string,
	allowApproval bool,
) (*hosted.EventEnvelope, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var envelope hosted.EventEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("decode hosted event: %w", err)
		}
		if allowApproval &&
			envelope.Event.Type == hosted.EventTypeApprovalRequest &&
			envelope.Interaction != nil &&
			strings.TrimSpace(envelope.Interaction.ID) != "" {
			if err := respondHostedApproval(conn, envelope.Interaction.ID, "approve", "once"); err != nil {
				return nil, err
			}
			continue
		}
		if envelope.Event.Type == hosted.EventTypeLifecycleError &&
			hostedExecutionTaskLabel(&envelope, "task_id") == taskID {
			return nil, fmt.Errorf("task failed: %s", strings.TrimSpace(hostedEventDetail(&envelope)))
		}
		if envelope.Event.Type == hosted.EventTypeLifecycleComplete &&
			hostedExecutionTaskLabel(&envelope, "task_id") == taskID {
			return &envelope, nil
		}
	}
	return nil, fmt.Errorf("timed out waiting for task completion")
}

func submitHostedTask(conn *websocket.Conn, taskID string, prompt string) error {
	return conn.WriteJSON(map[string]any{
		"type":    "task.submit",
		"task_id": taskID,
		"prompt":  prompt,
	})
}

func respondHostedApproval(conn *websocket.Conn, interactionID string, decision string, scope string) error {
	return conn.WriteJSON(map[string]any{
		"type":           "approval.response",
		"interaction_id": interactionID,
		"decision":       decision,
		"scope":          scope,
	})
}

func authorizedJSONRequest(
	ctx context.Context,
	httpClient *http.Client,
	method string,
	targetURL string,
	accessToken string,
	body any,
) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, targetURL, payload)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	return httpClient.Do(request)
}

func hostedExecutionTaskLabel(event *hosted.EventEnvelope, key string) string {
	if event == nil || event.Execution == nil || event.Execution.Labels == nil {
		return ""
	}
	return strings.TrimSpace(event.Execution.Labels[key])
}

func containsHostedEventType(events []hosted.EventEnvelope, eventType hosted.EventType) bool {
	for _, event := range events {
		if event.Event.Type == eventType {
			return true
		}
	}
	return false
}

func containsHostedLifecycleComplete(events []hosted.EventEnvelope, taskID string) bool {
	for _, event := range events {
		if event.Event.Type != hosted.EventTypeLifecycleComplete {
			continue
		}
		if hostedExecutionTaskLabel(&event, "task_id") == taskID {
			return true
		}
	}
	return false
}

func maxSequence(values ...int64) int64 {
	var current int64
	for _, value := range values {
		if value > current {
			current = value
		}
	}
	return current
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
