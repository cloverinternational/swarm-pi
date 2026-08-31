package builtin

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestDetectBufferingMethod(t *testing.T) {
	method := detectBufferingMethod()

	if method == bufferMethodUnknown {
		t.Error("Detection should never return Unknown")
	}

	// At minimum, direct execution should always be available as fallback
	if method != bufferMethodStdbuf &&
		method != bufferMethodScriptBSD &&
		method != bufferMethodScriptLinux &&
		method != bufferMethodDirect {
		t.Errorf("Invalid buffering method detected: %d", method)
	}

	t.Logf("✓ Detected buffering method: %s", GetBufferingMethodName())
}

func TestGetBufferingMethodName(t *testing.T) {
	name := GetBufferingMethodName()

	if name == "" || name == "unknown" {
		t.Error("Should detect a valid buffering method")
	}

	t.Logf("✓ Buffering method name: %s", name)

	// Verify it's one of the expected values
	validNames := []string{
		"stdbuf (GNU coreutils)",
		"script (BSD PTY)",
		"script (util-linux PTY)",
		"direct (no buffering control)",
	}

	found := slices.Contains(validNames, name)

	if !found {
		t.Errorf("Unexpected buffering method name: %s", name)
	}
}

func TestBuildBashCommand_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	cmd := buildBashCommand(context.Background(), "bash", "echo test")

	// On Windows, should use cmd.exe
	if !strings.Contains(cmd.Path, "cmd.exe") {
		t.Errorf("Expected cmd.exe on Windows, got %s", cmd.Path)
	}

	// Verify /C flag is present
	foundC := slices.Contains(cmd.Args, "/C")

	if !foundC {
		t.Error("Expected /C flag for cmd.exe")
	}

	t.Logf("✓ Windows command: %v", cmd.Args)
}

func TestBuildBashCommand_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	cmd := buildBashCommand(context.Background(), "/bin/bash", "echo test")

	// Should create a valid command (won't be nil)
	if cmd == nil {
		t.Fatal("buildBashCommand returned nil")
	}

	// Verify bash is in the args somewhere
	foundBash := false
	for _, arg := range cmd.Args {
		if strings.Contains(arg, "bash") {
			foundBash = true
			break
		}
	}

	if !foundBash {
		t.Errorf("Expected bash in command args: %v", cmd.Args)
	}

	// Verify -c flag is present (standard shell invocation)
	foundC := slices.Contains(cmd.Args, "-c")

	if !foundC {
		t.Errorf("Expected -c flag for bash: %v", cmd.Args)
	}

	t.Logf("✓ Unix command: %v", cmd.Args)
}

func TestBuildBashCommand_Execution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	// Test that the command actually executes successfully
	ctx := context.Background()
	cmd := buildBashCommand(ctx, "/bin/bash", "echo 'Hello World'")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Command execution failed: %v", err)
	}

	outputStr := strings.TrimSpace(string(output))

	if outputStr == "" {
		t.Error("Expected output from echo command")
	}

	if !strings.Contains(outputStr, "Hello World") {
		t.Errorf("Expected 'Hello World' in output, got: %s", outputStr)
	}

	t.Logf("✓ Command output: %s", outputStr)
}

func TestBuildBashCommand_ScriptBSDArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	cmd := buildBashCommandWithMethod(context.Background(), "/bin/bash", "echo 'Hello World'", bufferMethodScriptBSD)

	want := []string{"script", "-q", "/dev/null", "/bin/bash", "-c", "echo 'Hello World'"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("unexpected arg count: got %v want %v", cmd.Args, want)
	}
	for i, arg := range want {
		if cmd.Args[i] != arg {
			t.Fatalf("arg %d mismatch: got %q want %q (full args: %v)", i, cmd.Args[i], arg, cmd.Args)
		}
	}
}

func TestBuildBashCommand_ScriptLinuxArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	cmd := buildBashCommandWithMethod(context.Background(), "/bin/bash", "echo 'Hello World'", bufferMethodScriptLinux)

	want := []string{
		"script",
		"-q",
		"-c",
		buildScriptCommandString("/bin/bash", "echo 'Hello World'"),
		"/dev/null",
	}
	if len(cmd.Args) != len(want) {
		t.Fatalf("unexpected arg count: got %v want %v", cmd.Args, want)
	}
	for i, arg := range want {
		if cmd.Args[i] != arg {
			t.Fatalf("arg %d mismatch: got %q want %q (full args: %v)", i, cmd.Args[i], arg, cmd.Args)
		}
	}
}

func TestBuildScriptCommandString_QuotesShellAndCommand(t *testing.T) {
	got := buildScriptCommandString("/path with spaces/bash", `echo "hi" && printf '%s' "a'b"`)
	want := `'/path with spaces/bash' -c 'echo "hi" && printf '"'"'%s'"'"' "a'"'"'b"'`
	if got != want {
		t.Fatalf("unexpected command string: got %q want %q", got, want)
	}
}

func TestBuildBashCommand_PipedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	// Test piped command (this is where buffering matters most)
	ctx := context.Background()
	cmd := buildBashCommand(ctx, "/bin/bash", "echo -e 'line1\\nline2\\nline3' | grep line2")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Piped command execution failed: %v", err)
	}

	outputStr := strings.TrimSpace(string(output))

	if !strings.Contains(outputStr, "line2") {
		t.Errorf("Expected 'line2' in output, got: %s", outputStr)
	}

	// Should NOT contain line1 or line3 (grep filtered them out)
	if strings.Contains(outputStr, "line1") || strings.Contains(outputStr, "line3") {
		t.Errorf("Grep should have filtered out line1 and line3, got: %s", outputStr)
	}

	t.Logf("✓ Piped command output: %s", outputStr)
}

func TestBuildBashCommand_MultipleInvocations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	// Verify that detection caching works correctly
	// (multiple calls should return consistent results)
	ctx := context.Background()

	cmd1 := buildBashCommand(ctx, "/bin/bash", "echo test1")
	cmd2 := buildBashCommand(ctx, "/bin/bash", "echo test2")

	// Both should use the same buffering method (first arg should match)
	if len(cmd1.Args) > 0 && len(cmd2.Args) > 0 {
		if cmd1.Args[0] != cmd2.Args[0] {
			t.Errorf("Inconsistent buffering method: %s vs %s", cmd1.Args[0], cmd2.Args[0])
		}
	}

	t.Logf("✓ Consistent buffering method across invocations")
}

func TestBuildBashCommand_ContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	// Test that context cancellation is respected
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	cmd := buildBashCommand(ctx, "/bin/bash", "sleep 10")

	err := cmd.Run()
	if err == nil {
		t.Error("Expected error from cancelled context")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got: %v", err)
	}

	t.Logf("✓ Context cancellation respected")
}

// Benchmark to ensure detection overhead is minimal
func BenchmarkDetectBufferingMethod(b *testing.B) {
	for i := 0; i < b.N; i++ {
		detectBufferingMethod()
	}
}

// Benchmark command building
func BenchmarkBuildBashCommand(b *testing.B) {
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		buildBashCommand(ctx, "/bin/bash", "echo test")
	}
}
