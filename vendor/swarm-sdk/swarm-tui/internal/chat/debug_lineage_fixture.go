package chat

import (
	"fmt"
	"time"

	sdkobs "github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerror "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

const debugLineageFixtureConvID = "debug-lineage-fixture"

// loadDebugLineageFixture seeds a deterministic chat conversation that renders
// the real error-lineage panel in ScreenChat without requiring provider traffic.
func (a *App) loadDebugLineageFixture() {
	base := sdkerror.Permanent(
		"provider.http.payment_required",
		"HTTP 402: Payment required to access this resource. Visit your billing tab.",
		sdkerror.WithTraceID("trace-debug-lineage-001"),
		sdkerror.WithOperation("provider.http.check_error_response"),
		sdkerror.WithComponent("provider.http"),
		sdkerror.WithAttr("status_code", 402),
		sdkerror.WithAttr("provider", "openai"),
	)
	streamErr := sdkerror.Wrap(
		base,
		"provider.openai.stream.http_error_parse_failed",
		sdkerror.WithOperation("openai.stream"),
		sdkerror.WithComponent("provider.openai"),
	)
	providerErr := sdkerror.Wrap(
		streamErr,
		"agent.provider_call.failed",
		sdkerror.WithOperation("agent.provider_call"),
		sdkerror.WithComponent("agent"),
	)
	execErr := sdkerror.Wrap(
		providerErr,
		"agent.execute.failed",
		sdkerror.WithOperation("agent.execute"),
		sdkerror.WithComponent("agent"),
	)

	panel := buildErrorLineagePanel(execErr, false)
	if panel != nil {
		// Attach a deterministic synthetic report so VHS snapshots exercise the timing + event-count UI,
		// without requiring real provider traffic.
		panel.Report = buildSyntheticLineageReport(panel)
		if panel.Report != nil && panel.TraceID == "" && panel.Report.TraceID != "" {
			panel.TraceID = panel.Report.TraceID
		}
	}
	t0 := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)

	errorLine := formatChatErrorLine(panel)

	a.messages = []Message{
		{
			Role:      "user",
			Content:   "Run a provider request that will fail with HTTP 402 so we can inspect lineage.",
			Timestamp: t0,
		},
		{
			Role:      "assistant",
			Content:   errorLine,
			Timestamp: t0.Add(2 * time.Second),
			Metadata: map[string]any{
				"error_code": sdkerror.GetCode(execErr),
				"trace_id":   sdkerror.GetTraceID(execErr),
			},
			ErrorLineage: panel,
		},
	}

	a.conversations = []Conversation{
		{
			ID:           debugLineageFixtureConvID,
			Title:        "Debug Error Lineage Fixture",
			Preview:      "Deterministic fixture for lineage visual inspection",
			Status:       "idle",
			LastMessage:  t0.Add(2 * time.Second),
			MessageCount: len(a.messages),
			IsActive:     false,
		},
	}
	a.activeConv = &a.conversations[0]
	a.setCurrentConversationID(debugLineageFixtureConvID)

	// Keep the viewport focused on chat content for deterministic screenshots.
	a.screen = ScreenChat
	a.workspaceMode = false
	a.twoPaneMode = false
	a.showSidePanel = false
	a.sidebarVisible = false
	a.notifications = nil
	a.sdkInitError = ""

	if a.errorLineageExpandedByConv == nil {
		a.errorLineageExpandedByConv = make(map[string]bool)
	}
	a.errorLineageExpandedByConv[debugLineageFixtureConvID] = false

	a.invalidateViewportCache()
	a.updateViewportContent()
	a.msgViewport.GotoBottom()
}

func buildSyntheticLineageReport(panel *errorLineagePanel) *sdkobs.LineageReport {
	if panel == nil {
		return nil
	}

	traceID := panel.TraceID
	if traceID == "" {
		traceID = "trace-debug-lineage-fixture"
	}

	events := make([]sdkobs.TraceEvent, 0, len(panel.Frames)*2)
	now := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)

	for i, frame := range panel.Frames {
		if frame.ErrorID == "" {
			continue
		}

		spanID := fmt.Sprintf("span-fixture-%02d", i+1)
		durationMs := int64(45 + i*85)

		events = append(events, sdkobs.TraceEvent{
			EventType:     sdkobs.EventTypeSpanError,
			Timestamp:     now.Add(time.Duration(i) * 10 * time.Millisecond),
			TraceID:       traceID,
			SpanID:        spanID,
			Operation:     frame.Operation,
			Component:     frame.Component,
			ErrorID:       frame.ErrorID,
			ParentErrorID: frame.ParentErrorID,
			Message:       frame.Message,
			File:          frame.File,
			Line:          frame.Line,
		})

		events = append(events, sdkobs.TraceEvent{
			EventType:  sdkobs.EventTypeSpanEnd,
			Timestamp:  now.Add(time.Duration(i)*10*time.Millisecond + time.Duration(durationMs)*time.Millisecond),
			TraceID:    traceID,
			SpanID:     spanID,
			Operation:  frame.Operation,
			Component:  frame.Component,
			DurationMs: durationMs,
		})
	}

	return &sdkobs.LineageReport{
		ErrorID:           panel.ErrorID,
		TraceID:           traceID,
		OperationSequence: panel.OperationSequence,
		Events:            events,
	}
}
