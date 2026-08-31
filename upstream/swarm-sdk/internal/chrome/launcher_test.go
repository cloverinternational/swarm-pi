package chrome

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const testExtensionID = "abcdefghijklmnopabcdefghijklmnop"

type commandCall struct {
	executable string
	args       []string
}

type recordingRunner struct {
	calls []commandCall
	err   error
}

func (r *recordingRunner) Start(executable string, args ...string) error {
	r.calls = append(r.calls, commandCall{executable: executable, args: append([]string(nil), args...)})
	return r.err
}

func TestLauncherUsesExactSafeArguments(t *testing.T) {
	runner := &recordingRunner{}
	launcher, err := NewLauncher(testExtensionID, func() (string, error) {
		return "/opt/google/chrome", nil
	}, runner)
	if err != nil {
		t.Fatalf("NewLauncher() error = %v", err)
	}

	const launchID = "launch_ID-0123456789"
	if err := launcher.Launch(context.Background(), LauncherRequest{LaunchID: launchID}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}

	want := []commandCall{{
		executable: "/opt/google/chrome",
		args: []string{
			"--profile-directory=Default",
			"--new-window",
			"chrome-extension://" + testExtensionID + "/bootstrap.html#launch=" + launchID,
		},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("command calls = %#v, want %#v", runner.calls, want)
	}
	for _, forbidden := range []string{
		"--user-data-dir", "--incognito", "--guest", "--headless",
		"--remote-debugging-port", "--user-agent",
	} {
		for _, arg := range runner.calls[0].args {
			if strings.HasPrefix(strings.ToLower(arg), forbidden) {
				t.Fatalf("unsafe argument %q was passed", arg)
			}
		}
	}
}

func TestLauncherRejectsInvalidIdentifiersWithoutSideEffects(t *testing.T) {
	tests := []struct {
		name        string
		extensionID string
		launchID    string
		constructor bool
	}{
		{name: "extension too short", extensionID: "abcdefghijklmnop", constructor: true},
		{name: "extension outside alphabet", extensionID: "qrstuvwxyzabcdefqrstuvwxyzabcdef", constructor: true},
		{name: "launch too short", extensionID: testExtensionID, launchID: "short"},
		{name: "launch URL injection", extensionID: testExtensionID, launchID: "validlength#--guest"},
		{name: "launch whitespace", extensionID: testExtensionID, launchID: "valid launch identifier"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discoveryCalls := 0
			runner := &recordingRunner{}
			launcher, err := NewLauncher(tt.extensionID, func() (string, error) {
				discoveryCalls++
				return "chrome", nil
			}, runner)
			if tt.constructor {
				assertChromeErrorCode(t, err, ErrInvalidArguments)
				return
			}
			if err != nil {
				t.Fatalf("NewLauncher() error = %v", err)
			}
			err = launcher.Launch(context.Background(), LauncherRequest{LaunchID: tt.launchID})
			assertChromeErrorCode(t, err, ErrInvalidArguments)
			if discoveryCalls != 0 || len(runner.calls) != 0 {
				t.Fatalf("invalid input had side effects: discovery=%d starts=%d", discoveryCalls, len(runner.calls))
			}
		})
	}
}

func TestLauncherReturnsTypedDiscoveryAndStartErrors(t *testing.T) {
	t.Run("discovery", func(t *testing.T) {
		launcher, err := NewLauncher(testExtensionID, func() (string, error) {
			return "", errors.New("missing")
		}, &recordingRunner{})
		if err != nil {
			t.Fatalf("NewLauncher() error = %v", err)
		}
		err = launcher.Launch(context.Background(), LauncherRequest{LaunchID: "launch_0123456789"})
		assertChromeErrorCode(t, err, ErrChromeNotFound)
	})

	t.Run("unsupported is preserved", func(t *testing.T) {
		launcher, err := NewLauncher(testExtensionID, func() (string, error) {
			return "", NewError(ErrUnsupportedHost, "unsupported")
		}, &recordingRunner{})
		if err != nil {
			t.Fatalf("NewLauncher() error = %v", err)
		}
		err = launcher.Launch(context.Background(), LauncherRequest{LaunchID: "launch_0123456789"})
		assertChromeErrorCode(t, err, ErrUnsupportedHost)
	})

	t.Run("start", func(t *testing.T) {
		runner := &recordingRunner{err: errors.New("start failed")}
		launcher, err := NewLauncher(testExtensionID, func() (string, error) {
			return "chrome", nil
		}, runner)
		if err != nil {
			t.Fatalf("NewLauncher() error = %v", err)
		}
		err = launcher.Launch(context.Background(), LauncherRequest{LaunchID: "launch_0123456789"})
		assertChromeErrorCode(t, err, ErrLaunchFailed)
	})
}

func TestLauncherHonorsCancellationBeforeDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	discoveryCalls := 0
	launcher, err := NewLauncher(testExtensionID, func() (string, error) {
		discoveryCalls++
		return "chrome", nil
	}, &recordingRunner{})
	if err != nil {
		t.Fatalf("NewLauncher() error = %v", err)
	}

	err = launcher.Launch(ctx, LauncherRequest{LaunchID: "launch_0123456789"})
	assertChromeErrorCode(t, err, ErrCancelled)
	if discoveryCalls != 0 {
		t.Fatalf("discovery called %d times after cancellation", discoveryCalls)
	}
}

func TestNewLauncherRequiresDependencies(t *testing.T) {
	_, err := NewLauncher(testExtensionID, nil, &recordingRunner{})
	assertChromeErrorCode(t, err, ErrInvalidArguments)
	_, err = NewLauncher(testExtensionID, func() (string, error) { return "chrome", nil }, nil)
	assertChromeErrorCode(t, err, ErrInvalidArguments)
}

func assertChromeErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var chromeErr *Error
	if !errors.As(err, &chromeErr) {
		t.Fatalf("error = %T %v, want *chrome.Error", err, err)
	}
	if chromeErr.Code != want {
		t.Fatalf("error code = %q, want %q", chromeErr.Code, want)
	}
	if validateErr := chromeErr.Validate(); validateErr != nil {
		t.Fatalf("typed error does not validate: %v", validateErr)
	}
}
