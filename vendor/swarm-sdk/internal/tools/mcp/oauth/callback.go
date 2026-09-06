package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// CallbackServer handles OAuth redirect callbacks on localhost.
type CallbackServer struct {
	port       int
	server     *http.Server
	listener   net.Listener
	resultChan chan *CallbackResult
	mu         sync.Mutex
	started    bool
}

// NewCallbackServer creates a new callback server.
// If port is 0, an available port will be automatically selected.
func NewCallbackServer(port int) (*CallbackServer, error) {
	// Find an available port if not specified
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	// Get the actual port (useful when port was 0)
	actualPort := listener.Addr().(*net.TCPAddr).Port

	cs := &CallbackServer{
		port:       actualPort,
		listener:   listener,
		resultChan: make(chan *CallbackResult, 1),
	}

	return cs, nil
}

// Port returns the port the server is listening on.
func (cs *CallbackServer) Port() int {
	return cs.port
}

// RedirectURI returns the full redirect URI for OAuth configuration.
func (cs *CallbackServer) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", cs.port)
}

// Start starts the callback server.
func (cs *CallbackServer) Start() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.started {
		return fmt.Errorf("server already started")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handleCallback)

	cs.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	cs.started = true

	// Start serving in a goroutine
	go func() {
		if err := cs.server.Serve(cs.listener); err != nil && err != http.ErrServerClosed {
			// Send error through channel
			select {
			case cs.resultChan <- &CallbackResult{Error: err.Error()}:
			default:
			}
		}
	}()

	return nil
}

// handleCallback processes the OAuth callback.
func (cs *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	result := &CallbackResult{
		Code:  query.Get("code"),
		State: query.Get("state"),
		Error: query.Get("error"),
	}

	// If there's an error_description, append it
	if errDesc := query.Get("error_description"); errDesc != "" && result.Error != "" {
		result.Error = result.Error + ": " + errDesc
	}

	// Send result (non-blocking)
	select {
	case cs.resultChan <- result:
	default:
		// Channel full, result already sent
	}

	// Respond with a nice HTML page
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
</body>
</html>`)
}

// WaitForCallback waits for the OAuth callback with a timeout.
// Returns the authorization code and state, or an error.
func (cs *CallbackServer) WaitForCallback(ctx context.Context) (*CallbackResult, error) {
	select {
	case result := <-cs.resultChan:
		if result.Error != "" {
			return nil, fmt.Errorf("authorization failed: %s", result.Error)
		}
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Stop gracefully shuts down the callback server.
func (cs *CallbackServer) Stop() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if !cs.started || cs.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cs.started = false
	return cs.server.Shutdown(ctx)
}

// RunCallbackFlow is a convenience function that runs the complete callback flow:
// 1. Creates and starts a callback server
// 2. Returns the redirect URI
// 3. Waits for the callback
// 4. Cleans up the server
func RunCallbackFlow(ctx context.Context) (redirectURI string, waitFn func() (*CallbackResult, error), cleanup func(), err error) {
	server, err := NewCallbackServer(0)
	if err != nil {
		return "", nil, nil, err
	}

	if err := server.Start(); err != nil {
		return "", nil, nil, err
	}

	redirectURI = server.RedirectURI()

	waitFn = func() (*CallbackResult, error) {
		return server.WaitForCallback(ctx)
	}

	cleanup = func() {
		server.Stop()
	}

	return redirectURI, waitFn, cleanup, nil
}
