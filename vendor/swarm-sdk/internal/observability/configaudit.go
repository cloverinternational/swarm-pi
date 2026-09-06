package observability

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

var (
	auditMu   sync.Mutex
	auditFile *os.File
)

func getAuditFile() *os.File {
	auditMu.Lock()
	defer auditMu.Unlock()
	if auditFile != nil {
		return auditFile
	}

	dir := paths.Root()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	path := filepath.Join(dir, ".write_audit.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	auditFile = f
	return auditFile
}

// RecordConfigWrite emits an audit entry when SWARMOS_TRACE_CONFIG_WRITES=1.
// The entry is written both to ~/.swarm/.write_audit.jsonl and to stderr.
func RecordConfigWrite(file string, action string) {
	if os.Getenv("SWARMOS_TRACE_CONFIG_WRITES") != "1" {
		return
	}

	pc := make([]uintptr, 8)
	n := runtime.Callers(2, pc)
	frames := runtime.CallersFrames(pc[:n])

	var callers []string
	for range 5 {
		frame, more := frames.Next()
		if !more {
			break
		}
		callers = append(callers, fmt.Sprintf("%s:%d", frame.Function, frame.Line))
	}

	entry := map[string]any{
		"ts":     time.Now().UTC().Format(time.RFC3339Nano),
		"file":   file,
		"action": action,
		"caller": callers,
	}

	data, _ := json.Marshal(entry)
	data = append(data, '\n')

	if f := getAuditFile(); f != nil {
		_, _ = f.Write(data)
	}
	_, _ = os.Stderr.Write(data)
}

// GuardTestWrite returns an error when SWARMOS_TEST_FAIL_ON_REAL_WRITE=1
// and the target path is inside the real ~/.swarm directory (not a temp
// directory used by tests that call setTempHome).
func GuardTestWrite(path string) error {
	if os.Getenv("SWARMOS_TEST_FAIL_ON_REAL_WRITE") != "1" {
		return nil
	}

	// Allow writes when the target path is inside a temp directory.
	tmpDir := os.TempDir()
	if strings.HasPrefix(path, tmpDir) && (len(path) == len(tmpDir) || path[len(tmpDir)] == filepath.Separator) {
		return nil
	}

	realSwarm := paths.Root()
	if strings.HasPrefix(path, realSwarm) {
		pc := make([]uintptr, 8)
		n := runtime.Callers(2, pc)
		frames := runtime.CallersFrames(pc[:n])
		var callers []string
		for range 3 {
			frame, more := frames.Next()
			if !more {
				break
			}
			callers = append(callers, frame.Function)
		}
		return fmt.Errorf("TEST GUARD: refusing to write to real ~/.swarm during test (path=%s). Callers: %v. Use setTempHome(t) or set SWARMOS_TEST_ALLOW_REAL_WRITE=1", path, callers)
	}
	return nil
}
