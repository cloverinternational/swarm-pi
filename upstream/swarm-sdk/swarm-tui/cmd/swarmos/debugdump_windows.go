//go:build windows

package main

// Windows has no SIGUSR1 equivalent. Goroutine dumps remain available through
// writeGoroutineDump, but there is no process-signal handler to install.
func installGoroutineDumpHandler() {}
