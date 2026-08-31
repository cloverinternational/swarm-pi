// Apply patch tool for applying V4A diff format patches.
// Ported from ii-agent's file_patch.py
package ii

import (
	"context"
	"fmt"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
)

// DiffError represents an error during patch parsing or application
type DiffError struct {
	Message string
}

func (e *DiffError) Error() string {
	return e.Message
}

// ActionType represents the type of file operation
type ActionType string

const (
	ActionTypeAdd    ActionType = "add"
	ActionTypeDelete ActionType = "delete"
	ActionTypeUpdate ActionType = "update"
)

// Chunk represents a single change chunk within an update
type Chunk struct {
	OrigIndex int      // line index of the first line in the original file
	DelLines  []string // lines to delete
	InsLines  []string // lines to insert
}

// PatchAction represents a single file operation in a patch
type PatchAction struct {
	Type     ActionType
	NewFile  string  // for Add: the new file contents
	Chunks   []Chunk // for Update: the chunks to apply
	MovePath string  // for Update with rename
}

// Patch represents a complete patch with multiple file operations
type Patch struct {
	Actions map[string]*PatchAction
}

// FileChange represents a single file change in a commit
type FileChange struct {
	Type       ActionType
	OldContent string
	NewContent string
	MovePath   string
}

// Commit represents the final result of applying a patch
type Commit struct {
	Changes map[string]*FileChange
}

// Parser parses patch text into structured patch operations
type Parser struct {
	currentFiles map[string]string
	lines        []string
	index        int
	patch        *Patch
	fuzz         int
}

// NewParser creates a new parser
func NewParser(currentFiles map[string]string, lines []string) *Parser {
	return &Parser{
		currentFiles: currentFiles,
		lines:        lines,
		index:        1, // Skip "*** Begin Patch" line
		patch:        &Patch{Actions: make(map[string]*PatchAction)},
		fuzz:         0,
	}
}

func (p *Parser) isDone(prefixes ...string) bool {
	if p.index >= len(p.lines) {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(p.lines[p.index], prefix) {
			return true
		}
	}
	return false
}

func (p *Parser) startsWith(prefixes ...string) bool {
	if p.index >= len(p.lines) {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(p.lines[p.index], prefix) {
			return true
		}
	}
	return false
}

func (p *Parser) readStr(prefix string) string {
	if p.index >= len(p.lines) {
		return ""
	}
	if strings.HasPrefix(p.lines[p.index], prefix) {
		text := p.lines[p.index][len(prefix):]
		p.index++
		return text
	}
	return ""
}

func (p *Parser) Parse() error {
	for !p.isDone("*** End Patch") {
		// Skip empty lines
		if p.index < len(p.lines) && strings.TrimSpace(p.lines[p.index]) == "" {
			p.index++
			continue
		}

		// Try Update File
		path := p.readStr("*** Update File: ")
		if path != "" {
			if _, exists := p.patch.Actions[path]; exists {
				return &DiffError{Message: fmt.Sprintf("Update File Error: Duplicate Path: %s", path)}
			}
			moveTo := p.readStr("*** Move to: ")
			if _, exists := p.currentFiles[path]; !exists {
				return &DiffError{Message: fmt.Sprintf("Update File Error: Missing File: %s", path)}
			}
			text := p.currentFiles[path]
			action, err := p.parseUpdateFile(text)
			if err != nil {
				return err
			}
			action.MovePath = moveTo
			p.patch.Actions[path] = action
			continue
		}

		// Try Delete File
		path = p.readStr("*** Delete File: ")
		if path != "" {
			if _, exists := p.patch.Actions[path]; exists {
				return &DiffError{Message: fmt.Sprintf("Delete File Error: Duplicate Path: %s", path)}
			}
			if _, exists := p.currentFiles[path]; !exists {
				return &DiffError{Message: fmt.Sprintf("Delete File Error: Missing File: %s", path)}
			}
			p.patch.Actions[path] = &PatchAction{Type: ActionTypeDelete}
			continue
		}

		// Try Add File
		path = p.readStr("*** Add File: ")
		if path != "" {
			if _, exists := p.patch.Actions[path]; exists {
				return &DiffError{Message: fmt.Sprintf("Add File Error: Duplicate Path: %s", path)}
			}
			action, err := p.parseAddFile()
			if err != nil {
				return err
			}
			p.patch.Actions[path] = action
			continue
		}

		if p.index < len(p.lines) {
			return &DiffError{Message: fmt.Sprintf("Unknown Line: %s", p.lines[p.index])}
		}
	}

	if !p.startsWith("*** End Patch") {
		return &DiffError{Message: "Missing End Patch"}
	}
	p.index++
	return nil
}

