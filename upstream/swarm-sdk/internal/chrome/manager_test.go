package chrome

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock   *fakeClock
	at      time.Time
	fn      func()
	stopped bool
	fired   bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{clock: c, at: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := !t.stopped && !t.fired
	t.stopped = true
	return wasActive
}

func (c *fakeClock) Advance(d time.Duration) {
	callbacks := c.TakeDue(d)
	for _, callback := range callbacks {
		callback()
	}
}

func (c *fakeClock) TakeDue(d time.Duration) []func() {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var callbacks []func()
	for _, timer := range c.timers {
		if !timer.stopped && !timer.fired && !timer.at.After(c.now) {
			timer.fired = true
			callbacks = append(callbacks, timer.fn)
		}
	}
	c.mu.Unlock()
	return callbacks
}

type managerHarness struct {
	manager *Manager
	clock   *fakeClock
	launch  chan LaunchRequest
	close   chan CloseRequest
	cancel  chan CloseRequest
}

func newManagerHarness(t *testing.T) *managerHarness {
	t.Helper()
	h := &managerHarness{
		clock: newFakeClock(), launch: make(chan LaunchRequest, 20),
		close: make(chan CloseRequest, 20), cancel: make(chan CloseRequest, 20),
	}
	var err error
	h.manager, err = NewManager(ManagerConfig{
		Clock: h.clock, IdleTimeout: 10 * time.Minute,
		OnLaunch:      func(req LaunchRequest) { h.launch <- req },
		OnClose:       func(req CloseRequest) { h.close <- req },
		OnCancelClose: func(req CloseRequest) { h.cancel <- req },
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func testFamily(name string) FamilyContext { return familyForRoot("manager-test-" + name) }

func (h *managerHarness) ready(t *testing.T, family FamilyContext) GenerationToken {
	t.Helper()
	token, err := h.manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	<-h.launch
	if err := h.manager.MarkReady(family, token); err != nil {
		t.Fatal(err)
	}
	return token
}

func waitForSnapshot(t *testing.T, m *Manager, family FamilyContext, check func(Snapshot) bool) Snapshot {
	t.Helper()
	for i := 0; i < 10000; i++ {
		snapshot, err := m.Snapshot(family)
		if err != nil {
			t.Fatal(err)
		}
		if check(snapshot) {
			return snapshot
		}
		runtime.Gosched()
	}
	t.Fatal("manager state did not reach expected condition")
	return Snapshot{}
}

func receiveLease(t *testing.T, ch <-chan *Lease) *Lease {
	t.Helper()
	return <-ch
}

func TestManagerRequestsExposeOnlyTrustedFamilyAuthority(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("adapter-authority")

	token, err := h.manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	launch := <-h.launch
	if !launch.Family().SameFamily(family) {
		t.Fatal("launch request did not retain trusted family authority")
	}
	if launch.Generation() != token.generation {
		t.Fatalf("launch generation = %d, want %d", launch.Generation(), token.generation)
	}

	if err := h.manager.MarkReady(launch.Family(), launch.Token()); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(10 * time.Minute)
	closeReq := <-h.close
	if !closeReq.Family().SameFamily(family) {
		t.Fatal("close request did not retain trusted family authority")
	}
	if closeReq.Generation() != token.generation || closeReq.Epoch() == 0 {
		t.Fatalf("close request = generation %d epoch %d", closeReq.Generation(), closeReq.Epoch())
	}
}

func TestManagerRestoreLaunchingRequiresLiveClaimReconciliation(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("restore")

	token, err := h.manager.RestoreLaunching(family, 7)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-h.launch:
		t.Fatalf("restore emitted launch request for generation %d", request.Generation())
	default:
	}
	snapshot, err := h.manager.Snapshot(family)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionLaunching || snapshot.Generation != 7 || !snapshot.Reconciliation {
		t.Fatalf("restored snapshot = %+v", snapshot)
	}
	if repeated, err := h.manager.RestoreLaunching(family, 7); err != nil || repeated != token {
		t.Fatalf("idempotent restore = (%+v, %v)", repeated, err)
	}
	if _, err := h.manager.RestoreLaunching(family, 8); err == nil {
		t.Fatal("restore replaced an unresolved generation")
	}

	if err := h.manager.MarkReady(family, token); err != nil {
		t.Fatal(err)
	}
	if ready, err := h.manager.Snapshot(family); err != nil || ready.Reconciliation {
		t.Fatalf("ready reconciliation state = (%+v, %v)", ready, err)
	}
	if err := h.manager.MarkWindowAbsent(family, token); err != nil {
		t.Fatal(err)
	}
	next, err := h.manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	if next.generation != 8 {
		t.Fatalf("generation after restored claim closed = %d, want 8", next.generation)
	}
	if request := <-h.launch; request.Token() != next {
		t.Fatal("fresh launch callback did not carry generation 8")
	}
}

func TestManagerAuthorityIsolationAndGenerations(t *testing.T) {
	h := newManagerHarness(t)
	a, b := testFamily("a"), testFamily("b")
	if _, err := h.manager.EnsureFamily(FamilyContext{}); !errors.Is(err, ErrBrowserFamilyAuthority) {
		t.Fatalf("invalid authority error = %v", err)
	}
	ta, tb := h.ready(t, a), h.ready(t, b)
	if ta.generation != 1 || tb.generation != 1 {
		t.Fatalf("initial generations = %d, %d", ta.generation, tb.generation)
	}
	if err := h.manager.RecordTrustedHumanActivity(b, ta); !errors.Is(err, ErrBrowserFamilyAuthority) {
		t.Fatalf("cross-family token error = %v", err)
	}
	if err := h.manager.MarkWindowAbsent(a, ta); err != nil {
		t.Fatal(err)
	}
	ta2, err := h.manager.BeginLaunch(a)
	if err != nil {
		t.Fatal(err)
	}
	<-h.launch
	if ta2.generation != 2 {
		t.Fatalf("new generation = %d, want 2", ta2.generation)
	}
	if err := h.manager.MarkReady(a, ta); errorCode(err) != ErrStaleGeneration {
		t.Fatalf("stale generation error = %v", err)
	}
	bSnapshot, _ := h.manager.Snapshot(b)
	if bSnapshot.State != SessionReady || bSnapshot.Generation != 1 {
		t.Fatalf("family B changed with A: %+v", bSnapshot)
	}
}

func TestManagerFIFOQueuedCancellationAndFamilyIndependence(t *testing.T) {
	h := newManagerHarness(t)
	a, b := testFamily("fifo-a"), testFamily("fifo-b")
	h.ready(t, a)
	h.ready(t, b)
	first, err := h.manager.Admit(context.Background(), a, ActionOptions{CountsAsActivity: true})
	if err != nil {
		t.Fatal(err)
	}
	// A different family is never blocked by A's active lease.
	independent, err := h.manager.Admit(context.Background(), b, ActionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	independent.Release()

	order := make(chan int, 2)
	leases := make(chan *Lease, 2)
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() {
		lease, err := h.manager.Admit(ctx2, a, ActionOptions{CountsAsActivity: true})
		if err != nil {
			order <- -2
			return
		}
		order <- 2
		leases <- lease
	}()
	waitForSnapshot(t, h.manager, a, func(s Snapshot) bool { return s.QueuedActions == 1 })
	go func() {
		lease, err := h.manager.Admit(context.Background(), a, ActionOptions{})
		if err != nil {
			order <- -3
			return
		}
		order <- 3
		leases <- lease
	}()
	waitForSnapshot(t, h.manager, a, func(s Snapshot) bool { return s.QueuedActions == 2 })
	cancel2()
	waitForSnapshot(t, h.manager, a, func(s Snapshot) bool { return s.QueuedActions == 1 })
	if got := <-order; got != -2 {
		t.Fatalf("cancelled waiter result = %d", got)
	}
	first.Release()
	if got := <-order; got != 3 {
		t.Fatalf("FIFO result = %d, want 3", got)
	}
	receiveLease(t, leases).Release()
	snapshot, _ := h.manager.Snapshot(a)
	if snapshot.State != SessionReady || snapshot.LeaseCount != 0 {
		t.Fatalf("released state = %+v", snapshot)
	}
	// Double release is harmless.
	first.Release()
}

func TestManagerAdmitLaunchesFreshGenerationAndLaunchFailureDrainsQueue(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("admit-launch")
	if _, err := h.manager.EnsureFamily(family); err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, 2)
	go func() {
		_, err := h.manager.Admit(context.Background(), family, ActionOptions{})
		errs <- err
	}()
	firstLaunch := <-h.launch
	waitForSnapshot(t, h.manager, family, func(s Snapshot) bool { return s.QueuedActions == 1 })
	go func() {
		_, err := h.manager.Admit(context.Background(), family, ActionOptions{})
		errs <- err
	}()
	waitForSnapshot(t, h.manager, family, func(s Snapshot) bool { return s.QueuedActions == 2 })

	cause := NewFlexibleError(ErrTransportClosed, "launch failed", true, ExecutionNotStarted)
	if err := h.manager.MarkLaunchFailed(family, firstLaunch.Token(), cause); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-errs:
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != cause.Code || typed.Message != cause.Message {
				t.Fatalf("queued launch failure = %v, want typed cause %+v", err, cause)
			}
		case <-time.After(time.Second):
			t.Fatal("queued waiter hung after launch failure")
		}
	}
	failed, _ := h.manager.Snapshot(family)
	if failed.State != SessionFailed || failed.QueuedActions != 0 {
		t.Fatalf("launch failure state = %+v", failed)
	}

	leaseCh := make(chan *Lease, 1)
	go func() {
		lease, err := h.manager.Admit(context.Background(), family, ActionOptions{})
		if err != nil {
			errs <- err
			return
		}
		leaseCh <- lease
	}()
	relaunch := <-h.launch
	if relaunch.Generation() != firstLaunch.Generation()+1 {
		t.Fatalf("relaunch generation = %d, want %d", relaunch.Generation(), firstLaunch.Generation()+1)
	}
	if err := h.manager.MarkReady(family, relaunch.Token()); err != nil {
		t.Fatal(err)
	}
	receiveLease(t, leaseCh).Release()
}

