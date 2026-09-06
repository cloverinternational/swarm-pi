package chrome

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Clock supplies monotonic time and timers. Tests can inject a manual clock.
type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}

type Timer interface{ Stop() bool }

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
func RealClock() Clock                                      { return realClock{} }

// ManagerConfig contains policy and transport-neutral work notifications.
// Callbacks are serialized per family and are never invoked while a manager or
// family lock is held.
type ManagerConfig struct {
	Clock         Clock
	IdleTimeout   time.Duration
	OnLaunch      func(LaunchRequest)
	OnClose       func(CloseRequest)
	OnCancelClose func(CloseRequest)
}

// GenerationToken is manager-issued authority for one family generation.
type GenerationToken struct {
	family     FamilyID
	generation uint64
}

// LaunchRequest contains no browser/window identifiers.
type LaunchRequest struct{ token GenerationToken }

func (r LaunchRequest) Generation() uint64     { return r.token.generation }
func (r LaunchRequest) Token() GenerationToken { return r.token }

// Family returns immutable authority for trusted runtime adapters. It is not
// serializable and must never be exposed through model-facing arguments or
// wire messages.
func (r LaunchRequest) Family() FamilyContext { return FamilyContext{id: r.token.family} }

// CloseRequest binds a close to an exact generation and close epoch.
type CloseRequest struct {
	token GenerationToken
	epoch uint64
}

func (r CloseRequest) Generation() uint64 { return r.token.generation }
func (r CloseRequest) Epoch() uint64      { return r.epoch }

// Family returns immutable authority for trusted runtime adapters. It is not
// serializable and must never be exposed through model-facing arguments or
// wire messages.
func (r CloseRequest) Family() FamilyContext { return FamilyContext{id: r.token.family} }

// Snapshot is an immutable lifecycle/status view.
type Snapshot struct {
	State             SessionState
	ClosePhase        ClosePhase
	Generation        uint64
	CloseEpoch        uint64
	LeaseCount        int
	QueuedActions     int
	LastAgentActivity time.Time
	LastHumanActivity time.Time
	IdleDeadline      time.Time
	Failure           *Error
	Reconciliation    bool
	ShutdownRequested bool
}

type ActionOptions struct {
	// CountsAsActivity is false for status-only and terminal-close calls.
	CountsAsActivity bool
}

type actionWaiter struct {
	ctx      context.Context
	activity bool
	ready    chan actionResult
}

type actionResult struct {
	waiter *actionWaiter
	lease  *Lease
	err    error
}

type familyRecord struct {
	mu sync.Mutex

	id                  FamilyID
	state               SessionState
	phase               ClosePhase
	generation          uint64
	closeEpoch          uint64
	closeRequest        CloseRequest
	closeCancelNotified bool
	reconciliation      bool
	failure             *Error
	lastAgent           time.Time
	lastHuman           time.Time
	idleTimer           Timer
	idleTimerEpoch      uint64
	queue               []*actionWaiter
	active              *Lease
	shutdownRequested   bool
	changed             chan struct{}
	dispatcher          *notificationDispatcher
}

type notificationKind uint8

const (
	notificationLaunch notificationKind = iota
	notificationClose
	notificationCancelClose
)

type notification struct {
	kind   notificationKind
	launch LaunchRequest
	close  CloseRequest
}

type notificationDispatcher struct {
	mu      sync.Mutex
	cond    *sync.Cond
	queue   []notification
	stopped bool
	done    chan struct{}
	manager *Manager
	record  *familyRecord
}

// Manager owns isolated family-local records.
type Manager struct {
	mu       sync.Mutex
	families map[FamilyID]*familyRecord
	clock    Clock
	idle     time.Duration
	onLaunch func(LaunchRequest)
	onClose  func(CloseRequest)
	onCancel func(CloseRequest)
	shutting bool
	closed   bool
}

