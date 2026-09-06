// Package configmigrate performs a one-time migration of legacy ~/.swarmos
// data into the canonical ~/.swarm root.
//
// Migrate is idempotent: it writes a marker file on completion and becomes a
// no-op thereafter. It never touches the developer's live data unless the
// paths.Root() (via SWARM_HOME) and the home hook point at test temp dirs.
package configmigrate

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// homeDir resolves the user's home directory. It is overridable in tests so
// the legacy ~/.swarmos source root can be redirected to a temp dir.
var homeDir = os.UserHomeDir

// legacyDirName is the legacy root directory name under the home directory.
const legacyDirName = ".swarmos"

// markerName is the sentinel written under config/ once migration completes.
const markerName = ".migrated_from_swarmos"

// Report summarizes a migration run.
type Report struct {
	Moved     []string // canonical dest paths that were populated from legacy data
	Skipped   []string // legacy paths skipped (bak/corrupt/empty/clobbered)
	Conflicts []string // canonical dest paths that already existed (dest wins)
}

// Run is a best-effort startup entry point: it runs Migrate, logs the report
// via the standard logger, and never panics.
func Run() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("configmigrate: recovered from panic: %v", r)
		}
	}()
	rep, err := Migrate()
	if err != nil {
		log.Printf("configmigrate: migration error: %v", err)
		return
	}
	if len(rep.Moved) == 0 && len(rep.Skipped) == 0 && len(rep.Conflicts) == 0 {
		return
	}
	log.Printf("configmigrate: moved=%d skipped=%d conflicts=%d",
		len(rep.Moved), len(rep.Skipped), len(rep.Conflicts))
	for _, m := range rep.Moved {
		log.Printf("configmigrate: moved -> %s", m)
	}
	for _, c := range rep.Conflicts {
		log.Printf("configmigrate: conflict (kept dest) %s", c)
	}
	for _, s := range rep.Skipped {
		log.Printf("configmigrate: skipped %s", s)
	}
}

// legacyRoot returns the legacy ~/.swarmos root path.
func legacyRoot() (string, error) {
	h, err := homeDir()
	if err != nil || h == "" {
		return "", fmt.Errorf("configmigrate: cannot resolve home: %w", err)
	}
	return filepath.Join(h, legacyDirName), nil
}

// shouldSkip reports whether a legacy file name matches a skip pattern:
// *.bak*, *.corrupt*, *_empty, *.clobbered*.
func shouldSkip(name string) bool {
	base := filepath.Base(name)
	switch {
	case strings.Contains(base, ".bak"):
		return true
	case strings.Contains(base, ".corrupt"):
		return true
	case strings.Contains(base, ".clobbered"):
		return true
	case strings.HasSuffix(base, "_empty"):
		return true
	default:
		return false
	}
}

// Migrate moves/merges known legacy files into their canonical ~/.swarm
// locations. It is idempotent via a marker file.
func Migrate() (Report, error) {
	var rep Report

	markerPath := paths.In("config", markerName)
	if _, err := os.Stat(markerPath); err == nil {
		// Already migrated.
		return rep, nil
	}

	src, err := legacyRoot()
	if err != nil {
		return rep, err
	}
	if info, statErr := os.Stat(src); statErr != nil || !info.IsDir() {
		// Source missing: no-op, do not write marker.
		return rep, nil
	}

	// Record any skip-pattern files anywhere in the legacy tree.
	collectSkips(src, &rep)

	// Single-file concerns: map canonical dest -> ordered legacy candidates.
	migrateSingleFiles(src, &rep)

	// OAuth files: *_oauth.json / oauth.json / config/oauth/*.json.
	migrateOAuth(src, &rep)

	// Directory concerns: merge (prefer existing dest files).
	migrateDirs(src, &rep)

	// Write the marker so this runs exactly once.
	if err := atomicfile.Write(markerPath, []byte("migrated from "+legacyDirName+"\n")); err != nil {
		return rep, fmt.Errorf("configmigrate: write marker: %w", err)
	}
	return rep, nil
}

// collectSkips walks the legacy tree and records skip-pattern files.
func collectSkips(src string, rep *Report) {
	_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if shouldSkip(p) {
			rep.Skipped = append(rep.Skipped, p)
		}
		return nil
	})
}

// migrateSingleFiles handles the flat config files with possible duplicate
// legacy locations (root vs config/). The larger/most-recent candidate wins.
func migrateSingleFiles(src string, rep *Report) {
	type concern struct {
		dest       string
		candidates []string
	}
	// Legacy files may live at the legacy root OR under legacy config/.
	pair := func(name string) []string {
		return []string{
			filepath.Join(src, name),
			filepath.Join(src, "config", name),
		}
	}
	concerns := []concern{
		{paths.ProvidersFile(), pair("providers.json")},
		{paths.CredentialsFile(), pair("credentials.json")},
		{paths.HooksFile(), pair("hooks.json")},
		{paths.McpServersFile(), pair("mcp_servers.json")},
		{paths.AgentProfilesFile(), pair("agent_profiles.json")},
		{paths.ConfigFile(), pair("config.yaml")},
		{paths.SystemPromptsFile(), pair("system_prompts.yaml")},
		{paths.CustomAgentsFile(), pair("custom_agents.yaml")},
		{paths.A2ARegistryFile(), pair("a2a-registry.sqlite")},
		{paths.In("tui_accounts.json"), []string{filepath.Join(src, "tui_accounts.json")}},
		{paths.In("cloud.json"), []string{filepath.Join(src, "cloud.json")}},
		{paths.In("cloud_tokens.json"), []string{filepath.Join(src, "cloud_tokens.json")}},
	}
	for _, c := range concerns {
		best := pickBest(c.candidates, rep)
		if best == "" {
			continue
		}
		moveOne(best, c.dest, rep)
	}
}

