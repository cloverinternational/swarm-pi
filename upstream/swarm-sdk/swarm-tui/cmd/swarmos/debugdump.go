package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// writeGoroutineDump captures all goroutine stacks and writes them to a file.
// It grows the buffer until the full profile fits (runtime.Stack truncates to
// the provided buffer length).
func writeGoroutineDump() (string, error) {
	buf := make([]byte, 1<<20) // 1 MiB to start
	for {
		n := runtime.Stack(buf, true /* all goroutines */)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}

	dir := dumpDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("goroutines-%d-%s.txt", os.Getpid(), time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)

	header := fmt.Sprintf("# goroutine dump pid=%d time=%s numgoroutine=%d\n\n",
		os.Getpid(), time.Now().Format(time.RFC3339), runtime.NumGoroutine())
	out := append([]byte(header), buf...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// dumpDir picks a writable location for dumps, preferring the swarmos state
// directory and falling back to the OS temp dir.
func dumpDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".swarmos", "dumps")
	}
	return filepath.Join(os.TempDir(), "swarmos-dumps")
}
