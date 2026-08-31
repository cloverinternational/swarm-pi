// Package safego provides panic-safe goroutine launchers.
//
// A naked `go func()` that panics takes down the entire process, because a
// panic that unwinds past the top of a goroutine's stack is fatal and cannot
// be recovered by the parent. The helpers in this package wrap the supplied
// function in a deferred recover so a panic in background work is logged and
// contained instead of crashing the program. Behaviour is otherwise identical
// to launching the function directly with the `go` keyword.
package safego

import (
	"log"
	"runtime/debug"
)

// Go launches fn in a new goroutine with panic recovery.
//
// If fn panics, the panic is recovered and logged via log.Printf together with
// the supplied name (for attribution) and the goroutine's stack trace. The
// process keeps running. name should be a short, stable identifier for the
// work being performed (e.g. "chat.fetchUsage") so panics can be traced back
// to their launch site.
func Go(name string, fn func()) {
	GoWithLogger(name, log.Printf, fn)
}

// GoWithLogger launches fn in a new goroutine with panic recovery, routing the
// panic report through the caller-supplied logf instead of the standard logger.
//
// This is for callers that already own a structured or prefixed logger and want
// panic reports to land in the same place. logf must be safe to call from a
// goroutine. If logf is nil, GoWithLogger falls back to log.Printf. The format
// passed to logf is "safego: goroutine %q panicked: %v\n%s" with arguments
// name, the recovered value, and debug.Stack().
func GoWithLogger(name string, logf func(format string, args ...any), fn func()) {
	if logf == nil {
		logf = log.Printf
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logf("safego: goroutine %q panicked: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}