// pickBest returns the largest (tie: most-recent) existing, non-skipped
// candidate path. Skipped candidates are recorded.
func pickBest(candidates []string, rep *Report) string {
	type cand struct {
		path string
		size int64
		mod  int64
	}
	var valid []cand
	for _, p := range candidates {
		if shouldSkip(p) {
			rep.Skipped = appendUnique(rep.Skipped, p)
			continue
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		valid = append(valid, cand{p, info.Size(), info.ModTime().UnixNano()})
	}
	if len(valid) == 0 {
		return ""
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].size != valid[j].size {
			return valid[i].size > valid[j].size
		}
		return valid[i].mod > valid[j].mod
	})
	return valid[0].path
}

// moveOne copies srcPath to destPath unless dest already exists (dest wins).
func moveOne(srcPath, destPath string, rep *Report) {
	if _, err := os.Stat(destPath); err == nil {
		rep.Conflicts = appendUnique(rep.Conflicts, destPath)
		return
	}
	if err := copyToDest(srcPath, destPath); err != nil {
		log.Printf("configmigrate: copy %s -> %s: %v", srcPath, destPath, err)
		return
	}
	rep.Moved = appendUnique(rep.Moved, destPath)
}

// migrateOAuth discovers legacy oauth files and places them under
// config/oauth/<provider>.json.
func migrateOAuth(src string, rep *Report) {
	// Search locations: legacy root, legacy config/, legacy config/oauth/.
	searchDirs := []string{
		src,
		filepath.Join(src, "config"),
		filepath.Join(src, "config", "oauth"),
	}
	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		inOAuthDir := filepath.Base(dir) == "oauth"
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			full := filepath.Join(dir, name)
			if shouldSkip(name) {
				rep.Skipped = appendUnique(rep.Skipped, full)
				continue
			}
			provider, ok := oauthProvider(name, inOAuthDir)
			if !ok {
				continue
			}
			moveOne(full, paths.OAuthFile(provider), rep)
		}
	}
}

// oauthProvider derives the provider name from a legacy oauth file name.
// In an oauth/ directory, any *.json is <provider>.json. Elsewhere only
// *_oauth.json and oauth.json qualify.
func oauthProvider(name string, inOAuthDir bool) (string, bool) {
	if !strings.HasSuffix(name, ".json") {
		return "", false
	}
	if inOAuthDir {
		return strings.TrimSuffix(name, ".json"), true
	}
	if name == "oauth.json" {
		// The historical bare OAuth file was Anthropic's credential.
		return "anthropic", true
	}
	if strings.HasSuffix(name, "_oauth.json") {
		return strings.TrimSuffix(name, "_oauth.json"), true
	}
	return "", false
}

// migrateDirs merges legacy directory trees into their canonical dests,
// preferring any existing dest file.
func migrateDirs(src string, rep *Report) {
	type dirConcern struct {
		srcDir  string
		destDir string
	}
	dirs := []dirConcern{
		{filepath.Join(src, "skills"), paths.SkillsDir()},
		{filepath.Join(src, "conversations"), paths.ConversationsDir()},
		{filepath.Join(src, "vault"), paths.VaultDir()},
		{filepath.Join(src, "swarms"), paths.In("swarms")},
		{filepath.Join(src, "deepwiki"), paths.In("deepwiki")},
		{filepath.Join(src, "findings"), paths.In("findings")},
		{filepath.Join(src, "analytics-spool"), paths.In("analytics-spool")},
	}
	for _, dc := range dirs {
		mergeDir(dc.srcDir, dc.destDir, rep)
	}
}

// mergeDir copies every non-skipped file from srcDir into destDir preserving
// the relative layout, never overwriting an existing dest file.
func mergeDir(srcDir, destDir string, rep *Report) {
	info, err := os.Stat(srcDir)
	if err != nil || !info.IsDir() {
		return
	}
	_ = filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if shouldSkip(p) {
			rep.Skipped = appendUnique(rep.Skipped, p)
			return nil
		}
		rel, relErr := filepath.Rel(srcDir, p)
		if relErr != nil {
			return nil
		}
		dest := filepath.Join(destDir, rel)
		moveOne(p, dest, rep)
		return nil
	})
}

// copyToDest reads srcPath and writes it to destPath robustly at 0600.
func copyToDest(srcPath, destPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(destPath, data, atomicfile.AllowEmpty())
}

// appendUnique appends s to list if not already present.
func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
