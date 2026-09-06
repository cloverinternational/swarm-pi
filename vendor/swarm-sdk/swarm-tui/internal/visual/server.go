package visual

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// QuestionScreen is a pending question rendered to HTML.
type QuestionScreen struct {
	ID      string
	Agent   string
	HTML    string
	CSRF    string
	AddedAt time.Time
}

// ServerConfig drives Server construction.
type ServerConfig struct {
	SessionID string
	Host      string // defaults to 127.0.0.1
	ForcePort int    // 0 = dynamic (test hook)
	AuthToken string // empty = no auth
}

// ClickResult is returned to the waiter on user submission.
type ClickResult struct {
	QuestionID string
	Choices    []string
}

// Server is the per-session visual picker HTTP server.
type Server struct {
	cfg        ServerConfig
	httpSrv    *http.Server
	port       int
	sessionDir string

	mu     sync.Mutex
	closed bool

	questionsMu sync.Mutex
	questions   []QuestionScreen

	screensMu sync.Mutex
	screens   map[string]*Screen
	order     []string
	journal   *Journal

	waitersMu sync.Mutex
	waiters   map[string]chan ClickResult

	frameTpl    *template.Template
	csrfMu      sync.Mutex
	currentCSRF string
}

// NewServer binds a port and starts serving immediately.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.ForcePort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", addr, err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port

	frameBytes, err := templatesFS.ReadFile("templates/frame.html")
	if err != nil {
		return nil, fmt.Errorf("embed frame: %w", err)
	}
	tpl, err := template.New("frame").Parse(string(frameBytes))
	if err != nil {
		return nil, fmt.Errorf("parse frame: %w", err)
	}

	s := &Server{
		cfg:         cfg,
		port:        actualPort,
		frameTpl:    tpl,
		waiters:     map[string]chan ClickResult{},
		screens:     map[string]*Screen{},
		order:       []string{},
		currentCSRF: NewCSRFToken(),
	}
	mux := http.NewServeMux()
	s.register(mux)
	s.httpSrv = &http.Server{
		Handler:           s.authMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = s.httpSrv.Serve(listener) }()
	return s, nil
}

// URL returns http://host:port.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s:%d", s.cfg.Host, s.port)
}

// Port returns the bound port.
func (s *Server) Port() int { return s.port }

// SessionID returns the session identifier.
func (s *Server) SessionID() string { return s.cfg.SessionID }

// Host returns the bound host.
func (s *Server) Host() string { return s.cfg.Host }

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.httpSrv.Shutdown(ctx)
	if s.journal != nil {
		_ = s.journal.Close()
		s.journal = nil
	}
}

// SetSessionDir wires the session directory and opens the journal.
// Called by Registry.Ensure after WriteLockFile.
func (s *Server) SetSessionDir(dir string) error {
	s.sessionDir = dir
	if s.journal != nil {
		return nil
	}
	j, err := OpenJournal(filepath.Join(dir, "screens.jsonl"))
	if err != nil {
		return err
	}
	s.journal = j
	replayed, err := Replay(filepath.Join(dir, "screens.jsonl"))
	if err != nil {
		return err
	}
	s.screensMu.Lock()
	for id, sc := range replayed {
		s.screens[id] = sc
		s.order = append(s.order, id)
	}
	s.screensMu.Unlock()
	return nil
}

// PushQuestion appends a new question as the newest.
func (s *Server) PushQuestion(q QuestionScreen) {
	if q.AddedAt.IsZero() {
		q.AddedAt = time.Now()
	}
	if q.CSRF == "" {
		s.csrfMu.Lock()
		q.CSRF = s.currentCSRF
		s.csrfMu.Unlock()
	}
	s.questionsMu.Lock()
	s.questions = append(s.questions, q)
	s.questionsMu.Unlock()
}

// RemoveQuestion drops a question by ID.
func (s *Server) RemoveQuestion(id string) {
	s.questionsMu.Lock()
	defer s.questionsMu.Unlock()
	filtered := s.questions[:0]
	for _, q := range s.questions {
		if q.ID != id {
			filtered = append(filtered, q)
		}
	}
	s.questions = filtered
}

