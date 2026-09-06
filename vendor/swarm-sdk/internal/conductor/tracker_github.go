package conductor

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// GitHubTracker implements IssueTracker against the GitHub REST API v3.
// State is managed via labels, matching the same label model as ForgejoTracker.
type GitHubTracker struct {
	token          string
	owner          string
	repo           string
	activeLabels   []string
	terminalLabels []string
	http           *http.Client
}

// NewGitHubTracker creates a GitHub tracker adapter.
// token is a personal access token or fine-grained PAT with issues:write scope.
func NewGitHubTracker(token, owner, repo string, activeLabels, terminalLabels []string) *GitHubTracker {
	if len(activeLabels) == 0 {
		activeLabels = DefaultActiveLabels
	}
	if len(terminalLabels) == 0 {
		terminalLabels = DefaultTerminalLabels
	}
	return &GitHubTracker{
		token:          token,
		owner:          owner,
		repo:           repo,
		activeLabels:   activeLabels,
		terminalLabels: terminalLabels,
		http:           &http.Client{Timeout: 30 * time.Second},
	}
}

const githubAPIBase = "https://api.github.com"

func (t *GitHubTracker) FetchCandidateIssues(ctx context.Context) ([]Issue, error) {
	var all []Issue
	for _, label := range t.activeLabels {
		issues, err := t.fetchByLabel(ctx, label, "open")
		if err != nil {
			return nil, fmt.Errorf("github: fetch label %q: %w", label, err)
		}
		all = mergeIssues(all, issues)
	}
	return all, nil
}

func (t *GitHubTracker) FetchIssuesByIDs(ctx context.Context, ids []string) ([]Issue, error) {
	var out []Issue
	for _, id := range ids {
		url := fmt.Sprintf("%s/repos/%s/%s/issues/%s", githubAPIBase, t.owner, t.repo, id)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		t.setHeaders(req)
		resp, err := t.http.Do(req)
		if err != nil {
			return nil, err
		}
		var raw githubIssue
		if err := decodeJSON(resp, &raw); err != nil {
			return nil, err
		}
		out = append(out, normaliseGitHubIssue(raw))
	}
	return out, nil
}

func (t *GitHubTracker) FetchTerminalIssueIDs(ctx context.Context) ([]string, error) {
	var ids []string
	seen := map[string]bool{}
	for _, label := range t.terminalLabels {
		issues, err := t.fetchByLabel(ctx, label, "")
		if err != nil {
			return nil, err
		}
		for _, i := range issues {
			if !seen[i.ID] {
				ids = append(ids, i.ID)
				seen[i.ID] = true
			}
		}
	}
	return ids, nil
}

func (t *GitHubTracker) TransitionIssue(ctx context.Context, id, toState string) error {
	// Fetch current labels.
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%s/labels", githubAPIBase, t.owner, t.repo, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	t.setHeaders(req)
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	var labels []struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(resp, &labels); err != nil {
		return err
	}
	// Remove conductor labels.
	for _, l := range labels {
		if isConductorLabel(l.Name) {
			delURL := fmt.Sprintf("%s/repos/%s/%s/issues/%s/labels/%s",
				githubAPIBase, t.owner, t.repo, id, l.Name)
			req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, delURL, nil)
			t.setHeaders(req)
			resp, err := t.http.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
		}
	}
	// Add new label.
	addURL := fmt.Sprintf("%s/repos/%s/%s/issues/%s/labels", githubAPIBase, t.owner, t.repo, id)
	payload := fmt.Sprintf(`{"labels":[%s]}`, jsonString(toState))
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, addURL, strings.NewReader(payload))
	if err != nil {
		return err
	}
	t.setHeaders(req)
	resp, err = t.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (t *GitHubTracker) PostComment(ctx context.Context, id, body string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%s/comments", githubAPIBase, t.owner, t.repo, id)
	payload := fmt.Sprintf(`{"body":%s}`, jsonString(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return err
	}
	t.setHeaders(req)
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

type githubIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (t *GitHubTracker) fetchByLabel(ctx context.Context, label, state string) ([]Issue, error) {
	page := 1
	var all []Issue
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/issues?labels=%s&per_page=100&page=%d",
			githubAPIBase, t.owner, t.repo, label, page)
		if state != "" {
			url += "&state=" + state
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		t.setHeaders(req)
		resp, err := t.http.Do(req)
		if err != nil {
			return nil, err
		}
		var batch []githubIssue
		if err := decodeJSON(resp, &batch); err != nil {
			return nil, err
		}
		for _, raw := range batch {
			all = append(all, normaliseGitHubIssue(raw))
		}
		if len(batch) < 100 {
			break
		}
		page++
	}
	return all, nil
}

func (t *GitHubTracker) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func normaliseGitHubIssue(raw githubIssue) Issue {
	labels := make([]string, 0, len(raw.Labels))
	state := ""
	for _, l := range raw.Labels {
		name := strings.ToLower(l.Name)
		labels = append(labels, name)
		if isConductorLabel(l.Name) && state == "" {
			state = name
		}
	}
	if state == "" {
		state = raw.State
	}
	return Issue{
		ID:          fmt.Sprintf("%d", raw.Number),
		Identifier:  fmt.Sprintf("#%d", raw.Number),
		Title:       raw.Title,
		Description: raw.Body,
		State:       state,
		Labels:      labels,
		URL:         raw.HTMLURL,
		CreatedAt:   raw.CreatedAt,
		UpdatedAt:   raw.UpdatedAt,
	}
}