func TestManagerTrustedActivityAndIdleTimerRace(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("idle")
	token := h.ready(t, family)
	initial, _ := h.manager.Snapshot(family)
	h.clock.Advance(9 * time.Minute)
	if err := h.manager.RecordTrustedHumanActivity(family, token); err != nil {
		t.Fatal(err)
	}
	afterHuman, _ := h.manager.Snapshot(family)
	if !afterHuman.LastHumanActivity.After(initial.LastHumanActivity) ||
		!afterHuman.LastAgentActivity.Equal(initial.LastAgentActivity) {
		t.Fatalf("activity sources conflated: before=%+v after=%+v", initial, afterHuman)
	}
	// The stale timer at minute 10 was stopped by trusted activity.
	h.clock.Advance(time.Minute)
	if snapshot, _ := h.manager.Snapshot(family); snapshot.State != SessionReady {
		t.Fatalf("stale idle timer closed active family: %+v", snapshot)
	}
	lease, err := h.manager.Admit(context.Background(), family, ActionOptions{CountsAsActivity: true})
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := h.manager.Snapshot(family)
	if !agent.LastAgentActivity.After(initial.LastAgentActivity) {
		t.Fatal("agent queue admission did not record activity")
	}
	h.clock.Advance(20 * time.Minute)
	if snapshot, _ := h.manager.Snapshot(family); snapshot.State != SessionActive {
		t.Fatalf("lease did not suppress idle close: %+v", snapshot)
	}
	lease.Release()
	released, _ := h.manager.Snapshot(family)
	h.clock.Advance(10 * time.Minute)
	req := <-h.close
	closed, _ := h.manager.Snapshot(family)
	if req.Generation() != token.generation || closed.ClosePhase != CloseSent ||
		closed.LastAgentActivity.Before(released.LastAgentActivity) {
		t.Fatalf("idle close = req %+v snapshot %+v", req, closed)
	}
}