func (s *Server) newest() (QuestionScreen, bool) {
	s.questionsMu.Lock()
	defer s.questionsMu.Unlock()
	if len(s.questions) == 0 {
		return QuestionScreen{}, false
	}
	return s.questions[len(s.questions)-1], true
}

func (s *Server) findQuestion(id string) (QuestionScreen, bool) {
	s.questionsMu.Lock()
	defer s.questionsMu.Unlock()
	for _, q := range s.questions {
		if q.ID == id {
			return q, true
		}
	}
	return QuestionScreen{}, false
}

// RegisterWaiter subscribes to the click for a question.
func (s *Server) RegisterWaiter(id string, ch chan ClickResult) {
	s.waitersMu.Lock()
	s.waiters[id] = ch
	s.waitersMu.Unlock()
}

// UnregisterWaiter removes the waiter.
func (s *Server) UnregisterWaiter(id string) {
	s.waitersMu.Lock()
	delete(s.waiters, id)
	s.waitersMu.Unlock()
}

func (s *Server) routeClick(res ClickResult) bool {
	s.waitersMu.Lock()
	ch, ok := s.waiters[res.QuestionID]
	if ok {
		delete(s.waiters, res.QuestionID)
	}
	s.waitersMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- res:
		return true
	default:
		return false
	}
}

// RouteClickForTest is an internal testing hook.
func (s *Server) RouteClickForTest(res ClickResult) bool { return s.routeClick(res) }

// SetPlan stores the current plan markdown.
// DEPRECATED: retained as a no-op while callers migrate to PushScreen with
// kind=plan. Remove once TUI no longer calls it.
func (s *Server) SetPlan(md string) {
	_ = md
}

func (s *Server) register(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleRoot)
	staticFS, _ := fs.Sub(templatesFS, "templates")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/newest", s.handleNewest)
	mux.HandleFunc("/screen/", s.handleScreen)
	mux.HandleFunc("/click", s.handleClick)
	mux.HandleFunc("/screens", s.handleScreensList)
	mux.HandleFunc("/ack/", s.handleAck)
	mux.HandleFunc("/review/", s.handleReview)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/review/")
	sc := s.getScreen(id)
	if sc == nil || sc.Kind != ScreenKindPlan {
		http.Error(w, "plan not found", 404)
		return
	}
	if sc.CSRF != "" && r.Header.Get("X-Swarm-CSRF") != sc.CSRF {
		http.Error(w, "csrf mismatch", 403)
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.TransitionPlan(id, body.Decision); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.WriteHeader(200)
}

func (s *Server) PushScreen(sc *Screen) {
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = time.Now()
	}
	if sc.CSRF == "" {
		s.csrfMu.Lock()
		sc.CSRF = s.currentCSRF
		s.csrfMu.Unlock()
	}
	if sc.PrimitiveJSON == nil && sc.Primitive != nil {
		if raw, err := json.Marshal(sc.Primitive); err == nil {
			sc.PrimitiveJSON = raw
		}
	}
	// Guarantee a non-nil PrimitiveJSON so the frontend never receives
	// a screen with an undefined .primitive field (which crashes
	// renderPrimitive at "p.kind" because p is undefined).
	if sc.PrimitiveJSON == nil {
		content := sc.Description
		if content == "" {
			content = sc.Title
		}
		if content != "" {
			fallback, _ := json.Marshal(map[string]string{
				"kind":    "markdown",
				"content": content,
			})
			sc.PrimitiveJSON = fallback
		}
	}
	s.screensMu.Lock()
	s.screens[sc.ID] = sc
	s.order = append(s.order, sc.ID)
	s.screensMu.Unlock()
	if s.journal != nil {
		_ = s.journal.AppendCreate(sc)
	}
}

func (s *Server) getScreen(id string) *Screen {
	s.screensMu.Lock()
	defer s.screensMu.Unlock()
	return s.screens[id]
}

