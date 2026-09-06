package bridges

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRPCAuthSessionAndInvoke(t *testing.T) {
	b := New(func(tok string) (string, bool) { return "alice", tok == "secret" }, func(_ context.Context, id Identity, m string, p json.RawMessage) (any, error) {
		return map[string]string{"user": id.User, "session": id.Session, "method": m}, nil
	})
	r := httptest.NewRequest("POST", "/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"bridge.invoke","params":{"method":"echo"}}`))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Session-ID", "s1")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"session":"s1"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestSSEReplayIsSessionScoped(t *testing.T) {
	b := New(func(string) (string, bool) { return "u", true }, nil)
	e := b.Emit("a", "x", map[string]string{"v": "one"})
	b.Emit("b", "x", map[string]string{"v": "other"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, "GET", "/events", nil)
	r.Header.Set("Authorization", "Bearer x")
	r.Header.Set("X-Session-ID", "a")
	r.Header.Set("Last-Event-ID", "0")
	w := httptest.NewRecorder()
	go b.ServeHTTP(w, r)
	time.Sleep(20 * time.Millisecond)
	b.Emit("a", "x", map[string]string{"v": "two"})
	cancel()
	time.Sleep(20 * time.Millisecond)
	if !strings.Contains(w.Body.String(), `"v":"one"`) || !strings.Contains(w.Body.String(), `"v":"two"`) || strings.Contains(w.Body.String(), `other`) {
		t.Fatal(e, w.Body.String())
	}
}
func TestConstantTime(t *testing.T) {
	if ConstantTimeTokenEqual("a", "b") || ConstantTimeTokenEqual("a", "ab") || !ConstantTimeTokenEqual("a", "a") {
		t.Fatal()
	}
}
func TestMethodNotFound(t *testing.T) {
	b := New(func(string) (string, bool) { return "u", true }, nil)
	r := httptest.NewRequest("POST", "/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	r.Header.Set("Authorization", "Bearer x")
	r.Header.Set("X-Session-ID", "s")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	_ = http.MethodPost
}
