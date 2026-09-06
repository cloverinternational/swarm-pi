//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// installGoroutineDumpHandler wires SIGUSR1 to a non-destructive full
// goroutine-stack dump. Unlike SIGQUIT (which dumps and then kills the
// process), this lets us inspect a live, possibly-wedged TUI session without
// tearing down the user's session. Send the signal with kill -USR1 <pid>.
func installGoroutineDumpHandler() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			if _, err := writeGoroutineDump(); err != nil {
				continue
			}
		}
	}()
}
