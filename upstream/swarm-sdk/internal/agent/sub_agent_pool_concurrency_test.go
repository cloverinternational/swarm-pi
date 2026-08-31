package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type countingSubAgentFactory struct {
	creates atomic.Int32
}

func (f *countingSubAgentFactory) CreateSubAgent(context.Context, SubAgentConfig) (*Agent, error) {
	f.creates.Add(1)
	time.Sleep(5 * time.Millisecond)
	return &Agent{}, nil
}

func (f *countingSubAgentFactory) CreateFromDefinition(context.Context, *Definition, provider.Config) (*Agent, error) {
	return nil, nil
}

func (f *countingSubAgentFactory) CreateWorker(context.Context, WorkerConfig) (*Agent, error) {
	return nil, nil
}

func (f *countingSubAgentFactory) CreateSteering(context.Context, SteeringAgentConfig) (*Agent, error) {
	return nil, nil
}

func (f *countingSubAgentFactory) CreateBackground(context.Context, BackgroundConfig) (*Agent, error) {
	return nil, nil
}

func TestSubAgentPoolFindOrCreateConcurrentSameID(t *testing.T) {
	t.Parallel()

	factory := &countingSubAgentFactory{}
	pool, err := NewSubAgentPool(PoolConfig{
		Factory: factory,
		Logger:  noop.NewLogger(),
		Tracer:  noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewSubAgentPool returned error: %v", err)
	}

	const callers = 32
	results := make(chan *SubAgentExecutor, callers)
	errs := make(chan error, callers)
	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-start
			executor, err := pool.FindOrCreate(context.Background(), "shared", SubAgentConfig{})
			results <- executor
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("FindOrCreate returned error: %v", err)
		}
	}

	var first *SubAgentExecutor
	for executor := range results {
		if first == nil {
			first = executor
			continue
		}
		if executor != first {
			t.Fatal("concurrent FindOrCreate returned different executors for the same ID")
		}
	}
	if got := factory.creates.Load(); got != 1 {
		t.Fatalf("factory CreateSubAgent calls = %d, want 1", got)
	}
	if got := pool.Size(); got != 1 {
		t.Fatalf("pool size = %d, want 1", got)
	}
}
