// Package serve — mux.go
//
// Mux is the central method registry + dispatcher.  Transport handlers
// (HTTPHandler, WebSocketHandler, SSEHandler) all funnel through Mux.Dispatch.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/ws"
)

const maxDispatchChildren = 64

// dispatchLimiter bounds transport dispatch children across every Mux that
// uses the package default. A token belongs to the child that acquires it and
// must not be released by a request or connection supervisor.
type dispatchLimiter struct {
	slots         chan struct{}
	beforeAcquire func()
}

func newDispatchLimiter(capacity int) *dispatchLimiter {
	return &dispatchLimiter{slots: make(chan struct{}, capacity)}
}

func (l *dispatchLimiter) acquire(ctx context.Context) bool {
	if l.beforeAcquire != nil {
		l.beforeAcquire()
	}
	select {
	case l.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *dispatchLimiter) release() {
	<-l.slots
}

var defaultDispatchLimiter = newDispatchLimiter(maxDispatchChildren)

// Mux dispatches JSON-RPC method calls to *client.Client.
//
// Mux is goroutine-safe: Register and Dispatch may be called concurrently.
// However, callers should typically register all built-in + custom methods
// before serving any traffic.
type Mux struct {
	client *client.Client

	mu      sync.RWMutex
	methods map[string]Method

	auth       AuthFunc
	wsUpgrader *ws.ServerUpgrader

	// dispatchLimit is package-private so tests can inject a deliberately
	// small per-instance budget. Production Muxes all point at the one
	// process-wide default; dispatchLimiter supplies the same default for
	// zero-value and internal literal Muxes.
	dispatchLimit *dispatchLimiter
}

// NewMux returns a Mux backed by c, pre-populated with the built-in client
// method registrations (see client_methods.go).  Callers may add or override
// methods via Register.
func NewMux(c *client.Client) *Mux {
	m := &Mux{
		client:        c,
		methods:       make(map[string]Method),
		dispatchLimit: defaultDispatchLimiter,
	}
	registerBuiltinMethods(m)
	return m
}

func (m *Mux) dispatchLimiter() *dispatchLimiter {
	if m.dispatchLimit == nil {
		return defaultDispatchLimiter
	}
	return m.dispatchLimit
}

// Client returns the *client.Client backing this Mux.
func (m *Mux) Client() *client.Client { return m.client }

// Register adds or replaces an RPC method.  An empty Name or nil Handler
// causes the registration to be silently dropped.
func (m *Mux) Register(method Method) {
	if method.Name == "" || method.Handler == nil {
		return
	}
	m.mu.Lock()
	m.methods[method.Name] = method
	m.mu.Unlock()
}

// Methods returns the names of every registered method (sorted not
// guaranteed).  Useful for diagnostics and reflection.
func (m *Mux) Methods() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.methods))
	for name := range m.methods {
		out = append(out, name)
	}
	return out
}

// Lookup returns the registered method by name and whether it exists.
func (m *Mux) Lookup(name string) (Method, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mth, ok := m.methods[name]
	return mth, ok
}

// ErrMethodMissing is returned by Dispatch when no handler matches the
// supplied method name.  Transports map this to NewError(ErrMethodNotFound,
// ...) to surface a JSON-RPC error.
var ErrMethodMissing = errors.New("method not found")

// Dispatch routes a single (method, params) pair to its handler and returns
// the result.  Unknown methods produce ErrMethodNotFound; handler errors are
// returned verbatim so callers can preserve *ErrorObject codes.
func (m *Mux) Dispatch(ctx context.Context, method string, params json.RawMessage) (result any, err error) {
	mth, ok := m.Lookup(method)
	if !ok {
		return nil, NewError(ErrMethodNotFound, "method not found", method)
	}
	// Recover from a panicking handler so one bad/edge-case request returns a
	// clean JSON-RPC error instead of unwinding through the transport — which
	// would drop a /ws serve connection or hand the mobile PWA a bare HTTP 500.
	// Named returns let the deferred recover set the response.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = NewError(ErrInternalError, "internal error handling "+method, nil)
		}
	}()
	return mth.Handler(ctx, m.client, params)
}

// WithAuth installs an AuthFunc that gates every transport-level request.
// Returns the receiver for fluent configuration.
func (m *Mux) WithAuth(auth AuthFunc) *Mux {
	m.auth = auth
	return m
}
