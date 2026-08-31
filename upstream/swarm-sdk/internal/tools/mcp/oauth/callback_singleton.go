package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// SingletonCallbackServer is a shared callback server for multiple OAuth flows.
// It uses state parameter routing to direct callbacks to the correct flow.
// Inspired by OpenCode's singleton callback server design.
type SingletonCallbackServer struct {
	port     int
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	started  bool

	// Map of state -> result channel
	pendingAuths map[string]chan *CallbackResult
	authMu       sync.RWMutex
}

var (
	globalCallbackServer   *SingletonCallbackServer
	globalCallbackServerMu sync.Mutex
)

// DefaultCallbackPort is the default port for the singleton callback server.
// Using 19876 to match OpenCode's convention.
const DefaultCallbackPort = 19876

// GetSingletonCallbackServer returns the global singleton callback server.
// It creates and starts the server on first call.
func GetSingletonCallbackServer(port int) (*SingletonCallbackServer, error) {
	globalCallbackServerMu.Lock()
	defer globalCallbackServerMu.Unlock()

	if globalCallbackServer != nil {
		return globalCallbackServer, nil
	}

	if port == 0 {
		port = DefaultCallbackPort
	}

	server, err := newSingletonCallbackServer(port)
	if err != nil {
		return nil, err
	}

	if err := server.Start(); err != nil {
		return nil, err
	}

	globalCallbackServer = server
	return globalCallbackServer, nil
}

// newSingletonCallbackServer creates a new singleton callback server.
func newSingletonCallbackServer(port int) (*SingletonCallbackServer, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port

	return &SingletonCallbackServer{
		port:         actualPort,
		listener:     listener,
		pendingAuths: make(map[string]chan *CallbackResult),
	}, nil
}

// Port returns the port the server is listening on.
func (s *SingletonCallbackServer) Port() int {
	return s.port
}

// RedirectURI returns the full redirect URI for OAuth configuration.
func (s *SingletonCallbackServer) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", s.port)
}

// Start starts the singleton callback server.
func (s *SingletonCallbackServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil // Already started
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	s.started = true

	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash - the server will be non-functional
			// but we don't want to panic
		}
	}()

	return nil
}

// handleCallback processes OAuth callbacks and routes them by state parameter.
func (s *SingletonCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	state := query.Get("state")
	if state == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>OAuth Error</title></head>
<body style="font-family: system-ui, sans-serif; text-align: center; padding: 50px;">
<h1>❌ Missing State Parameter</h1>
<p>Invalid OAuth callback - state parameter is required for security.</p>
<p>You can close this window.</p>
</body>
</html>`)
		return
	}

	result := &CallbackResult{
		Code:  query.Get("code"),
		State: state,
		Error: query.Get("error"),
	}

	if errDesc := query.Get("error_description"); errDesc != "" && result.Error != "" {
		result.Error = result.Error + ": " + errDesc
	}

	// Find the pending auth for this state
	s.authMu.RLock()
	resultChan, ok := s.pendingAuths[state]
	s.authMu.RUnlock()

	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>OAuth Error</title></head>
<body style="font-family: system-ui, sans-serif; text-align: center; padding: 50px;">
<h1>❌ Invalid or Expired State</h1>
<p>This OAuth flow has expired or the state parameter is invalid.</p>
<p>Please try authenticating again.</p>
<p>You can close this window.</p>
</body>
</html>`)
		return
	}

	// Send result (non-blocking)
	select {
	case resultChan <- result:
	default:
		// Channel full or closed
	}

	// Clean up
	s.authMu.Lock()
	delete(s.pendingAuths, state)
	s.authMu.Unlock()

	// Respond with HTML
	if result.Error != "" {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Authorization Failed</title></head>
<body style="font-family: system-ui, sans-serif; text-align: center; padding: 50px;">
<h1>❌ Authorization Failed</h1>
<p>%s</p>
<p>You can close this window.</p>
</body>
</html>`, result.Error)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Authorization Successful</title></head>
<body style="font-family: system-ui, sans-serif; text-align: center; padding: 50px;">
<h1>✅ Authorization Successful</h1>
<p>You have been authenticated. You can close this window and return to SwarmOS.</p>
<script>setTimeout(() => window.close(), 2000);</script>
</body>
</html>`)
}

// RegisterPendingAuth registers a pending OAuth flow by state.
// Returns a channel that will receive the callback result.
func (s *SingletonCallbackServer) RegisterPendingAuth(state string) <-chan *CallbackResult {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	resultChan := make(chan *CallbackResult, 1)
	s.pendingAuths[state] = resultChan
	return resultChan
}

// UnregisterPendingAuth removes a pending OAuth flow.
func (s *SingletonCallbackServer) UnregisterPendingAuth(state string) {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	if ch, ok := s.pendingAuths[state]; ok {
		close(ch)
		delete(s.pendingAuths, state)
	}
}

// Stop gracefully shuts down the singleton callback server.
func (s *SingletonCallbackServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started || s.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.started = false

	// Close all pending channels
	s.authMu.Lock()
	for state, ch := range s.pendingAuths {
		close(ch)
		delete(s.pendingAuths, state)
	}
	s.authMu.Unlock()

	return s.server.Shutdown(ctx)
}

// IsRunning returns true if the server is started.
func (s *SingletonCallbackServer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}