func (s *Server) listScreens(kindFilter string) []*Screen {
	s.screensMu.Lock()
	defer s.screensMu.Unlock()
	out := make([]*Screen, 0, len(s.order))
	for _, id := range s.order {
		sc := s.screens[id]
		if sc == nil {
			continue
		}
		if kindFilter != "" && string(sc.Kind) != kindFilter {
			continue
		}
		out = append(out, sc)
	}
	return out
}

func (s *Server) handleScreensList(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	list := s.listScreens(kind)
	out := make([]map[string]any, 0, len(list))
	for _, sc := range list {
		entry := map[string]any{
			"id":             sc.ID,
			"parentScreenID": sc.ParentScreenID,
			"agentID":        sc.AgentID,
			"kind":           sc.Kind,
			"title":          sc.Title,
			"description":    sc.Description,
			"createdAt":      sc.CreatedAt.Unix(),
			"planState":      sc.PlanState,
			"answer":         sc.Answer,
			"usedDefault":    sc.UsedDefault,
		}
		if len(sc.PrimitiveJSON) > 0 {
			entry["primitive"] = json.RawMessage(sc.PrimitiveJSON)
		} else {
			// Fallback: synthesize a markdown primitive so the
			// frontend never sees screen.primitive === undefined.
			content := sc.Description
			if content == "" {
				content = sc.Title
			}
			if content != "" {
				entry["primitive"] = map[string]string{
					"kind":    "markdown",
					"content": content,
				}
			} else {
				entry["primitive"] = map[string]string{
					"kind":    "markdown",
					"content": "(no content)",
				}
			}
		}
		out = append(out, entry)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// ResolveScreen marks a screen as terminal (resolved/timed_out/cancelled) and
// records the answer. Safe to call on an already-terminal screen (no-op).
func (s *Server) ResolveScreen(id string, answer []string, usedDefault bool, kind ScreenKind) {
	s.screensMu.Lock()
	sc := s.screens[id]
	if sc == nil || sc.Kind.IsTerminal() {
		s.screensMu.Unlock()
		return
	}
	sc.Kind = kind
	sc.Answer = answer
	sc.UsedDefault = usedDefault
	now := time.Now()
	sc.ResolvedAt = &now
	s.screensMu.Unlock()
	if s.journal != nil {
		switch kind {
		case ScreenKindResolved:
			_ = s.journal.AppendResolve(id, answer, usedDefault)
		case ScreenKindTimedOut:
			_ = s.journal.AppendTimeout(id, answer, usedDefault)
		case ScreenKindCancelled:
			_ = s.journal.AppendCancel(id)
		}
	}
}

func (s *Server) TransitionPlan(id, decision string) error {
	sc := s.getScreen(id)
	if sc == nil || sc.Kind != ScreenKindPlan {
		return fmt.Errorf("plan not found: %s", id)
	}
	s.screensMu.Lock()
	switch decision {
	case "approve":
		sc.PlanState = PlanStateApproved
	case "reject":
		sc.PlanState = PlanStateRejected
	case "abandon":
		sc.PlanState = PlanStateAbandoned
	}
	s.screensMu.Unlock()
	if s.journal != nil {
		_ = s.journal.AppendReviewPlan(id, decision)
	}
	return nil
}

func (s *Server) handleAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/ack/")
	sc := s.getScreen(id)
	if sc == nil {
		http.Error(w, "not found", 404)
		return
	}
	if sc.CSRF != "" && r.Header.Get("X-Swarm-CSRF") != sc.CSRF {
		http.Error(w, "csrf mismatch", 403)
		return
	}
	s.screensMu.Lock()
	sc.Kind = ScreenKindAcknowledged
	now := time.Now()
	sc.ResolvedAt = &now
	s.screensMu.Unlock()
	if s.journal != nil {
		_ = s.journal.AppendAck(id)
	}
	w.WriteHeader(200)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// CSRF is session-scoped: minted in NewServer so questions pushed before
	// the frame is first loaded are still protected.
	s.csrfMu.Lock()
	token := s.currentCSRF
	s.csrfMu.Unlock()
	data := struct{ CSRF, SessionID, AuthToken string }{
		CSRF:      token,
		SessionID: s.cfg.SessionID,
		AuthToken: s.cfg.AuthToken,
	}
	if err := s.frameTpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessionID": s.cfg.SessionID,
		"port":      s.port,
	})
}

