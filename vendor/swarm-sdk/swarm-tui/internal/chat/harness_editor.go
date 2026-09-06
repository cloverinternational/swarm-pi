package chat

// Harness Phase 4d — interactive TUI harness editor.
//
// Available ONLY when the interactive TUI was launched with an active harness
// (AppOptions.HarnessPath set). It lets an operator inspect the REDACTED
// effective harness posture (HarnessSnapshot), draft-edit the hot-appliable
// fields through a comment-preserving harness.EditSession, preview a redacted
// semantic diff, and transactionally Validate -> Save -> ApplyHarnessPlan the
// result — surfacing the HOT / RESTART-REQUIRED / FORBIDDEN classification. It
// can also toggle the opt-in WatchHarness reload controller.
//
// It is deliberately self-contained: the only wiring it needs is a harness path,
// an AllowYolo posture, and a small client interface (satisfied by *client.Client
// and by a fake in tests). It NEVER renders secrets: prompts appear only as
// hashes, credentials only as their provenance label, paths only as hashes.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// harnessEditorClient is the minimal live-client surface the editor needs. It is
// satisfied by *client.Client; tests inject a fake so no real client.New/provider
// is required.
type harnessEditorClient interface {
	HarnessSnapshot() sdkclient.HarnessSnapshot
	// ApplyHarnessPlanAudited (Phase 11b), not the bare ApplyHarnessPlan: the
	// manual editor "apply" action is a privileged live reconfiguration and
	// must not bypass the audit wrapper defined in swarm-sdk
	// client/harness_audit.go.
	ApplyHarnessPlanAudited(context.Context, *harness.Plan, sdkclient.ApplyHarnessOptions) (sdkclient.ApplyHarnessResult, error)
	WatchHarness(context.Context, string, sdkclient.ReloadPolicy, func(sdkclient.ReloadOutcome)) error
}

// harnessFieldKind identifies one editable, hot-appliable configuration field.
type harnessFieldKind int

const (
	fieldModel harnessFieldKind = iota
	fieldTools
	fieldPromptInline
	fieldPromptFile
	fieldApprovalMode
	fieldMaxOutputTokens
	fieldMaxTurns
	fieldTimeout
	fieldSkills
)

type harnessField struct {
	kind  harnessFieldKind
	label string
	// secret marks a field whose entered value must NEVER be echoed back (the
	// inline system prompt). Its presence is surfaced, never its content.
	secret bool
}

var harnessEditorFields = []harnessField{
	{kind: fieldModel, label: "classic_chat_2.harness.field.model"},
	{kind: fieldTools, label: "classic_chat_2.harness.field.tools"},
	{kind: fieldPromptInline, label: "classic_chat_2.harness.field.prompt_inline", secret: true},
	{kind: fieldPromptFile, label: "classic_chat_2.harness.field.prompt_file"},
	{kind: fieldApprovalMode, label: "classic_chat_2.harness.field.approval_mode"},
	{kind: fieldMaxOutputTokens, label: "classic_chat_2.harness.field.max_output_tokens"},
	{kind: fieldMaxTurns, label: "classic_chat_2.harness.field.max_turns"},
	{kind: fieldTimeout, label: "classic_chat_2.harness.field.timeout"},
	{kind: fieldSkills, label: "classic_chat_2.harness.field.skills"},
}

// harnessEditorModel is the interactive editor. It implements commands.Command so
// it slots into the App's existing activeCommand overlay (Update/View routing).
type harnessEditorModel struct {
	client    harnessEditorClient
	path      string
	allowYolo bool

	session  *harness.EditSession
	openDiag harness.Diagnostics
	openErr  error

	snapshot sdkclient.HarnessSnapshot

	selected int
	editing  bool
	buffer   string

	// pending records committed, non-secret field edits for display. Secret
	// fields record only a redacted marker.
	pending map[harnessFieldKind]string

	diff    *harness.EditDiff
	diffErr error

	applyResult *sdkclient.ApplyHarnessResult
	diagnostics harness.Diagnostics
	status      string
	lastErr     error

	mu          sync.Mutex
	watching    bool
	watchCancel context.CancelFunc
	watchStatus string

	width  int
	height int
	active bool
}

