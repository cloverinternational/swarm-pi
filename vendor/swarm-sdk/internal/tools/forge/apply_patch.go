// Package forge: canonical apply_patch — the single model-visible file
// mutation tool. It accepts the Codex V4A patch envelope (Add/Update/Delete
// File plus Move to), preflights every operation before touching disk, and
// commits transactionally with undo snapshots and reverse-order rollback.
package forge

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/filetracker"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ─── Patch model ─────────────────────────────────────────────────────────────

type patchOpKind int

const (
	patchOpAdd patchOpKind = iota
	patchOpUpdate
	patchOpDelete
)

type patchChunk struct {
	ctxOffset int
	del       []string
	ins       []string
}

type patchHunk struct {
	anchors []string
	context []string
	chunks  []patchChunk
	eof     bool
}

type patchOp struct {
	kind    patchOpKind
	path    string
	moveTo  string
	addBody string
	hunks   []patchHunk
}

// ─── Parser (patch text → ordered ops, no filesystem access) ─────────────────

const (
	patchBegin   = "*** Begin Patch"
	patchEnd     = "*** End Patch"
	patchEOF     = "*** End of File"
	hdrUpdate    = "*** Update File: "
	hdrDelete    = "*** Delete File: "
	hdrAdd       = "*** Add File: "
	hdrMoveTo    = "*** Move to: "
	anchorPrefix = "@@"
)

func parsePatch(input string) ([]patchOp, error) {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	trimmed := strings.TrimSpace(input)
	lines := strings.Split(trimmed, "\n")
	if len(lines) >= 4 {
		first := strings.TrimSpace(lines[0])
		last := strings.TrimSpace(lines[len(lines)-1])
		if (first == "<<EOF" || first == "<<'EOF'" || first == `<<"EOF"`) && strings.HasSuffix(last, "EOF") {
			lines = lines[1 : len(lines)-1]
		}
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != patchBegin {
		return nil, fmt.Errorf("The first line of the patch must be %q", patchBegin)
	}
	if strings.TrimSpace(lines[len(lines)-1]) != patchEnd {
		return nil, fmt.Errorf("The last line of the patch must be %q", patchEnd)
	}
	lines = lines[1 : len(lines)-1]

	var ops []patchOp
	i := 0
	for i < len(lines) {
		line := lines[i]
		header := strings.TrimSpace(line)
		switch {
		case header == "":
			i++
		case strings.HasPrefix(header, hdrUpdate):
			path := strings.TrimSpace(header[len(hdrUpdate):])
			if path == "" {
				return nil, fmt.Errorf("update: missing path")
			}
			op := patchOp{kind: patchOpUpdate, path: path}
			i++
			if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), hdrMoveTo) {
				moveHeader := strings.TrimSpace(lines[i])
				op.moveTo = strings.TrimSpace(moveHeader[len(hdrMoveTo):])
				if op.moveTo == "" {
					return nil, fmt.Errorf("update %s: empty move target", path)
				}
				i++
			}
			hunks, next, err := parseHunks(lines, i, path)
			if err != nil {
				return nil, err
			}
			if len(hunks) == 0 {
				return nil, fmt.Errorf("update %s: no hunks", path)
			}
			op.hunks = hunks
			ops = append(ops, op)
			i = next
		case strings.HasPrefix(header, hdrDelete):
			path := strings.TrimSpace(header[len(hdrDelete):])
			if path == "" {
				return nil, fmt.Errorf("delete: missing path")
			}
			ops = append(ops, patchOp{kind: patchOpDelete, path: path})
			i++
		case strings.HasPrefix(header, hdrAdd):
			path := strings.TrimSpace(header[len(hdrAdd):])
			if path == "" {
				return nil, fmt.Errorf("add: missing path")
			}
			var body []string
			i++
			for i < len(lines) && !isSectionHeaderTrimmed(lines[i]) {
				l := lines[i]
				if !strings.HasPrefix(l, "+") {
					return nil, fmt.Errorf("add %s: line must start with '+': %q", path, l)
				}
				body = append(body, l[1:])
				i++
			}
			if len(body) == 0 {
				return nil, fmt.Errorf("add %s: file hunk is empty", path)
			}
			ops = append(ops, patchOp{kind: patchOpAdd, path: path, addBody: strings.Join(body, "\n") + "\n"})
		default:
			return nil, fmt.Errorf("unknown patch line: %q", line)
		}
	}
	return ops, nil
}

func isSectionHeader(line string) bool {
	return strings.HasPrefix(line, hdrUpdate) || strings.HasPrefix(line, hdrDelete) || strings.HasPrefix(line, hdrAdd)
}

func isSectionHeaderTrimmed(line string) bool {
	return isSectionHeader(strings.TrimSpace(line))
}

// parseHunks reads consecutive hunks for one Update section.
func parseHunks(lines []string, start int, path string) ([]patchHunk, int, error) {
	var hunks []patchHunk
	i := start
	for i < len(lines) && !isSectionHeader(lines[i]) {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		var hunk patchHunk
		for i < len(lines) && strings.HasPrefix(lines[i], anchorPrefix) {
			anchor := strings.TrimPrefix(lines[i], anchorPrefix)
			anchor = strings.TrimPrefix(anchor, " ")
			if strings.TrimSpace(anchor) != "" {
				hunk.anchors = append(hunk.anchors, anchor)
			}
			i++
		}
		context, chunks, next, eof, err := readHunkBody(lines, i, path)
		if err != nil {
			return nil, 0, err
		}
		if len(context) == 0 && len(chunks) == 0 && len(hunk.anchors) == 0 {
			break
		}
		hunk.context, hunk.chunks, hunk.eof = context, chunks, eof
		hunks = append(hunks, hunk)
		i = next
	}
	return hunks, i, nil
}

