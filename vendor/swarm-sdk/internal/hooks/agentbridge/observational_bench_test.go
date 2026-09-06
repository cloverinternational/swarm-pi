package agentbridge_test

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/agentbridge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
)

// These benchmarks answer the question the Subagent skip comment raised and
// never measured (PLAN.md §10.4: "The comment cites steering latency; there is
// no benchmark in the repo quantifying it").
//
// BenchmarkObservational_* is the cost this change ADDS to a sub-agent tool
// call. BenchmarkFullHookPath_* is what inheriting the real hooks manager would
// have cost instead. Run:
//
//	go test -tags fts5 -run XXX -bench 'Observational|FullHookPath' -benchtime 2000x ./internal/hooks/agentbridge/

// benchHook does a small amount of real work so the numbers are not measuring
// an empty function.
type benchHook struct {
	declared bool
	sleep    time.Duration
	seen     int
}

func (b *benchHook) Name() string              { return "bench-hook" }
func (b *benchHook) Priority() int             { return 10 }
func (b *benchHook) Filter(_ hooks.Event) bool { return true }

func (b *benchHook) OnEvent(_ context.Context, event hooks.Event) (hooks.HookResult, error) {
	if b.sleep > 0 {
		time.Sleep(b.sleep)
	}
	b.seen += len(event.Data)
	return hooks.Continue(), nil
}

type declaredBenchHook struct{ benchHook }

func (d *declaredBenchHook) ObservationalOnly() {}

var _ hooks.ObservationalHook = (*declaredBenchHook)(nil)

func benchParams() map[string]any {
	return map[string]any{
		"command":     "go build ./...",
		"timeout":     60,
		"description": "a representative tool parameter payload",
		"env":         map[string]any{"CGO_ENABLED": "0", "GOFLAGS": "-tags=fts5"},
	}
}

// BenchmarkObservational_BeforeAndAfter measures the added per-tool-call cost
// of observational coverage: one deep copy of the parameter payload plus one
// non-blocking channel send, twice (before + after).
func BenchmarkObservational_BeforeAndAfter(b *testing.B) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	if err := mgr.Register(&declaredBenchHook{}, hooks.ScopeGlobal, ""); err != nil {
		b.Fatalf("Register: %v", err)
	}
	observer := agentbridge.New(mgr).ObservationalHooks()
	if observer == nil {
		b.Fatalf("no observational surface")
	}
	b.Cleanup(func() { agentbridge.CloseObservational(observer) })

	ctx := context.Background()
	params := benchParams()
	outcome := toolout.Command(0, 12, false)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		observer.ObserveToolBeforeExecute(ctx, "bash", params)
		observer.ObserveToolAfterExecute(ctx, "bash", params, outcome, "build succeeded", nil)
	}
}

// BenchmarkObservational_NotAttached is the gate-OFF cost: the field is nil, so
// the agent's observe helpers return immediately. This is the number that must
// stay at ~0 for the default path.
func BenchmarkObservational_NotAttached(b *testing.B) {
	var observer agent.ObservationalHooks // nil, as when the gate is off
	ctx := context.Background()
	params := benchParams()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if observer != nil {
			observer.ObserveToolBeforeExecute(ctx, "bash", params)
		}
	}
}

// BenchmarkFullHookPath_BeforeAndAfter is the counterfactual: what the sub-agent
// would have paid if it had simply inherited the blocking-capable hooks
// manager, which is what the skip comment refused. Hook bodies run
// SYNCHRONOUSLY on the tool path here.
func BenchmarkFullHookPath_BeforeAndAfter(b *testing.B) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	if err := mgr.Register(&benchHook{}, hooks.ScopeGlobal, ""); err != nil {
		b.Fatalf("Register: %v", err)
	}
	bridge := agentbridge.New(mgr)

	ctx := context.Background()
	params := benchParams()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bridge.EmitToolBeforeExecute(ctx, "bash", params)
		_ = bridge.EmitToolAfterExecute(ctx, "bash", params, nil, nil)
	}
}

// BenchmarkFullHookPath_SlowHook shows why the sub-agent skip exists at all: a
// hook that takes 1ms adds 2ms to EVERY tool call on the full path, and ~0 on
// the observational path.
func BenchmarkFullHookPath_SlowHook(b *testing.B) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	if err := mgr.Register(&benchHook{sleep: time.Millisecond}, hooks.ScopeGlobal, ""); err != nil {
		b.Fatalf("Register: %v", err)
	}
	bridge := agentbridge.New(mgr)
	ctx := context.Background()
	params := benchParams()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bridge.EmitToolBeforeExecute(ctx, "bash", params)
		_ = bridge.EmitToolAfterExecute(ctx, "bash", params, nil, nil)
	}
}

// BenchmarkObservational_SlowHook is the same 1ms hook reached through the
// observational view. The caller must not pay for it.
func BenchmarkObservational_SlowHook(b *testing.B) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	if err := mgr.Register(&declaredBenchHook{benchHook{sleep: time.Millisecond}}, hooks.ScopeGlobal, ""); err != nil {
		b.Fatalf("Register: %v", err)
	}
	observer := agentbridge.New(mgr).ObservationalHooks()
	b.Cleanup(func() { agentbridge.CloseObservational(observer) })
	ctx := context.Background()
	params := benchParams()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		observer.ObserveToolBeforeExecute(ctx, "bash", params)
		observer.ObserveToolAfterExecute(ctx, "bash", params, nil, "", nil)
	}
}