// newHarnessEditor opens an edit session over path and reads the current redacted
// snapshot. An empty path yields nil (the open-hook is inert for non-harness
// sessions). A read/parse failure still returns a usable model that reports the
// error rather than editing a non-existent harness.
func newHarnessEditor(path string, allowYolo bool, cl harnessEditorClient) *harnessEditorModel {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	m := &harnessEditorModel{
		client:    cl,
		path:      path,
		allowYolo: allowYolo,
		pending:   make(map[harnessFieldKind]string),
		active:    true,
	}
	if cl != nil {
		m.snapshot = cl.HarnessSnapshot()
	}
	session, diag, err := harness.OpenEditSession(path)
	m.session = session
	m.openDiag = diag
	m.openErr = err
	if err != nil {
		m.status = i18n.T("classic_chat_2.harness.status.open_failed", err)
	}
	return m
}

// ── commands.Command interface ──────────────────────────────────────────────

func (m *harnessEditorModel) Name() string { return "harness" }
func (m *harnessEditorModel) Description() string {
	return i18n.T("classic_chat_2.harness.description")
}
func (m *harnessEditorModel) Aliases() []string        { return nil }
func (m *harnessEditorModel) Execute([]string) tea.Cmd { return nil }
func (m *harnessEditorModel) IsInteractive() bool      { return m.active }

func (m *harnessEditorModel) Update(msg tea.Msg) (commands.Command, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		m.handleKey(msg.String())
	}
	return m, nil
}

func (m *harnessEditorModel) handleKey(key string) {
	if m.editing {
		switch key {
		case "esc":
			m.editing = false
			m.buffer = ""
		case "enter":
			m.commitEdit(harnessEditorFields[m.selected].kind, m.buffer)
			m.editing = false
			m.buffer = ""
		case "backspace":
			if n := len(m.buffer); n > 0 {
				m.buffer = m.buffer[:n-1]
			}
		default:
			if len(key) == 1 {
				m.buffer += key
			} else if key == "space" {
				m.buffer += " "
			}
		}
		return
	}

	switch key {
	case "esc", "q":
		m.stopWatch()
		m.active = false
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(harnessEditorFields)-1 {
			m.selected++
		}
	case "enter", "e":
		m.editing = true
		m.buffer = ""
	case "p":
		m.refreshDiff()
	case "a":
		m.apply()
	case "w":
		m.toggleWatch()
	}
}

// commitEdit applies one field edit to the session via its typed setter, records
// a (redacted) pending marker, then refreshes the diff preview.
func (m *harnessEditorModel) commitEdit(kind harnessFieldKind, value string) {
	if m.session == nil {
		m.status = i18n.T("classic_chat_2.harness.status.no_session")
		return
	}
	value = strings.TrimSpace(value)
	var err error
	switch kind {
	case fieldModel:
		err = m.session.SetProviderModel(value)
	case fieldTools:
		err = m.session.SetToolsExact(splitHarnessTools(value))
	case fieldPromptInline:
		err = m.session.SetSystemPromptInline(value)
	case fieldPromptFile:
		err = m.session.SetSystemPromptFile(value)
	case fieldApprovalMode:
		err = m.session.SetApprovalMode(value)
	case fieldMaxOutputTokens:
		err = m.setIntField(m.session.SetMaxOutputTokens, value)
	case fieldMaxTurns:
		err = m.setIntField(m.session.SetMaxTurns, value)
	case fieldTimeout:
		err = m.setIntField(m.session.SetTimeout, value)
	case fieldSkills:
		err = m.session.SetSkillsExact(splitHarnessSkills(value))
	}
	if err != nil {
		m.status = i18n.T("classic_chat_2.harness.status.edit_failed", err)
		return
	}
	if kind == fieldPromptInline {
		m.pending[kind] = i18n.T("classic_chat_2.harness.inline_redacted", len(value))
	} else {
		m.pending[kind] = value
	}
	m.status = i18n.T("classic_chat_2.harness.status.staged", harnessFieldLabel(kind))
	m.refreshDiff()
}

