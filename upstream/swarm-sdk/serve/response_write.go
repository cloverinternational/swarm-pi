// Package serve — response_write.go
//
// WithResponseWriteBounds is the shared response-write-bound primitive from
// CONTRACT.md's "Shared response-bound contract": every entry point that can
// write to an http.ResponseWriter — this package's own HTTP/auth/SSE/
// WebSocket handlers, and (via this exported wrapper) a caller's own outer
// handler stack such as the gateway's complete handler and standalone mux —
// must install a bounded write deadline before any write can occur, and must
// fail closed (never write anything at all) if the writer cannot support
// one.
package serve

import (
	"net/http"
	"time"
)

// responseWriteBoundsTimeout is the write deadline WithResponseWriteBounds
// installs before invoking the wrapped handler. It matches
// httpResponseWriteTimeout (http.go) so every entry point in this package —
// whether it uses this exported wrapper directly or the equivalent
// per-write setResponseWriteDeadline helper — shares one bound.
const responseWriteBoundsTimeout = 10 * time.Second

// WithResponseWriteBounds returns a handler that installs a fresh
// responseWriteBoundsTimeout write deadline (via http.ResponseController) on
// w before invoking next, so a handler that writes slowly — or a client that
// reads slowly, holding the write open — cannot pin the response goroutine
// or the underlying connection open indefinitely.
//
// If w does not support a write deadline, SetWriteDeadline returns a non-nil
// error (including the sentinel http.ErrNotSupported production code uses
// for writers that genuinely lack the capability). Either way this wrapper
// fails closed: next is never invoked, and neither next nor this wrapper
// itself gets a chance to call Write, WriteHeader, Flush, http.Error, or an
// upgrader on a writer this wrapper could not bound. There is no
// unconditional fall-through "assume it's fine and write anyway" path.
//
// The standard net/http server's ResponseWriter (and any wrapper that
// implements Unwrap() http.ResponseWriter to expose it — see the
// http.ResponseController doc comment) always supports write deadlines, so
// this fail-closed branch is only ever reached by a deliberately minimal
// test double or a genuinely non-conforming production writer. Tests that
// want to exercise the normal (deadline-supported) path with
// httptest.NewRecorder must wrap it in an explicit deadline-capable adapter
// exposing SetWriteDeadline and Unwrap() rather than relying on any
// generically fail-open behavior — see the writeDeadlineRecorder type in
// mux_test.go.
//
// A successful WebSocket (or other) hijack performed by next retains
// whatever deadline was already installed on the underlying socket by this
// wrapper or by next's own subsequent socket-level deadline management:
// WithResponseWriteBounds does not touch w again after next.ServeHTTP
// returns, so it can never race or clobber a hijacked connection's own
// deadlines.
func WithResponseWriteBounds(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(responseWriteBoundsTimeout)); err != nil {
			return
		}
		next.ServeHTTP(w, r)
	})
}
