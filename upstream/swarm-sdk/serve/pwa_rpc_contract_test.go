package serve

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// TestMobilePWARPCContract guards the mobile PWA (swarm-sdk/gateway/web/app.js —
// the phone "front door") against the single most common cause of the "rpc
// errors from the mobile app" class of bug: the PWA calling an rpc("<method>")
// the serve bridge never registered. When that happens Mux.Dispatch returns
// ErrMethodNotFound and the phone shows a raw "<method> failed: method not
// found" toast at runtime — with no compile-time warning.
//
// The PWA talks to whichever peer it attaches to over that peer's serve /rpc
// bridge, which is exactly the Mux built by NewMux here. So the live method set
// this test checks IS the set the phone dispatches against. Add an rpc() call
// in app.js without a matching serve method and this test goes red before it
// can ever reach a device.
//
// Only rpc("...") calls are checked. The PWA's other backend calls
// (fetch("api/peers"), fetch("api/select"), new EventSource("sse"), …) are
// gateway HTTP endpoints, not serve-bridge RPC methods, and are out of scope.
func TestMobilePWARPCContract(t *testing.T) {
	appJS := findMobilePWA(t)
	if appJS == "" {
		t.Skip("swarm-sdk/gateway/web/app.js not found (source-only checkout); skipping mobile PWA RPC contract check")
	}

	registered := make(map[string]bool)
	for _, name := range NewMux(&client.Client{}).Methods() {
		registered[name] = true
	}
	if len(registered) == 0 {
		t.Fatal("serve Mux registered zero methods — registerBuiltinMethods wiring changed?")
	}

	data, err := os.ReadFile(appJS)
	if err != nil {
		t.Fatalf("reading %s: %v", appJS, err)
	}

	// rpc("client.foo", …) / rpc('client.foo') — the PWA always uses a string
	// literal method name.
	re := regexp.MustCompile(`\brpc\(\s*['"]([^'"]+)['"]`)
	called := map[string]int{}
	for i, line := range strings.Split(string(data), "\n") {
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			if called[m[1]] == 0 {
				called[m[1]] = i + 1 // first-seen line
			}
		}
	}
	if len(called) == 0 {
		t.Fatal("found zero rpc('...') calls in gateway/web/app.js — the regex or the PWA client changed; " +
			"this guard is only meaningful if it actually sees the PWA's RPC calls")
	}

	var missing []string
	for method, line := range called {
		if !registered[method] {
			missing = append(missing, method+"  (app.js:"+itoaPWA(line)+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("the mobile PWA calls %d rpc() method(s) the serve bridge does not register — these surface "+
			"as \"method not found\" errors on the phone at runtime:\n  %s\n\n"+
			"Fix: register the method in serve/client_methods.go, or correct the name in gateway/web/app.js.",
			len(missing), strings.Join(missing, "\n  "))
	}

	t.Logf("mobile PWA RPC contract OK: all %d called method(s) are registered (of %d serve methods)", len(called), len(registered))
}

// findMobilePWA walks up from the test's working directory to locate the
// sibling gateway/web/app.js. Returns "" when absent (Go-only checkout).
func findMobilePWA(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 12; i++ {
		candidate := filepath.Join(dir, "gateway", "web", "app.js")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		// Also try swarm-sdk/gateway/web/app.js when running from repo root.
		candidate = filepath.Join(dir, "swarm-sdk", "gateway", "web", "app.js")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func itoaPWA(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
