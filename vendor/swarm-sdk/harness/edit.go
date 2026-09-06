package harness

// This file deliberately depends only on the Go standard library and yaml.v3.
// The harness editor is a pure configuration surface: it has no application,
// presentation, command, private-package, runtime, watcher, or goroutine dependency.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// EditSession is a comment-preserving editing session for one harness manifest.
// It retains the original content hash for optimistic conflict detection and the
// original compiled plan for secret-safe semantic diffs.
type EditSession struct {
	path         string
	original     []byte
	originalHash string
	document     *yaml.Node
	baselinePlan *Plan
}

// SaveOptions controls conflict handling during Save.
type SaveOptions struct {
	// Force permits replacing a file even when it changed after the session opened.
	Force bool
	// ExpectHash permits replacement only when it equals the current on-disk
	// sha256 hash. It provides an explicit compare-and-swap override.
	ExpectHash string
}

// SaveResult describes a completed atomic save. BackupPath is always path+".bak"
// and contains the complete version that was on disk immediately before Save.
type SaveResult struct {
	Path        string
	BackupPath  string
	ContentHash string
	Plan        *Plan
}

// saveHookAfterValidate is a test-only seam for deterministic concurrent-write
// coverage. It is nil and inert in production.
var saveHookAfterValidate func()

// ConflictError reports that the manifest changed after the edit session opened.
type ConflictError struct {
	Path         string
	OriginalHash string
	CurrentHash  string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("harness edit conflict: %s changed on disk (opened %s, current %s)",
		e.Path, e.OriginalHash, e.CurrentHash)
}

// ValidationError reports that a rendered candidate cannot be compiled. Save
// returns this error before creating a backup or writing any target data.
type ValidationError struct {
	Diagnostics Diagnostics
}

func (e *ValidationError) Error() string {
	return "harness edit validation failed: " + e.Diagnostics.Error()
}

// OpenEditSession reads path once, parses its comment-bearing YAML syntax tree,
// and compiles a baseline plan. A schema-invalid manifest still returns a usable
// session plus diagnostics; malformed YAML cannot be edited and returns an error.
func OpenEditSession(path string) (*EditSession, Diagnostics, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve harness edit path: %w", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, fmt.Errorf("read harness edit path: %w", err)
	}
	document, err := parseEditDocument(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse harness edit path: %w", err)
	}

	plan, compileErr := CompileBytes(raw, abs)
	ds, ok := AsDiagnostics(compileErr)
	if compileErr != nil && !ok {
		return nil, nil, compileErr
	}
	return &EditSession{
		path:         abs,
		original:     append([]byte(nil), raw...),
		originalHash: hashBytes(raw),
		document:     document,
		baselinePlan: plan,
	}, ds, nil
}

// Path returns the absolute manifest path owned by the session.
func (s *EditSession) Path() string { return s.path }

// OriginalHash returns the sha256 hash used for conflict detection.
func (s *EditSession) OriginalHash() string { return s.originalHash }

// SetScalar sets a YAML string scalar at path. Existing nodes are mutated in
// place, retaining their comments and scalar style (including quoting).
func (s *EditSession) SetScalar(path []string, value string) error {
	return s.setNode(path, func(node *yaml.Node) {
		mutateScalar(node, "!!str", value)
	})
}

// SetStringSlice sets an exact string sequence at path. The existing sequence
// node, its comments, and its position in the parent mapping are retained.
func (s *EditSession) SetStringSlice(path []string, values []string) error {
	return s.setNode(path, func(node *yaml.Node) {
		head, line, foot := node.HeadComment, node.LineComment, node.FootComment
		node.Kind = yaml.SequenceNode
		node.Tag = "!!seq"
		node.Value = ""
		node.Alias = nil
		node.Content = make([]*yaml.Node, 0, len(values))
		node.HeadComment, node.LineComment, node.FootComment = head, line, foot
		for _, value := range values {
			node.Content = append(node.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: value,
			})
		}
	})
}

// SetProviderModel updates provider.model.
func (s *EditSession) SetProviderModel(model string) error {
	return s.SetScalar([]string{"provider", "model"}, model)
}

// SetSystemPromptFile selects a file-backed prompt and removes only the
// conflicting inline key.
func (s *EditSession) SetSystemPromptFile(path string) error {
	if err := s.SetScalar([]string{"agent", "systemPrompt", "file"}, path); err != nil {
		return err
	}
	return s.deleteMappingKey([]string{"agent", "systemPrompt"}, "inline")
}

// SetSystemPromptInline selects an inline prompt and removes only the
// conflicting file key.
func (s *EditSession) SetSystemPromptInline(prompt string) error {
	if err := s.SetScalar([]string{"agent", "systemPrompt", "inline"}, prompt); err != nil {
		return err
	}
	return s.deleteMappingKey([]string{"agent", "systemPrompt"}, "file")
}

// SetApprovalMode updates permissions.approvalMode.
func (s *EditSession) SetApprovalMode(mode string) error {
	return s.SetScalar([]string{"permissions", "approvalMode"}, mode)
}

