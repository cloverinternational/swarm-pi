package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

type annoyedGitHubIssue struct {
	HTMLURL string `json:"html_url"`
}

func reportGitHubFeedbackIssue(ctx context.Context, publication FeedbackPublication) (string, error) {
	payload := map[string]any{
		"title": publication.Title,
		"body":  publication.Body,
	}
	var created annoyedGitHubIssue
	if err := annoyedGHAPI(ctx, "POST", "repos/"+publication.Repository+"/issues", payload, &created); err != nil {
		return "", err
	}
	if err := validateAnnoyedIssueURL(created.HTMLURL); err != nil {
		return "", err
	}
	return created.HTMLURL, nil
}

func validateAnnoyedIssueURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || !strings.Contains(parsed.Path, "/issues/") {
		return fmt.Errorf("annoyed: gh returned invalid issue URL")
	}
	return nil
}

func annoyedGHAPI(ctx context.Context, method, endpoint string, input, output any) error {
	args := []string{"api", "--method", method, endpoint}
	var stdin bytes.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("annoyed: encode GitHub API request: %w", err)
		}
		stdin.Reset(encoded)
		args = append(args, "--input", "-")
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	if input != nil {
		cmd.Stdin = &stdin
	}
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := sanitizeAnnoyedError(stderr.String())
		if detail == "" {
			return fmt.Errorf("annoyed: GitHub API %s %s: %w", method, endpoint, err)
		}
		return fmt.Errorf("annoyed: GitHub API %s %s: %w: %s", method, endpoint, err, detail)
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
		return fmt.Errorf("annoyed: decode GitHub API %s %s response: %w", method, endpoint, err)
	}
	return nil
}
