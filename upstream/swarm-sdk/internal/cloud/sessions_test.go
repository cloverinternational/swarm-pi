package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientCreateHostedSessionCloud(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var gotAuth string
	var gotBody CreateHostedSessionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "accepted",
			"session": map[string]any{
				"id":                   "sess-cloud-1",
				"status":               "queued",
				"runtime":              "cloud",
				"auto_stop_after_task": false,
				"websocket_url":        "https://api.example/ws",
				"protocol_version":     "hosted.v1alpha1",
				"resume_token": map[string]any{
					"value": "resume-token",
				},
				"cursor": map[string]any{
					"value":    "cursor-1",
					"sequence": 1,
				},
				"metrics": map[string]float64{},
			},
		})
	}))
	defer server.Close()

	tokenManager, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tokenManager.SaveTokens(&TokenSet{
		AccessToken: "cloud-access-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(10 * time.Minute).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	client := NewClient(&CloudConfig{APIBaseURL: server.URL}, tokenManager, server.Client())
	session, err := client.CreateHostedSession(context.Background(), CreateHostedSessionRequest{
		ProjectID: "project-123",
		Runtime:   SessionRuntimeCloud,
	})
	if err != nil {
		t.Fatalf("CreateHostedSession: %v", err)
	}

	if gotAuth != "Bearer cloud-access-token" {
		t.Fatalf("Authorization header = %q", gotAuth)
	}
	if gotBody.ProjectID != "project-123" {
		t.Fatalf("project_id = %q", gotBody.ProjectID)
	}
	if gotBody.Runtime != SessionRuntimeCloud {
		t.Fatalf("runtime = %q", gotBody.Runtime)
	}
	if session.Runtime != SessionRuntimeCloud {
		t.Fatalf("session runtime = %q", session.Runtime)
	}
}

func TestClientCreateCloudTaskFromRepoURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var gotAuth string
	var gotBody CreateCloudTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/cloud-tasks" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "accepted",
			"task_id": "task-123",
			"project": map[string]any{
				"id":             "project-123",
				"name":           "cloud-smoke",
				"repo_url":       gotBody.RepoURL,
				"default_branch": "main",
			},
			"session": map[string]any{
				"id":                   "sess-cloud-ask-1",
				"status":               "running",
				"runtime":              "cloud",
				"auto_stop_after_task": true,
				"websocket_url":        "https://api.example/ws",
				"protocol_version":     "hosted.v1alpha1",
				"resume_token": map[string]any{
					"value": "resume-token",
				},
				"cursor": map[string]any{
					"value":    "cursor-1",
					"sequence": 1,
				},
				"metrics": map[string]float64{},
			},
		})
	}))
	defer server.Close()

	tokenManager, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tokenManager.SaveTokens(&TokenSet{
		AccessToken: "cloud-access-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(10 * time.Minute).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	client := NewClient(&CloudConfig{APIBaseURL: server.URL}, tokenManager, server.Client())
	response, err := client.CreateCloudTask(context.Background(), CreateCloudTaskRequest{
		RepoURL: "https://github.com/swarmcode/cloud-smoke.git",
		Prompt:  "What does this repo do?",
	})
	if err != nil {
		t.Fatalf("CreateCloudTask: %v", err)
	}

	if gotAuth != "Bearer cloud-access-token" {
		t.Fatalf("Authorization header = %q", gotAuth)
	}
	if gotBody.RepoURL != "https://github.com/swarmcode/cloud-smoke.git" {
		t.Fatalf("repo_url = %q", gotBody.RepoURL)
	}
	if gotBody.Prompt != "What does this repo do?" {
		t.Fatalf("prompt = %q", gotBody.Prompt)
	}
	if response.TaskID != "task-123" {
		t.Fatalf("task_id = %q", response.TaskID)
	}
	if response.Session.Runtime != SessionRuntimeCloud {
		t.Fatalf("session runtime = %q", response.Session.Runtime)
	}
	if !response.Session.AutoStopAfterTask {
		t.Fatalf("expected auto_stop_after_task to be true")
	}
}