// SetToolsExact updates the exact ordered capability IDs selected by agent.tools.
func (s *EditSession) SetToolsExact(tools []string) error {
	return s.SetStringSlice([]string{"agent", "tools"}, tools)
}

// SetSkillsExact rewrites the document's `skills:` selection to exactly the
// given entries, preserving surrounding comments/structure the same way
// SetStringSlice does for `agent.tools`. Each entry renders as a `{id, path?}`
// mapping (path is omitted for id-only entries, mirroring the on-disk sequence
// form parsed by SkillsSection.UnmarshalJSON). An empty slice empties the
// entry list — zero skills is a valid selection.
//
// Two on-disk shapes are handled (mirroring SkillsSection.UnmarshalJSON):
//   - sequence form `skills: [...]`: the whole node is rewritten as a sequence,
//     as before;
//   - object form `skills: {entries: [...], searchRoots: [...]}`: ONLY the
//     `entries` child is rewritten in place. `searchRoots` and any other
//     sibling key are left byte-identical — they are NOT reachable from this
//     setter and must not be silently dropped.
//
// Entries are validated BEFORE any mutation, mirroring the compile-time checks
// in resolveSkills: an empty id or a duplicate id is rejected and the document
// is left untouched.
func (s *EditSession) SetSkillsExact(entries []SkillEntry) error {
	seen := make(map[string]struct{}, len(entries))
	for i, e := range entries {
		if e.ID == "" {
			return fmt.Errorf("edit harness: skills[%d].id must not be empty", i)
		}
		if _, dup := seen[e.ID]; dup {
			return fmt.Errorf("edit harness: skills[%d].id %q is a duplicate", i, e.ID)
		}
		seen[e.ID] = struct{}{}
	}
	buildSkillsSequence := func(node *yaml.Node) {
		head, line, foot := node.HeadComment, node.LineComment, node.FootComment
		node.Kind = yaml.SequenceNode
		node.Tag = "!!seq"
		node.Value = ""
		node.Alias = nil
		node.Content = make([]*yaml.Node, 0, len(entries))
		node.HeadComment, node.LineComment, node.FootComment = head, line, foot
		for _, e := range entries {
			entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "id"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.ID},
			)
			if e.Path != "" {
				entry.Content = append(entry.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "path"},
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.Path},
				)
			}
			node.Content = append(node.Content, entry)
		}
	}
	return s.setNode([]string{"skills"}, func(node *yaml.Node) {
		if node.Kind == yaml.MappingNode {
			// Object form: rewrite ONLY the `entries` sibling in place so
			// `searchRoots` (and any other sibling key) survives untouched.
			if entriesNode, ok := mappingValue(node, "entries"); ok {
				buildSkillsSequence(entriesNode)
				return
			}
			entriesNode := &yaml.Node{}
			buildSkillsSequence(entriesNode)
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "entries"},
				entriesNode,
			)
			return
		}
		// Sequence form, or a fresh/absent node (Kind == 0): rewrite the whole
		// node as a sequence, exactly as before.
		buildSkillsSequence(node)
	})
}

// SetMaxOutputTokens updates agent.limits.maxOutputTokens.
func (s *EditSession) SetMaxOutputTokens(tokens int) error {
	return s.setInteger([]string{"agent", "limits", "maxOutputTokens"}, tokens)
}

// SetMaxTurns updates agent.limits.maxTurns.
func (s *EditSession) SetMaxTurns(turns int) error {
	return s.setInteger([]string{"agent", "limits", "maxTurns"}, turns)
}

// SetTimeout updates agent.limits.timeoutSeconds.
func (s *EditSession) SetTimeout(seconds int) error {
	return s.setInteger([]string{"agent", "limits", "timeoutSeconds"}, seconds)
}

// SetWatchEnabled updates runtime.watch.
func (s *EditSession) SetWatchEnabled(enabled bool) error {
	return s.setNode([]string{"runtime", "watch"}, func(node *yaml.Node) {
		mutateScalar(node, "!!bool", strconv.FormatBool(enabled))
	})
}

