package openai

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestProcessSSEStream_ThinkingModelUsesLongerTimeout(t *testing.T) {
	t.Helper()

	// Set very short idle timeout, but a longer thinking timeout.
	// After reasoning content is seen, the stream should survive the
	// short idle gap because the thinking timeout takes over.
	prevIdle := openAIStreamIdleTimeout
	prevThinking := openAIStreamThinkingIdleTimeout
	prevPostUsage := openAIStreamPostUsageTimeout
	openAIStreamIdleTimeout = 50 * time.Millisecond
	openAIStreamThinkingIdleTimeout = 500 * time.Millisecond
	openAIStreamPostUsageTimeout = 50 * time.Millisecond
	defer func() {
		openAIStreamIdleTimeout = prevIdle
		openAIStreamThinkingIdleTimeout = prevThinking
		openAIStreamPostUsageTimeout = prevPostUsage
	}()

	reader, writer := io.Pipe()
	defer reader.Close()

	go func() {
		defer writer.Close()
		// Send reasoning content first — this should switch the timer to the longer thinking timeout
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-think\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"Let me think step by step\"}}]}\n\n")
		// Sleep longer than the short idle timeout (50ms) but less than the thinking timeout (500ms)
		// Without the fix, this would trigger the idle timeout and kill the stream.
		time.Sleep(150 * time.Millisecond)
		// Send content after the thinking pause
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-think\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"The answer is 42\"},\"finish_reason\":\"stop\"}]}\n\n")
		// Send usage
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-think\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}()

	p := &Provider{
		name:   "test-thinking-provider",
		logger: noop.NewLogger(),
		tracer: noop.NewTracer(),
	}

	chunks := make(chan provider.StreamChunk, 16)
	err := p.processSSEStream(context.Background(), reader, chunks, "glm-5.1")
	if err != nil {
		t.Fatalf("processSSEStream returned error: %v", err)
	}
	close(chunks)

	var sawThinking bool
	var sawContent bool
	var final provider.StreamChunk
	for chunk := range chunks {
		if chunk.Thinking != "" {
			sawThinking = true
		}
		if chunk.Delta != "" && !chunk.Done {
			sawContent = true
		}
		if chunk.Done {
			final = chunk
		}
	}

	if !sawThinking {
		t.Fatal("expected thinking chunk to be received")
	}
	if !sawContent {
		t.Fatal("expected content chunk after thinking pause — stream was killed by idle timeout")
	}
	if final.FinishReason != provider.FinishReasonStop {
		t.Fatalf("expected finish_reason stop, got %q", final.FinishReason)
	}
}

func TestProcessSSEStream_RecoversAfterUsageWhenDoneMissing(t *testing.T) {
	t.Helper()

	prevTimeout := openAIStreamIdleTimeout
	prevPostUsageTimeout := openAIStreamPostUsageTimeout
	openAIStreamIdleTimeout = 25 * time.Millisecond
	openAIStreamPostUsageTimeout = 25 * time.Millisecond
	defer func() {
		openAIStreamIdleTimeout = prevTimeout
		openAIStreamPostUsageTimeout = prevPostUsageTimeout
	}()

	reader, writer := io.Pipe()
	defer reader.Close()

	go func() {
		defer writer.Close()
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ACK FROM BETA\"}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-test\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\n")
		time.Sleep(200 * time.Millisecond)
	}()

	p := &Provider{
		name:   "fireworks",
		logger: noop.NewLogger(),
		tracer: noop.NewTracer(),
	}

	chunks := make(chan provider.StreamChunk, 8)
	err := p.processSSEStream(context.Background(), reader, chunks, "test-model")
	if err != nil {
		t.Fatalf("processSSEStream returned error: %v", err)
	}
	close(chunks)

	var sawDelta bool
	var sawUsage bool
	var final provider.StreamChunk
	var sawFinal bool
	for chunk := range chunks {
		if chunk.Delta == "ACK FROM BETA" && !chunk.Done {
			sawDelta = true
		}
		if chunk.Usage != nil && !chunk.Done {
			sawUsage = true
		}
		if chunk.Done {
			final = chunk
			sawFinal = true
		}
	}

	if !sawDelta {
		t.Fatalf("expected streamed delta chunk before recovery")
	}
	if !sawUsage {
		t.Fatalf("expected usage chunk before recovery")
	}
	if !sawFinal {
		t.Fatalf("expected final recovered done chunk")
	}
	if final.FinishReason != provider.FinishReasonStop {
		t.Fatalf("expected recovered finish reason %q, got %q", provider.FinishReasonStop, final.FinishReason)
	}
	if final.Usage == nil || final.Usage.Total != 13 {
		t.Fatalf("expected recovered usage total 13, got %#v", final.Usage)
	}
	if got := final.Metadata["message_id"]; got != "chatcmpl-test" {
		t.Fatalf("expected recovered metadata message_id chatcmpl-test, got %#v", got)
	}
}

func TestProcessSSEStream_IgnoresEmptyChoiceHeartbeatsAfterUsage(t *testing.T) {
	t.Helper()

	prevTimeout := openAIStreamIdleTimeout
	prevPostUsageTimeout := openAIStreamPostUsageTimeout
	openAIStreamIdleTimeout = 100 * time.Millisecond
	openAIStreamPostUsageTimeout = 25 * time.Millisecond
	defer func() {
		openAIStreamIdleTimeout = prevTimeout
		openAIStreamPostUsageTimeout = prevPostUsageTimeout
	}()

	reader, writer := io.Pipe()
	defer reader.Close()

	go func() {
		defer writer.Close()
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-heartbeat\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ACK FROM BETA\"}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-heartbeat\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\n")
		for range 10 {
			_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-heartbeat\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":null}]}\n\n")
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
	}()

	p := &Provider{
		name:   "fireworks",
		logger: noop.NewLogger(),
		tracer: noop.NewTracer(),
	}

	chunks := make(chan provider.StreamChunk, 8)
	start := time.Now()
	err := p.processSSEStream(context.Background(), reader, chunks, "test-model")
	if err != nil {
		t.Fatalf("processSSEStream returned error: %v", err)
	}
	elapsed := time.Since(start)
	close(chunks)

	var sawFinal bool
	for chunk := range chunks {
		if chunk.Done {
			sawFinal = true
		}
	}
	if !sawFinal {
		t.Fatalf("expected recovered done chunk")
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("expected recovery before heartbeat tail finished, elapsed=%s", elapsed)
	}
}