func (m *harnessEditorModel) setIntField(set func(int) error, value string) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("classic_chat_2.harness.expected_integer"), err)
	}
	return set(n)
}

// refreshDiff computes the redacted semantic before/after diff. A validation
// error is recorded (never a raw value) and the previous diff cleared.
func (m *harnessEditorModel) refreshDiff() {
	if m.session == nil {
		return
	}
	d, err := m.session.Diff()
	if err != nil {
		m.diff = nil
		m.diffErr = err
		return
	}
	m.diff = &d
	m.diffErr = nil
}

// apply runs the transaction: Validate -> (on success) Save -> ApplyHarnessPlan.
// On validation failure it shows diagnostics and does NOT save or apply. The
// outcome is classified Applied / RestartRequired / Rejected.
func (m *harnessEditorModel) apply() {
	m.diagnostics = nil
	m.lastErr = nil
	m.applyResult = nil
	if m.session == nil {
		m.status = i18n.T("classic_chat_2.harness.status.no_session")
		return
	}

	_, ds, err := m.session.Validate()
	if err != nil {
		m.status = i18n.T("classic_chat_2.harness.status.validate_error", err)
		m.lastErr = err
		return
	}
	if ds.HasErrors() {
		m.diagnostics = ds
		m.status = i18n.T("classic_chat_2.harness.status.validation_failed")
		return
	}

	saved, err := m.session.Save(harness.SaveOptions{})
	if err != nil {
		var ve *harness.ValidationError
		if errors.As(err, &ve) {
			m.diagnostics = ve.Diagnostics
			m.status = i18n.T("classic_chat_2.harness.status.validation_failed")
			return
		}
		m.status = i18n.T("classic_chat_2.harness.status.save_failed", err)
		m.lastErr = err
		return
	}

	if m.client == nil {
		m.status = i18n.T("classic_chat_2.harness.status.saved_restart")
		return
	}

	// Audited (Phase 11b): the manual editor "apply" action is a privileged
	// live reconfiguration, so it must not bypass the audit wrapper — see
	// swarm-sdk client/harness_audit.go's ApplyHarnessPlanAudited.
	res, err := m.client.ApplyHarnessPlanAudited(context.Background(), saved.Plan, sdkclient.ApplyHarnessOptions{AllowYolo: m.allowYolo})
	m.applyResult = &res
	switch {
	case err == nil && len(res.Applied) > 0:
		m.status = i18n.T("classic_chat_2.harness.status.applied", strings.Join(res.Applied, ", "))
	case errors.Is(err, sdkclient.ErrHarnessRestartRequired):
		m.status = i18n.T("classic_chat_2.harness.status.restart_required", strings.Join(res.RestartRequired, ", "))
		m.lastErr = err
	case err != nil:
		m.status = i18n.T("classic_chat_2.harness.status.rejected", err)
		m.lastErr = err
	default:
		m.status = i18n.T("classic_chat_2.harness.status.saved_no_changes")
	}
}