func TestManagerStaleRunnableTimerCannotReplaceCurrentTimer(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("timer-identity")
	token := h.ready(t, family)

	oldCallbacks := h.clock.TakeDue(10 * time.Minute)
	if len(oldCallbacks) != 1 {
		t.Fatalf("due callbacks = %d, want 1", len(oldCallbacks))
	}
	if err := h.manager.RecordTrustedHumanActivity(family, token); err != nil {
		t.Fatal(err)
	}
	record, err := h.manager.record(family, false)
	if err != nil {
		t.Fatal(err)
	}
	record.mu.Lock()
	currentTimer := record.idleTimer
	currentEpoch := record.idleTimerEpoch
	record.mu.Unlock()

	oldCallbacks[0]()

	record.mu.Lock()
	if record.idleTimer != currentTimer || record.idleTimerEpoch != currentEpoch {
		t.Fatalf("stale callback replaced current timer: timer %p -> %p, epoch %d -> %d",
			currentTimer, record.idleTimer, currentEpoch, record.idleTimerEpoch)
	}
	record.mu.Unlock()
	h.clock.Advance(10 * time.Minute)
	req := <-h.close
	if req.Generation() != token.generation {
		t.Fatalf("close generation = %d, want %d", req.Generation(), token.generation)
	}
}

