package launch

import (
	"errors"
	"strings"
	"testing"
)

func TestCanOpen_DisabledByEnv(t *testing.T) {
	t.Setenv("SWARMOS_NO_BROWSER", "1")
	var ok bool
	var reason string
	ok, reason = CanOpen()
	if ok {
		t.Fatalf("expected CanOpen to return false")
	}
	if reason == "" {
		t.Fatalf("expected reason to be set")
	}
}

func TestOpen_DisabledByEnv(t *testing.T) {
	t.Setenv("SWARMOS_NO_BROWSER", "true")
	var target string = "https://example.com"
	var err error = Open(target)
	if err == nil {
		t.Fatalf("expected error")
	}
	var openErr *OpenError
	if !errors.As(err, &openErr) {
		t.Fatalf("expected OpenError, got %T", err)
	}
	if openErr.Target != target {
		t.Fatalf("expected target %q, got %q", target, openErr.Target)
	}
	if !strings.Contains(err.Error(), "Open manually") {
		t.Fatalf("expected manual hint in error message")
	}
}
