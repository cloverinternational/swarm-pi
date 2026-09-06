package bgprocess

import (
	"context"
	"os/exec"
	"runtime"
	"sync"
)

// bufferingMethod tracks which command wrapper to use for line buffering
type bufferingMethod int

const (
	bufferMethodUnknown     bufferingMethod = iota
	bufferMethodStdbuf                      // stdbuf -oL -eL (Linux/GNU)
	bufferMethodScriptBSD                   // script -q /dev/null <shell> -c <command>
	bufferMethodScriptLinux                 // script -q -c "<shell> -c <command>" /dev/null
	bufferMethodDirect                      // No wrapper (fallback)
)

var (
	detectedMethod bufferingMethod
	detectionOnce  sync.Once
)

// detectBufferingMethod determines the best available method for line buffering.
// Runs once and caches the result for performance.
func detectBufferingMethod() bufferingMethod {
	detectionOnce.Do(func() {
		// Try stdbuf first (Linux/GNU coreutils)
		// This is the preferred method with best compatibility
		if _, err := exec.LookPath("stdbuf"); err == nil {
			detectedMethod = bufferMethodStdbuf
			return
		}

		// Try script next (macOS/BSD built-in, forces PTY which gives line buffering)
		// Available on all macOS and most BSD systems by default. util-linux
		// exposes a different CLI on Linux, so select the right invocation form.
		if _, err := exec.LookPath("script"); err == nil {
			switch runtime.GOOS {
			case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
				detectedMethod = bufferMethodScriptBSD
			default:
				detectedMethod = bufferMethodScriptLinux
			}
			return
		}

		// Fallback: direct execution (no buffering control)
		// May cause delayed output for piped commands, but at least works
		detectedMethod = bufferMethodDirect
	})

	return detectedMethod
}

// buildBashCommand creates an exec.Cmd with appropriate buffering control
// for the current platform. Falls back gracefully if stdbuf is unavailable.
//
// On Linux (with stdbuf):   stdbuf -oL -eL /bin/bash -c "command"
// On macOS (with script):   script -q /dev/null /bin/bash -c "command"
// On Windows:               cmd.exe /C "command"
// Fallback:                 /bin/bash -c "command"
//
// The buffering control ensures incremental output for piped commands
// (e.g., "tail -f file | grep pattern") instead of buffering until process exit.
func buildBashCommand(ctx context.Context, shell, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/C", command)
	}

	return buildBashCommandWithMethod(ctx, shell, command, detectBufferingMethod())
}

func buildBashCommandWithMethod(ctx context.Context, shell, command string, method bufferingMethod) *exec.Cmd {
	switch method {
	case bufferMethodStdbuf:
		// Linux/GNU: Use stdbuf for line buffering
		// -oL = line buffered stdout
		// -eL = line buffered stderr
		// Without this, piped commands buffer output internally (libc uses
		// full buffering when stdout is not a TTY) and don't show incremental
		// output until the buffer fills or process exits.
		return exec.CommandContext(ctx, "stdbuf", "-oL", "-eL", shell, "-c", command)

	case bufferMethodScriptBSD:
		// macOS/BSD: Use script to force PTY allocation (provides line buffering)
		// -q = quiet mode (suppresses script's own startup/exit messages)
		// /dev/null = discards script's internal log (we don't need it)
		// The shell command's stdout/stderr still flow normally to our pipes
		//
		// Note: PTY allocation forces line buffering in the kernel, achieving
		// the same effect as stdbuf without requiring GNU coreutils.
		return exec.CommandContext(ctx, "script", "-q", "/dev/null", shell, "-c", command)

	case bufferMethodScriptLinux:
		// util-linux script expects -c "<command>" followed by the log file path.
		// Build the shell invocation explicitly so Linux hosts without stdbuf still
		// get PTY-backed incremental output instead of a broken fallback command.
		return exec.CommandContext(ctx, "script", "-q", "-c", buildScriptCommandString(shell, command), "/dev/null")

	case bufferMethodDirect:
		// Fallback: Direct execution without buffering control
		// This may cause delayed output for piped commands, but ensures
		// the tool works even on minimal/unusual Unix environments.
		// Output will eventually appear, just not incrementally.
		return exec.CommandContext(ctx, shell, "-c", command)

	default:
		// Should never reach here due to detectBufferingMethod logic,
		// but provide safe fallback anyway
		return exec.CommandContext(ctx, shell, "-c", command)
	}
}

// buildScriptCommandString constructs the shell invocation string passed to
// util-linux's "script -c" flag.  The command is single-quoted to prevent the
// outer shell from interpreting metacharacters.
func buildScriptCommandString(shell, command string) string {
	return shell + " -c " + "'" + command + "'"
}
