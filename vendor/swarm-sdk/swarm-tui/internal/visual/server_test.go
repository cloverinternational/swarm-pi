package visual

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServer_Health(t *testing.T) {
	srv, err := NewServer(ServerConfig{SessionID: "test-123"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown() })

	resp, err := http.Get(srv.URL() + "/health")
	if err != nil {
		t.Fatalf("health get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["sessionID"] != "test-123" {
		t.Errorf("sessionID = %v", payload["sessionID"])
	}
}

func TestServer_AuthRequired(t *testing.T) {
	srv, err := NewServer(ServerConfig{SessionID: "auth-test", AuthToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown() })

	r, _ := http.Get(srv.URL() + "/health")
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Errorf("status without token = %d, want 401", r.StatusCode)
	}

	r, _ = http.Get(srv.URL() + "/health?k=secret")
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Errorf("status with token = %d, want 200", r.StatusCode)
	}
}

func TestServer_ShutdownReleasesPort(t *testing.T) {
	srv, err := NewServer(ServerConfig{SessionID: "reuse-test"})
	if err != nil {
		t.Fatal(err)
	}
	port := srv.Port()
	srv.Shutdown()

	time.Sleep(100 * time.Millisecond)
	srv2, err := NewServer(ServerConfig{SessionID: "after-shutdown", ForcePort: port})
	if err != nil {
		t.Fatalf("reuse port %d: %v", port, err)
	}
	t.Cleanup(func() { srv2.Shutdown() })
}

func TestServer_NewestEmpty(t *testing.T) {
	srv, _ := NewServer(ServerConfig{SessionID: "s1"})
	t.Cleanup(func() { srv.Shutdown() })
	r, _ := http.Get(srv.URL() + "/newest")
	r.Body.Close()
	if r.StatusCode != 204 {
		t.Errorf("empty status = %d, want 204", r.StatusCode)
	}
}

func TestServer_NewestAfterPush(t *testing.T) {
	srv, _ := NewServer(ServerConfig{SessionID: "s2"})
	t.Cleanup(func() { srv.Shutdown() })

	srv.PushQuestion(QuestionScreen{ID: "q-1", Agent: "architect", HTML: `<div>q1</div>`})
	r, err := http.Get(srv.URL() + "/newest")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	if payload["questionID"] != "q-1" {
		t.Errorf("questionID = %v", payload["questionID"])
	}
}

func TestServer_ScreenReturnsHTML(t *testing.T) {
	srv, _ := NewServer(ServerConfig{SessionID: "s3"})
	t.Cleanup(func() { srv.Shutdown() })

	srv.PushQuestion(QuestionScreen{ID: "q-x", HTML: `<div>hi</div>`})
	r, err := http.Get(srv.URL() + "/screen/q-x")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), "<div>hi</div>") {
		t.Errorf("got %q", body)
	}
}

func TestServer_ClickRoutesToWaiter(t *testing.T) {
	srv, _ := NewServer(ServerConfig{SessionID: "cl1"})
	t.Cleanup(func() { srv.Shutdown() })

	respCh := make(chan ClickResult, 1)
	srv.RegisterWaiter("q-1", respCh)
	srv.PushQuestion(QuestionScreen{ID: "q-1", HTML: "<div></div>", CSRF: "tok"})

	body := strings.NewReader(`{"questionID":"q-1","choices":["a"]}`)
	req, _ := http.NewRequest("POST", srv.URL()+"/click", body)
	req.Header.Set("X-Swarm-CSRF", "tok")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Errorf("status = %d", r.StatusCode)
	}

	select {
	case got := <-respCh:
		if len(got.Choices) != 1 || got.Choices[0] != "a" {
			t.Errorf("choices = %v", got.Choices)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter never received result")
	}
}

func TestServer_ClickRejectsBadCSRF(t *testing.T) {
	srv, _ := NewServer(ServerConfig{SessionID: "cl2"})
	t.Cleanup(func() { srv.Shutdown() })
	srv.PushQuestion(QuestionScreen{ID: "q-c", HTML: "<div></div>", CSRF: "good"})

	body := strings.NewReader(`{"questionID":"q-c","choices":["a"]}`)
	req, _ := http.NewRequest("POST", srv.URL()+"/click", body)
	req.Header.Set("X-Swarm-CSRF", "wrong")
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Errorf("status = %d, want 403", r.StatusCode)
	}
}

func TestServer_ClickMissingQuestion410(t *testing.T) {
	srv, _ := NewServer(ServerConfig{SessionID: "cl3"})
	t.Cleanup(func() { srv.Shutdown() })

	body := strings.NewReader(`{"questionID":"nope","choices":["a"]}`)
	req, _ := http.NewRequest("POST", srv.URL()+"/click", body)
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 410 {
		t.Errorf("status = %d, want 410", r.StatusCode)
	}
}