func readHunkBody(lines []string, start int, path string) (context []string, chunks []patchChunk, next int, eof bool, err error) {
	var del, ins []string
	mode := "keep"
	i := start
	flush := func() {
		if len(del) > 0 || len(ins) > 0 {
			chunks = append(chunks, patchChunk{ctxOffset: len(context) - len(del), del: del, ins: ins})
			del, ins = nil, nil
		}
	}
	for i < len(lines) {
		raw := lines[i]
		if strings.HasPrefix(raw, anchorPrefix) || isSectionHeader(raw) {
			break
		}
		if raw == patchEOF {
			i++
			eof = true
			break
		}
		if strings.HasPrefix(raw, "***") {
			return nil, nil, 0, false, fmt.Errorf("update %s: invalid line %q", path, raw)
		}
		i++
		line := raw
		if line == "" {
			line = " "
		}
		last := mode
		switch line[0] {
		case '+':
			mode = "add"
		case '-':
			mode = "delete"
		case ' ':
			mode = "keep"
		default:
			return nil, nil, 0, false, fmt.Errorf("update %s: hunk line must start with '+', '-', or ' ': %q", path, raw)
		}
		if mode == "keep" && last != mode {
			flush()
		}
		content := line[1:]
		switch mode {
		case "delete":
			del = append(del, content)
			context = append(context, content)
		case "add":
			ins = append(ins, content)
		default:
			context = append(context, content)
		}
	}
	flush()
	return context, chunks, i, eof, nil
}

// ─── Hunk resolution (three-tier unique matching) ────────────────────────────

func lineEqual(a, b string, tier int) bool {
	switch tier {
	case 0:
		return a == b
	case 1:
		return strings.TrimRight(a, " \t") == strings.TrimRight(b, " \t")
	case 2:
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	default:
		return normalizePatchLine(a) == normalizePatchLine(b)
	}
}

func normalizePatchLine(line string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			return '-'
		case '\u2018', '\u2019', '\u201A', '\u201B':
			return '\''
		case '\u201C', '\u201D', '\u201E', '\u201F':
			return '"'
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006',
			'\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			return ' '
		default:
			return r
		}
	}, strings.TrimSpace(line))
}

func matchAt(fileLines, context []string, at, tier int) bool {
	if at < 0 || at+len(context) > len(fileLines) {
		return false
	}
	for j := range context {
		if !lineEqual(fileLines[at+j], context[j], tier) {
			return false
		}
	}
	return true
}

// findContextUnique locates context within fileLines at or after cursor.
// A tier must yield exactly one match; multiple matches fail as ambiguous so a
// fuzzy tier can never silently edit the wrong copy of repeated code.
//
// anchored relaxes the uniqueness requirement. When the hunk carried an
// explicit @@ anchor, the caller has already located the region and cursor
// sits at the anchor line, so a later duplicate elsewhere in the file is not a
// genuine ambiguity — it is exactly the ambiguity the anchor was written to
// resolve. Reporting it anyway made anchors decorative: a patch that named
// `@@ func Foo` was still refused because the same line appeared in `func Bar`
// hundreds of lines below (issues #260, #291). With an anchor, the match
// nearest the anchor wins.
func findContextUnique(fileLines, context []string, cursor int, eof bool, anchored bool) (int, int, error) {
	if len(context) == 0 {
		return cursor, 0, nil
	}
	if eof {
		end := len(fileLines) - len(context)
		for tier := 0; tier <= 3; tier++ {
			if end >= cursor && matchAt(fileLines, context, end, tier) {
				return end, tier, nil
			}
		}
	}
	for tier := 0; tier <= 3; tier++ {
		var found []int
		for i := cursor; i+len(context) <= len(fileLines); i++ {
			if matchAt(fileLines, context, i, tier) {
				found = append(found, i)
			}
		}
		// An explicit anchor already scoped the search; take the nearest match.
		if anchored && len(found) > 0 {
			return found[0], tier, nil
		}
		if len(found) > 1 {
			lineNumbers := make([]string, 0, len(found))
			for _, index := range found {
				lineNumbers = append(lineNumbers, fmt.Sprintf("%d", index+1))
			}
			return 0, 0, fmt.Errorf(
				"ambiguous context at lines %s for:\n%s\nAdd an @@ class/function anchor or more surrounding lines",
				strings.Join(lineNumbers, ", "), strings.Join(context, "\n"))
		}
		if len(found) == 1 {
			return found[0], tier, nil
		}
	}
	return 0, 0, fmt.Errorf("context not found:\n%s", strings.Join(context, "\n"))
}

