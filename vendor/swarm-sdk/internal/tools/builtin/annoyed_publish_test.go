package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportGitHubFeedbackIssueUsesSingleStdinRequest(t *testing.T) {
	argsPath, bodyPath, promptPath := installAnnoyedGHStub(t)
	publication := FeedbackPublication{
		Repository: "Swarm-Code/mono",
		Title:      "Agent feedback (high)",
		Body:       "sanitized conversation transcript",
	}
	got, err := reportGitHubFeedbackIssue(context.Background(), publication)
	if err != nil {
		t.Fatalf("reportGitHubFeedbackIssue: %v", err)
	}
	if got != "https://github.com/Swarm-Code/mono/issues/42" {
		t.Fatalf("issue URL = %q", got)
	}
	args := readAnnoyedTestFile(t, argsPath)
	if strings.TrimSpace(args) != "POST repos/Swarm-Code/mono/issues --input -" {
		t.Fatalf("unexpected gh calls:\n%s", args)
	}
	for _, forbidden := range []string{publication.Title, publication.Body} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("gh argv leaked issue content %q:\n%s", forbidden, args)
		}
	}
	body := readAnnoyedTestFile(t, bodyPath)
	for _, want := range []string{publication.Title, publication.Body} {
		if !strings.Contains(body, want) {
			t.Fatalf("request body missing %q:\n%s", want, body)
		}
	}
	if got := readAnnoyedTestFile(t, promptPath); !strings.Contains(got, "1") {
		t.Fatalf("GH_PROMPT_DISABLED log = %q", got)
	}
}

func TestReportGitHubFeedbackIssueRedactsCLIError(t *testing.T) {
	installAnnoyedGHStub(t)
	t.Setenv("ANNOYED_FAIL_ISSUE", "1")
	_, err := reportGitHubFeedbackIssue(context.Background(), FeedbackPublication{
		Repository: "Swarm-Code/mono",
		Title:      "Agent feedback",
		Body:       "sanitized transcript",
	})
	if err == nil {
		t.Fatal("expected issue creation failure")
	}
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz123456"
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[REDACTED") {
		t.Fatalf("failure was not redacted: %v", err)
	}
}

func installAnnoyedGHStub(t *testing.T) (argsPath, bodyPath, promptPath string) {
	t.Helper()
	tempDir := t.TempDir()
	ghPath := filepath.Join(tempDir, "gh")
	argsPath = filepath.Join(tempDir, "args")
	bodyPath = filepath.Join(tempDir, "body")
	promptPath = filepath.Join(tempDir, "prompts")
	script := `#!/bin/sh
method="$3"
endpoint="$4"
body=$(/bin/cat)
printf '%s %s %s\n' "$method" "$endpoint" "$5 $6" >> "$ANNOYED_GH_ARGS"
printf '%s\n' "$body" >> "$ANNOYED_GH_BODY"
printf '%s\n' "$GH_PROMPT_DISABLED" >> "$ANNOYED_GH_PROMPTS"
if [ "$ANNOYED_FAIL_ISSUE" = "1" ]; then
	printf '%s\n' 'auth failed: ghp_abcdefghijklmnopqrstuvwxyz123456' >&2
	exit 1
fi
printf '%s\n' '{"html_url":"https://github.com/Swarm-Code/mono/issues/42"}'
`
	if err := os.WriteFile(ghPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tempDir)
	t.Setenv("ANNOYED_GH_ARGS", argsPath)
	t.Setenv("ANNOYED_GH_BODY", bodyPath)
	t.Setenv("ANNOYED_GH_PROMPTS", promptPath)
	return argsPath, bodyPath, promptPath
}

func readAnnoyedTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