func TestServer_RootServesFrame(t *testing.T) {
	srv, _ := NewServer(ServerConfig{SessionID: "frame"})
	t.Cleanup(func() { srv.Shutdown() })

	r, err := http.Get(srv.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Errorf("status = %d", r.StatusCode)
	}
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), `/static/app.bundle.js`) {
		t.Errorf("frame not served: %s", body)
	}
	if !strings.Contains(string(body), `/static/transport.js`) {
		t.Errorf("transport bootstrap missing: %s", body)
	}
}

func TestServer_StaticAssets(t *testing.T) {
	srv, _ := NewServer(ServerConfig{SessionID: "static"})
	t.Cleanup(func() { srv.Shutdown() })
	for _, p := range []string{"/static/app.css", "/static/app.bundle.css", "/static/app.bundle.js", "/static/transport.js"} {
		r, err := http.Get(srv.URL() + p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if r.StatusCode != 200 {
			t.Errorf("%s status = %d", p, r.StatusCode)
		}
		r.Body.Close()
	}
}

func TestEnsure_LazyAndReuse(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry(dir)
	t.Cleanup(reg.ShutdownAll)
	a, err := reg.Ensure("x")
	if err != nil {
		t.Fatal(err)
	}
	b, err := reg.Ensure("x")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("second Ensure must reuse")
	}
}

func TestEnsure_DifferentSessions_DifferentPorts(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry(dir)
	t.Cleanup(reg.ShutdownAll)
	a, _ := reg.Ensure("a")
	b, _ := reg.Ensure("b")
	if a.Port() == b.Port() {
		t.Error("different sessions must have different ports")
	}
}

func TestEnsure_WritesLockFile(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry(dir)
	t.Cleanup(reg.ShutdownAll)
	s, _ := reg.Ensure("lock")
	info, err := ReadLockFile(LockFilePath(filepath.Join(dir, "lock", "visual")))
	if err != nil {
		t.Fatal(err)
	}
	if info.Port != s.Port() {
		t.Errorf("port mismatch: %d vs %d", info.Port, s.Port())
	}
}

// TestServer_CSRF_EnforcedBeforeFrameLoad guards against the bug where a
// question pushed before the frame is first loaded would have empty CSRF and
// let any token through. CSRF is minted in NewServer now so the check always
// runs — this is the realistic production flow (agent pushes, user opens URL).
func TestServer_CSRF_EnforcedBeforeFrameLoad(t *testing.T) {
	srv, err := NewServer(ServerConfig{SessionID: "csrf-early"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown() })

	// Push BEFORE any GET / — this is how agents actually push questions.
	respCh := make(chan ClickResult, 1)
	srv.RegisterWaiter("q-early", respCh)
	srv.PushQuestion(QuestionScreen{ID: "q-early", HTML: "<div></div>"})

	// POST /click with wrong CSRF must 403.
	body := strings.NewReader(`{"questionID":"q-early","choices":["a"]}`)
	req, _ := http.NewRequest("POST", srv.URL()+"/click", body)
	req.Header.Set("X-Swarm-CSRF", "not-the-real-token")
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Errorf("status with wrong CSRF = %d, want 403", r.StatusCode)
	}

	// POST /click with NO CSRF header must also 403.
	body2 := strings.NewReader(`{"questionID":"q-early","choices":["a"]}`)
	req2, _ := http.NewRequest("POST", srv.URL()+"/click", body2)
	r2, _ := http.DefaultClient.Do(req2)
	r2.Body.Close()
	if r2.StatusCode != 403 {
		t.Errorf("status with no CSRF = %d, want 403", r2.StatusCode)
	}

	// Now fetch / to get the real token (this is what the browser does).
	frame, err := http.Get(srv.URL() + "/")
	if err != nil {
		t.Fatalf("failed to get frame: %v", err)
	}
	defer frame.Body.Close()
	frameHTML, _ := io.ReadAll(frame.Body)
	tok := extractCSRF(t, string(frameHTML))

	// POST /click with the real token must succeed.
	body3 := strings.NewReader(`{"questionID":"q-early","choices":["a"]}`)
	req3, _ := http.NewRequest("POST", srv.URL()+"/click", body3)
	req3.Header.Set("X-Swarm-CSRF", tok)
	r3, _ := http.DefaultClient.Do(req3)
	r3.Body.Close()
	if r3.StatusCode != 200 {
		t.Errorf("status with real CSRF = %d, want 200", r3.StatusCode)
	}

	select {
	case got := <-respCh:
		if len(got.Choices) != 1 || got.Choices[0] != "a" {
			t.Errorf("choices = %v", got.Choices)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter never received result")
	}
}