func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Clock == nil {
		cfg.Clock = RealClock()
	}
	if cfg.IdleTimeout <= 0 {
		return nil, fmt.Errorf("chrome: idle timeout must be positive")
	}
	m := &Manager{
		families: make(map[FamilyID]*familyRecord),
		clock:    cfg.Clock, idle: cfg.IdleTimeout,
		onLaunch: cfg.OnLaunch, onClose: cfg.OnClose, onCancel: cfg.OnCancelClose,
	}
	return m, nil
}

func newNotificationDispatcher(m *Manager, r *familyRecord) *notificationDispatcher {
	d := &notificationDispatcher{done: make(chan struct{}), manager: m, record: r}
	d.cond = sync.NewCond(&d.mu)
	go d.run()
	return d
}

func (d *notificationDispatcher) enqueue(n notification) {
	d.mu.Lock()
	if !d.stopped {
		d.queue = append(d.queue, n)
		d.cond.Signal()
	}
	d.mu.Unlock()
}

func (d *notificationDispatcher) run() {
	defer close(d.done)
	for {
		d.mu.Lock()
		for len(d.queue) == 0 && !d.stopped {
			d.cond.Wait()
		}
		if len(d.queue) == 0 {
			d.mu.Unlock()
			return
		}
		n := d.queue[0]
		d.queue[0] = notification{}
		d.queue = d.queue[1:]
		d.mu.Unlock()
		d.manager.dispatch(d.record, n)
	}
}

func (d *notificationDispatcher) requestStop() {
	d.mu.Lock()
	d.stopped = true
	d.cond.Broadcast()
	d.mu.Unlock()
}

