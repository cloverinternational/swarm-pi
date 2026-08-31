package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

const (
	defaultHostedStreamTimeout time.Duration = 2 * time.Minute
)

const hostedEventTypeContent hosted.EventType = "content"

type SessionRuntime string

const (
	SessionRuntimeVM    SessionRuntime = "vm"
	SessionRuntimeCloud SessionRuntime = "cloud"
)

type HostedProjectRecord struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RepoURL       string `json:"repo_url"`
	DefaultBranch string `json:"default_branch"`
}

type HostedSessionSummary struct {
	ID                string              `json:"id"`
	Status            string              `json:"status"`
	Runtime           SessionRuntime      `json:"runtime,omitempty"`
	AutoStopAfterTask bool                `json:"auto_stop_after_task,omitempty"`
	WebsocketURL      string              `json:"websocket_url"`
	ProtocolVersion   string              `json:"protocol_version"`
	ResumeToken       hosted.ResumeToken  `json:"resume_token"`
	Cursor            hosted.ReplayCursor `json:"cursor"`
	Metrics           map[string]float64  `json:"metrics"`
	Error             string              `json:"error,omitempty"`
}

type HostedSessionEventsResponse struct {
	Status string                 `json:"status"`
	Events []hosted.EventEnvelope `json:"events"`
}

type CreateHostedSessionRequest struct {
	ProjectID         string            `json:"project_id"`
	Runtime           SessionRuntime    `json:"runtime,omitempty"`
	AutoStopAfterTask bool              `json:"auto_stop_after_task,omitempty"`
	InitialTask       *HostedTaskSubmit `json:"initial_task,omitempty"`
}

type HostedTaskSubmit struct {
	Prompt    string `json:"prompt"`
	TaskClass string `json:"task_class,omitempty"`
}