// extractCSRF pulls the __SW_CSRF__ token out of the frame HTML.
func extractCSRF(t *testing.T, html string) string {
	t.Helper()
	const needle = `window.__SW_CSRF__ = "`
	_, after, ok := strings.Cut(html, needle)
	if !ok {
		t.Fatalf("CSRF token not found in frame: %s", html)
	}
	rest := after
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("CSRF token terminator not found")
	}
	return rest[:end]
}

func TestServer_ScreensList(t *testing.T) {
	dir := t.TempDir()
	srv, _ := NewServer(ServerConfig{SessionID: "sl"})
	t.Cleanup(func() { srv.Shutdown() })
	if err := srv.SetSessionDir(dir); err != nil {
		t.Fatal(err)
	}

	srv.PushScreen(&Screen{ID: "p1", Kind: ScreenKindPending, Title: "Q?", AgentID: "a1"})
	srv.PushScreen(&Screen{ID: "i1", Kind: ScreenKindInformational, Title: "Info", AgentID: "a1"})

	r, err := http.Get(srv.URL() + "/screens")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var list []map[string]any
	_ = json.NewDecoder(r.Body).Decode(&list)
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}
}

func TestServer_ScreensFilterByKind(t *testing.T) {
	dir := t.TempDir()
	srv, _ := NewServer(ServerConfig{SessionID: "slf"})
	t.Cleanup(func() { srv.Shutdown() })
	_ = srv.SetSessionDir(dir)
	srv.PushScreen(&Screen{ID: "p1", Kind: ScreenKindPending})
	srv.PushScreen(&Screen{ID: "i1", Kind: ScreenKindInformational})

	r, err := http.Get(srv.URL() + "/screens?kind=pending")
	if err != nil {
		t.Fatalf("failed to get screens: %v", err)
	}
	defer r.Body.Close()
	var list []map[string]any
	_ = json.NewDecoder(r.Body).Decode(&list)
	if len(list) != 1 {
		t.Errorf("filtered len = %d, want 1", len(list))
	}
	if list[0]["id"] != "p1" {
		t.Errorf("got id %v", list[0]["id"])
	}
}

func TestServer_AckTransitionsInformational(t *testing.T) {
	dir := t.TempDir()
	srv, _ := NewServer(ServerConfig{SessionID: "ack"})
	t.Cleanup(func() { srv.Shutdown() })
	_ = srv.SetSessionDir(dir)

	srv.PushScreen(&Screen{ID: "i1", Kind: ScreenKindInformational, CSRF: "tok"})

	req, _ := http.NewRequest("POST", srv.URL()+"/ack/i1", nil)
	req.Header.Set("X-Swarm-CSRF", "tok")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Errorf("status = %d", r.StatusCode)
	}

	sc := srv.getScreen("i1")
	if sc.Kind != ScreenKindAcknowledged {
		t.Errorf("kind = %s, want acknowledged", sc.Kind)
	}
}

func TestServer_JournalReplayOnRestart(t *testing.T) {
	dir := t.TempDir()
	srv, _ := NewServer(ServerConfig{SessionID: "restart"})
	_ = srv.SetSessionDir(dir)
	srv.PushScreen(&Screen{ID: "p1", Kind: ScreenKindPending, Title: "Q?"})
	srv.PushScreen(&Screen{ID: "i1", Kind: ScreenKindInformational, Title: "info"})
	srv.Shutdown()

	srv2, _ := NewServer(ServerConfig{SessionID: "restart"})
	t.Cleanup(func() { srv2.Shutdown() })
	_ = srv2.SetSessionDir(dir)

	if srv2.getScreen("p1") == nil {
		t.Error("p1 not replayed")
	}
	if srv2.getScreen("i1") == nil {
		t.Error("i1 not replayed")
	}
	if srv2.getScreen("i1").Kind != ScreenKindInformational {
		t.Error("i1 kind drift")
	}
}

func TestServer_ReviewPlan(t *testing.T) {
	dir := t.TempDir()
	srv, _ := NewServer(ServerConfig{SessionID: "rv"})
	t.Cleanup(func() { srv.Shutdown() })
	_ = srv.SetSessionDir(dir)

	srv.PushScreen(&Screen{ID: "p1", Kind: ScreenKindPlan, CSRF: "tok", PlanState: PlanStateProposed})
	body := strings.NewReader(`{"decision":"approve"}`)
	req, _ := http.NewRequest("POST", srv.URL()+"/review/p1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Swarm-CSRF", "tok")
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("status = %d", r.StatusCode)
	}
	if srv.getScreen("p1").PlanState != PlanStateApproved {
		t.Error("planState not updated")
	}
}