// toggleWatch starts/stops the opt-in reload controller (HOT auto-apply). It is
// off by default and stops cleanly on close.
func (m *harnessEditorModel) toggleWatch() {
	m.mu.Lock()
	watching := m.watching
	m.mu.Unlock()
	if watching {
		m.stopWatch()
		return
	}
	if m.client == nil {
		m.status = i18n.T("classic_chat_2.harness.status.no_watch_client")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.watching = true
	m.watchCancel = cancel
	m.watchStatus = i18n.T("classic_chat_2.harness.watch.watching")
	m.mu.Unlock()
	go func() {
		err := m.client.WatchHarness(ctx, m.path, sdkclient.ReloadPolicy{AutoApplyHot: true}, m.onReloadOutcome)
		m.mu.Lock()
		m.watching = false
		if err != nil && ctx.Err() == nil {
			m.watchStatus = i18n.T("classic_chat_2.harness.watch.stopped_error", err)
		} else {
			m.watchStatus = i18n.T("classic_chat_2.harness.watch.stopped")
		}
		m.mu.Unlock()
	}()
}

func (m *harnessEditorModel) stopWatch() {
	m.mu.Lock()
	cancel := m.watchCancel
	m.watchCancel = nil
	m.watching = false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *harnessEditorModel) onReloadOutcome(o sdkclient.ReloadOutcome) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case o.Applied:
		m.watchStatus = i18n.T("classic_chat_2.harness.watch.applied")
	case o.RestartRequired:
		m.watchStatus = i18n.T("classic_chat_2.harness.watch.restart")
	case o.Rejected:
		m.watchStatus = i18n.T("classic_chat_2.harness.watch.rejected")
	default:
		m.watchStatus = i18n.T("classic_chat_2.harness.watch.observed")
	}
}

// ── View ────────────────────────────────────────────────────────────────────

func (m *harnessEditorModel) View() string {
	var b strings.Builder
	b.WriteString(i18n.T("classic_chat_2.harness.title") + "\n")
	b.WriteString(i18n.T("classic_chat_2.harness.path", m.path) + "\n")
	if m.openErr != nil {
		b.WriteString("\n" + i18n.T("classic_chat_2.harness.error", m.openErr) + "\n")
		b.WriteString("\n" + i18n.T("classic_chat_2.harness.close_hint") + "\n")
		return b.String()
	}

	b.WriteString(m.renderSnapshot())
	b.WriteString("\n" + i18n.T("classic_chat_2.harness.editable_fields") + "\n")
	for i, f := range harnessEditorFields {
		cursor := "  "
		if i == m.selected {
			cursor = "> "
		}
		val := m.pendingLabel(f)
		label := i18n.T(f.label)
		line := fmt.Sprintf("%s%-32s %s", cursor, label, val)
		if m.editing && i == m.selected {
			echo := m.buffer
			if f.secret {
				echo = strings.Repeat("*", len(m.buffer))
			}
			line = fmt.Sprintf("%s%-32s [%s]", cursor, label, echo)
		}
		b.WriteString(line + "\n")
	}

	b.WriteString(m.renderDiff())
	b.WriteString(m.renderOutcome())

	m.mu.Lock()
	ws := m.watchStatus
	watching := m.watching
	m.mu.Unlock()
	b.WriteString("\n" + i18n.T("classic_chat_2.harness.watch.label") + " ")
	if watching {
		b.WriteString(i18n.T("classic_chat_2.harness.on"))
	} else {
		b.WriteString(i18n.T("classic_chat_2.harness.off"))
	}
	if ws != "" {
		b.WriteString(" — " + ws)
	}
	b.WriteString("\n")

	if m.status != "" {
		b.WriteString("\n" + i18n.T("classic_chat_2.harness.status", m.status) + "\n")
	}
	if len(m.diagnostics) > 0 {
		b.WriteString("\n" + i18n.T("classic_chat_2.harness.diagnostics") + "\n")
		for _, d := range m.diagnostics {
			b.WriteString("  - " + d.Error() + "\n")
		}
	}
	return b.String()
}