func TestManagerCloseCancellationBeforeCommitAndStaleEpoch(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("cancel-close")
	h.ready(t, family)
	h.clock.Advance(10 * time.Minute)
	req := <-h.close

	leaseCh := make(chan *Lease, 1)
	go func() {
		lease, _ := h.manager.Admit(context.Background(), family, ActionOptions{CountsAsActivity: true})
		leaseCh <- lease
	}()
	waitForSnapshot(t, h.manager, family, func(s Snapshot) bool { return s.QueuedActions == 1 })
	if got := <-h.cancel; got != req {
		t.Fatal("cancel notification did not preserve close token")
	}
	select {
	case <-leaseCh:
		t.Fatal("lease granted before cancellation acknowledgement")
	default:
	}
	stale := req
	stale.epoch--
	if err := h.manager.AcknowledgeCloseCancellation(family, stale, true); errorCode(err) != ErrStaleGeneration {
		t.Fatalf("stale close epoch error = %v", err)
	}
	if err := h.manager.AcknowledgeCloseCancellation(family, req, true); err != nil {
		t.Fatal(err)
	}
	lease := receiveLease(t, leaseCh)
	lease.Release()
}

func TestManagerCommittedCloseReconcilesThenRelaunches(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("reconcile")
	old := h.ready(t, family)
	h.clock.Advance(10 * time.Minute)
	req := <-h.close
	if err := h.manager.MarkCloseAccepted(family, req); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.MarkCloseCommitted(family, req); err != nil {
		t.Fatal(err)
	}
	leaseCh := make(chan *Lease, 1)
	go func() {
		lease, _ := h.manager.Admit(context.Background(), family, ActionOptions{})
		leaseCh <- lease
	}()
	waitForSnapshot(t, h.manager, family, func(s Snapshot) bool { return s.QueuedActions == 1 })
	if err := h.manager.AcknowledgeCloseCancellation(family, req, true); errorCode(err) != ErrSessionClosing {
		t.Fatalf("post-commit cancellation error = %v", err)
	}
	uncertain := NewFlexibleError(ErrTransportClosed, "close response lost", true, ExecutionIndeterminate)
	if err := h.manager.CloseUncertainAfterCommit(family, req, uncertain); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.ReconcileClose(family, req, false); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := h.manager.Snapshot(family)
	if !snapshot.Reconciliation || snapshot.State != SessionClosing {
		t.Fatalf("existing window did not remain closing: %+v", snapshot)
	}
	if err := h.manager.ReconcileClose(family, req, true); err != nil {
		t.Fatal(err)
	}
	launch := <-h.launch
	if launch.Generation() != old.generation+1 {
		t.Fatalf("relaunch generation = %d", launch.Generation())
	}
	if err := h.manager.MarkReady(family, launch.Token()); err != nil {
		t.Fatal(err)
	}
	lease := receiveLease(t, leaseCh)
	if lease.Generation() != old.generation+1 {
		t.Fatalf("lease generation = %d", lease.Generation())
	}
	lease.Release()
	if err := h.manager.MarkReady(family, old); errorCode(err) != ErrStaleGeneration {
		t.Fatalf("old generation accepted after reconciliation: %v", err)
	}
}

