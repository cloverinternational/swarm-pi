package mockserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMockServer_StartStop(t *testing.T) {
	srv, err := New(Config{})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx := context.Background()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Verify server is running
	resp, err := http.Get(fmt.Sprintf("http://%s/healthz", srv.Addr()))
	if err != nil {
		t.Fatalf("failed to call health endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("health check returned %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}
}

func TestMockServer_Authentication(t *testing.T) {
	srv, err := New(Config{
		APIKeys: []string{"test-key-123"},
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	baseURL := srv.URL()

	tests := []struct {
		name       string
		apiKey     string
		path       string
		method     string
		body       string
		wantStatus int
	}{
		{
			name:       "health endpoint no auth",
			method:     "GET",
			path:       "/healthz",
			wantStatus: http.StatusOK,
		},
		{
			name:       "protected endpoint no auth",
			method:     "POST",
			path:       "/v1/projects/init",
			body:       "{}",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "protected endpoint with valid key",
			method:     "POST",
			apiKey:     "test-key-123",
			path:       "/v1/projects/init",
			body:       `{"node_id":"test","project_id":"test"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "protected endpoint with invalid key",
			method:     "POST",
			apiKey:     "invalid-key",
			path:       "/v1/projects/init",
			body:       "{}",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req, err := http.NewRequest(tt.method, baseURL+tt.path, body)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			if tt.apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+tt.apiKey)
			}
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("failed to send request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestMockServer_ProjectInit(t *testing.T) {
	srv, err := New(Config{})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	baseURL := srv.URL()

	// Create project
	reqBody := `{"node_id":"test-node","project_id":"test-project-001"}`
	req, err := http.NewRequest("POST", baseURL+"/v1/projects/init", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var result ProjectInitResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.ProjectID != "test-project-001" {
		t.Errorf("project_id = %s, want test-project-001", result.ProjectID)
	}

	if result.Status != "initialized" {
		t.Errorf("status = %s, want initialized", result.Status)
	}

	// Verify project was created
	projects := srv.GetProjects()
	if len(projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(projects))
	}
}

func TestMockServer_SessionBoot(t *testing.T) {
	srv, err := New(Config{})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	baseURL := srv.URL()

	// Create project first
	projectReq := `{"node_id":"test-node","project_id":"test-project"}`
	req, _ := http.NewRequest("POST", baseURL+"/v1/projects/init", strings.NewReader(projectReq))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}
	resp.Body.Close()

	// Boot session
	sessionReq := `{"node_id":"test-node","session_id":"test-session-001","project_id":"test-project"}`
	req, _ = http.NewRequest("POST", baseURL+"/v1/sessions/boot", strings.NewReader(sessionReq))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var result BootSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.SessionID != "test-session-001" {
		t.Errorf("session_id = %s, want test-session-001", result.SessionID)
	}

	if result.Status != "running" {
		t.Errorf("status = %s, want running", result.Status)
	}

	// Verify session was created
	sessions := srv.GetSessions()
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
}

func TestMockServer_SessionStop(t *testing.T) {
	srv, err := New(Config{})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	baseURL := srv.URL()

	// Create project and boot session
	projectReq := `{"node_id":"test-node","project_id":"test-project"}`
	req, _ := http.NewRequest("POST", baseURL+"/v1/projects/init", strings.NewReader(projectReq))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}
	resp.Body.Close()

	sessionReq := `{"node_id":"test-node","session_id":"test-session-002","project_id":"test-project"}`
	req, _ = http.NewRequest("POST", baseURL+"/v1/sessions/boot", strings.NewReader(sessionReq))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to boot session: %v", err)
	}
	resp.Body.Close()

	// Stop session
	req, _ = http.NewRequest("POST", baseURL+"/v1/sessions/test-session-002/stop", nil)
	req.Header.Set("Authorization", "Bearer test-api-key")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var result StopSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Status != "stopped" {
		t.Errorf("status = %s, want stopped", result.Status)
	}
}

func TestMockServer_Callbacks(t *testing.T) {
	bootCalled := false
	stopCalled := false

	srv, err := New(Config{
		OnBootSession: func(sessionID, projectID string) error {
			bootCalled = true
			if sessionID != "callback-session" {
				t.Errorf("unexpected session_id: %s", sessionID)
			}
			return nil
		},
		OnStopSession: func(sessionID string) error {
			stopCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	baseURL := srv.URL()

	// Create project and boot session
	projectReq := `{"node_id":"test-node","project_id":"test-project"}`
	req, _ := http.NewRequest("POST", baseURL+"/v1/projects/init", strings.NewReader(projectReq))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}
	resp.Body.Close()

	sessionReq := `{"node_id":"test-node","session_id":"callback-session","project_id":"test-project"}`
	req, _ = http.NewRequest("POST", baseURL+"/v1/sessions/boot", strings.NewReader(sessionReq))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to boot session: %v", err)
	}
	resp.Body.Close()

	if !bootCalled {
		t.Error("OnBootSession callback was not called")
	}

	// Stop session
	req, _ = http.NewRequest("POST", baseURL+"/v1/sessions/callback-session/stop", nil)
	req.Header.Set("Authorization", "Bearer test-api-key")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	if !stopCalled {
		t.Error("OnStopSession callback was not called")
	}
}

func TestMockServer_Idempotency(t *testing.T) {
	srv, err := New(Config{})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	baseURL := srv.URL()

	// Create same project twice
	projectReq := `{"node_id":"test-node","project_id":"idempotent-project"}`
	for i := range 2 {
		req, _ := http.NewRequest("POST", baseURL+"/v1/projects/init", strings.NewReader(projectReq))
		req.Header.Set("Authorization", "Bearer test-api-key")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}

		var result ProjectInitResponse
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if result.Status != "initialized" {
			t.Errorf("request %d: status = %s, want initialized", i+1, result.Status)
		}
	}

	// Should still have only 1 project
	projects := srv.GetProjects()
	if len(projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(projects))
	}

	// Boot same session twice
	sessionReq := `{"node_id":"test-node","session_id":"idempotent-session","project_id":"idempotent-project"}`
	for i := range 2 {
		req, _ := http.NewRequest("POST", baseURL+"/v1/sessions/boot", strings.NewReader(sessionReq))
		req.Header.Set("Authorization", "Bearer test-api-key")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}

		var result BootSessionResponse
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if result.Status != "running" {
			t.Errorf("request %d: status = %s, want running", i+1, result.Status)
		}
	}

	// Should still have only 1 session
	sessions := srv.GetSessions()
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
}