func (s *Server) handleNewest(w http.ResponseWriter, r *http.Request) {
	q, ok := s.newest()
	if !ok {
		w.WriteHeader(204)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"questionID": q.ID, "agent": q.Agent,
	})
}

func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/screen/")
	q, ok := s.findQuestion(id)
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, q.HTML)
}

func (s *Server) handleClick(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		QuestionID string   `json:"questionID"`
		Choices    []string `json:"choices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	q, ok := s.findQuestion(body.QuestionID)
	if !ok {
		// Question is gone from the legacy waiter map. Treat as idempotent:
		// if the screen was already resolved (same id), this is a harmless
		// double-click after a successful submit — respond 200 so the UI
		// doesn't flash a spurious error toast. Only return 410 when there's
		// no record of the question at all.
		if sc := s.getScreen(body.QuestionID); sc != nil && sc.Kind.IsTerminal() {
			w.WriteHeader(200)
			return
		}
		http.Error(w, "gone", 410)
		return
	}
	if q.CSRF != "" {
		if r.Header.Get("X-Swarm-CSRF") != q.CSRF {
			http.Error(w, "csrf mismatch", 403)
			return
		}
	}
	if !s.routeClick(ClickResult{QuestionID: body.QuestionID, Choices: body.Choices}) {
		// Same idempotency check: if the screen is already resolved, this is
		// a stale duplicate click — succeed silently.
		if sc := s.getScreen(body.QuestionID); sc != nil && sc.Kind.IsTerminal() {
			w.WriteHeader(200)
			return
		}
		http.Error(w, "no waiter (cancelled?)", 410)
		return
	}
	s.RemoveQuestion(body.QuestionID)
	w.WriteHeader(200)
}

// Registry manages one Server per session. Safe for concurrent Ensure.
type Registry struct {
	rootDir     string
	defaultHost string
	defaultAuth string

	mu      sync.Mutex
	servers map[string]*Server
}

// NewRegistry creates a Registry rooted at a conversations directory.
// Each session's visual data lives at {rootDir}/{sessionID}/visual/.
func NewRegistry(rootDir string) *Registry {
	return &Registry{rootDir: rootDir, servers: map[string]*Server{}}
}

// NewRegistryWithDefaults stamps every new server with the given host/auth.
func NewRegistryWithDefaults(rootDir, host, authToken string) *Registry {
	r := NewRegistry(rootDir)
	r.defaultHost = host
	r.defaultAuth = authToken
	return r
}

// Ensure returns an existing server or starts one.
func (r *Registry) Ensure(sessionID string) (*Server, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.servers[sessionID]; ok {
		return s, nil
	}

	sessionDir := filepath.Join(r.rootDir, sessionID, "visual")
	lf := LockFilePath(sessionDir)
	// Reap any stale or live prior lock — v1 always starts fresh.
	_ = ReapLockFile(lf)

	srv, err := NewServer(ServerConfig{
		SessionID: sessionID,
		Host:      r.defaultHost,
		AuthToken: r.defaultAuth,
	})
	if err != nil {
		return nil, err
	}

	info := LockInfo{
		Port: srv.Port(), PID: os.Getpid(), SessionID: sessionID, AuthToken: srv.cfg.AuthToken,
	}
	if err := WriteLockFile(lf, info); err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("write lock: %w", err)
	}
	if err := srv.SetSessionDir(sessionDir); err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("open journal: %w", err)
	}
	r.servers[sessionID] = srv
	return srv, nil
}

// ShutdownAll gracefully stops every server the registry owns.
func (r *Registry) ShutdownAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.servers {
		s.Shutdown()
		_ = ReapLockFile(LockFilePath(s.sessionDir))
	}
	r.servers = map[string]*Server{}
}

// authMiddleware checks the auth token when configured.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-Swarm-Auth")
		if token == "" {
			token = r.URL.Query().Get("k")
		}
		if token != s.cfg.AuthToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
