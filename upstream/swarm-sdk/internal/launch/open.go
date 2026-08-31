// Package launch provides helpers for opening files or URLs in the default system handler.
package launch

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OpenError describes a failure to launch a target in the default handler.
type OpenError struct {
	Target string
	Reason string
	Err    error
}

// Error returns a user-friendly error message with a manual fallback hint.
func (e *OpenError) Error() string {
	var detail string
	if e == nil {
		return "open failed"
	}
	if e.Reason != "" && e.Err != nil {
		detail = fmt.Sprintf("%s: %v", e.Reason, e.Err)
	} else if e.Reason != "" {
		detail = e.Reason
	} else if e.Err != nil {
		detail = e.Err.Error()
	} else {
		detail = "unknown error"
	}
	if e.Target == "" {
		return fmt.Sprintf("open failed (%s)", detail)
	}
	return fmt.Sprintf("open failed (%s). Open manually: %s", detail, e.Target)
}

// Unwrap returns the underlying error, if any.
func (e *OpenError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var execCommand func(name string, args ...string) *exec.Cmd = exec.Command
var execLookPath func(file string) (string, error) = exec.LookPath

// CanOpen reports whether this environment can launch GUI handlers.
func CanOpen() (bool, string) {
	if envFlagSet("SWARMOS_NO_BROWSER") || envFlagSet("NO_BROWSER") {
		return false, "browser launch disabled"
	}
	if envFlagSet("CI") {
		return false, "CI environment"
	}
	if runtime.GOOS == "linux" {
		var display string = strings.TrimSpace(os.Getenv("DISPLAY"))
		var wayland string = strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY"))
		if display == "" && wayland == "" {
			return false, "no display detected"
		}
	}
	return true, ""
}

// linuxBrowsers is an ordered list of browser commands to try on Linux.
// These are checked in order, and the first available browser is used.
// This avoids the issues with xdg-open not properly detaching browsers.
var linuxBrowsers = []string{
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"firefox",
	"firefox-esr",
}

// Open launches the target in the OS default handler.
func Open(target string) error {
	var ok bool
	var reason string
	ok, reason = CanOpen()
	if !ok {
		return &OpenError{Target: target, Reason: reason}
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = execCommand("open", target)
	case "linux":
		// Try to find a browser directly - this is more reliable than xdg-open
		// because we can properly detach the process
		cmd = findLinuxBrowser(target)
		if cmd == nil {
			// Fall back to xdg-open
			cmd = execCommand("xdg-open", target)
		}
	case "windows":
		cmd = execCommand("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		return &OpenError{Target: target, Reason: "unsupported platform"}
	}

	configureProcess(cmd)

	var err error = cmd.Start() // Use Start() instead of Run() to not wait
	if err != nil {
		return &OpenError{Target: target, Reason: "command failed", Err: err}
	}

	releaseProcess(cmd)

	return nil
}

// findLinuxBrowser searches for an available browser and returns a command
// that will launch it with the target URL. Returns nil if no browser found.
func findLinuxBrowser(target string) *exec.Cmd {
	for _, browser := range linuxBrowsers {
		if path, err := execLookPath(browser); err == nil && path != "" {
			// Use nohup to ensure the browser stays open even if TUI closes
			return execCommand("nohup", path, target)
		}
	}
	return nil
}

func envFlagSet(name string) bool {
	var value string = strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false
	}
	var lower string = strings.ToLower(value)
	switch lower {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
