// Harness Phase 5b — client-side STATIC skill registration on the closed path.
//
// buildHarnessSkillTool loads the EXACT skills a compiled *harness.Plan selected
// from within the manifest directory (Plan.RevealSkillBaseDir) and wires a single
// `Skill` invocation tool over a FRESH, EMPTY skills.Registry. It is the closed,
// autoskills-OFF counterpart to client.go's autogenskills wiring:
//
//   - NO autogenskills.Service, NO SkillManage tool, NO watcher, NO AutoActivate,
//     NO budget hook, NO ambient discovery (~/.swarm, cwd, HOME, env).
//   - Skills load ONLY from inside the manifest subtree; a path escaping the base
//     (via "../" or a symlink) is rejected.
//   - FAIL-CLOSED everywhere: empty base, missing/unreadable file, content-hash
//     mismatch, or an ambiguous/absent id-only match returns an error so the
//     caller (initHarnessAgent) aborts and NO agent becomes observable.
//
// When the plan selects zero skills, the helper returns (nil, nil, false, nil)
// and the caller registers nothing — behavior is byte-for-byte unchanged.
package client

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/skilltools"
)

// buildHarnessSkillTool loads the plan's selected skills into a fresh registry
// and returns a single `Skill` tool over them. It returns:
//
//   - (nil, nil, false, nil) when the plan selected zero skills;
//   - (tool, reg, true, nil) when skills loaded cleanly;
//   - (nil, nil, false, err) fail-closed on any resolution/integrity/load error.
//
// The caller stores reg (e.g. on c.skillRegistry) so the loaded skills share the
// registry the tool resolves against. sessionIDGetter feeds ${SWARM_SESSION_ID}
// substitution; ForkExecutor is intentionally nil (inline-only in 5b).
func buildHarnessSkillTool(
	plan *harness.Plan,
	logger observability.Logger,
	tracer observability.Tracer,
	sessionIDGetter func() string,
) (tools.Tool, *skills.Registry, bool, error) {
	if plan == nil {
		return nil, nil, false, fmt.Errorf("harness: buildHarnessSkillTool: nil plan")
	}
	specs := plan.Skills()
	if len(specs) == 0 {
		// Zero skills selected: register nothing, keep the closed path unchanged.
		return nil, nil, false, nil
	}

	base := plan.RevealSkillBaseDir()
	if base == "" {
		// Fail closed: skills were selected but there is no trusted base to load
		// them from. Never fall back to cwd/HOME/ambient discovery.
		return nil, nil, false, fmt.Errorf("harness: skills selected but plan has no skill base directory")
	}

	// realBase is the symlink-evaluated manifest dir, used for containment so a
	// base that itself lives under a symlinked root (e.g. /tmp) compares cleanly.
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		realBase = filepath.Clean(base)
	}

	// Fresh, EMPTY registry: no search paths, no remotes, no autoskills.
	reg := skills.NewRegistry()
	roots := plan.SkillSearchRoots()

	for _, spec := range specs {
		loadDir, hashTarget, rerr := resolveHarnessSkillTarget(base, realBase, roots, spec)
		if rerr != nil {
			return nil, nil, false, rerr
		}

		// Integrity: re-verify the recorded content hash against the file NOW
		// (it may have changed since compile). Fail closed on mismatch.
		if spec.ContentHash != "" {
			got, herr := hashHarnessSkillFile(hashTarget)
			if herr != nil {
				return nil, nil, false, fmt.Errorf("harness: skill %q: %w", spec.ID, herr)
			}
			if got != spec.ContentHash {
				return nil, nil, false, fmt.Errorf(
					"harness: skill %q content hash mismatch (compiled %s, on-disk %s); refusing to load",
					spec.ID, spec.ContentHash, got)
			}
		}

		if _, lerr := reg.LoadFromPath(loadDir); lerr != nil {
			return nil, nil, false, fmt.Errorf("harness: load skill %q from %s: %w", spec.ID, loadDir, lerr)
		}
	}

	skillTool, terr := skilltools.NewSkillTool(skilltools.SkillToolConfig{
		Registry:        reg,
		Logger:          logger,
		Tracer:          tracer,
		SessionIDGetter: sessionIDGetter,
		ForkExecutor:    nil, // inline-only in Phase 5b.
	})
	if terr != nil {
		return nil, nil, false, fmt.Errorf("harness: build Skill tool: %w", terr)
	}
	return skillTool, reg, true, nil
}