func applyHunks(original string, hunks []patchHunk, path string) (string, int, error) {
	newline := "\n"
	normalized := original
	if strings.Contains(original, "\r\n") {
		newline = "\r\n"
		normalized = strings.ReplaceAll(original, "\r\n", "\n")
	}
	fileLines := strings.Split(normalized, "\n")
	var out []string
	cursor := 0
	totalFuzz := 0
	for _, hunk := range hunks {
		for _, anchor := range hunk.anchors {
			idx, tier, err := findAnchorUnique(fileLines, anchor, cursor)
			if err != nil {
				return "", 0, fmt.Errorf("update %s: %w", path, err)
			}
			out = append(out, fileLines[cursor:idx]...)
			cursor = idx
			totalFuzz += tier
		}
		at, tier, err := findContextUnique(fileLines, hunk.context, cursor, hunk.eof, len(hunk.anchors) > 0)
		if err != nil {
			// Last resort: the context may be missing blank lines that the
			// file really contains, which no tier can absorb because tiers
			// only relax per-line comparison, never line presence. The repair
			// restores those blanks and re-verifies an exact match, or gives
			// up and returns the original error unchanged.
			repaired, repairedAt, ok := repairHunkForFileBlanks(fileLines, hunk, cursor)
			if !ok {
				return "", 0, fmt.Errorf("update %s: %w", path, err)
			}
			hunk = repaired
			at = repairedAt
			tier = 3
			err = nil
		}
		pureAddition := len(hunk.context) == 0 && len(hunk.chunks) > 0
		for _, chunk := range hunk.chunks {
			if len(chunk.del) != 0 {
				pureAddition = false
				break
			}
		}
		if pureAddition {
			if len(hunk.anchors) > 0 {
				at = cursor
			} else {
				at = len(fileLines)
				if len(fileLines) > 0 && fileLines[len(fileLines)-1] == "" {
					at--
				}
			}
		}
		totalFuzz += tier
		out = append(out, fileLines[cursor:at]...)
		ctxCursor := at
		for _, chunk := range hunk.chunks {
			chunkAt := at + chunk.ctxOffset
			if chunkAt < ctxCursor {
				return "", 0, fmt.Errorf("update %s: overlapping hunk chunks", path)
			}
			out = append(out, fileLines[ctxCursor:chunkAt]...)
			out = append(out, chunk.ins...)
			ctxCursor = chunkAt + len(chunk.del)
		}
		out = append(out, fileLines[ctxCursor:at+len(hunk.context)]...)
		cursor = at + len(hunk.context)
	}
	out = append(out, fileLines[cursor:]...)
	result := strings.Join(out, "\n")
	if result != "" && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	if newline == "\r\n" {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return result, totalFuzz, nil
}

// ─── Transactional engine ────────────────────────────────────────────────────

type plannedChange struct {
	op             patchOp
	absPath        string
	absDest        string
	oldContent     string
	oldMode        os.FileMode
	newContent     string
	destExisted    bool
	destOldContent string
	destOldMode    os.FileMode
}

// gitBlobSHA1 computes the git object hash for content the same way `git
// hash-object` computes the hash of a blob: sha1("blob "+len(content)+"\x00"+content).
//
// This intentionally duplicates internal/bench/effect.go's blobHash rather
// than importing internal/bench: forge is a low-level, widely-imported tool
// package and must not gain a new dependency edge just to reuse six lines of
// stdlib hashing. Both implementations compute the identical, well-defined
// git object format, and each is independently tested against a real git
// object (see apply_patch_outcome_test.go and internal/bench/effect_test.go)
// — a divergence between them would fail both tests, not just one.
//
// Called only from committedEffects, on content (oldContent/newContent)
// already fully materialised in memory as part of building the patch — this
// adds zero file reads, zero stats, and zero subprocess forks to the commit
// path.
func gitBlobSHA1(content string) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}

// committedEffects converts a committed change set into typed file effects
// for ToolResult.Outcome (G4).
//
// It is called only after commit returns nil, and commit is transactional —
// every change is applied or every change is rolled back — so each plannedChange
// here corresponds to a mutation that really happened. That is the whole point:
// the paths are recorded by the tool that performed the writes, at the moment
// it performed them, so no consumer has to special-case apply_patch's patch
// envelope versus Write/Edit's file_path parameter, and no consumer has to
// parse them back out of the "A/M/D path" summary text.
//
// Cost is a slice walk over data already in memory: no stat, no re-read, no
// git fork, nothing added to the hot path.
//
// It also populates PreBlobSHA1/PostBlobSHA1 (PLAN.md §3 "store git BLOB
// hashes, not just a commit SHA") from change.oldContent/change.newContent —
// the same in-memory strings this function already reads to fill
// BytesWritten, never a fresh read of the file this tool just wrote.
// PreBlobSHA1 is left empty for patchOpAdd (there is no prior content to
// hash — destExisted is false and oldContent is never populated for that
// case) and PostBlobSHA1 is left empty for patchOpDelete (nothing survives).
func committedEffects(changes []plannedChange) []toolout.FileEffect {
	if len(changes) == 0 {
		return nil
	}
	effects := make([]toolout.FileEffect, 0, len(changes))
	for _, change := range changes {
		switch change.op.kind {
		case patchOpAdd:
			effects = append(effects, toolout.FileEffect{
				Path:         change.absDest,
				Op:           toolout.FileOpCreate,
				BytesWritten: int64(len(change.newContent)),
				PostBlobSHA1: gitBlobSHA1(change.newContent),
			})
		case patchOpDelete:
			effects = append(effects, toolout.FileEffect{
				Path:        change.absPath,
				Op:          toolout.FileOpDelete,
				PreBlobSHA1: gitBlobSHA1(change.oldContent),
			})
		case patchOpUpdate:
			effect := toolout.FileEffect{
				Path:         change.absDest,
				Op:           toolout.FileOpUpdate,
				BytesWritten: int64(len(change.newContent)),
				PreBlobSHA1:  gitBlobSHA1(change.oldContent),
				PostBlobSHA1: gitBlobSHA1(change.newContent),
			}
			// A V4A "Move to" writes the destination and removes the source;
			// recording only the destination would lose the fact that the
			// original path no longer exists.
			if change.absDest != change.absPath {
				effect.Op = toolout.FileOpMove
				effect.FromPath = change.absPath
			}
			effects = append(effects, effect)
		}
	}
	return effects
}

// ApplyPatchTool is the canonical single file-mutation tool.
type ApplyPatchTool struct {
	workspacePath string
	// baseDir is the directory relative paths resolve against for one call.
	// Empty means "use workspacePath". It is set only on a per-call copy made
	// by withBase, never mutated on a shared instance.
	baseDir      string
	snapshotDir  string
	writeFileFn  func(string, string, os.FileMode) error // test fault injection
	removeFileFn func(string) error                      // test fault injection
}

