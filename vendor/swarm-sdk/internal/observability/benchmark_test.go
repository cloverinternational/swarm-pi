package observability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
)

type discardSink struct{}

func (d discardSink) WriteEvent(_ context.Context, _ TraceEvent) error { return nil }
func (d discardSink) Close() error                                     { return nil }

func BenchmarkTracerOverhead(b *testing.B) {
	b.Run("noop", func(b *testing.B) {
		tracer := NewNoopTracer()
		benchmarkTracerLoop(b, tracer)
	})

	b.Run("local", func(b *testing.B) {
		tracer := NewLocalTracer(LocalTracerConfig{
			Sink:     discardSink{},
			Redactor: NewDefaultRedactor(),
		})
		benchmarkTracerLoop(b, tracer)
	})
}

func benchmarkTracerLoop(b *testing.B, tracer Tracer) {
	ctx := context.Background()
	payload := bytes.Repeat([]byte("swarm-sdk-observability-benchmark-"), 128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spanCtx, span := tracer.StartSpan(ctx, "agent.execute")
		span.SetAttribute("iteration", i)
		_ = representativeAgentWork(payload, i)
		span.End()
		_ = spanCtx
	}
}

func representativeAgentWork(payload []byte, iteration int) byte {
	var out byte
	for i := range 30 {
		buf := append(payload, byte(iteration), byte(i))
		sum := sha256.Sum256(buf)
		out ^= sum[0]
	}
	return out
}
