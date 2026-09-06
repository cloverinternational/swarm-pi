//go:build cloud_integration

package cloud

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

func TestCloudIntegration_CloudSession_Smoke(t *testing.T) {
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
		t.Skip("SWARM_CLOUD_SMOKE_PROJECT_REPO_URL not set; skipping cloud session smoke test")
	}

	oneShotPrompt := firstNonEmptyString(
		strings.TrimSpace(os.Getenv("SWARM_CLOUD_SMOKE_CLOUD_PROMPT")),
		"Summarize this repository, including its top-level structure and primary purpose. Do not suggest file changes.",
	)
	followUpPrompt := firstNonEmptyString(
		strings.TrimSpace(os.Getenv("SWARM_CLOUD_SMOKE_FOLLOW_UP_PROMPT")),
		"Read the README and summarize the most important usage details without making any changes.",
	)
	provider := firstNonEmptyString(
		strings.TrimSpace(os.Getenv("SWARM_CLOUD_SMOKE_PROVIDER")),
		"openai",
	)

	cfg := integrationConfig()
	tokenManager, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	seedIntegrationTokens(t, cfg, tokenManager)

	client := NewClient(cfg, tokenManager, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	if err := client.SyncHostedProviderCredential(ctx, provider); err != nil {
		t.Fatalf("SyncHostedProviderCredential(%s): %v", provider, err)
	}

	oneShot, err := client.CreateCloudTask(ctx, CreateCloudTaskRequest{
		RepoURL:     repoURL,
		Prompt:      oneShotPrompt,
		ProjectName: fmt.Sprintf("Cloud Smoke %d", time.Now().UTC().Unix()),
	})
	if err != nil {
		t.Fatalf("CreateCloudTask: %v", err)
	}
	if oneShot.Session.Runtime != SessionRuntimeCloud {
		t.Fatalf("expected cloud runtime, got %q", oneShot.Session.Runtime)
	}
	if !oneShot.Session.AutoStopAfterTask {
		t.Fatalf("expected one-shot cloud session to auto-stop")
	}
	if strings.TrimSpace(oneShot.Project.ID) == "" {
		t.Fatalf("expected durable project to be created")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_ = client.DeleteHostedProject(cleanupCtx, oneShot.Project.ID)
	})

	oneShotOutput, err := client.CollectCloudSessionTask(ctx, &oneShot.Session, oneShot.TaskID)
	if err != nil {
		t.Fatalf("CollectCloudSessionTask(one-shot): %v", err)
	}
	if strings.TrimSpace(oneShotOutput) == "" {
		t.Fatalf("expected non-empty one-shot output")
	}

	stoppedOneShot, err := client.WaitForHostedSessionStatus(ctx, oneShot.Session.ID, "stopped")
	if err != nil {
		t.Fatalf("WaitForHostedSessionStatus(one-shot stopped): %v", err)
	}
	if stoppedOneShot.Runtime != SessionRuntimeCloud {
		t.Fatalf("expected stopped one-shot session to remain cloud runtime, got %q", stoppedOneShot.Runtime)
	}

	interactive, err := client.CreateHostedSession(ctx, CreateHostedSessionRequest{
		ProjectID: oneShot.Project.ID,
		Runtime:   SessionRuntimeCloud,
	})
	if err != nil {
		t.Fatalf("CreateHostedSession(cloud): %v", err)
	}
	if interactive.Runtime != SessionRuntimeCloud {
		t.Fatalf("expected interactive cloud session runtime, got %q", interactive.Runtime)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_, _ = client.StopHostedSession(cleanupCtx, interactive.ID)
	})

	interactive, err = client.WaitForHostedSessionStatus(ctx, interactive.ID, "running")
	if err != nil {
		t.Fatalf("WaitForHostedSessionStatus(interactive running): %v", err)
	}

	conn, err := client.DialHostedSession(ctx, interactive, 0)
	if err != nil {
		t.Fatalf("DialHostedSession(interactive): %v", err)
	}

	var lastSequence int64
	attachEvent, err := WaitForHostedEvent(conn, 30*time.Second, func(event *hosted.EventEnvelope) bool {
		return event.Event.Type == hosted.EventTypeSessionAttach
	})
	if err != nil {
		_ = conn.Close()
		t.Fatalf("WaitForHostedEvent(session_attach): %v", err)
	}
	lastSequence = attachEvent.Sequence

	if err := SubmitHostedTask(conn, "cloud-follow-up", followUpPrompt); err != nil {
		_ = conn.Close()
		t.Fatalf("SubmitHostedTask(cloud-follow-up): %v", err)
	}
	followUpComplete, err := WaitForHostedEvent(conn, 2*time.Minute, func(event *hosted.EventEnvelope) bool {
		return event.Event.Type == hosted.EventTypeLifecycleComplete &&
			payloadString(payloadObject(event.Event.Payload), "task_id") == "cloud-follow-up"
	})
	if err != nil {
		_ = conn.Close()
		t.Fatalf("WaitForHostedEvent(cloud-follow-up complete): %v", err)
	}
	lastSequence = maxSequence(lastSequence, followUpComplete.Sequence)
	_ = conn.Close()

	replayAfter := lastSequence - 1
	if replayAfter < 0 {
		replayAfter = 0
	}
	conn, err = client.DialHostedSession(ctx, interactive, replayAfter)
	if err != nil {
		t.Fatalf("DialHostedSession(reconnect): %v", err)
	}
	defer conn.Close()

	replayed, err := WaitForHostedEvent(conn, 30*time.Second, func(event *hosted.EventEnvelope) bool {
		return event.Sequence == lastSequence
	})
	if err != nil {
		t.Fatalf("WaitForHostedEvent(replay): %v", err)
	}
	lastSequence = maxSequence(lastSequence, replayed.Sequence)

	if _, err := client.StopHostedSession(ctx, interactive.ID); err != nil {
		t.Fatalf("StopHostedSession(interactive): %v", err)
	}
	finalInteractive, err := client.WaitForHostedSessionStatus(ctx, interactive.ID, "stopped")
	if err != nil {
		t.Fatalf("WaitForHostedSessionStatus(interactive stopped): %v", err)
	}
	if finalInteractive.Runtime != SessionRuntimeCloud {
		t.Fatalf("expected final interactive session runtime to be cloud, got %q", finalInteractive.Runtime)
	}

	events, err := client.ListHostedSessionEvents(ctx, interactive.ID)
	if err != nil {
		t.Fatalf("ListHostedSessionEvents(interactive): %v", err)
	}
	if !containsHostedEventType(events, hosted.EventTypeSessionAttach) {
		t.Fatalf("expected session_attach event in interactive cloud audit trail, got %d events", len(events))
	}
	if !containsHostedEventType(events, hosted.EventTypeSessionResume) {
		t.Fatalf("expected session_resume event in interactive cloud audit trail, got %d events", len(events))
	}
	if !containsHostedLifecycleComplete(events, "cloud-follow-up") {
		t.Fatalf("expected lifecycle_complete for interactive cloud task, got %d events", len(events))
	}
	for _, event := range events {
		if event.Execution != nil && strings.TrimSpace(event.Execution.NodeID) != "" {
			t.Fatalf("expected cloud session events to avoid compute-node ids, found %q", event.Execution.NodeID)
		}
		if event.Execution != nil && event.Execution.Labels != nil {
			if runtime := strings.TrimSpace(event.Execution.Labels["runtime"]); runtime != "" && runtime != "cloud" {
				t.Fatalf("expected cloud session runtime labels only, found %q", runtime)
			}
		}
	}
}
