package backfill

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	sdkanalytics "github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
)

func backfillArtifacts(ctx context.Context, opts Options, s *sender, index workspaceIndex) error {
	projectsRoot := filepath.Join(opts.SwarmDir, "projects")
	entries, err := os.ReadDir(projectsRoot)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	workspaceProjectHash := ""
	if opts.Workspace != "" {
		workspaceProjectHash = projectHashForWorkspace(opts.Workspace)
	}
	for _, entry := range entries {
		if s.result.limitReached(opts.Limit) {
			return nil
		}
		if !entry.IsDir() {
			continue
		}
		projectHash := entry.Name()
		projectDir := filepath.Join(projectsRoot, projectHash)
		workspacePath := index.byProjectHash[projectHash]
		if workspaceProjectHash != "" && projectHash != workspaceProjectHash {
			continue
		}
		if workspacePath == "" && workspaceProjectHash == projectHash {
			workspacePath = opts.Workspace
		}
		if err := scanProjectArtifacts(ctx, opts, s, projectDir, projectHash, workspacePath); err != nil {
			s.result.Errors = append(s.result.Errors, err.Error())
		}
	}

	if !s.result.limitReached(opts.Limit) {
		if err := scanWorkspaceLocalArtifacts(ctx, opts, s, index); err != nil {
			s.result.Errors = append(s.result.Errors, err.Error())
		}
	}
	if opts.Workspace == "" && !s.result.limitReached(opts.Limit) {
		if err := scanGoldDir(ctx, opts, s, filepath.Join(opts.SwarmDir, "gold"), "", ""); err != nil {
			s.result.Errors = append(s.result.Errors, err.Error())
		}
	}
	if opts.Workspace == "" && !s.result.limitReached(opts.Limit) {
		if err := scanFindingsDir(ctx, opts, s, opts.LegacyFindingsDir, "", ""); err != nil {
			s.result.Errors = append(s.result.Errors, err.Error())
		}
	}
	return nil
}

func scanWorkspaceLocalArtifacts(ctx context.Context, opts Options, s *sender, index workspaceIndex) error {
	workspaces := index.workspaces()
	if opts.Workspace != "" && !slicesContains(workspaces, opts.Workspace) {
		workspaces = append(workspaces, opts.Workspace)
	}
	for _, workspacePath := range workspaces {
		if s.result.limitReached(opts.Limit) {
			return nil
		}
		if opts.Workspace != "" && workspacePath != opts.Workspace {
			continue
		}
		projectHash := projectHashForWorkspace(workspacePath)
		localSwarmDir := filepath.Join(workspacePath, ".swarm")
		if err := scanBronzeRoot(ctx, opts, s, filepath.Join(localSwarmDir, "bronze"), projectHash, workspacePath); err != nil {
			s.result.Errors = append(s.result.Errors, err.Error())
		}
		if s.result.limitReached(opts.Limit) {
			return nil
		}
		if err := scanLegacyGoldMarkdownDir(ctx, opts, s, filepath.Join(localSwarmDir, "gold"), projectHash, workspacePath); err != nil {
			s.result.Errors = append(s.result.Errors, err.Error())
		}
	}
	return nil
}

func scanProjectArtifacts(ctx context.Context, opts Options, s *sender, projectDir, projectHash, workspacePath string) error {
	scanners := []struct {
		dir string
		fn  func(context.Context, Options, *sender, string, string, string) error
	}{
		{dir: projectDir, fn: scanBronzeDirs},
		{dir: projectDir, fn: scanFindingsDir},
		{dir: projectDir, fn: scanSilverDir},
		{dir: filepath.Join(projectDir, "gold"), fn: scanGoldDir},
	}
	for _, scanner := range scanners {
		if s.result.limitReached(opts.Limit) {
			return nil
		}
		if err := scanner.fn(ctx, opts, s, scanner.dir, projectHash, workspacePath); err != nil {
			s.result.Errors = append(s.result.Errors, err.Error())
		}
	}
	return nil
}

