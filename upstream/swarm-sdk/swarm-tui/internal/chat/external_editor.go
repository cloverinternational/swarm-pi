package chat

// External editor integration.
//
// Lets the user pop the current prompt out into their $EDITOR (nvim, vim,
// nano, VS Code, ...) with Ctrl+E — the same ergonomics Claude Code exposes.
// The edited buffer is read back into the prompt input when the editor exits.
//
// Terminal handoff is done via tea.ExecProcess so bubbletea suspends its own
// renderer + input reader, hands the real TTY to the child editor, and then
// restores itself when the editor exits. Doing a bare exec.Cmd.Run() here
// would fight bubbletea for stdin and corrupt the terminal, so we never do
// that on the interactive path.

import (
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// editorFinishedMsg is delivered after the external editor process exits. It
// carries the temp file path (so we can read the edited content back and then
// delete it) and any error from launching / running the editor.
type editorFinishedMsg struct {
	path string
	err  error
}

// fallbackEditors is the ordered list of editors we probe on $PATH when
// neither $VISUAL nor $EDITOR is set. nvim is preferred first (matches the
// user's stated preference); vi is the POSIX-guaranteed last resort.
var fallbackEditors = []string{"nvim", "vim", "nano", "vi"}

// resolveEditor determines which editor binary to launch and any leading
// arguments the user configured.
//
// Resolution order mirrors Claude Code (utils/editor.ts):
//  1. $VISUAL   (full-screen editor preference)
//  2. $EDITOR   (general editor preference)
//  3. first of fallbackEditors found on $PATH
//
// The env values may contain flags (e.g. "code --wait" or "nvim -u NONE"), so
// we split on whitespace: the first token is the binary, the rest are args
// that must be forwarded ahead of the file path.
//
// Returns ok=false when no usable editor can be found.
func resolveEditor() (bin string, args []string, ok bool) {
	for _, envVar := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
			parts := strings.Fields(v)
			return parts[0], parts[1:], true
		}
	}

	for _, candidate := range fallbackEditors {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil, true
		}
	}

	return "", nil, false
}

// openPromptInExternalEditor writes the current prompt to a temp file and
// returns a tea.Cmd that launches the user's editor on it. When the editor
// exits, an editorFinishedMsg is dispatched back into Update so the edited
// content can be read back (see handleEditorFinished).
//
// GetSubmitValue() is used rather than Value() so collapsed paste indicators
// are expanded to their real text before the user edits them — otherwise the
// user would see an opaque "[Pasted N lines]" chip instead of the content.
func (a *App) openPromptInExternalEditor() tea.Cmd {
	bin, args, ok := resolveEditor()
	if !ok {
		a.addNotification("warning", i18n.T("classic_chat_2.editor.not_found"))
		return nil
	}

	// Expand paste indicators so the user edits real content.
	content := a.textInput.GetSubmitValue()

	// .md suffix gives the editor sensible syntax highlighting / soft-wrap for
	// prose prompts without committing us to any particular filetype.
	tmp, err := os.CreateTemp("", "swarm-prompt-*.md")
	if err != nil {
		a.addNotification("error", i18n.T("classic_chat_2.editor.create_temp_failed", err))
		return nil
	}
	path := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		a.addNotification("error", i18n.T("classic_chat_2.editor.write_temp_failed", err))
		return nil
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		a.addNotification("error", i18n.T("classic_chat_2.editor.finalize_temp_failed", err))
		return nil
	}

	// Binary first, configured args next, file path last. exec.Command wires
	// the child to inherit the current process' stdio, and tea.ExecProcess
	// handles the terminal suspend/restore dance around it.
	cmdArgs := append(append([]string{}, args...), path)
	cmd := exec.Command(bin, cmdArgs...)

	logDebug("[externalEditor] launching %s %v on %s", bin, cmdArgs, path)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorFinishedMsg{path: path, err: err}
	})
}

// handleEditorFinished is invoked from Update when the external editor process
// exits. It reads the (possibly edited) content back into the prompt input and
// always cleans up the temp file.
func (a *App) handleEditorFinished(msg editorFinishedMsg) (tea.Model, tea.Cmd) {
	// Always remove the temp file, regardless of outcome.
	defer func() {
		if msg.path != "" {
			if rmErr := os.Remove(msg.path); rmErr != nil {
				logDebug("[externalEditor] temp cleanup failed for %s: %v", msg.path, rmErr)
			}
		}
	}()

	if msg.err != nil {
		a.addNotification("error", i18n.T("classic_chat_2.editor.exited_error", msg.err))
		return a, nil
	}

	data, err := os.ReadFile(msg.path)
	if err != nil {
		a.addNotification("error", i18n.T("classic_chat_2.editor.read_failed", err))
		return a, nil
	}

	edited := string(data)

	// Editors conventionally append a single trailing newline on save. Trim
	// exactly one so a round-trip through the editor doesn't grow the prompt,
	// while preserving an intentional blank final line (\n\n).
	if strings.HasSuffix(edited, "\n") && !strings.HasSuffix(edited, "\n\n") {
		edited = strings.TrimSuffix(edited, "\n")
	}

	// Only mutate the buffer when the content actually changed — avoids
	// clobbering cursor state / paste indicators on a no-op edit or quit.
	if edited != a.textInput.GetSubmitValue() {
		a.textInput.SetValue(edited)
		logDebug("[externalEditor] prompt updated from editor (%d chars)", len(edited))
	}

	return a, nil
}