// withBase returns a per-call copy of the tool whose relative-path base is
// cwd. The receiver is never modified, so concurrent calls with different
// working directories cannot interfere.
func (t *ApplyPatchTool) withBase(cwd string) *ApplyPatchTool {
	if cwd == "" {
		return t
	}
	clone := *t
	clone.baseDir = cwd
	return &clone
}

// effectiveBase returns the directory relative paths resolve against.
func (t *ApplyPatchTool) effectiveBase() string {
	if t.baseDir != "" {
		return t.baseDir
	}
	return t.workspacePath
}

// NewApplyPatchTool creates the canonical apply_patch tool.
func NewApplyPatchTool(workspacePath string) *ApplyPatchTool {
	snapshotDir := ""
	if workspacePath != "" {
		snapshotDir = filepath.Join(workspacePath, ".swarm", "snapshots")
	}
	return &ApplyPatchTool{workspacePath: workspacePath, snapshotDir: snapshotDir}
}

// Name returns the tool name.
func (t *ApplyPatchTool) Name() string { return "apply_patch" }

// Description returns the tool description.
func (t *ApplyPatchTool) Description() string {
	return `Edit files with a V4A patch. The ONLY tool for creating, updating, deleting, or moving files.

*** Begin Patch
*** Update File: relative/or/absolute/path
[*** Move to: new/path]
@@ optional anchor (class/function line) to disambiguate
 context line (space prefix)
-removed line
+added line
 context line
*** Add File: new/file (every body line starts with '+')
*** Delete File: old/file
*** End Patch

Rules: 3 lines of context around each change; use @@ anchors when context repeats; no line numbers. Multiple files may be combined — the patch applies atomically (all operations succeed or none are written).`
}

// Parameters returns the JSON schema.
func (t *ApplyPatchTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type":        "string",
				"description": "The full patch, from '*** Begin Patch' to '*** End Patch'.",
			},
			"cwd": map[string]any{
				"type": "string",
				"description": "Optional directory that relative paths in the patch resolve against. " +
					"Use this to edit a linked git worktree of the same repository when the " +
					"conversation workspace is pinned to a different checkout. Absolute paths " +
					"are still permitted when they fall inside that worktree.",
			},
		},
		"required": []string{"input"},
	}
}

// Validate checks parameters.
func (t *ApplyPatchTool) Validate(params map[string]any) error {
	input, _ := params["input"].(string)
	if strings.TrimSpace(input) == "" {
		return fmt.Errorf("input is required")
	}
	if cwd, ok := params["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
		info, err := os.Stat(cwd)
		if err != nil {
			return fmt.Errorf("cwd %q is not usable: %w", cwd, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("cwd %q is not a directory", cwd)
		}
	}
	return nil
}

// IsIdempotent returns false.
func (t *ApplyPatchTool) IsIdempotent() bool { return false }

// RequiresPermission returns required permissions.
func (t *ApplyPatchTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileWrite}
}

// SupportedContentTypes returns supported content types.
func (t *ApplyPatchTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil.
func (t *ApplyPatchTool) OptimizationHints() *tools.OptimizationHints { return nil }

// Execute preflights and applies the patch transactionally.
func (t *ApplyPatchTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "apply_patch.cancelled")
	default:
	}
	input, _ := params["input"].(string)
	ops, err := parsePatch(input)
	if err != nil {
		return nil, sdkerr.Permanent("apply_patch.parse", err.Error())
	}

	// Resolve this call against cwd when supplied, without mutating the
	// shared tool instance.
	cwd, _ := params["cwd"].(string)
	call := t.withBase(strings.TrimSpace(cwd))

	changes, _, err := call.preflight(ctx, ops)
	if err != nil {
		return nil, err
	}
	summary, err := call.commit(ctx, changes)
	if err != nil {
		return nil, err
	}
	return tools.NewToolResult(summary).
		WithOutcome(toolout.Files(committedEffects(changes)...)), nil
}

func (t *ApplyPatchTool) resolvePath(ctx context.Context, path string) (string, error) {
	absPath, err := resolveWorkspacePath(t.effectiveBase(), path)
	if err != nil {
		return "", sdkerr.Permanent("apply_patch.invalid_path", fmt.Sprintf("cannot resolve %q: %v", path, err))
	}
	if t.workspacePath == "" {
		return absPath, nil
	}
	workspaceAbs, err := filepath.Abs(filepath.Clean(t.workspacePath))
	if err != nil {
		return "", sdkerr.Permanent("apply_patch.invalid_workspace", fmt.Sprintf("resolve workspace: %v", err))
	}
	workspace, err := filepath.EvalSymlinks(workspaceAbs)
	if err != nil {
		return "", sdkerr.Permanent("apply_patch.invalid_workspace", fmt.Sprintf("resolve workspace: %v", err))
	}
	canonical, err := canonicalProspectivePath(absPath)
	if err != nil {
		return "", sdkerr.Permanent("apply_patch.invalid_path", fmt.Sprintf("resolve %q: %v", path, err))
	}
	if !pathWithin(workspace, canonical) {
		if tools.IsPathApproved(ctx, absPath) || tools.IsPathApproved(ctx, canonical) {
			return canonical, nil
		}
		// A linked worktree of the same repository is an alternate checkout of
		// this project, not an unrelated location. Allowing it is what lets an
		// explicitly assigned sibling worktree actually be edited (#289, #290,
		// #292, #295) instead of being readable and testable but not writable.
		if t.baseDir != "" && sameGitRepository(workspace, canonical) {
			return canonical, nil
		}
		return "", sdkerr.Permanent("apply_patch.path_outside_workspace", fmt.Sprintf(
			"path %q resolves to %s, which is outside the workspace %s.\n"+
				"Use a workspace-relative path, or pass cwd=<dir> to target a linked git worktree of the same repository.",
			path, canonical, workspace))
	}
	return canonical, nil
}