func (d *notificationDispatcher) wait(ctx context.Context) error {
	select {
	case <-d.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) record(family FamilyContext, create bool) (*familyRecord, error) {
	if m == nil || !family.Valid() {
		return nil, ErrBrowserFamilyAuthority
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, NewError(ErrSessionClosed, "Chrome runtime manager is shut down.")
	}
	r := m.families[family.id]
	if m.shutting && (create || r == nil) {
		return nil, NewError(ErrSessionClosing, "Chrome runtime manager is shutting down.")
	}
	if r == nil && create {
		now := m.clock.Now()
		r = &familyRecord{id: family.id, state: SessionUninitialized, lastAgent: now, lastHuman: now, changed: make(chan struct{})}
		r.dispatcher = newNotificationDispatcher(m, r)
		m.families[family.id] = r
	}
	if r == nil {
		return nil, NewError(ErrSessionClosed, "Browser family is not initialized.")
	}
	return r, nil
}

func (m *Manager) EnsureFamily(family FamilyContext) (Snapshot, error) {
	r, err := m.record(family, true)
	if err != nil {
		return Snapshot{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return m.snapshotLocked(r), nil
}

// RestoreLaunching installs restart-safe generation state without asserting
// that a persisted claim is live. Trusted runtime reconciliation must prove
// the exact extension claim before calling MarkReady. No launch callback is
// emitted.
func (m *Manager) RestoreLaunching(family FamilyContext, generation uint64) (GenerationToken, error) {
	if generation == 0 {
		return GenerationToken{}, NewError(ErrInvalidArguments, "Restored browser generation must be positive.")
	}
	r, err := m.record(family, true)
	if err != nil {
		return GenerationToken{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shutdownRequested {
		return GenerationToken{}, NewError(ErrSessionClosing, "Browser family is shutting down.")
	}
	if r.state == SessionLaunching && r.generation == generation {
		return GenerationToken{family: r.id, generation: generation}, nil
	}
	if r.state != SessionUninitialized || r.generation != 0 {
		return GenerationToken{}, NewError(ErrInvalidArguments, "Browser family already has runtime state.")
	}
	m.stopTimerLocked(r)
	r.generation = generation
	r.state, r.phase = SessionLaunching, CloseNone
	r.closeRequest = CloseRequest{}
	r.closeCancelNotified, r.reconciliation = false, true
	r.failure = nil
	m.signalLocked(r)
	return GenerationToken{family: r.id, generation: generation}, nil
}

// RestoreClosing installs a persisted close without making the generation
// ready. It is reserved for trusted runtime recovery and emits no callback.
func (m *Manager) RestoreClosing(family FamilyContext, generation, closeEpoch uint64, phase ClosePhase) (GenerationToken, CloseRequest, error) {
	if generation == 0 || closeEpoch == 0 || (phase != CloseSent && phase != CloseCommitted) {
		return GenerationToken{}, CloseRequest{}, NewError(ErrInvalidArguments, "Restored close metadata is invalid.")
	}
	r, err := m.record(family, true)
	if err != nil {
		return GenerationToken{}, CloseRequest{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != SessionUninitialized || r.generation != 0 {
		return GenerationToken{}, CloseRequest{}, NewError(ErrInvalidArguments, "Browser family already has runtime state.")
	}
	m.stopTimerLocked(r)
	token := GenerationToken{family: r.id, generation: generation}
	req := CloseRequest{token: token, epoch: closeEpoch}
	r.generation, r.closeEpoch = generation, closeEpoch
	r.state, r.phase, r.closeRequest = SessionClosing, phase, req
	r.closeCancelNotified, r.reconciliation = false, true
	r.failure = nil
	m.signalLocked(r)
	return token, req, nil
}

// BeginLaunch starts a fresh monotonically increasing generation. Repeated
// calls while launching return the same token.
func (m *Manager) BeginLaunch(family FamilyContext) (GenerationToken, error) {
	r, err := m.record(family, true)
	if err != nil {
		return GenerationToken{}, err
	}
	var req LaunchRequest
	notify := false
	r.mu.Lock()
	if r.shutdownRequested {
		code, message := ErrSessionClosing, "Browser family is shutting down."
		if r.state == SessionClosed {
			code, message = ErrSessionClosed, "Browser family is shut down."
		}
		r.mu.Unlock()
		return GenerationToken{}, NewError(code, message)
	}
	switch r.state {
	case SessionUninitialized, SessionClosed, SessionFailed:
		req = m.beginLaunchLocked(r)
		notify = true
	case SessionLaunching:
		req = LaunchRequest{token: GenerationToken{family: r.id, generation: r.generation}}
	default:
		r.mu.Unlock()
		return GenerationToken{}, NewError(ErrInvalidArguments, "Browser family already has a live generation.")
	}
	r.mu.Unlock()
	if notify {
		m.notifyLaunch(req)
	}
	return req.token, nil
}

func (m *Manager) beginLaunchLocked(r *familyRecord) LaunchRequest {
	m.stopTimerLocked(r)
	r.generation++
	r.state, r.phase = SessionLaunching, CloseNone
	r.closeRequest = CloseRequest{}
	r.closeCancelNotified, r.reconciliation = false, false
	r.failure = nil
	m.signalLocked(r)
	return LaunchRequest{token: GenerationToken{family: r.id, generation: r.generation}}
}

func (m *Manager) MarkReady(family FamilyContext, token GenerationToken) error {
	r, err := m.record(family, false)
	if err != nil {
		return err
	}
	var results []actionResult
	var closeReq *CloseRequest
	r.mu.Lock()
	if err = validateGenerationLocked(r, family, token); err == nil {
		if r.state != SessionLaunching {
			err = NewError(ErrInvalidArguments, "Generation is not launching.")
		} else {
			r.state, r.failure, r.reconciliation = SessionReady, nil, false
			m.signalLocked(r)
			if r.shutdownRequested {
				req := m.reserveCloseLocked(r)
				closeReq = &req
			} else {
				results = m.promoteLocked(r)
				m.scheduleIdleLocked(r)
			}
		}
	}
	r.mu.Unlock()
	deliver(results)
	if closeReq != nil {
		m.notifyClose(*closeReq)
	}
	return err
}

func (m *Manager) MarkLaunchFailed(family FamilyContext, token GenerationToken, cause *Error) error {
	if cause == nil {
		return NewError(ErrInvalidArguments, "Launch failure cause is required.")
	}
	r, err := m.record(family, false)
	if err != nil {
		return err
	}
	var results []actionResult
	r.mu.Lock()
	if err := validateGenerationLocked(r, family, token); err != nil {
		r.mu.Unlock()
		return err
	}
	if r.state != SessionLaunching {
		r.mu.Unlock()
		return NewError(ErrInvalidArguments, "Generation is not launching.")
	}
	if r.shutdownRequested {
		r.state, r.failure = SessionClosed, cloneError(cause)
	} else {
		r.state, r.failure = SessionFailed, cloneError(cause)
	}
	results = m.failQueueLocked(r, cloneError(cause))
	m.signalLocked(r)
	r.mu.Unlock()
	deliver(results)
	return nil
}

func (m *Manager) Snapshot(family FamilyContext) (Snapshot, error) {
	r, err := m.record(family, false)
	if err != nil {
		return Snapshot{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return m.snapshotLocked(r), nil
}

func (m *Manager) snapshotLocked(r *familyRecord) Snapshot {
	s := Snapshot{
		State: r.state, ClosePhase: r.phase, Generation: r.generation, CloseEpoch: r.closeEpoch,
		QueuedActions: len(r.queue), LastAgentActivity: r.lastAgent, LastHumanActivity: r.lastHuman,
		Failure: cloneError(r.failure), Reconciliation: r.reconciliation, ShutdownRequested: r.shutdownRequested,
	}
	if r.active != nil {
		s.LeaseCount = 1
	}
	if r.state == SessionReady && r.active == nil {
		s.IdleDeadline = maxTime(r.lastAgent, r.lastHuman).Add(m.idle)
	}
	return s
}

// Admit queues an action FIFO and waits without holding manager locks.
func (m *Manager) Admit(ctx context.Context, family FamilyContext, opts ActionOptions) (*Lease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := m.record(family, false)
	if err != nil {
		return nil, err
	}
	w := &actionWaiter{ctx: ctx, activity: opts.CountsAsActivity, ready: make(chan actionResult, 1)}
	var cancel *CloseRequest
	var launch *LaunchRequest
	r.mu.Lock()
	if r.shutdownRequested {
		r.mu.Unlock()
		return nil, NewError(ErrSessionClosing, "Browser family is shutting down.")
	}
	if opts.CountsAsActivity {
		r.lastAgent = m.clock.Now()
	}
	r.queue = append(r.queue, w)
	m.stopTimerLocked(r)
	if r.state == SessionUninitialized || r.state == SessionClosed || r.state == SessionFailed {
		req := m.beginLaunchLocked(r)
		launch = &req
	}
	if r.state == SessionClosing && r.phase != CloseCommitted && !r.closeCancelNotified {
		r.closeCancelNotified = true
		req := r.closeRequest
		cancel = &req
	}
	results := m.promoteLocked(r)
	m.signalLocked(r)
	r.mu.Unlock()
	deliver(results)
	if launch != nil {
		m.notifyLaunch(*launch)
	}
	if cancel != nil {
		m.notifyCancel(*cancel)
	}

	select {
	case result := <-w.ready:
		return result.lease, result.err
	case <-ctx.Done():
		r.mu.Lock()
		removed := removeWaiterLocked(r, w)
		r.mu.Unlock()
		if removed {
			return nil, cancelledQueued()
		}
		result := <-w.ready
		return result.lease, result.err
	}
}

// Lease serializes actions within a family and is safe to release repeatedly.
type Lease struct {
	manager    *Manager
	record     *familyRecord
	generation uint64
	activity   bool
	once       sync.Once
}

func (l *Lease) Generation() uint64 {
	if l == nil {
		return 0
	}
	return l.generation
}

func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		r := l.record
		var closeReq *CloseRequest
		var results []actionResult
		r.mu.Lock()
		if r.active == l {
			r.active = nil
			if l.activity {
				r.lastAgent = l.manager.clock.Now()
			}
			if r.state == SessionActive {
				r.state = SessionReady
			}
			if r.shutdownRequested {
				req := l.manager.reserveCloseLocked(r)
				closeReq = &req
			} else {
				results = l.manager.promoteLocked(r)
				l.manager.scheduleIdleLocked(r)
			}
			l.manager.signalLocked(r)
		}
		r.mu.Unlock()
		deliver(results)
		if closeReq != nil {
			l.manager.notifyClose(*closeReq)
		}
	})
}

// RecordTrustedHumanActivity is reserved for bridge-verified physical input or
// events carrying recent trusted-input provenance.
func (m *Manager) RecordTrustedHumanActivity(family FamilyContext, token GenerationToken) error {
	r, err := m.record(family, false)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateGenerationLocked(r, family, token); err != nil {
		return err
	}
	if r.state != SessionReady && r.state != SessionActive {
		return NewError(ErrSessionClosing, "Browser generation cannot accept activity.")
	}
	r.lastHuman = m.clock.Now()
	if r.state == SessionReady {
		m.scheduleIdleLocked(r)
	}
	m.signalLocked(r)
	return nil
}

// MarkCloseAccepted records a normal ACK; it remains reversible.
func (m *Manager) MarkCloseAccepted(family FamilyContext, req CloseRequest) error {
	return m.updateClose(family, req, func(r *familyRecord) error {
		if r.phase != CloseSent {
			return NewError(ErrInvalidArguments, "Close request has not been sent.")
		}
		return nil
	})
}

func (m *Manager) MarkCloseCommitted(family FamilyContext, req CloseRequest) error {
	return m.updateClose(family, req, func(r *familyRecord) error {
		if r.phase != CloseSent {
			return NewError(ErrInvalidArguments, "Close request is not awaiting commit.")
		}
		r.phase, r.reconciliation = CloseCommitted, false
		return nil
	})
}

func (m *Manager) AcknowledgeCloseCancellation(family FamilyContext, req CloseRequest, cancelled bool) error {
	r, err := m.record(family, false)
	if err != nil {
		return err
	}
	var results []actionResult
	var closeReq *CloseRequest
	r.mu.Lock()
	err = validateCloseLocked(r, family, req)
	if err == nil {
		if r.phase == CloseCommitted {
			err = NewError(ErrSessionClosing, "Close is already committed.")
		} else if cancelled {
			r.state, r.phase = SessionReady, CloseNone
			r.closeRequest = CloseRequest{}
			r.closeCancelNotified = false
			if r.shutdownRequested {
				replacement := m.reserveCloseLocked(r)
				closeReq = &replacement
			} else {
				results = m.promoteLocked(r)
				m.scheduleIdleLocked(r)
				m.signalLocked(r)
			}
		}
	}
	r.mu.Unlock()
	deliver(results)
	if closeReq != nil {
		m.notifyClose(*closeReq)
	}
	return err
}

// CloseFailedBeforeCommit proves no durable close intent exists.
func (m *Manager) CloseFailedBeforeCommit(family FamilyContext, req CloseRequest, cause *Error) error {
	r, err := m.record(family, false)
	if err != nil {
		return err
	}
	var results []actionResult
	var closeReq *CloseRequest
	r.mu.Lock()
	err = validateCloseLocked(r, family, req)
	if err == nil {
		if r.phase == CloseCommitted {
			err = NewError(ErrExecutionIndeterminate, "Committed close requires reconciliation.")
		} else {
			r.state, r.phase = SessionReady, CloseNone
			r.closeRequest = CloseRequest{}
			r.closeCancelNotified = false
			r.failure = cloneError(cause)
			if r.shutdownRequested {
				replacement := m.reserveCloseLocked(r)
				closeReq = &replacement
			} else {
				results = m.promoteLocked(r)
				m.scheduleIdleLocked(r)
				m.signalLocked(r)
			}
		}
	}
	r.mu.Unlock()
	deliver(results)
	if closeReq != nil {
		m.notifyClose(*closeReq)
	}
	return err
}

// CloseUncertainAfterCommit leaves waiters blocked pending reconciliation.
func (m *Manager) CloseUncertainAfterCommit(family FamilyContext, req CloseRequest, cause *Error) error {
	return m.updateClose(family, req, func(r *familyRecord) error {
		if r.phase != CloseCommitted {
			return NewError(ErrInvalidArguments, "Close uncertainty occurred before commit.")
		}
		r.reconciliation, r.failure = true, cloneError(cause)
		return nil
	})
}

// ReconcileClose requires the exact manager-issued generation+epoch token.
func (m *Manager) ReconcileClose(family FamilyContext, req CloseRequest, windowAbsent bool) error {
	r, err := m.record(family, false)
	if err != nil {
		return err
	}
	var launch *LaunchRequest
	var results []actionResult
	r.mu.Lock()
	err = validateCloseLocked(r, family, req)
	if err == nil {
		if r.phase != CloseCommitted {
			err = NewError(ErrInvalidArguments, "Only a committed close can be reconciled.")
		} else if !windowAbsent {
			r.reconciliation = true
			m.signalLocked(r)
		} else {
			r.state, r.phase = SessionClosed, CloseNone
			r.closeRequest = CloseRequest{}
			r.reconciliation, r.failure = false, nil
			m.signalLocked(r)
			if len(r.queue) != 0 && !r.shutdownRequested {
				lr := m.beginLaunchLocked(r)
				launch = &lr
			} else {
				results = m.failQueueLocked(r, NewError(ErrSessionClosed, "Browser generation closed."))
			}
		}
	}
	r.mu.Unlock()
	deliver(results)
	if launch != nil {
		m.notifyLaunch(*launch)
	}
	return err
}

// MarkWindowAbsent handles an independently observed owned-window removal.
func (m *Manager) MarkWindowAbsent(family FamilyContext, token GenerationToken) error {
	r, err := m.record(family, false)
	if err != nil {
		return err
	}
	var launch *LaunchRequest
	var results []actionResult
	r.mu.Lock()
	if err := validateGenerationLocked(r, family, token); err != nil {
		r.mu.Unlock()
		return err
	}
	r.state, r.phase = SessionClosed, CloseNone
	r.closeRequest, r.reconciliation = CloseRequest{}, false
	m.stopTimerLocked(r)
	m.signalLocked(r)
	if len(r.queue) != 0 && !r.shutdownRequested {
		lr := m.beginLaunchLocked(r)
		launch = &lr
	} else {
		results = m.failQueueLocked(r, NewError(ErrSessionClosed, "Browser window closed."))
	}
	r.mu.Unlock()
	deliver(results)
	if launch != nil {
		m.notifyLaunch(*launch)
	}
	return nil
}

// ShutdownFamily performs an idempotent family-scoped graceful close.
func (m *Manager) ShutdownFamily(ctx context.Context, family FamilyContext) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := m.record(family, false)
	if err != nil {
		var ce *Error
		if errors.As(err, &ce) && ce.Code == ErrSessionClosed {
			return nil
		}
		return err
	}
	var req *CloseRequest
	var results []actionResult
	r.mu.Lock()
	r.shutdownRequested = true
	m.stopTimerLocked(r)
	results = m.failQueueLocked(r, NewError(ErrSessionClosing, "Browser family is shutting down."))
	switch r.state {
	case SessionUninitialized, SessionClosed, SessionFailed:
		r.state = SessionClosed
		m.signalLocked(r)
	case SessionReady:
		if r.active == nil {
			cr := m.reserveCloseLocked(r)
			req = &cr
		}
	}
	r.mu.Unlock()
	deliver(results)
	if req != nil {
		m.notifyClose(*req)
	}
	if err := waitClosed(ctx, r); err != nil {
		return err
	}
	r.dispatcher.requestStop()
	return r.dispatcher.wait(ctx)
}

// Shutdown closes each known family and drains its callback lane. A caller that
// times out can retry; concurrent callers independently wait on their contexts.
func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.shutting = true
	records := make([]*familyRecord, 0, len(m.families))
	for _, r := range m.families {
		records = append(records, r)
	}
	m.mu.Unlock()
	for _, r := range records {
		var req *CloseRequest
		var results []actionResult
		r.mu.Lock()
		r.shutdownRequested = true
		m.stopTimerLocked(r)
		results = m.failQueueLocked(r, NewError(ErrSessionClosing, "Chrome runtime is shutting down."))
		if r.state == SessionReady && r.active == nil {
			cr := m.reserveCloseLocked(r)
			req = &cr
		} else if r.state == SessionUninitialized || r.state == SessionFailed {
			r.state = SessionClosed
			m.signalLocked(r)
		}
		r.mu.Unlock()
		deliver(results)
		if req != nil {
			m.notifyClose(*req)
		}
	}
	for _, r := range records {
		if err := waitClosed(ctx, r); err != nil {
			return err
		}
	}
	for _, r := range records {
		r.dispatcher.requestStop()
	}
	for _, r := range records {
		if err := r.dispatcher.wait(ctx); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.closed = true
	m.shutting = false
	m.mu.Unlock()
	return nil
}

func waitClosed(ctx context.Context, r *familyRecord) error {
	for {
		r.mu.Lock()
		if r.state == SessionClosed {
			r.mu.Unlock()
			return nil
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *Manager) updateClose(family FamilyContext, req CloseRequest, fn func(*familyRecord) error) error {
	r, err := m.record(family, false)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateCloseLocked(r, family, req); err != nil {
		return err
	}
	if err := fn(r); err != nil {
		return err
	}
	m.signalLocked(r)
	return nil
}

func (m *Manager) reserveCloseLocked(r *familyRecord) CloseRequest {
	m.stopTimerLocked(r)
	r.closeEpoch++
	r.state, r.phase = SessionClosing, CloseReserved
	r.closeRequest = CloseRequest{token: GenerationToken{family: r.id, generation: r.generation}, epoch: r.closeEpoch}
	r.closeCancelNotified, r.reconciliation = false, false
	m.signalLocked(r)
	return r.closeRequest
}

func (m *Manager) notifyClose(req CloseRequest) {
	if r := m.lookupToken(req.token); r != nil {
		r.dispatcher.enqueue(notification{kind: notificationClose, close: req})
	}
}

func (m *Manager) notifyLaunch(req LaunchRequest) {
	if r := m.lookupToken(req.token); r != nil {
		r.dispatcher.enqueue(notification{kind: notificationLaunch, launch: req})
	}
}

func (m *Manager) notifyCancel(req CloseRequest) {
	if r := m.lookupToken(req.token); r != nil {
		r.dispatcher.enqueue(notification{kind: notificationCancelClose, close: req})
	}
}

func (m *Manager) dispatch(r *familyRecord, n notification) {
	switch n.kind {
	case notificationLaunch:
		r.mu.Lock()
		valid := r.id == n.launch.token.family &&
			r.generation == n.launch.token.generation &&
			r.state == SessionLaunching
		r.mu.Unlock()
		if !valid {
			return
		}
		if m.onLaunch != nil {
			m.onLaunch(n.launch)
		}
	case notificationClose:
		r.mu.Lock()
		if r.state != SessionClosing || r.closeRequest != n.close || r.phase != CloseReserved {
			r.mu.Unlock()
			return
		}
		r.phase = CloseSent
		m.signalLocked(r)
		r.mu.Unlock()
		if m.onClose != nil {
			m.onClose(n.close)
		}
	case notificationCancelClose:
		r.mu.Lock()
		valid := r.state == SessionClosing && r.closeRequest == n.close && r.closeCancelNotified &&
			r.phase != CloseCommitted
		sendClose := valid && r.phase == CloseReserved
		if sendClose {
			r.phase = CloseSent
			m.signalLocked(r)
		}
		r.mu.Unlock()
		if sendClose && m.onClose != nil {
			m.onClose(n.close)
		}
		if !valid {
			return
		}
		r.mu.Lock()
		valid = r.state == SessionClosing && r.closeRequest == n.close && r.closeCancelNotified &&
			r.phase != CloseCommitted
		r.mu.Unlock()
		if valid && m.onCancel != nil {
			m.onCancel(n.close)
		}
	}
}

func (m *Manager) lookupToken(token GenerationToken) *familyRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.families[token.family]
}

func (m *Manager) promoteLocked(r *familyRecord) []actionResult {
	if r.state != SessionReady || r.active != nil {
		return nil
	}
	var rejected []actionResult
	for len(r.queue) != 0 {
		w := r.queue[0]
		r.queue = r.queue[1:]
		if w.ctx.Err() != nil {
			rejected = append(rejected, actionResult{waiter: w, err: cancelledQueued()})
			continue
		}
		lease := &Lease{manager: m, record: r, generation: r.generation, activity: w.activity}
		r.active, r.state = lease, SessionActive
		m.signalLocked(r)
		return append(rejected, actionResult{waiter: w, lease: lease})
	}
	m.scheduleIdleLocked(r)
	return rejected
}

func deliver(results []actionResult) {
	for _, result := range results {
		result.waiter.ready <- result
	}
}

func (m *Manager) scheduleIdleLocked(r *familyRecord) {
	m.stopTimerLocked(r)
	if r.state != SessionReady || r.active != nil || len(r.queue) != 0 || r.shutdownRequested {
		return
	}
	deadline := maxTime(r.lastAgent, r.lastHuman).Add(m.idle)
	delay := deadline.Sub(m.clock.Now())
	if delay < 0 {
		delay = 0
	}
	id, generation := r.id, r.generation
	r.idleTimerEpoch++
	timerEpoch := r.idleTimerEpoch
	r.idleTimer = m.clock.AfterFunc(delay, func() { m.idleFired(id, generation, timerEpoch) })
}

func (m *Manager) idleFired(id FamilyID, generation, timerEpoch uint64) {
	m.mu.Lock()
	r := m.families[id]
	m.mu.Unlock()
	if r == nil {
		return
	}
	var req *CloseRequest
	r.mu.Lock()
	if r.generation != generation || r.idleTimerEpoch != timerEpoch {
		r.mu.Unlock()
		return
	}
	r.idleTimer = nil
	if r.state == SessionReady && r.active == nil && len(r.queue) == 0 && !r.shutdownRequested {
		deadline := maxTime(r.lastAgent, r.lastHuman).Add(m.idle)
		if m.clock.Now().Before(deadline) {
			m.scheduleIdleLocked(r)
		} else {
			cr := m.reserveCloseLocked(r)
			req = &cr
		}
	}
	r.mu.Unlock()
	if req != nil {
		m.notifyClose(*req)
	}
}

func (m *Manager) stopTimerLocked(r *familyRecord) {
	r.idleTimerEpoch++
	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
}

func (m *Manager) signalLocked(r *familyRecord) {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (m *Manager) failQueueLocked(r *familyRecord, err error) []actionResult {
	results := make([]actionResult, 0, len(r.queue))
	for _, w := range r.queue {
		results = append(results, actionResult{waiter: w, err: err})
	}
	r.queue = nil
	return results
}

func removeWaiterLocked(r *familyRecord, target *actionWaiter) bool {
	for i, w := range r.queue {
		if w == target {
			copy(r.queue[i:], r.queue[i+1:])
			r.queue = r.queue[:len(r.queue)-1]
			return true
		}
	}
	return false
}

func validateGenerationLocked(r *familyRecord, family FamilyContext, token GenerationToken) error {
	if !family.Valid() || token.family != family.id {
		return ErrBrowserFamilyAuthority
	}
	if token.generation == 0 || token.generation != r.generation {
		return NewError(ErrStaleGeneration, "Browser generation is stale.")
	}
	return nil
}

func validateCloseLocked(r *familyRecord, family FamilyContext, req CloseRequest) error {
	if err := validateGenerationLocked(r, family, req.token); err != nil {
		return err
	}
	if r.state != SessionClosing || req.epoch == 0 || req.epoch != r.closeEpoch || req != r.closeRequest {
		return NewError(ErrStaleGeneration, "Close epoch is stale.")
	}
	return nil
}

func cancelledQueued() error {
	return NewFlexibleError(ErrCancelled, "Browser action was cancelled while queued.", false, ExecutionNotStarted)
}

func cloneError(in *Error) *Error {
	if in == nil {
		return nil
	}
	out := *in
	out.Details = make(map[string]any, len(in.Details))
	for k, v := range in.Details {
		out.Details[k] = v
	}
	return &out
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