func (p *Parser) parseUpdateFile(text string) (*PatchAction, error) {
	action := &PatchAction{
		Type:   ActionTypeUpdate,
		Chunks: []Chunk{},
	}
	lines := strings.Split(text, "\n")
	lineIndex := 0

	for !p.isDone("*** End Patch", "*** Update File:", "*** Delete File:", "*** Add File:", "*** End of File") {
		defStr := p.readStr("@@ ")
		sectionStr := ""
		if defStr == "" && p.index < len(p.lines) && p.lines[p.index] == "@@" {
			sectionStr = p.lines[p.index]
			p.index++
		}

		if defStr == "" && sectionStr == "" && lineIndex > 0 {
			if p.index < len(p.lines) {
				return nil, &DiffError{Message: fmt.Sprintf("Invalid Line:\n%s", p.lines[p.index])}
			}
		}

		// Handle context jumping with @@ markers
		if strings.TrimSpace(defStr) != "" {
			found := false
			// Look for exact match first
			for i := lineIndex; i < len(lines); i++ {
				if lines[i] == defStr {
					lineIndex = i + 1
					found = true
					break
				}
			}
			// Try stripped match
			if !found {
				for i := lineIndex; i < len(lines); i++ {
					if strings.TrimSpace(lines[i]) == strings.TrimSpace(defStr) {
						lineIndex = i + 1
						p.fuzz++
						found = true
						break
					}
				}
			}
		}

		// Parse the next section of changes
		contextLines, chunks, endIndex, isEOF := p.peekNextSection()

		if len(contextLines) == 0 && len(chunks) == 0 {
			break
		}

		newIndex, fuzz := findContext(lines, contextLines, lineIndex, isEOF)
		if newIndex == -1 {
			contextText := strings.Join(contextLines, "\n")
			if isEOF {
				return nil, &DiffError{Message: fmt.Sprintf("Invalid EOF Context %d:\n%s", lineIndex, contextText)}
			}
			return nil, &DiffError{Message: fmt.Sprintf("Invalid Context %d:\n%s", lineIndex, contextText)}
		}

		p.fuzz += fuzz
		for i := range chunks {
			chunks[i].OrigIndex += newIndex
			action.Chunks = append(action.Chunks, chunks[i])
		}
		lineIndex = newIndex + len(contextLines)
		p.index = endIndex
	}

	return action, nil
}

func (p *Parser) parseAddFile() (*PatchAction, error) {
	var lines []string
	for !p.isDone("*** End Patch", "*** Update File:", "*** Delete File:", "*** Add File:") {
		if p.index >= len(p.lines) {
			break
		}
		s := p.lines[p.index]
		p.index++

		// Skip empty lines between file sections
		if strings.TrimSpace(s) == "" {
			continue
		}
		if !strings.HasPrefix(s, "+") {
			return nil, &DiffError{Message: fmt.Sprintf("Invalid Add File Line: %s", s)}
		}
		lines = append(lines, s[1:])
	}
	return &PatchAction{
		Type:    ActionTypeAdd,
		NewFile: strings.Join(lines, "\n"),
	}, nil
}

func (p *Parser) peekNextSection() ([]string, []Chunk, int, bool) {
	var oldLines []string
	var delLines []string
	var insLines []string
	var chunks []Chunk
	mode := "keep"
	origIndex := p.index

	for p.index < len(p.lines) {
		s := p.lines[p.index]

		// Check for section terminators
		if strings.HasPrefix(s, "@@") ||
			strings.HasPrefix(s, "*** End Patch") ||
			strings.HasPrefix(s, "*** Update File:") ||
			strings.HasPrefix(s, "*** Delete File:") ||
			strings.HasPrefix(s, "*** Add File:") ||
			strings.HasPrefix(s, "*** End of File") ||
			s == "***" {
			break
		}

		if strings.HasPrefix(s, "***") {
			// Invalid *** line
			break
		}

		p.index++
		lastMode := mode

		// Handle empty lines as context
		if s == "" {
			s = " "
		}

		switch s[0] {
		case '+':
			mode = "add"
		case '-':
			mode = "delete"
		case ' ':
			mode = "keep"
		default:
			// Unexpected character, revert and break
			p.index--
			goto done
		}

		content := s[1:]

		// When switching from add/delete to keep, flush the chunk
		if mode == "keep" && lastMode != mode {
			if len(insLines) > 0 || len(delLines) > 0 {
				chunks = append(chunks, Chunk{
					OrigIndex: len(oldLines) - len(delLines),
					DelLines:  delLines,
					InsLines:  insLines,
				})
			}
			delLines = nil
			insLines = nil
		}

		switch mode {
		case "delete":
			delLines = append(delLines, content)
			oldLines = append(oldLines, content)
		case "add":
			insLines = append(insLines, content)
		case "keep":
			oldLines = append(oldLines, content)
		}
	}

done:
	// Flush any remaining changes
	if len(insLines) > 0 || len(delLines) > 0 {
		chunks = append(chunks, Chunk{
			OrigIndex: len(oldLines) - len(delLines),
			DelLines:  delLines,
			InsLines:  insLines,
		})
	}

	// Check for EOF marker
	if p.index < len(p.lines) && p.lines[p.index] == "*** End of File" {
		p.index++
		return oldLines, chunks, p.index, true
	}

	if p.index == origIndex && p.index < len(p.lines) {
		// Nothing parsed
		return nil, nil, p.index, false
	}

	return oldLines, chunks, p.index, false
}