// canonicalProspectivePath resolves symlinks even when the final file does not
// exist by resolving the nearest existing parent and appending the missing tail.
func canonicalProspectivePath(path string) (string, error) {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	parent := path
	for {
		if _, err := os.Lstat(parent); err == nil {
			resolvedParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(parent, path)
			if err != nil {
				return "", err
			}
			return filepath.Clean(filepath.Join(resolvedParent, rel)), nil
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
		parent = next
	}
}

func (t *ApplyPatchTool) revalidateCommitPath(ctx context.Context, expected string) error {
	resolved, err := t.resolvePath(ctx, expected)
	if err != nil {
		return err
	}
	if filepath.Clean(resolved) != filepath.Clean(expected) {
		return sdkerr.Permanent("apply_patch.path_changed",
			fmt.Sprintf("path changed after preflight: %s now resolves to %s", expected, resolved))
	}
	return nil
}

func (t *ApplyPatchTool) preflight(ctx context.Context, ops []patchOp) ([]plannedChange, int, error) {
	if len(ops) == 0 {
		return nil, 0, sdkerr.Permanent("apply_patch.no_files_modified", "No files were modified.")
	}

	type virtualFile struct {
		exists  bool
		isDir   bool
		content string
		mode    os.FileMode
	}
	virtual := make(map[string]virtualFile)
	loadVirtual := func(absPath string) (virtualFile, error) {
		if state, ok := virtual[absPath]; ok {
			return state, nil
		}
		info, err := os.Lstat(absPath)
		if os.IsNotExist(err) {
			state := virtualFile{mode: 0o644}
			virtual[absPath] = state
			return state, nil
		}
		if err != nil {
			return virtualFile{}, sdkerr.Wrap(err, "apply_patch.stat_failed")
		}
		state := virtualFile{exists: true, isDir: info.IsDir(), mode: info.Mode().Perm()}
		if !state.isDir {
			raw, readErr := os.ReadFile(absPath)
			if readErr != nil {
				return virtualFile{}, sdkerr.Wrap(readErr, "apply_patch.read_failed")
			}
			state.content = string(raw)
		}
		virtual[absPath] = state
		return state, nil
	}

	changes := make([]plannedChange, 0, len(ops))
	totalFuzz := 0

	for _, op := range ops {
		absPath, err := t.resolvePath(ctx, op.path)
		if err != nil {
			return nil, 0, err
		}
		change := plannedChange{op: op, absPath: absPath, absDest: absPath, oldMode: 0o644}
		source, err := loadVirtual(absPath)
		if err != nil {
			return nil, 0, err
		}
		switch op.kind {
		case patchOpAdd:
			if source.isDir {
				return nil, 0, sdkerr.Permanent("apply_patch.not_a_file", fmt.Sprintf("%s is a directory", op.path))
			}
			change.destExisted = source.exists
			if source.exists {
				change.oldContent = source.content
				change.oldMode = source.mode
				change.destOldContent = source.content
				change.destOldMode = source.mode
			}
			change.newContent = op.addBody
			mode := change.oldMode
			virtual[absPath] = virtualFile{exists: true, content: change.newContent, mode: mode}
		case patchOpDelete, patchOpUpdate:
			if !source.exists {
				return nil, 0, sdkerr.Permanent("apply_patch.missing_file",
					fmt.Sprintf("%s: file not found", op.path))
			}
			if source.isDir {
				return nil, 0, sdkerr.Permanent("apply_patch.not_a_file", fmt.Sprintf("%s is a directory", op.path))
			}
			change.oldContent = source.content
			change.oldMode = source.mode
			if op.kind == patchOpDelete {
				virtual[absPath] = virtualFile{mode: source.mode}
			}
			if op.kind == patchOpUpdate {
				newContent, fuzz, applyErr := applyHunks(change.oldContent, op.hunks, op.path)
				if applyErr != nil {
					return nil, 0, sdkerr.Permanent("apply_patch.context_mismatch", applyErr.Error())
				}
				totalFuzz += fuzz
				change.newContent = newContent
				if op.moveTo != "" {
					dest, destErr := t.resolvePath(ctx, op.moveTo)
					if destErr != nil {
						return nil, 0, destErr
					}
					if dest != absPath {
						destination, loadErr := loadVirtual(dest)
						if loadErr != nil {
							return nil, 0, loadErr
						}
						if destination.isDir {
							return nil, 0, sdkerr.Permanent("apply_patch.not_a_file",
								fmt.Sprintf("move target %s is a directory", op.moveTo))
						}
						change.destExisted = destination.exists
						change.destOldContent = destination.content
						change.destOldMode = destination.mode
						virtual[absPath] = virtualFile{mode: source.mode}
						virtual[dest] = virtualFile{exists: true, content: newContent, mode: source.mode}
					} else {
						virtual[absPath] = virtualFile{exists: true, content: newContent, mode: source.mode}
					}
					change.absDest = dest
				} else {
					virtual[absPath] = virtualFile{exists: true, content: newContent, mode: source.mode}
				}
			}
		}
		if change.op.kind != patchOpDelete && introducesConflictMarkers(change.oldContent, change.newContent) {
			return nil, 0, sdkerr.Permanent("apply_patch.conflict_markers",
				fmt.Sprintf("%s introduces unresolved merge conflict markers", change.op.path))
		}
		changes = append(changes, change)
	}
	return changes, totalFuzz, nil
}

func introducesConflictMarkers(oldContent, newContent string) bool {
	countBlocks := func(content string) int {
		blocks := 0
		inConflict := false
		sawSeparator := false
		for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "<<<<<<<"):
				inConflict = true
				sawSeparator = false
			case inConflict && strings.HasPrefix(line, "======="):
				sawSeparator = true
			case inConflict && sawSeparator && strings.HasPrefix(line, ">>>>>>>"):
				blocks++
				inConflict = false
				sawSeparator = false
			}
		}
		return blocks
	}
	return countBlocks(newContent) > countBlocks(oldContent)
}