// resolveHarnessSkillTarget resolves one skill spec to (loadDir, hashTarget)
// within the manifest subtree, fail-closed. loadDir is the directory containing
// SKILL.md (what skills.Registry.LoadFromPath expects); hashTarget is the
// SKILL.md file whose content hash is verified.
func resolveHarnessSkillTarget(base, realBase string, roots []string, spec harness.SkillSpec) (string, string, error) {
	if spec.Path != "" {
		// Path-backed: resolve manifest-relative and re-verify containment.
		return resolveContainedSkillPath(base, realBase, spec.Path, spec.ID)
	}

	// Id-only: search each declared root for base/<root>/<id>/SKILL.md. Exactly
	// one distinct existing match is required (zero or ambiguous => fail closed).
	type match struct{ loadDir, hashTarget string }
	seen := make(map[string]struct{})
	var matches []match
	for _, root := range roots {
		rel := filepath.Join(root, spec.ID)
		loadDir, hashTarget, err := resolveContainedSkillPath(base, realBase, rel, spec.ID)
		if err != nil {
			// A missing candidate under one root is not fatal on its own; keep
			// scanning the other roots. Only a containment/escape error is a hard
			// stop — surface it immediately.
			if isSkillContainmentError(err) {
				return "", "", err
			}
			continue
		}
		if _, dup := seen[loadDir]; dup {
			continue
		}
		seen[loadDir] = struct{}{}
		matches = append(matches, match{loadDir: loadDir, hashTarget: hashTarget})
	}
	switch len(matches) {
	case 0:
		return "", "", fmt.Errorf("harness: id-only skill %q not found under any declared search root", spec.ID)
	case 1:
		return matches[0].loadDir, matches[0].hashTarget, nil
	default:
		return "", "", fmt.Errorf("harness: id-only skill %q is ambiguous: %d distinct matches under search roots", spec.ID, len(matches))
	}
}

// skillContainmentErr marks a hard containment/escape failure so id-only scanning
// stops immediately instead of treating it like a benign missing candidate.
type skillContainmentErr struct{ err error }

func (e skillContainmentErr) Error() string { return e.err.Error() }

func isSkillContainmentError(err error) bool {
	_, ok := err.(skillContainmentErr)
	return ok
}

// resolveContainedSkillPath resolves rel against base, enforces the containment +
// symlink policy (mirroring harness.resolveContainedFile), and classifies the
// result into (loadDir, hashTarget). A "../"/symlink escape returns a
// skillContainmentErr; a missing target returns a plain error.
func resolveContainedSkillPath(base, realBase, rel, id string) (string, string, error) {
	var joined string
	if filepath.IsAbs(rel) {
		joined = filepath.Clean(rel)
	} else {
		joined = filepath.Clean(filepath.Join(base, rel))
	}

	// Pre-symlink containment: reject "../" escapes early.
	if skillEscapesDir(realBase, joined) {
		return "", "", skillContainmentErr{err: fmt.Errorf(
			"harness: skill %q path escapes the manifest directory", id)}
	}

	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("harness: skill %q target does not exist: %s", id, rel)
		}
		return "", "", fmt.Errorf("harness: skill %q target could not be resolved: %w", id, err)
	}

	// Post-symlink containment: the real target must also stay within the subtree.
	if skillEscapesDir(realBase, real) {
		return "", "", skillContainmentErr{err: fmt.Errorf(
			"harness: skill %q resolves (via symlink) outside the manifest directory", id)}
	}

	info, err := os.Stat(real)
	if err != nil {
		return "", "", fmt.Errorf("harness: skill %q target could not be read: %w", id, err)
	}
	if info.IsDir() {
		hashTarget := filepath.Join(real, "SKILL.md")
		if _, serr := os.Stat(hashTarget); serr != nil {
			return "", "", fmt.Errorf("harness: skill %q directory does not contain SKILL.md", id)
		}
		return real, hashTarget, nil
	}
	// A file target is the SKILL.md itself; its directory is the load dir.
	return filepath.Dir(real), real, nil
}

// skillEscapesDir reports whether target is outside root's subtree. Both must be
// clean absolute paths. A target equal to root does not escape. It mirrors the
// harness-package escapesDir, replicated here because that helper is unexported.
func skillEscapesDir(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return true
	}
	if rel == "." {
		return false
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// hashHarnessSkillFile computes "sha256:<hex>" of the target file, matching the
// harness-package hashBytes exactly so a compile-time ContentHash can be
// re-verified byte-for-byte at load time.
func hashHarnessSkillFile(target string) (string, error) {
	data, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("referenced skill file could not be read: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