type CreateCloudTaskRequest struct {
	ProjectID     string `json:"project_id,omitempty"`
	RepoURL       string `json:"repo_url,omitempty"`
	Prompt        string `json:"prompt"`
	ProjectName   string `json:"project_name,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

type CreateCloudTaskResponse struct {
	Status  string               `json:"status"`
	TaskID  string               `json:"task_id"`
	Project HostedProjectRecord  `json:"project"`
	Session HostedSessionSummary `json:"session"`
}

type hostedProjectCreateResponse struct {
	Status  string              `json:"status"`
	Project HostedProjectRecord `json:"project"`
}

type hostedSessionResponse struct {
	Status  string               `json:"status"`
	Session HostedSessionSummary `json:"session"`
}

func (c *Client) CreateHostedProject(
	ctx context.Context,
	name string,
	repoURL string,
	defaultBranch string,
) (*HostedProjectRecord, error) {
	response, err := c.authorizedJSONRequest(ctx, http.MethodPost, "/v1/projects", map[string]string{
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

func (c *Client) DeleteHostedProject(
	ctx context.Context,
	projectID string,
) error {
	response, err := c.authorizedJSONRequest(ctx, http.MethodDelete, "/v1/projects/"+projectID, nil)
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

func (c *Client) CreateHostedSession(
	ctx context.Context,
	request CreateHostedSessionRequest,
) (*HostedSessionSummary, error) {
	if strings.TrimSpace(request.ProjectID) == "" {
		return nil, fmt.Errorf("project id is required")
	}
	if request.Runtime == "" {
		request.Runtime = SessionRuntimeVM
	}
	response, err := c.authorizedJSONRequest(ctx, http.MethodPost, "/v1/sessions", request)
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

func (c *Client) CreateCloudTask(
	ctx context.Context,
	request CreateCloudTaskRequest,
) (*CreateCloudTaskResponse, error) {
	if strings.TrimSpace(request.ProjectID) == "" && strings.TrimSpace(request.RepoURL) == "" {
		return nil, fmt.Errorf("project_id or repo_url is required")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	response, err := c.authorizedJSONRequest(ctx, http.MethodPost, "/v1/cloud-tasks", request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("create cloud task returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload CreateCloudTaskResponse
	if err := decodeJSONResponse(response, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *Client) GetHostedSession(
	ctx context.Context,
	sessionID string,
) (*HostedSessionSummary, error) {
	response, err := c.authorizedJSONRequest(ctx, http.MethodGet, "/v1/sessions/"+sessionID, nil)
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

func (c *Client) StopHostedSession(
	ctx context.Context,
	sessionID string,
) (*HostedSessionSummary, error) {
	response, err := c.authorizedJSONRequest(ctx, http.MethodPost, "/v1/sessions/"+sessionID+"/stop", map[string]string{})
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

func (c *Client) WaitForHostedSessionStatus(
	ctx context.Context,
	sessionID string,
	expected string,
) (*HostedSessionSummary, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		session, err := c.GetHostedSession(ctx, sessionID)
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

func (c *Client) ListHostedSessionEvents(
	ctx context.Context,
	sessionID string,
) ([]hosted.EventEnvelope, error) {
	response, err := c.authorizedJSONRequest(ctx, http.MethodGet, "/v1/sessions/"+sessionID+"/events", nil)
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

func (c *Client) DialHostedSession(
	ctx context.Context,
	session *HostedSessionSummary,
	replayAfterSequence int64,
) (*websocket.Conn, error) {
	if c == nil {
		return nil, fmt.Errorf("cloud client is required")
	}
	if session == nil {
		return nil, fmt.Errorf("session is required")
	}
	accessToken, err := c.GetValidAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	parsedURL, err := url.Parse(session.WebsocketURL)
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
	headers.Set("X-Swarm-Protocol-Version", session.ProtocolVersion)
	headers.Set("X-Swarm-Resume-Token", session.ResumeToken.Value)
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

func SubmitHostedTask(conn *websocket.Conn, taskID string, prompt string) error {
	return conn.WriteJSON(map[string]any{
		"type":    "task.submit",
		"task_id": taskID,
		"prompt":  prompt,
	})
}

func RespondHostedApproval(conn *websocket.Conn, interactionID string, decision string, scope string) error {
	return conn.WriteJSON(map[string]any{
		"type":           "approval.response",
		"interaction_id": interactionID,
		"decision":       decision,
		"scope":          scope,
	})
}

func RespondHostedQuestion(conn *websocket.Conn, interactionID string, value string) error {
	return conn.WriteJSON(map[string]any{
		"type":           "question.response",
		"interaction_id": interactionID,
		"value":          value,
	})
}

func WaitForHostedEvent(
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
			continue
		}
		if predicate(&envelope) {
			return &envelope, nil
		}
	}
	return nil, fmt.Errorf("timed out waiting for hosted event")
}

func (c *Client) RunCloudSessionTask(
	ctx context.Context,
	session *HostedSessionSummary,
	taskID string,
	prompt string,
) (string, error) {
	if session == nil {
		return "", fmt.Errorf("session is required")
	}
	conn, err := c.DialHostedSession(ctx, session, 0)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	_, _ = WaitForHostedEvent(conn, 15*time.Second, func(event *hosted.EventEnvelope) bool {
		return event.Event.Type == hosted.EventTypeSessionAttach
	})

	if err := SubmitHostedTask(conn, taskID, prompt); err != nil {
		return "", err
	}

	return collectHostedTaskOutput(conn, taskID)
}

func (c *Client) CollectCloudSessionTask(
	ctx context.Context,
	session *HostedSessionSummary,
	taskID string,
) (string, error) {
	if session == nil {
		return "", fmt.Errorf("session is required")
	}
	conn, err := c.DialHostedSession(ctx, session, 0)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	return collectHostedTaskOutput(conn, taskID)
}

func collectHostedTaskOutput(conn *websocket.Conn, taskID string) (string, error) {
	var output strings.Builder
	deadline := time.Now().Add(defaultHostedStreamTimeout)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return "", err
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return "", err
		}

		var envelope hosted.EventEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			continue
		}

		switch envelope.Event.Type {
		case hostedEventTypeContent:
			payload := payloadObject(envelope.Event.Payload)
			if strings.TrimSpace(payloadString(payload, "task_id")) == taskID {
				output.WriteString(payloadString(payload, "content"))
			}
		case hosted.EventTypeLifecycleComplete:
			if hostedTaskLabel(&envelope, "task_id") == taskID {
				return strings.TrimSpace(output.String()), nil
			}
		case hosted.EventTypeLifecycleError:
			if hostedTaskLabel(&envelope, "task_id") == taskID {
				return "", errors.New(hostedEventDetail(&envelope))
			}
		}
	}

	return strings.TrimSpace(output.String()), fmt.Errorf("timed out waiting for cloud task completion")
}

func (c *Client) authorizedJSONRequest(
	ctx context.Context,
	method string,
	path string,
	body any,
) (*http.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("cloud client is required")
	}
	if c.Config == nil {
		return nil, fmt.Errorf("cloud config is required")
	}
	if c.TokenManager == nil {
		return nil, fmt.Errorf("token manager is required")
	}
	accessToken, err := getValidAccessTokenWithHTTP(ctx, c.httpClient(), c.Config, c.TokenManager)
	if err != nil {
		return nil, err
	}
	targetURL := strings.TrimRight(c.Config.APIBaseURL, "/") + path
	return authorizedJSONRequestWithHTTP(ctx, c.httpClient(), method, targetURL, accessToken, body)
}

func authorizedJSONRequestWithHTTP(
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

	req, err := http.NewRequestWithContext(ctx, method, targetURL, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return httpClient.Do(req)
}

func decodeJSONResponse(response *http.Response, target any) error {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("request returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func hostedTaskLabel(event *hosted.EventEnvelope, key string) string {
	if event == nil {
		return ""
	}
	return payloadString(payloadObject(event.Event.Payload), key)
}

func hostedEventDetail(event *hosted.EventEnvelope) string {
	if event == nil {
		return "hosted_task_failed"
	}
	if detail := payloadString(payloadObject(event.Event.Payload), "detail"); detail != "" {
		return detail
	}
	return "hosted_task_failed"
}

func payloadObject(payload any) map[string]any {
	if payload == nil {
		return nil
	}
	if mapped, ok := payload.(map[string]any); ok {
		return mapped
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var mapped map[string]any
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil
	}
	return mapped
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}