type appliedStep struct {
	path string
	undo func() error
}

func (t *ApplyPatchTool) commit(ctx context.Context, changes []plannedChange) (string, error) {
	// Anchor every in-workspace mutation on an os.Root handle. Root refuses
	// symlink escapes on each path component, closing the parent-swap TOCTOU
	// window that pure pre-mutation revalidation cannot close.
	var wsRoot *os.Root
	if t.workspacePath != "" {
		workspaceAbs, err := filepath.Abs(filepath.Clean(t.workspacePath))
		if err != nil {
			return "", sdkerr.Wrap(err, "apply_patch.workspace_open_failed")
		}
		canonicalWorkspace, err := filepath.EvalSymlinks(workspaceAbs)
		if err != nil {
			return "", sdkerr.Wrap(err, "apply_patch.workspace_open_failed")
		}
		opened, err := os.OpenRoot(canonicalWorkspace)
		if err != nil {
			return "", sdkerr.Wrap(err, "apply_patch.workspace_open_failed")
		}
		wsRoot = opened
		defer wsRoot.Close()
	}

	// Undo is a contract, not best effort: prepare every snapshot before the
	// first mutation so a successful write is always recoverable.
	if err := t.prepareSnapshots(ctx, wsRoot, changes); err != nil {
		return "", sdkerr.Wrap(err, "apply_patch.snapshot_failed")
	}

	var journal []appliedStep
	rollback := func(cause error) error {
		var failures []string
		for i := len(journal) - 1; i >= 0; i-- {
			if undoErr := journal[i].undo(); undoErr != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", journal[i].path, undoErr))
			}
		}
		if len(failures) > 0 {
			return sdkerr.Permanent("apply_patch.rollback_failed",
				fmt.Sprintf("apply failed (%v); rollback failures: %s — inspect these paths", cause, strings.Join(failures, "; ")))
		}
		return cause
	}

	var added, modified, deleted []string
	for _, change := range changes {
		// Re-resolve immediately before mutation so approved outside-workspace
		// targets (which cannot use the workspace root handle) stay fail-closed.
		if err := t.revalidateCommitPath(ctx, change.absPath); err != nil {
			return "", rollback(err)
		}
		if change.absDest != change.absPath {
			if err := t.revalidateCommitPath(ctx, change.absDest); err != nil {
				return "", rollback(err)
			}
		}
		switch change.op.kind {
		case patchOpAdd:
			mode := change.oldMode
			if mode == 0 {
				mode = 0o644
			}
			if err := t.writeFile(wsRoot, change.absDest, change.newContent, mode); err != nil {
				return "", rollback(sdkerr.Wrap(err, "apply_patch.write_failed"))
			}
			dest := change.absDest
			if change.destExisted {
				body, oldMode := change.destOldContent, change.destOldMode
				journal = append(journal, appliedStep{path: dest, undo: func() error {
					return t.writeFile(wsRoot, dest, body, oldMode)
				}})
			} else {
				journal = append(journal, appliedStep{path: dest, undo: func() error { return t.removeFile(wsRoot, dest) }})
			}
			added = append(added, change.op.path)
		case patchOpDelete:
			if err := t.removeFile(wsRoot, change.absPath); err != nil {
				return "", rollback(sdkerr.Wrap(err, "apply_patch.delete_failed"))
			}
			src, body, mode := change.absPath, change.oldContent, change.oldMode
			journal = append(journal, appliedStep{path: src, undo: func() error { return t.writeFile(wsRoot, src, body, mode) }})
			deleted = append(deleted, change.op.path)
		case patchOpUpdate:
			if err := t.writeFile(wsRoot, change.absDest, change.newContent, change.oldMode); err != nil {
				return "", rollback(sdkerr.Wrap(err, "apply_patch.write_failed"))
			}
			if change.absDest != change.absPath {
				dest := change.absDest
				if change.destExisted {
					body, oldMode := change.destOldContent, change.destOldMode
					journal = append(journal, appliedStep{path: dest, undo: func() error {
						return t.writeFile(wsRoot, dest, body, oldMode)
					}})
				} else {
					journal = append(journal, appliedStep{path: dest, undo: func() error { return t.removeFile(wsRoot, dest) }})
				}
				if err := t.removeFile(wsRoot, change.absPath); err != nil {
					return "", rollback(sdkerr.Wrap(err, "apply_patch.move_failed"))
				}
				src, body, mode := change.absPath, change.oldContent, change.oldMode
				journal = append(journal, appliedStep{path: src, undo: func() error { return t.writeFile(wsRoot, src, body, mode) }})
				modified = append(modified, change.op.moveTo)
			} else {
				src, body, mode := change.absPath, change.oldContent, change.oldMode
				journal = append(journal, appliedStep{path: src, undo: func() error { return t.writeFile(wsRoot, src, body, mode) }})
				modified = append(modified, change.op.path)
			}
		}
	}

	for _, change := range changes {
		if change.op.kind != patchOpDelete {
			filetracker.RecordAccess(ctx, change.absDest, true, filetracker.EstimateTokens(len(change.newContent)))
		}
	}
	var summary strings.Builder
	summary.WriteString("Success. Updated the following files:\n")
	for _, path := range added {
		fmt.Fprintf(&summary, "A %s\n", path)
	}
	for _, path := range modified {
		fmt.Fprintf(&summary, "M %s\n", path)
	}
	for _, path := range deleted {
		fmt.Fprintf(&summary, "D %s\n", path)
	}
	return summary.String(), nil
}