// Render serializes the edited YAML syntax tree, including retained comments,
// mapping order, scalar quoting style, and untouched node content.
func (s *EditSession) Render() ([]byte, error) {
	if s == nil || s.document == nil {
		return nil, errors.New("render harness edit: nil session")
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(s.document); err != nil {
		return nil, fmt.Errorf("render harness edit: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("render harness edit: %w", err)
	}
	return out.Bytes(), nil
}

// Validate renders and compiles the candidate without writing it.
func (s *EditSession) Validate() (*Plan, Diagnostics, error) {
	raw, err := s.Render()
	if err != nil {
		return nil, nil, err
	}
	plan, compileErr := CompileBytes(raw, s.path)
	if compileErr == nil {
		return plan, nil, nil
	}
	if ds, ok := AsDiagnostics(compileErr); ok {
		return nil, ds, nil
	}
	return nil, nil, compileErr
}

// Save validates the complete candidate, creates path+".bak" from the current
// disk version, then atomically renames a synced same-directory temp file over
// the target. Validation failures perform no writes. Temp files are removed on
// all failed paths.
func (s *EditSession) Save(opts SaveOptions) (SaveResult, error) {
	if s == nil {
		return SaveResult{}, errors.New("save harness edit: nil session")
	}
	candidate, err := s.Render()
	if err != nil {
		return SaveResult{}, err
	}
	plan, ds, err := s.Validate()
	if err != nil {
		return SaveResult{}, err
	}
	if ds.HasErrors() {
		return SaveResult{}, &ValidationError{Diagnostics: ds}
	}
	if saveHookAfterValidate != nil {
		saveHookAfterValidate()
	}

	info, err := os.Stat(s.path)
	if err != nil {
		return SaveResult{}, fmt.Errorf("stat current harness before save: %w", err)
	}
	current, err := os.ReadFile(s.path)
	if err != nil {
		return SaveResult{}, fmt.Errorf("read current harness before save: %w", err)
	}
	currentHash := hashBytes(current)
	if currentHash != s.originalHash && !opts.Force && opts.ExpectHash != currentHash {
		return SaveResult{}, &ConflictError{
			Path:         s.path,
			OriginalHash: s.originalHash,
			CurrentHash:  currentHash,
		}
	}

	backupPath := s.path + ".bak"
	if err := atomicWriteFile(backupPath, current, info.Mode().Perm()); err != nil {
		return SaveResult{}, fmt.Errorf("write harness backup: %w", err)
	}
	if err := atomicWriteFile(s.path, candidate, info.Mode().Perm()); err != nil {
		return SaveResult{}, fmt.Errorf("replace harness atomically: %w", err)
	}

	s.original = append(s.original[:0], candidate...)
	s.originalHash = hashBytes(candidate)
	s.baselinePlan = plan
	return SaveResult{
		Path:        s.path,
		BackupPath:  backupPath,
		ContentHash: s.originalHash,
		Plan:        plan,
	}, nil
}

func parseEditDocument(raw []byte) (*yaml.Node, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not supported")
		}
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("harness YAML root must be a mapping")
	}
	return &document, nil
}

func (s *EditSession) setInteger(path []string, value int) error {
	return s.setNode(path, func(node *yaml.Node) {
		mutateScalar(node, "!!int", strconv.Itoa(value))
	})
}

func (s *EditSession) setNode(path []string, mutate func(*yaml.Node)) error {
	if s == nil || s.document == nil {
		return errors.New("edit harness: nil session")
	}
	if len(path) == 0 {
		return errors.New("edit harness: path must not be empty")
	}
	current := s.document.Content[0]
	for i, key := range path {
		if key == "" {
			return fmt.Errorf("edit harness: path component %d is empty", i)
		}
		if current.Kind != yaml.MappingNode {
			return fmt.Errorf("edit harness: %q is not a mapping", joinEditPath(path[:i]))
		}
		value, ok := mappingValue(current, key)
		if !ok {
			value = &yaml.Node{}
			current.Content = append(current.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				value,
			)
		}
		if i == len(path)-1 {
			mutate(value)
			return nil
		}
		if value.Kind == 0 {
			value.Kind = yaml.MappingNode
			value.Tag = "!!map"
		}
		if value.Kind != yaml.MappingNode {
			return fmt.Errorf("edit harness: %q is not a mapping", joinEditPath(path[:i+1]))
		}
		current = value
	}
	return nil
}

func (s *EditSession) deleteMappingKey(parentPath []string, key string) error {
	current := s.document.Content[0]
	for i, component := range parentPath {
		if current.Kind != yaml.MappingNode {
			return fmt.Errorf("edit harness: %q is not a mapping", joinEditPath(parentPath[:i]))
		}
		next, ok := mappingValue(current, component)
		if !ok {
			return nil
		}
		current = next
	}
	if current.Kind != yaml.MappingNode {
		return fmt.Errorf("edit harness: %q is not a mapping", joinEditPath(parentPath))
	}
	for i := 0; i < len(current.Content); i += 2 {
		if current.Content[i].Value == key {
			current.Content = append(current.Content[:i], current.Content[i+2:]...)
			return nil
		}
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1], true
		}
	}
	return nil, false
}

func mutateScalar(node *yaml.Node, tag, value string) {
	head, line, foot, style := node.HeadComment, node.LineComment, node.FootComment, node.Style
	node.Kind = yaml.ScalarNode
	node.Tag = tag
	node.Value = value
	node.Content = nil
	node.Alias = nil
	node.HeadComment, node.LineComment, node.FootComment, node.Style = head, line, foot, style
}

func joinEditPath(path []string) string {
	if len(path) == 0 {
		return "<root>"
	}
	var out string
	for i, part := range path {
		if i > 0 {
			out += "."
		}
		out += part
	}
	return out
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".edit-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err = temp.Chmod(mode); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tempPath, path); err != nil {
		return err
	}
	if dirHandle, openErr := os.Open(dir); openErr == nil {
		syncErr := dirHandle.Sync()
		closeErr := dirHandle.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