func scanBronzeDirs(ctx context.Context, opts Options, s *sender, projectDir, projectHash, workspacePath string) error {
	for _, dir := range []string{
		filepath.Join(projectDir, "bronze"),
		filepath.Join(projectDir, "findings", "bronze"),
	} {
		if err := scanBronzeRoot(ctx, opts, s, dir, projectHash, workspacePath); err != nil {
			return err
		}
	}
	return nil
}

func scanBronzeRoot(ctx context.Context, opts Options, s *sender, dir, projectHash, workspacePath string) error {
	return walkFiles(dir, func(path string, info fs.FileInfo) error {
		if s.result.limitReached(opts.Limit) {
			return filepath.SkipAll
		}
		if !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		return scanBronzeJSONL(ctx, opts, s, path, projectHash, workspacePath)
	})
}

func scanBronzeJSONL(ctx context.Context, opts Options, s *sender, path, projectHash, workspacePath string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		if s.result.limitReached(opts.Limit) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var bronze map[string]any
		if err := json.Unmarshal([]byte(line), &bronze); err != nil {
			s.result.Errors = append(s.result.Errors, fmt.Sprintf("%s:%d: %v", path, lineNo, err))
			continue
		}
		occurredAt := anyTime(bronze["ts"], bronze["timestamp"], bronze["occurred_at"])
		if occurredAt.IsZero() {
			occurredAt = fileModTime(path)
		}
		conversationID := mapString(bronze, "conv_id", "conversation_id")
		artifactID := stableID("artifact", "bronze_event", path, fmt.Sprint(lineNo), mapString(bronze, "event_id"))
		payload := map[string]any{
			"bronze_event":  bronze,
			"path":          path,
			"artifact_path": path,
			"project_hash":  projectHash,
			"backfill":      true,
		}
		if err := emitArtifact(opts, s, "bronze_event", artifactID, occurredAt, workspacePath, projectHash, conversationID, conversationID, path, payload); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func scanFindingsDir(ctx context.Context, opts Options, s *sender, dir, projectHash, workspacePath string) error {
	findingsRoot := dir
	if projectHash != "" {
		findingsRoot = filepath.Join(dir, "findings")
	}
	return walkFiles(findingsRoot, func(path string, info fs.FileInfo) error {
		if s.result.limitReached(opts.Limit) {
			return filepath.SkipAll
		}
		if !strings.HasSuffix(info.Name(), ".jsonl") || !strings.Contains(path, string(filepath.Separator)+"cache"+string(filepath.Separator)) {
			return nil
		}
		return scanFindingsJSONL(ctx, opts, s, path, projectHash, workspacePath)
	})
}

func scanFindingsJSONL(ctx context.Context, opts Options, s *sender, path, projectHash, workspacePath string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		if s.result.limitReached(opts.Limit) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		lineNo++
		var finding map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &finding); err != nil {
			s.result.Errors = append(s.result.Errors, fmt.Sprintf("%s:%d: %v", path, lineNo, err))
			continue
		}
		occurredAt := anyTime(finding["timestamp"], finding["created_at"], finding["captured_at"])
		if occurredAt.IsZero() {
			occurredAt = fileModTime(path)
		}
		findingID := mapString(finding, "finding_id", "id")
		conversationID := mapString(finding, "conversation_id", "conv_id")
		artifactID := stableID("artifact", "finding", path, fmt.Sprint(lineNo), findingID)
		payload := map[string]any{
			"finding":       finding,
			"path":          path,
			"artifact_path": path,
			"project_hash":  projectHash,
			"backfill":      true,
		}
		if err := emitArtifact(opts, s, "finding", artifactID, occurredAt, workspacePath, projectHash, conversationID, conversationID, path, payload); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func scanSilverDir(ctx context.Context, opts Options, s *sender, projectDir, projectHash, workspacePath string) error {
	silverDir := filepath.Join(projectDir, "silver")
	return walkFiles(silverDir, func(path string, info fs.FileInfo) error {
		if s.result.limitReached(opts.Limit) {
			return filepath.SkipAll
		}
		if !strings.HasPrefix(info.Name(), "tree_") || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		tree, err := readJSONMap(path)
		if err != nil {
			s.result.Errors = append(s.result.Errors, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		occurredAt := anyTime(tree["generated_at"], tree["start_time"])
		if occurredAt.IsZero() {
			occurredAt = fileModTime(path)
		}
		conversationID := mapString(tree, "conversation_id", "conv_id")
		payload := map[string]any{
			"tree":          tree,
			"path":          path,
			"artifact_path": path,
			"project_hash":  projectHash,
			"backfill":      true,
		}
		artifactID := stableID("artifact", "silver_tree", path, stableContentHash(tree))
		return emitArtifact(opts, s, "silver_tree", artifactID, occurredAt, workspacePath, projectHash, conversationID, conversationID, path, payload)
	})
}

func scanGoldDir(ctx context.Context, opts Options, s *sender, goldDir, projectHash, workspacePath string) error {
	if err := scanGoldRuns(ctx, opts, s, goldDir, projectHash, workspacePath); err != nil {
		return err
	}
	return scanGoldInsights(ctx, opts, s, goldDir, projectHash, workspacePath)
}

func scanGoldRuns(ctx context.Context, opts Options, s *sender, goldDir, projectHash, workspacePath string) error {
	return walkFiles(filepath.Join(goldDir, "runs"), func(path string, info fs.FileInfo) error {
		if s.result.limitReached(opts.Limit) {
			return filepath.SkipAll
		}
		if !strings.HasPrefix(info.Name(), "run_") || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		run, err := readJSONMap(path)
		if err != nil {
			s.result.Errors = append(s.result.Errors, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		occurredAt := anyTime(run["started_at"], run["completed_at"])
		if occurredAt.IsZero() {
			occurredAt = fileModTime(path)
		}
		runID := mapString(run, "id", "run_id")
		payloadProjectHash := projectHash
		if payloadProjectHash == "" {
			payloadProjectHash = mapString(run, "project_hash")
		}
		payload := map[string]any{
			"run":                run,
			"run_id":             runID,
			"trees_analyzed":     run["trees_analyzed"],
			"insights_generated": run["insights_generated"],
			"duration_ms":        run["duration_ms"],
			"path":               path,
			"artifact_path":      path,
			"project_hash":       payloadProjectHash,
			"backfill":           true,
		}
		artifactID := stableID("artifact", "gold_run", path, runID)
		return emitArtifact(opts, s, "gold_run", artifactID, occurredAt, workspacePath, payloadProjectHash, "", "", path, payload)
	})
}

func scanGoldInsights(ctx context.Context, opts Options, s *sender, goldDir, projectHash, workspacePath string) error {
	return walkFiles(filepath.Join(goldDir, "insights"), func(path string, info fs.FileInfo) error {
		if s.result.limitReached(opts.Limit) {
			return filepath.SkipAll
		}
		if !strings.HasPrefix(info.Name(), "insights_") || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var insights []map[string]any
		if err := json.Unmarshal(body, &insights); err != nil {
			var single map[string]any
			if singleErr := json.Unmarshal(body, &single); singleErr != nil {
				s.result.Errors = append(s.result.Errors, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			insights = []map[string]any{single}
		}
		for i, insight := range insights {
			if s.result.limitReached(opts.Limit) {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			occurredAt := anyTime(insight["generated_at"])
			if occurredAt.IsZero() {
				occurredAt = fileModTime(path)
			}
			insightID := mapString(insight, "id", "insight_id")
			runID := mapString(insight, "analysis_run_id", "run_id")
			payloadProjectHash := projectHash
			if payloadProjectHash == "" {
				payloadProjectHash = mapString(insight, "project_hash")
			}
			payload := map[string]any{
				"insight":       insight,
				"insight_id":    insightID,
				"run_id":        runID,
				"title":         mapString(insight, "title"),
				"category":      mapString(insight, "category"),
				"severity":      mapString(insight, "severity"),
				"path":          path,
				"artifact_path": path,
				"project_hash":  payloadProjectHash,
				"backfill":      true,
			}
			artifactID := stableID("artifact", "gold_insight", path, fmt.Sprint(i), insightID)
			if err := emitArtifact(opts, s, "gold_insight", artifactID, occurredAt, workspacePath, payloadProjectHash, "", "", path, payload); err != nil {
				return err
			}
		}
		return nil
	})
}

func scanLegacyGoldMarkdownDir(ctx context.Context, opts Options, s *sender, goldDir, projectHash, workspacePath string) error {
	return walkFiles(goldDir, func(path string, info fs.FileInfo) error {
		if s.result.limitReached(opts.Limit) {
			return filepath.SkipAll
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		metadata, content := parseMarkdownFrontmatter(string(body))
		occurredAt := anyTime(metadata["timestamp"], metadata["generated_at"], metadata["created_at"])
		if occurredAt.IsZero() {
			occurredAt = fileModTime(path)
		}
		title := mapString(metadata, "title")
		if title == "" {
			title = strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
		}
		insightID := stableID("legacy-gold-markdown", path, stableContentHash(string(body)))
		insight := map[string]any{
			"id":            insightID,
			"title":         title,
			"category":      mapString(metadata, "category"),
			"severity":      mapString(metadata, "severity"),
			"content":       content,
			"metadata":      metadata,
			"legacy_format": true,
		}
		payload := map[string]any{
			"insight":       insight,
			"insight_id":    insightID,
			"title":         title,
			"category":      insight["category"],
			"severity":      insight["severity"],
			"path":          path,
			"artifact_path": path,
			"project_hash":  projectHash,
			"legacy_format": true,
			"backfill":      true,
		}
		artifactID := stableID("artifact", "gold_legacy_markdown", path, stableContentHash(string(body)))
		return emitArtifact(opts, s, "gold_insight", artifactID, occurredAt, workspacePath, projectHash, "", "", path, payload)
	})
}

func emitArtifact(opts Options, s *sender, artifactType, artifactID string, occurredAt time.Time, workspacePath, projectHash, sessionID, conversationID, path string, payload map[string]any) error {
	if !withinWindow(opts, occurredAt) {
		s.result.SkippedFilter++
		return nil
	}
	if projectHash != "" {
		payload["project_hash"] = projectHash
	}
	return s.enqueueArtifact(sdkanalytics.ArtifactEnvelope{
		ArtifactID:     artifactID,
		ArtifactType:   artifactType,
		OccurredAt:     occurredAt.UTC(),
		SessionID:      sessionID,
		ConversationID: conversationID,
		WorkspaceHash:  sdkanalytics.WorkspaceHash(s.cfg.WorkspaceNamespace, workspacePath),
		WorkspaceLabel: sdkanalytics.WorkspaceLabel(workspacePath),
		ProjectHash:    projectHash,
		ArtifactPath:   path,
		Payload:        payload,
	}, path)
}

func withinWindow(opts Options, occurredAt time.Time) bool {
	if occurredAt.IsZero() {
		return true
	}
	if !opts.Since.IsZero() && occurredAt.Before(opts.Since) {
		return false
	}
	if !opts.Until.IsZero() && occurredAt.After(opts.Until) {
		return false
	}
	return true
}

func walkFiles(root string, fn func(string, fs.FileInfo) error) error {
	if root == "" {
		return nil
	}
	if info, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	} else if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(path, info)
	})
}

func parseMarkdownFrontmatter(markdown string) (map[string]any, string) {
	metadata := make(map[string]any)
	if !strings.HasPrefix(markdown, "---\n") {
		return metadata, markdown
	}
	rest := strings.TrimPrefix(markdown, "---\n")
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return metadata, markdown
	}
	frontmatter := before
	body := strings.TrimPrefix(after, "\n")
	for line := range strings.SplitSeq(frontmatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		metadata[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return metadata, body
}

func slicesContains(values []string, value string) bool {
	return slices.Contains(values, value)
}
