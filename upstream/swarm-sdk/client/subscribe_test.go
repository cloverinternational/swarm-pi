package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// fakeUpdate is a minimal IntermediateUpdate stand-in for fanout tests.
// It carries an ID so listeners can assert ordering without relying on any
// specific concrete update type from the agent package.
type fakeUpdate struct{ id int }

func (fakeUpdate) UpdateType() string { return "fake" }

// newTestClient returns a Client configured just enough for Subscribe/fanout
// tests. It bypasses New() — which requires credentials — because the
// subscriber machinery lives entirely on the Client struct and doesn't touch
// the agent, provider, storage, or hook subsystems.
func newTestClient() *Client { return &Client{} }

func TestSubscribe_SingleListener_ReceivesAllEvents(t *testing.T) {
	c := newTestClient()

	var got []int
	unsub := c.SubscribeUpdates(func(_ context.Context, u agent.IntermediateUpdate) error {
		got = append(got, u.(fakeUpdate).id)
		return nil
	})
	defer unsub()

	for i := range 5 {
		if err := c.fanout(context.Background(), fakeUpdate{id: i}); err != nil {
			t.Fatalf("fanout(%d): %v", i, err)
		}
	}

	want := []int{0, 1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestSubscribe_MultipleListeners_CalledInOrder(t *testing.T) {
	c := newTestClient()

	var order []string
	c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		order = append(order, "A")
		return nil
	})
	c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		order = append(order, "B")
		return nil
	})
	c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		order = append(order, "C")
		return nil
	})

	if err := c.fanout(context.Background(), fakeUpdate{}); err != nil {
		t.Fatalf("fanout: %v", err)
	}

	want := []string{"A", "B", "C"}
	if len(order) != len(want) {
		t.Fatalf("order length: got %v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d]: got %q want %q", i, order[i], want[i])
		}
	}
}

func TestSubscribe_Unsubscribe_StopsDelivery(t *testing.T) {
	c := newTestClient()

	var aCount, bCount int
	unsubA := c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		aCount++
		return nil
	})
	c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		bCount++
		return nil
	})

	_ = c.fanout(context.Background(), fakeUpdate{})
	unsubA()
	_ = c.fanout(context.Background(), fakeUpdate{})
	_ = c.fanout(context.Background(), fakeUpdate{})

	if aCount != 1 {
		t.Errorf("A count: got %d want 1 (unsubscribed after first fanout)", aCount)
	}
	if bCount != 3 {
		t.Errorf("B count: got %d want 3", bCount)
	}
}

func TestSubscribe_UnsubscribeDuringCallback_Safe(t *testing.T) {
	c := newTestClient()

	var unsubSelf func()
	var called int
	unsubSelf = c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		called++
		unsubSelf() // unsubscribe from within the callback
		return nil
	})

	// First fanout: callback runs, unsubscribes itself mid-flight.
	if err := c.fanout(context.Background(), fakeUpdate{}); err != nil {
		t.Fatalf("first fanout: %v", err)
	}
	// Second fanout: callback should not be invoked.
	if err := c.fanout(context.Background(), fakeUpdate{}); err != nil {
		t.Fatalf("second fanout: %v", err)
	}

	if called != 1 {
		t.Errorf("callback count: got %d want 1", called)
	}
}

func TestSubscribe_ListenerError_AbortsChain(t *testing.T) {
	c := newTestClient()

	sentinel := errors.New("boom")
	var aCalled, cCalled bool
	c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		aCalled = true
		return nil
	})
	c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		return sentinel
	})
	c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		cCalled = true
		return nil
	})

	err := c.fanout(context.Background(), fakeUpdate{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if !aCalled {
		t.Error("listener A should have been called")
	}
	if cCalled {
		t.Error("listener C should NOT have been called (chain aborted)")
	}
}

func TestSubscribe_ConcurrentSubscribeUnsubscribe_NoDataRace(t *testing.T) {
	c := newTestClient()

	const workers = 20
	const iterations = 100

	var deliveries atomic.Int64
	stop := make(chan struct{})

	// Fanout goroutine — keeps delivering events while subscribers churn.
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = c.fanout(context.Background(), fakeUpdate{})
			}
		}
	})

	// Subscriber churn — register and unregister many listeners concurrently.
	var workerWG sync.WaitGroup
	for range workers {
		workerWG.Go(func() {
			for range iterations {
				unsub := c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
					deliveries.Add(1)
					return nil
				})
				unsub()
			}
		})
	}
	workerWG.Wait()
	close(stop)
	wg.Wait()

	// Main assertion is -race clean completion; the delivery count is
	// non-deterministic so we only sanity check it.
	if deliveries.Load() < 0 {
		t.Errorf("delivery counter went negative: %d", deliveries.Load())
	}
}

func TestSubscribe_UnsubscribeIdempotent(t *testing.T) {
	c := newTestClient()

	var called int
	unsub := c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
		called++
		return nil
	})

	unsub()
	unsub() // second call must not panic or affect other subscribers

	if err := c.fanout(context.Background(), fakeUpdate{}); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	if called != 0 {
		t.Errorf("callback should not have fired, got %d calls", called)
	}
}

func TestSubscribe_ManyListenersRetainInsertionOrder(t *testing.T) {
	c := newTestClient()

	const n = 50
	var received []int
	var mu sync.Mutex
	for i := range n {
		idx := i
		c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error {
			mu.Lock()
			received = append(received, idx)
			mu.Unlock()
			return nil
		})
	}

	if err := c.fanout(context.Background(), fakeUpdate{}); err != nil {
		t.Fatalf("fanout: %v", err)
	}

	for i := range n {
		if received[i] != i {
			t.Fatalf("order broken at index %d: got %d want %d (full: %v)", i, received[i], i, received[:10])
		}
	}
}

// Compile-time guard: fanout must satisfy agent.IntermediateCallback so it can
// be passed to (*agent.Agent).SetIntermediateCallback in initAgent.
var _ agent.IntermediateCallback = (*Client)(nil).fanout

// Example verifies Subscribe returns a valid unsubscribe closure even when the
// caller never unsubscribes (e.g., client.Close() is relied on for teardown).
func TestSubscribe_UnsubReturnedIsNotNil(t *testing.T) {
	c := newTestClient()
	unsub := c.SubscribeUpdates(func(_ context.Context, _ agent.IntermediateUpdate) error { return nil })
	if unsub == nil {
		t.Fatal("Subscribe returned nil unsubscribe")
	}
	// Touch the fn so the compiler doesn't optimize it out.
	fmt.Fprintf(&nopWriter{}, "%T\n", unsub)
}

type nopWriter struct{}

func (*nopWriter) Write(p []byte) (int, error) { return len(p), nil }