func (m *harnessEditorModel) renderSnapshot() string {
	s := m.snapshot
	var b strings.Builder
	b.WriteString("\n" + i18n.T("classic_chat_2.harness.snapshot.title") + "\n")
	if !s.Harness {
		b.WriteString(i18n.T("classic_chat_2.harness.snapshot.none") + "\n")
		return b.String()
	}
	b.WriteString(i18n.T("classic_chat_2.harness.snapshot.plan", s.PlanName, shortHarnessHash(s.PlanDigest)) + "\n")
	b.WriteString(i18n.T("classic_chat_2.harness.snapshot.provider", s.Provider, s.Model) + "\n")
	b.WriteString(i18n.T("classic_chat_2.harness.snapshot.prompt", shortHarnessHash(s.SystemPromptSHA256), s.SystemPromptBytes) + "\n")
	b.WriteString(i18n.T("classic_chat_2.harness.snapshot.approval", s.ApprovalMode, s.WorkspaceBoundary, s.AllowMutation) + "\n")
	b.WriteString(i18n.T("classic_chat_2.harness.snapshot.catalogs", strings.Join(s.SelectedCatalogIDs, ", ")) + "\n")
	b.WriteString(i18n.T("classic_chat_2.harness.snapshot.exposed", strings.Join(s.ExposedTools, ", ")) + "\n")
	if s.CredentialSource != "" {
		b.WriteString(i18n.T("classic_chat_2.harness.snapshot.credential", s.CredentialSource) + "\n")
	}
	b.WriteString(i18n.T("classic_chat_2.harness.snapshot.hashes", shortHarnessHash(s.WorkspaceSHA256), shortHarnessHash(s.StorageSHA256)) + "\n")
	b.WriteString(renderHarnessSkills(s.Skills, s.SkillSearchRoots))
	return b.String()
}

// renderHarnessSkills renders the selected skills (INSPECTION) — id, path
// label, short content hash, and provenance source — plus the resolved
// search roots. It NEVER shows a SKILL.md body or an absolute host path:
// every field it reads is already redacted on sdkclient.HarnessSkillView.
func renderHarnessSkills(skills []sdkclient.HarnessSkillView, roots []string) string {
	if len(skills) == 0 && len(roots) == 0 {
		return ""
	}
	var b strings.Builder
	if len(skills) > 0 {
		b.WriteString(i18n.T("classic_chat_2.harness.snapshot.skills") + "\n")
		for _, sk := range skills {
			line := "    - " + sk.ID
			if sk.Path != "" {
				line += "  path=" + sk.Path
			}
			if sk.ContentHash != "" {
				line += "  hash=" + shortHarnessHash(sk.ContentHash)
			}
			line += "  source=" + sk.Source
			b.WriteString(line + "\n")
		}
	}
	if len(roots) > 0 {
		b.WriteString(i18n.T("classic_chat_2.harness.snapshot.skill_roots", strings.Join(roots, ", ")) + "\n")
	}
	return b.String()
}

func (m *harnessEditorModel) renderDiff() string {
	if m.diffErr != nil {
		return "\n" + i18n.T("classic_chat_2.harness.diff.invalid", m.diffErr) + "\n"
	}
	if m.diff == nil {
		return ""
	}
	if !m.diff.Changed {
		return "\n" + i18n.T("classic_chat_2.harness.diff.no_changes") + "\n"
	}
	before, after := m.diff.Before, m.diff.After
	var b strings.Builder
	b.WriteString("\n" + i18n.T("classic_chat_2.harness.diff.title") + "\n")
	appendDiffLine(&b, "model", before.Model, after.Model)
	appendDiffLine(&b, "promptHash", shortHarnessHash(before.PromptHash), shortHarnessHash(after.PromptHash))
	appendDiffLine(&b, "tools", strings.Join(before.Tools, ","), strings.Join(after.Tools, ","))
	appendDiffLine(&b, "approvalMode", before.ApprovalMode, after.ApprovalMode)
	appendDiffLine(&b, "maxOutputTokens", strconv.Itoa(before.Limits.MaxOutputTokens), strconv.Itoa(after.Limits.MaxOutputTokens))
	appendDiffLine(&b, "maxTurns", strconv.Itoa(before.Limits.MaxTurns), strconv.Itoa(after.Limits.MaxTurns))
	appendDiffLine(&b, "timeoutSeconds", strconv.Itoa(before.Limits.TimeoutSeconds), strconv.Itoa(after.Limits.TimeoutSeconds))
	appendDiffLine(&b, "skills", strings.Join(before.Skills, ","), strings.Join(after.Skills, ","))
	appendDiffLine(&b, "skillSearchRoots", strings.Join(before.SkillRoots, ","), strings.Join(after.SkillRoots, ","))
	return b.String()
}