func TestManagerClosePrecommitFailureAndCallbackLockBoundary(t *testing.T) {
	clock := newFakeClock()
	family := testFamily("failure")
	var manager *Manager
	closeCh := make(chan CloseRequest, 1)
	var callbackSnapshot Snapshot
	manager, _ = NewManager(ManagerConfig{
		Clock: clock, IdleTimeout: time.Minute,
		OnClose: func(req CloseRequest) {
			// This would deadlock if the family or manager lock were held.
			callbackSnapshot, _ = manager.Snapshot(family)
			closeCh <- req
		},
	})
	token, _ := manager.BeginLaunch(family)
	if err := manager.MarkReady(family, token); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	req := <-closeCh
	if callbackSnapshot.ClosePhase != CloseSent {
		t.Fatalf("callback boundary = %s, want sent", callbackSnapshot.ClosePhase)
	}
	wait := make(chan *Lease, 1)
	go func() {
		lease, _ := manager.Admit(context.Background(), family, ActionOptions{})
		wait <- lease
	}()
	waitForSnapshot(t, manager, family, func(s Snapshot) bool { return s.QueuedActions == 1 })
	cause := NewFlexibleError(ErrTransportClosed, "not dispatched", true, ExecutionNotStarted)
	if err := manager.CloseFailedBeforeCommit(family, req, cause); err != nil {
		t.Fatal(err)
	}
	lease := receiveLease(t, wait)
	lease.Release()
	snapshot, _ := manager.Snapshot(family)
	if snapshot.State != SessionReady || snapshot.Failure == nil || snapshot.Failure.Code != ErrTransportClosed {
		t.Fatalf("precommit recovery = %+v", snapshot)
	}
}