// workspaceRel returns the root-relative path when abs is inside the opened
// workspace root, enabling descriptor-anchored symlink-safe operations.
func workspaceRel(root *os.Root, abs string) (string, bool) {
	if root == nil {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(root.Name()), filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return rel, true
}

func (t *ApplyPatchTool) writeFile(root *os.Root, path, content string, mode os.FileMode) error {
	if t.writeFileFn != nil {
		return t.writeFileFn(path, content, mode)
	}
	if rel, ok := workspaceRel(root, path); ok {
		return writeFileAtomicRoot(root, rel, content, mode)
	}
	if root != nil && pathWithin(filepath.Clean(root.Name()), filepath.Clean(path)) {
		return fmt.Errorf("refusing unanchored in-workspace write to %s", path)
	}
	return writeFileAtomic(path, content, mode)
}

func (t *ApplyPatchTool) removeFile(root *os.Root, path string) error {
	if t.removeFileFn != nil {
		return t.removeFileFn(path)
	}
	if rel, ok := workspaceRel(root, path); ok {
		return root.Remove(rel)
	}
	if root != nil && pathWithin(filepath.Clean(root.Name()), filepath.Clean(path)) {
		return fmt.Errorf("refusing unanchored in-workspace removal of %s", path)
	}
	return os.Remove(path)
}

// writeFileAtomicRoot writes via the workspace root handle: every path
// component is symlink-checked by os.Root, and the temp+rename keeps the
// final publish atomic.
func writeFileAtomicRoot(root *os.Root, rel, content string, mode os.FileMode) error {
	dir := filepath.Dir(rel)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmpRel := filepath.Join(dir, fmt.Sprintf(".apply-patch-%d.tmp", time.Now().UnixNano()))
	file, err := root.OpenFile(tmpRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		root.Remove(tmpRel)
		return err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		root.Remove(tmpRel)
		return err
	}
	if err := file.Close(); err != nil {
		root.Remove(tmpRel)
		return err
	}
	if err := root.Rename(tmpRel, rel); err != nil {
		root.Remove(tmpRel)
		return err
	}
	return nil
}

func writeFileAtomic(path, content string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".apply-patch-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func saveSnapshotTo(snapshotDir, absPath, content string) (string, error) {
	if err := ensureSnapshotDir(snapshotDir); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%d.bak", filepath.Base(absPath), time.Now().UnixNano())
	path := filepath.Join(snapshotDir, name)
	return path, os.WriteFile(path, []byte(content), 0o600)
}

func (t *ApplyPatchTool) prepareSnapshots(ctx context.Context, root *os.Root, changes []plannedChange) error {
	if t.snapshotDir == "" {
		return nil
	}
	snapshotted := make(map[string]bool)
	snapshotOnce := func(path string, existed bool, content string) error {
		if snapshotted[path] {
			return nil
		}
		snapshotted[path] = true
		if existed {
			return t.snapshotExisting(ctx, root, path, content)
		}
		return t.snapshotNewFile(ctx, root, path)
	}
	for _, change := range changes {
		switch change.op.kind {
		case patchOpAdd:
			if err := snapshotOnce(change.absDest, change.destExisted, change.destOldContent); err != nil {
				return err
			}
		case patchOpDelete:
			if err := snapshotOnce(change.absPath, true, change.oldContent); err != nil {
				return err
			}
		case patchOpUpdate:
			if err := snapshotOnce(change.absPath, true, change.oldContent); err != nil {
				return err
			}
			if change.absDest != change.absPath {
				if err := snapshotOnce(change.absDest, change.destExisted, change.destOldContent); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (t *ApplyPatchTool) snapshotExisting(ctx context.Context, root *os.Root, absPath, content string) error {
	bakPath, err := t.saveSnapshot(root, absPath, content)
	if err != nil {
		return err
	}
	writeSnapshotContext(bakPath, ctx, t.Name(), absPath)
	return nil
}

func (t *ApplyPatchTool) snapshotNewFile(ctx context.Context, root *os.Root, absPath string) error {
	bakPath, err := t.saveSnapshot(root, absPath, "")
	if err != nil {
		return err
	}
	writeNewFileSnapshotContext(bakPath, ctx, t.Name(), absPath)
	return nil
}

// saveSnapshot writes an undo backup. Inside a workspace it goes through the
// os.Root handle so a hostile `.swarm` (or `snapshots`) symlink cannot
// redirect secret-bearing backups outside the workspace.
func (t *ApplyPatchTool) saveSnapshot(root *os.Root, absPath, content string) (string, error) {
	name := fmt.Sprintf("%s-%d.bak", filepath.Base(absPath), time.Now().UnixNano())
	bakPath := filepath.Join(t.snapshotDir, name)
	if root == nil {
		return saveSnapshotTo(t.snapshotDir, absPath, content)
	}
	relDir := filepath.Join(".swarm", "snapshots")
	bakPath = filepath.Join(root.Name(), relDir, name)
	if err := root.MkdirAll(relDir, 0o700); err != nil {
		return "", err
	}
	if err := root.Chmod(relDir, 0o700); err != nil {
		return "", err
	}
	gitignoreRel := filepath.Join(relDir, ".gitignore")
	if _, err := root.Lstat(gitignoreRel); os.IsNotExist(err) {
		if file, createErr := root.OpenFile(gitignoreRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); createErr == nil {
			_, _ = file.WriteString("# Auto-generated by swarm: agent file snapshots — not for version control.\n*\n")
			_ = file.Close()
		}
	}
	file, err := root.OpenFile(filepath.Join(relDir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return bakPath, nil
}

// executeLegacyWrite and executeLegacyReplace keep persisted Write/Edit calls
// working while routing every mutation through the canonical transaction path.
func (t *ApplyPatchTool) executeLegacyWrite(ctx context.Context, path, content string, overwrite bool) (*tools.ToolResult, error) {
	absPath, err := t.resolvePath(ctx, path)
	if err != nil {
		return nil, err
	}
	change := plannedChange{absPath: absPath, absDest: absPath, newContent: content, oldMode: 0o644}
	info, statErr := os.Lstat(absPath)
	if statErr == nil {
		if !overwrite {
			return nil, sdkerr.Permanent("fs_write.exists", fmt.Sprintf("file already exists: %s", absPath))
		}
		old, readErr := os.ReadFile(absPath)
		if readErr != nil {
			return nil, sdkerr.Wrap(readErr, "fs_write.read_failed")
		}
		change.op = patchOp{kind: patchOpUpdate, path: path}
		change.oldContent, change.oldMode = string(old), info.Mode().Perm()
	} else if os.IsNotExist(statErr) {
		change.op = patchOp{kind: patchOpAdd, path: path, addBody: content}
	} else {
		return nil, sdkerr.Wrap(statErr, "fs_write.stat_failed")
	}
	if change.oldContent == content && change.op.kind == patchOpUpdate {
		return nil, sdkerr.Permanent("fs_write.no_op", "new content is identical to the existing file")
	}
	summary, err := t.commit(ctx, []plannedChange{change})
	if err != nil {
		return nil, err
	}
	// Write routes through the same transaction engine, so it reports the
	// same typed effects — the consumer never learns which of the three
	// mutating tools ran.
	return tools.NewToolResult(summary).
		WithOutcome(toolout.Files(committedEffects([]plannedChange{change})...)), nil
}

func (t *ApplyPatchTool) executeLegacyReplace(ctx context.Context, path, oldString, newString string, replaceAll bool) (*tools.ToolResult, error) {
	absPath, err := t.resolvePath(ctx, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return nil, sdkerr.Permanent("fs_patch.not_found", fmt.Sprintf("file not found: %s", absPath))
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, sdkerr.Wrap(err, "fs_patch.read_failed")
	}
	count := strings.Count(string(content), oldString)
	if count == 0 {
		return nil, sdkerr.Permanent("fs_patch.not_found_string", "old_string not found; re-read the file or use apply_patch with surrounding context")
	}
	if count > 1 && !replaceAll {
		return nil, sdkerr.Permanent("fs_patch.multiple_matches", fmt.Sprintf("old_string matches %d locations; use more context", count))
	}
	limit := 1
	if replaceAll {
		limit = -1
	}
	newContent := strings.Replace(string(content), oldString, newString, limit)
	change := plannedChange{
		op:         patchOp{kind: patchOpUpdate, path: path},
		absPath:    absPath,
		absDest:    absPath,
		oldContent: string(content),
		oldMode:    info.Mode().Perm(),
		newContent: newContent,
	}
	if introducesConflictMarkers(change.oldContent, change.newContent) {
		return nil, sdkerr.Permanent("fs_patch.conflict_markers", "replacement introduces unresolved merge conflict markers")
	}
	summary, err := t.commit(ctx, []plannedChange{change})
	if err != nil {
		return nil, err
	}
	return tools.NewToolResult(fmt.Sprintf("%s (%d replacement(s))", strings.TrimRight(summary, "\n"), count)).
		WithOutcome(toolout.Files(committedEffects([]plannedChange{change})...)), nil
}

// ─── Registration ────────────────────────────────────────────────────────────

// RegisterFileEditingTools installs the single visible mutation tool
// (apply_patch) plus hidden Edit/Write compatibility adapters so historical
// tool calls keep executing. Registration is atomic: on any failure the
// already-registered entries are rolled back.
func RegisterFileEditingTools(registry tools.Registry, workspacePath string) error {
	var registered []string
	rollback := func() {
		for i := len(registered) - 1; i >= 0; i-- {
			_ = registry.Unregister(registered[i])
		}
	}
	install := func(tool tools.Tool, hidden bool) error {
		if err := registry.Register(tool); err != nil {
			return err
		}
		registered = append(registered, tool.Name())
		if hidden {
			if err := registry.HideTool(tool.Name()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := install(NewApplyPatchTool(workspacePath), false); err != nil {
		rollback()
		return fmt.Errorf("register apply_patch: %w", err)
	}
	for _, legacy := range []tools.Tool{NewFSPatch(workspacePath), NewFSWrite(workspacePath)} {
		if err := install(legacy, true); err != nil {
			rollback()
			return fmt.Errorf("register hidden legacy editor %s: %w", legacy.Name(), err)
		}
	}
	return nil
}

func anchorLineMatches(line, snippet string, tier int) bool {
	switch tier {
	case 0:
		return line == snippet
	case 1:
		return strings.TrimRight(line, " \t") == strings.TrimRight(snippet, " \t")
	case 2:
		return strings.TrimSpace(line) == strings.TrimSpace(snippet)
	default:
		return strings.Contains(line, snippet)
	}
}

func findAnchorUnique(fileLines []string, anchor string, cursor int) (int, int, error) {
	snippets := strings.Split(anchor, `\n`)
	for tier := 0; tier <= 3; tier++ {
		var found []int
		for i := cursor; i+len(snippets) <= len(fileLines); i++ {
			matched := true
			for offset, snippet := range snippets {
				if !anchorLineMatches(fileLines[i+offset], snippet, tier) {
					matched = false
					break
				}
			}
			if matched {
				found = append(found, i)
			}
		}
		if len(found) > 1 {
			lineNumbers := make([]string, 0, len(found))
			for _, index := range found {
				lineNumbers = append(lineNumbers, fmt.Sprintf("%d", index+1))
			}
			return 0, 0, fmt.Errorf(
				"ambiguous @@ anchor at lines %s: %q; use a longer or multiline anchor",
				strings.Join(lineNumbers, ", "), anchor)
		}
		if len(found) == 1 {
			return found[0], tier, nil
		}
	}
	return 0, 0, fmt.Errorf("@@ anchor not found from line %d: %q", cursor+1, anchor)
}