// renderOutcome shows the HOT / RESTART-REQUIRED / FORBIDDEN classification from
// the last apply transaction (the authoritative source of change classes).
func (m *harnessEditorModel) renderOutcome() string {
	if m.applyResult == nil {
		return ""
	}
	r := m.applyResult
	var b strings.Builder
	b.WriteString("\n" + i18n.T("classic_chat_2.harness.outcome.title") + "\n")
	if len(r.Applied) > 0 {
		b.WriteString(i18n.T("classic_chat_2.harness.outcome.hot", strings.Join(r.Applied, ", ")) + "\n")
	}
	if len(r.RestartRequired) > 0 {
		b.WriteString(i18n.T("classic_chat_2.harness.outcome.restart", strings.Join(r.RestartRequired, ", ")) + "\n")
	}
	if len(r.Forbidden) > 0 {
		b.WriteString(i18n.T("classic_chat_2.harness.outcome.forbidden", strings.Join(r.Forbidden, ", ")) + "\n")
	}
	for _, ch := range r.Diff.Changes {
		b.WriteString(fmt.Sprintf("  - %-20s %s: %s -> %s\n", ch.Field, strings.ToUpper(ch.Class), ch.Old, ch.New))
	}
	return b.String()
}

func (m *harnessEditorModel) pendingLabel(f harnessField) string {
	if v, ok := m.pending[f.kind]; ok {
		return "-> " + v
	}
	return ""
}

// ── helpers ─────────────────────────────────────────────────────────────────

func harnessFieldLabel(kind harnessFieldKind) string {
	for _, f := range harnessEditorFields {
		if f.kind == kind {
			return i18n.T(f.label)
		}
	}
	return i18n.T("classic_chat_2.harness.field.generic")
}

func splitHarnessTools(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// splitHarnessSkills parses the editor's "Skills" field buffer into an exact
// ordered skill entry list, mirroring splitHarnessTools' comma-separated
// convention. Each token is "id" (id-only, resolved by search root) or
// "id:path" (path-backed, manifest-relative). It is the sole edit surface:
// re-typing the full list both adds and removes skills, exactly like the
// existing Tools field — there is no separate parallel apply path.
func splitHarnessSkills(value string) []harness.SkillEntry {
	parts := strings.Split(value, ",")
	out := make([]harness.SkillEntry, 0, len(parts))
	for _, p := range parts {
		tok := strings.TrimSpace(p)
		if tok == "" {
			continue
		}
		id, path, _ := strings.Cut(tok, ":")
		out = append(out, harness.SkillEntry{ID: strings.TrimSpace(id), Path: strings.TrimSpace(path)})
	}
	return out
}

func shortHarnessHash(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if h == "" {
		return "-"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func appendDiffLine(b *strings.Builder, label, before, after string) {
	if before == after {
		return
	}
	b.WriteString(fmt.Sprintf("  %-16s %s -> %s\n", label, before, after))
}

var _ commands.Command = (*harnessEditorModel)(nil)

// openHarnessEditor is the App-side open-hook for the `/harness` slash command.
// It is a strict no-op for non-harness sessions (never edits a non-existent
// harness) and otherwise installs the editor as the active interactive overlay.
func (a *App) openHarnessEditor(_ []string) tea.Cmd {
	a.textInput.SetValue("")
	a.cmdAutocomplete.Hide()
	a.mentionAutocomplete.Hide()

	if strings.TrimSpace(a.appOptions.HarnessPath) == "" {
		a.addNotification("info", i18n.T("classic_chat_2.harness.notification.none"))
		return nil
	}

	var cl harnessEditorClient
	if a.sdk != nil {
		if c := a.sdk.SDKClient(); c != nil {
			cl = c
		}
	}

	editor := newHarnessEditor(a.appOptions.HarnessPath, a.appOptions.HarnessAllowYolo, cl)
	if editor == nil {
		a.addNotification("error", i18n.T("classic_chat_2.harness.notification.open_failed"))
		return nil
	}
	editor.width = a.width
	editor.height = a.height
	a.activeCommand = editor
	return nil
}
