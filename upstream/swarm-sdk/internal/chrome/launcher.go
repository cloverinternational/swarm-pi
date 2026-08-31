package chrome

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

const defaultProfileArgument = "--profile-directory=Default"

var (
	extensionIDPattern = regexp.MustCompile(`^[a-p]{32}$`)
	launchIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

// LauncherRequest identifies one pending, single-use window claim.
// LaunchID is correlation only and does not authorize the resulting window.
type LauncherRequest struct {
	LaunchID string
}

// Launcher opens the packaged extension bootstrap page in a visible Chrome
// Default-profile window. It does not return or retain a process identifier:
// Chrome may route the request to an existing process.
type Launcher interface {
	Launch(context.Context, LauncherRequest) error
}

// DiscoverFunc resolves a supported Google Chrome executable.
type DiscoverFunc func() (string, error)

// CommandRunner starts an executable without treating the resulting process as
// ownership authority. Implementations must not add or rewrite arguments.
type CommandRunner interface {
	Start(executable string, args ...string) error
}

// CommandRunnerFunc adapts a function to CommandRunner.
type CommandRunnerFunc func(executable string, args ...string) error

// Start implements CommandRunner.
func (f CommandRunnerFunc) Start(executable string, args ...string) error {
	return f(executable, args...)
}

type chromeLauncher struct {
	extensionID string
	discover    DiscoverFunc
	runner      CommandRunner
}

// NewLauncher constructs a launcher with injectable executable discovery and
// command execution. The extension ID is pinned configuration, not request
// input.
func NewLauncher(extensionID string, discover DiscoverFunc, runner CommandRunner) (Launcher, error) {
	if !extensionIDPattern.MatchString(extensionID) {
		return nil, NewError(ErrInvalidArguments, "Chrome extension ID must be 32 lowercase characters from a through p")
	}
	if discover == nil {
		return nil, NewError(ErrInvalidArguments, "Chrome executable discovery is required")
	}
	if runner == nil {
		return nil, NewError(ErrInvalidArguments, "Chrome command runner is required")
	}
	return &chromeLauncher{
		extensionID: extensionID,
		discover:    discover,
		runner:      runner,
	}, nil
}

// NewPlatformLauncher constructs the launcher for the current host.
func NewPlatformLauncher(extensionID string) (Launcher, error) {
	return NewLauncher(extensionID, platformDiscoverChrome, execCommandRunner{})
}

func (l *chromeLauncher) Launch(ctx context.Context, request LauncherRequest) error {
	if !launchIDPattern.MatchString(request.LaunchID) {
		return NewError(ErrInvalidArguments, "launch ID must be 16 to 128 URL-safe ASCII characters")
	}
	if err := ctx.Err(); err != nil {
		return cancelledLaunchError(err)
	}

	executable, err := l.discover()
	if err != nil {
		var chromeErr *Error
		if errors.As(err, &chromeErr) {
			return chromeErr
		}
		return NewError(ErrChromeNotFound, fmt.Sprintf("Google Chrome executable not found: %v", err))
	}
	if executable == "" {
		return NewError(ErrChromeNotFound, "Google Chrome executable not found")
	}
	if err := ctx.Err(); err != nil {
		return cancelledLaunchError(err)
	}

	bootstrapURL := "chrome-extension://" + l.extensionID + "/bootstrap.html#launch=" + request.LaunchID
	args := []string{defaultProfileArgument, "--new-window", bootstrapURL}
	if err := l.runner.Start(executable, args...); err != nil {
		return NewError(ErrLaunchFailed, fmt.Sprintf("failed to start Google Chrome: %v", err))
	}
	return nil
}

func cancelledLaunchError(cause error) *Error {
	return NewFlexibleError(ErrCancelled, fmt.Sprintf("Chrome launch cancelled: %v", cause), false, ExecutionNotStarted)
}