func TestManagerBlockedCloseCallbackDoesNotBlockContextShutdown(t *testing.T) {
	clock := newFakeClock()
	family := testFamily("blocked-callback")
	entered := make(chan struct{})
	release := make(chan struct{})
	manager, err := NewManager(ManagerConfig{
		Clock: clock, IdleTimeout: time.Minute,
		OnClose: func(CloseRequest) {
			close(entered)
			<-release
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.MarkReady(family, token); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.ShutdownFamily(ctx, family) }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked in synchronous callback")
	}
	close(release)
}

func TestManagerBlockedFamilyCallbackDoesNotDelayAnotherFamily(t *testing.T) {
	clock := newFakeClock()
	a := testFamily("blocked-family-a")
	b := testFamily("progress-family-b")
	aEntered := make(chan struct{})
	releaseA := make(chan struct{})
	bLaunched := make(chan LaunchRequest, 1)
	manager, err := NewManager(ManagerConfig{
		Clock: clock, IdleTimeout: time.Minute,
		OnLaunch: func(req LaunchRequest) {
			switch req.token.family {
			case a.id:
				close(aEntered)
				<-releaseA
			case b.id:
				bLaunched <- req
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.BeginLaunch(a); err != nil {
		t.Fatal(err)
	}
	<-aEntered
	bToken, err := manager.BeginLaunch(b)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-bLaunched:
		if req.Token() != bToken {
			t.Fatalf("family B launch = %+v, want generation %d", req, bToken.generation)
		}
	case <-time.After(time.Second):
		t.Fatal("family B callback was delayed by blocked family A callback")
	}
	close(releaseA)
}

func TestManagerDropsDelayedStaleLaunchNotification(t *testing.T) {
	clock := newFakeClock()
	family := testFamily("stale-delayed-launch")
	closeEntered := make(chan struct{})
	releaseClose := make(chan struct{})
	launches := make(chan LaunchRequest, 3)
	manager, err := NewManager(ManagerConfig{
		Clock: clock, IdleTimeout: time.Minute,
		OnLaunch: func(req LaunchRequest) { launches <- req },
		OnClose: func(CloseRequest) {
			close(closeEntered)
			<-releaseClose
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	if req := <-launches; req.Token() != first {
		t.Fatalf("first launch = %+v", req)
	}
	if err := manager.MarkReady(family, first); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	<-closeEntered

	record, err := manager.record(family, false)
	if err != nil {
		t.Fatal(err)
	}
	record.mu.Lock()
	closeReq := record.closeRequest
	record.mu.Unlock()
	manager.notifyLaunch(LaunchRequest{token: first})
	if err := manager.MarkCloseCommitted(family, closeReq); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileClose(family, closeReq, true); err != nil {
		t.Fatal(err)
	}
	second, err := manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	close(releaseClose)

	select {
	case req := <-launches:
		if req.Token() != second {
			t.Fatalf("delayed launch callback = generation %d, want current generation %d", req.Generation(), second.generation)
		}
	case <-time.After(time.Second):
		t.Fatal("current generation launch callback was not delivered")
	}
	select {
	case req := <-launches:
		t.Fatalf("stale launch callback was delivered: generation %d", req.Generation())
	default:
	}
}

func TestManagerOnCloseCanCommitReentrantlyAtSentBoundary(t *testing.T) {
	clock := newFakeClock()
	family := testFamily("reentrant-close")
	var manager *Manager
	committed := make(chan error, 1)
	manager, _ = NewManager(ManagerConfig{
		Clock: clock, IdleTimeout: time.Minute,
		OnClose: func(req CloseRequest) {
			committed <- manager.MarkCloseCommitted(family, req)
		},
	})
	token, err := manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.MarkReady(family, token); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	select {
	case err := <-committed:
		if err != nil {
			t.Fatalf("reentrant commit failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reentrant commit deadlocked")
	}
	snapshot, _ := manager.Snapshot(family)
	if snapshot.ClosePhase != CloseCommitted {
		t.Fatalf("reentrant callback boundary = %+v", snapshot)
	}
}

func TestManagerCallbacksRemainOrderedWhenCancelQueuesBeforeClose(t *testing.T) {
	clock := newFakeClock()
	family := testFamily("callback-order")
	launchEntered := make(chan struct{})
	releaseLaunch := make(chan struct{})
	events := make(chan string, 2)
	manager, err := NewManager(ManagerConfig{
		Clock: clock, IdleTimeout: time.Minute,
		OnLaunch: func(LaunchRequest) {
			close(launchEntered)
			<-releaseLaunch
		},
		OnClose:       func(CloseRequest) { events <- "close" },
		OnCancelClose: func(CloseRequest) { events <- "cancel" },
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	<-launchEntered
	if err := manager.MarkReady(family, token); err != nil {
		t.Fatal(err)
	}
	record, err := manager.record(family, false)
	if err != nil {
		t.Fatal(err)
	}
	record.mu.Lock()
	req := manager.reserveCloseLocked(record)
	record.mu.Unlock()

	leaseCh := make(chan *Lease, 1)
	go func() {
		lease, _ := manager.Admit(context.Background(), family, ActionOptions{})
		leaseCh <- lease
	}()
	waitForSnapshot(t, manager, family, func(s Snapshot) bool { return s.QueuedActions == 1 })
	for {
		record.dispatcher.mu.Lock()
		queued := len(record.dispatcher.queue)
		record.dispatcher.mu.Unlock()
		if queued != 0 {
			break
		}
		runtime.Gosched()
	}
	manager.notifyClose(req)
	close(releaseLaunch)

	if first, second := <-events, <-events; first != "close" || second != "cancel" {
		t.Fatalf("callback order = %q, %q", first, second)
	}
	if err := manager.AcknowledgeCloseCancellation(family, req, true); err != nil {
		t.Fatal(err)
	}
	receiveLease(t, leaseCh).Release()
}

func TestManagerShutdownReservesReplacementAfterReversibleClose(t *testing.T) {
	tests := []struct {
		name    string
		recover func(*Manager, FamilyContext, CloseRequest) error
	}{
		{
			name: "cancellation acknowledged",
			recover: func(manager *Manager, family FamilyContext, req CloseRequest) error {
				return manager.AcknowledgeCloseCancellation(family, req, true)
			},
		},
		{
			name: "dispatch failed before commit",
			recover: func(manager *Manager, family FamilyContext, req CloseRequest) error {
				return manager.CloseFailedBeforeCommit(
					family,
					req,
					NewFlexibleError(ErrTransportClosed, "not committed", true, ExecutionNotStarted),
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newManagerHarness(t)
			family := testFamily("shutdown-replacement-" + test.name)
			h.ready(t, family)
			h.clock.Advance(10 * time.Minute)
			original := <-h.close

			done := make(chan error, 1)
			go func() { done <- h.manager.ShutdownFamily(context.Background(), family) }()
			waitForSnapshot(t, h.manager, family, func(s Snapshot) bool {
				return s.ShutdownRequested && s.CloseEpoch == original.Epoch()
			})
			if err := test.recover(h.manager, family, original); err != nil {
				t.Fatal(err)
			}
			replacement := <-h.close
			if replacement.Generation() != original.Generation() ||
				replacement.Epoch() != original.Epoch()+1 {
				t.Fatalf("replacement close = %+v, original = %+v", replacement, original)
			}
			snapshot, _ := h.manager.Snapshot(family)
			if snapshot.State != SessionClosing || snapshot.ClosePhase != CloseSent ||
				snapshot.CloseEpoch != replacement.Epoch() {
				t.Fatalf("replacement close state = %+v", snapshot)
			}
			if err := h.manager.MarkCloseCommitted(family, replacement); err != nil {
				t.Fatal(err)
			}
			if err := h.manager.ReconcileClose(family, replacement, true); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("shutdown hung after replacement close")
			}
		})
	}
}

func TestManagerScopedRepeatedShutdown(t *testing.T) {
	h := newManagerHarness(t)
	a, b := testFamily("shutdown-a"), testFamily("shutdown-b")
	h.ready(t, a)
	h.ready(t, b)
	done := make(chan error, 1)
	go func() { done <- h.manager.ShutdownFamily(context.Background(), a) }()
	req := <-h.close
	if req.Generation() != 1 {
		t.Fatal("shutdown close has wrong generation")
	}
	if err := h.manager.MarkCloseCommitted(a, req); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.ReconcileClose(a, req, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := h.manager.ShutdownFamily(context.Background(), a); err != nil {
		t.Fatalf("repeated family shutdown: %v", err)
	}
	bSnapshot, _ := h.manager.Snapshot(b)
	if bSnapshot.State != SessionReady {
		t.Fatalf("scoped shutdown affected family B: %+v", bSnapshot)
	}
}

func TestManagerShutdownFamilyStopsNotificationLane(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("shutdown-stops-lane")
	h.ready(t, family)
	record, err := h.manager.record(family, false)
	if err != nil {
		t.Fatal(err)
	}
	laneDone := record.dispatcher.done

	done := make(chan error, 1)
	go func() { done <- h.manager.ShutdownFamily(context.Background(), family) }()
	req := <-h.close
	if err := h.manager.MarkCloseCommitted(family, req); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.ReconcileClose(family, req, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-laneDone:
	default:
		t.Fatal("family notification lane still running after successful shutdown")
	}
	if err := h.manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("global shutdown with stopped family lane: %v", err)
	}
	if err := h.manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated global shutdown with stopped family lane: %v", err)
	}
}

func TestManagerShutdownFamilyCallbackTimeoutCanRetry(t *testing.T) {
	clock := newFakeClock()
	family := testFamily("family-shutdown-dispatcher-retry")
	closeRequest := make(chan CloseRequest, 1)
	closeEntered := make(chan struct{})
	releaseClose := make(chan struct{})
	manager, err := NewManager(ManagerConfig{
		Clock: clock, IdleTimeout: time.Minute,
		OnClose: func(req CloseRequest) {
			closeRequest <- req
			close(closeEntered)
			<-releaseClose
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.MarkReady(family, token); err != nil {
		t.Fatal(err)
	}
	record, err := manager.record(family, false)
	if err != nil {
		t.Fatal(err)
	}
	laneDone := record.dispatcher.done

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelFirst()
	firstDone := make(chan error, 1)
	go func() { firstDone <- manager.ShutdownFamily(firstCtx, family) }()
	req := <-closeRequest
	<-closeEntered
	if err := manager.MarkCloseCommitted(family, req); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileClose(family, req, true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first family shutdown error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("family shutdown did not honor context timeout")
	}
	select {
	case <-laneDone:
		t.Fatal("family notification lane exited while callback remained blocked")
	default:
	}

	retryDone := make(chan error, 1)
	go func() { retryDone <- manager.ShutdownFamily(context.Background(), family) }()
	select {
	case err := <-retryDone:
		t.Fatalf("family shutdown retry returned while callback remained blocked: %v", err)
	default:
	}
	close(releaseClose)
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("family shutdown retry did not finish after callback release")
	}
	select {
	case <-laneDone:
	case <-time.After(time.Second):
		t.Fatal("family notification lane did not exit after retry")
	}
}

func TestManagerBeginLaunchRejectsShutdownFamily(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("shutdown-rejects-launch")
	h.ready(t, family)
	done := make(chan error, 1)
	go func() { done <- h.manager.ShutdownFamily(context.Background(), family) }()
	req := <-h.close
	if _, err := h.manager.BeginLaunch(family); errorCode(err) != ErrSessionClosing {
		t.Fatalf("begin launch during family shutdown error = %v, want %s", err, ErrSessionClosing)
	}
	select {
	case req := <-h.launch:
		t.Fatalf("begin launch during family shutdown emitted launch: %+v", req)
	default:
	}
	if err := h.manager.MarkCloseCommitted(family, req); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.ReconcileClose(family, req, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if _, err := h.manager.BeginLaunch(family); errorCode(err) != ErrSessionClosed {
		t.Fatalf("begin launch after family shutdown error = %v, want %s", err, ErrSessionClosed)
	}
	select {
	case req := <-h.launch:
		t.Fatalf("begin launch after family shutdown emitted launch: %+v", req)
	default:
	}
}

func TestManagerRepeatedGlobalShutdown(t *testing.T) {
	h := newManagerHarness(t)
	family := testFamily("global-shutdown")
	h.ready(t, family)
	done := make(chan error, 2)
	go func() { done <- h.manager.Shutdown(context.Background()) }()
	req := <-h.close
	go func() { done <- h.manager.Shutdown(context.Background()) }()
	waitForSnapshot(t, h.manager, family, func(s Snapshot) bool {
		return s.ShutdownRequested && s.State == SessionClosing
	})
	if err := h.manager.MarkCloseCommitted(family, req); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.ReconcileClose(family, req, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := h.manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("completed shutdown was not idempotent: %v", err)
	}
	select {
	case duplicate := <-h.close:
		t.Fatalf("repeated shutdown emitted duplicate close: %+v", duplicate)
	default:
	}
}

func TestManagerShutdownCallersHaveIndependentContextsAndCanRetry(t *testing.T) {
	clock := newFakeClock()
	family := testFamily("shutdown-dispatcher-retry")
	closeEntered := make(chan struct{})
	releaseClose := make(chan struct{})
	manager, err := NewManager(ManagerConfig{
		Clock: clock, IdleTimeout: time.Minute,
		OnClose: func(CloseRequest) {
			close(closeEntered)
			<-releaseClose
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.BeginLaunch(family)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.MarkReady(family, token); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	<-closeEntered

	record, err := manager.record(family, false)
	if err != nil {
		t.Fatal(err)
	}
	record.mu.Lock()
	req := record.closeRequest
	laneDone := record.dispatcher.done
	record.mu.Unlock()
	if err := manager.MarkCloseCommitted(family, req); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileClose(family, req, true); err != nil {
		t.Fatal(err)
	}

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelFirst()
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- manager.Shutdown(firstCtx) }()
	go func() { secondDone <- manager.Shutdown(context.Background()) }()
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first shutdown error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed-out shutdown caller did not honor its own context")
	}
	select {
	case err := <-secondDone:
		t.Fatalf("independent shutdown returned while callback remained blocked: %v", err)
	default:
	}
	select {
	case <-laneDone:
		t.Fatal("family callback lane exited before blocked callback was released")
	default:
	}

	close(releaseClose)
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after callback release")
	}
	select {
	case <-laneDone:
	case <-time.After(time.Second):
		t.Fatal("family callback worker did not exit after successful shutdown")
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown retry failed: %v", err)
	}
}

func errorCode(err error) ErrorCode {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}