// findContextCore finds context lines within the file
func findContextCore(lines, context []string, start int) (int, int) {
	if len(context) == 0 {
		return start, 0
	}

	maxStart := len(lines) - len(context)
	if maxStart < 0 {
		return -1, 0
	}

	// Pass 1: Exact match
	for i := start; i <= maxStart; i++ {
		match := true
		for j := range context {
			if i+j >= len(lines) || lines[i+j] != context[j] {
				match = false
				break
			}
		}
		if match {
			return i, 0
		}
	}

	// Pass 2: Trim trailing whitespace
	for i := start; i <= maxStart; i++ {
		match := true
		for j := range context {
			if i+j >= len(lines) || strings.TrimRight(lines[i+j], " \t") != strings.TrimRight(context[j], " \t") {
				match = false
				break
			}
		}
		if match {
			return i, 1
		}
	}

	// Pass 3: Trim both sides
	for i := start; i <= maxStart; i++ {
		match := true
		for j := range context {
			if i+j >= len(lines) || strings.TrimSpace(lines[i+j]) != strings.TrimSpace(context[j]) {
				match = false
				break
			}
		}
		if match {
			return i, 100
		}
	}

	return -1, 0
}

// findContext finds context with EOF handling
func findContext(lines, context []string, start int, eof bool) (int, int) {
	if eof && len(lines) >= len(context) {
		newIndex, fuzz := findContextCore(lines, context, len(lines)-len(context))
		if newIndex != -1 {
			return newIndex, fuzz
		}
		newIndex, fuzz = findContextCore(lines, context, start)
		return newIndex, fuzz + 10000
	}
	return findContextCore(lines, context, start)
}

// ApplyPatchTool implements the apply_patch functionality
type ApplyPatchTool struct {
	delegate *forge.ApplyPatchTool
}

// NewApplyPatchTool creates a new ApplyPatchTool
func NewApplyPatchTool(wm *WorkspaceManager) *ApplyPatchTool {
	workspace := ""
	if wm != nil {
		workspace = wm.WorkspacePath()
	}
	return &ApplyPatchTool{
		delegate: forge.NewApplyPatchTool(workspace),
	}
}

// Name returns the tool name
func (t *ApplyPatchTool) Name() string {
	return "apply_patch"
}

// DisplayName returns the human-readable display name
func (t *ApplyPatchTool) DisplayName() string {
	return "Apply Patch"
}

// Description returns the tool description
func (t *ApplyPatchTool) Description() string {
	if t.delegate != nil {
		return t.delegate.Description()
	}
	return "The `apply_patch` tool can be used to edit files."
}

// Parameters returns the JSON schema for tool parameters
func (t *ApplyPatchTool) Parameters() any {
	if t.delegate != nil {
		return t.delegate.Parameters()
	}
	return map[string]any{"type": "object"}
}

// Execute applies the patch
func (t *ApplyPatchTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if t.delegate == nil {
		return nil, sdkerr.Permanent("apply_patch.not_initialized", "apply_patch tool is not initialized")
	}
	return t.delegate.Execute(ctx, params)
}

// Validate checks if the given parameters are valid
func (t *ApplyPatchTool) Validate(params map[string]any) error {
	if t.delegate != nil {
		return t.delegate.Validate(params)
	}
	return fmt.Errorf("apply_patch tool is not initialized")
}

// IsIdempotent returns false as patches modify state
func (t *ApplyPatchTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies files
func (t *ApplyPatchTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions
func (t *ApplyPatchTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileWrite}
}

// SupportedContentTypes returns the content types this tool can produce
func (t *ApplyPatchTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use
func (t *ApplyPatchTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns confirmation details for patch operations
func (t *ApplyPatchTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	input, _ := params["input"].(string)
	return &ConfirmationDetails{
		Type:    ConfirmationTypeEdit,
		Message: fmt.Sprintf("Apply the following patch:\n%s", input),
	}
}

// Metadata returns metadata about the patch format
func (t *ApplyPatchTool) Metadata() map[string]any {
	return map[string]any{
		"format": map[string]any{
			"type":   "custom",
			"syntax": "lark",
			"definition": `start: begin_patch hunk+ end_patch
begin_patch: "*** Begin Patch" LF
end_patch: "*** End Patch" LF?
hunk: add_hunk | delete_hunk | update_hunk
add_hunk: "*** Add File: " filename LF add_line+
delete_hunk: "*** Delete File: " filename LF
update_hunk: "*** Update File: " filename LF change_move? change?
filename: /(.+)/
add_line: "+" /(.*)/ LF -> line
change_move: "*** Move to: " filename LF
change: (change_context | change_line)+ eof_line?
change_context: ("@@" | "@@ " /(.+)/) LF
change_line: ("+" | "-" | " ") /(.*)/ LF
eof_line: "*** End of File" LF
%import common.LF`,
		},
	}
}
