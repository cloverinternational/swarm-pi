package chat

import (
	"fmt"
	"strings"
	"time"
)

// AgentActivityPhase is the canonical UI-level phase for the visible agent turn.
// It intentionally maps back to the legacy Conversation.Status strings so the
// conversation list, side panel, and existing active-turn checks keep working.
type AgentActivityPhase string

const (
	ActivityPhaseIdle       AgentActivityPhase = "idle"
	ActivityPhaseThinking   AgentActivityPhase = "thinking"
	ActivityPhaseToolUse    AgentActivityPhase = "tool_use"
	ActivityPhaseResponding AgentActivityPhase = "responding"
	ActivityPhaseCompacting AgentActivityPhase = "compacting"
	ActivityPhasePeer       AgentActivityPhase = "peer"
)

// ActivitySnapshot is the only data the App needs to project activity state into
// existing UI components. Keeping this as a value object avoids coupling the
// manager to Bubble Tea, spinners, loading indicators, or conversation storage.
type ActivitySnapshot struct {
	Phase        AgentActivityPhase
	Label        string
	ConvStatus   string
	ConvIsActive bool
}

// ToolActivityDescriber describes one tool call for the status row. New tools can
// be added by registering a descriptor instead of changing App.Update logic.
type ToolActivityDescriber interface {
	Describe(toolName string, params map[string]any) string
}

type toolActivityDescriptorFunc func(params map[string]any) string

// DefaultToolActivityDescriber is deliberately data-driven: the switch on active
// Bubble Tea messages stays outside this layer, while tool-specific copy lives in
// one extensible registry.
type DefaultToolActivityDescriber struct {
	descriptors map[string]toolActivityDescriptorFunc
}

func NewDefaultToolActivityDescriber() *DefaultToolActivityDescriber {
	d := &DefaultToolActivityDescriber{descriptors: make(map[string]toolActivityDescriptorFunc)}
	d.registerAliases([]string{"Read"}, func(params map[string]any) string {
		if fp := firstStringParam(params, "file_path", "path"); fp != "" {
			return tr("classic.activity.reading_item", lastPathSegment(fp))
		}
		return tr("classic.activity.reading_file")
	})
	d.registerAliases([]string{"list_dir"}, func(params map[string]any) string {
		if path := firstStringParam(params, "path", "dir", "file_path"); path != "" {
			return tr("classic.activity.listing_item", lastPathSegment(path))
		}
		return tr("classic.activity.listing_directory")
	})
	for _, lang := range []string{"go", "python", "javascript", "ruby", "rust", "haskell", "elixir", "lua", "sql", "css", "html", "json", "yaml", "toml", "markdown", "dockerfile", "makefile", "indent"} {
		name := lang
		d.Register(name, func(params map[string]any) string {
			return tr("classic.activity.analyzing", humanizeToolName(name))
		})
	}
	d.registerAliases([]string{"Grep", "grep", "semantic_grep"}, func(params map[string]any) string {
		if pattern := firstStringParam(params, "pattern", "query", "symbol"); pattern != "" {
			return tr("classic.activity.searching_for", truncateStatusText(pattern, 30))
		}
		return tr("classic.activity.searching_code")
	})
	d.registerAliases([]string{"Glob"}, func(params map[string]any) string {
		if pattern := firstStringParam(params, "pattern", "path"); pattern != "" {
			return tr("classic.activity.searching_item", pattern)
		}
		return tr("classic.activity.searching_files")
	})
	d.registerAliases([]string{"Edit", "MultiEdit"}, describeFileMutation(tr("classic.activity.editing")))
	d.registerAliases([]string{"Write"}, describeFileMutation(tr("classic.activity.writing")))
	d.registerAliases([]string{"Undo"}, func(params map[string]any) string {
		if path := firstStringParam(params, "path", "file_path"); path != "" {
			return tr("classic.activity.reverting_item", lastPathSegment(path))
		}
		return tr("classic.activity.reverting_file")
	})
	d.registerAliases([]string{"apply_patch"}, func(params map[string]any) string {
		return tr("classic.activity.applying_patch")
	})
	d.registerAliases([]string{"semantic_rename"}, func(params map[string]any) string {
		oldName := firstStringParam(params, "old_name", "oldName")
		newName := firstStringParam(params, "new_name", "newName")
		if oldName != "" && newName != "" {
			return tr("classic.activity.renaming", oldName, newName)
		}
		return tr("classic.activity.renaming_symbol")
	})
	d.registerAliases([]string{"Bash", "bash", "Shell", "shell"}, func(params map[string]any) string {
		if command := firstStringParam(params, "command", "cmd"); command != "" {
			if fields := strings.Fields(command); len(fields) > 0 {
				return tr("classic.activity.running_item", fields[0])
			}
		}
		return tr("classic.activity.running_command")
	})
	d.registerAliases([]string{"run_code"}, func(params map[string]any) string { return tr("classic.activity.running_code") })
	d.registerAliases([]string{"Agent", "Task", "Subagent", "Delegate", "clone_agent", "create_agent", "update_agent"}, describeSubAgent)
	d.registerAliases([]string{"BackgroundTask"}, func(params map[string]any) string { return tr("classic.activity.starting_agent") })
	d.registerAliases([]string{"SubagentOutput", "TaskOutput", "DelegateOutput", "ReadBackgroundCommand"}, func(params map[string]any) string { return tr("classic.activity.checking_agent_output") })
	d.registerAliases([]string{"wait_for_agent", "multi_agent_wait"}, func(params map[string]any) string { return tr("classic.activity.waiting_agents") })
	d.registerAliases([]string{"list_agents"}, func(params map[string]any) string { return tr("classic.activity.listing_agents") })
	d.registerAliases([]string{"set_capabilities", "set_default_agent", "delete_agent", "configure_tools", "configure_hooks"}, func(params map[string]any) string { return tr("classic.activity.configuring_agents") })
	d.registerAliases([]string{"TaskManage"}, func(params map[string]any) string { return tr("classic.activity.managing_tasks") })
	d.registerAliases([]string{"CronCreate", "ScheduleWakeup"}, func(params map[string]any) string { return tr("classic.activity.scheduling_wakeup") })
	d.registerAliases([]string{"CronDelete"}, func(params map[string]any) string { return tr("classic.activity.cancelling_schedule") })
	d.registerAliases([]string{"CronList"}, func(params map[string]any) string { return tr("classic.activity.listing_schedules") })
	d.registerAliases([]string{"WebSearch", "websearch", "anthropic_web_search", "x_search", "xai_web_search"}, func(params map[string]any) string {
		if query := firstStringParam(params, "query", "search_query"); query != "" {
			return tr("classic.activity.searching_web_for", truncateStatusText(query, 30))
		}
		return tr("classic.activity.searching_web")
	})
	d.registerAliases([]string{"WebFetch", "web_fetch"}, func(params map[string]any) string { return tr("classic.activity.fetching_web") })
	d.registerAliases([]string{"LSP"}, func(params map[string]any) string { return tr("classic.activity.querying_lsp") })
	d.registerAliases([]string{"HistorySearch"}, func(params map[string]any) string { return tr("classic.activity.searching_history") })
	d.registerAliases([]string{"HistoryGet"}, func(params map[string]any) string { return tr("classic.activity.reading_history") })
	d.registerAliases([]string{"Skill"}, func(params map[string]any) string {
		if skill := firstStringParam(params, "skill", "name"); skill != "" {
			return tr("classic.activity.loading_skill_name", skill)
		}
		return tr("classic.activity.loading_skill")
	})
	d.registerAliases([]string{"SkillManage"}, func(params map[string]any) string { return tr("classic.activity.managing_skills") })
	d.registerAliases([]string{"ask_user_question", "ask_user"}, func(params map[string]any) string { return tr("classic.activity.asking_user") })
	d.registerAliases([]string{"ask_parent"}, func(params map[string]any) string { return tr("classic.activity.asking_parent") })
	d.registerAliases([]string{"enter_plan_mode"}, func(params map[string]any) string { return tr("classic.activity.entering_plan") })
	d.registerAliases([]string{"exit_plan_mode"}, func(params map[string]any) string { return tr("classic.activity.presenting_plan") })
	d.registerAliases([]string{"vault"}, func(params map[string]any) string { return tr("classic.activity.managing_vault") })
	d.registerAliases([]string{"vault_add"}, func(params map[string]any) string { return tr("classic.activity.storing_credential") })
	d.registerAliases([]string{"vault_exec"}, func(params map[string]any) string { return tr("classic.activity.running_credential") })
	d.registerAliases([]string{"vault_list"}, func(params map[string]any) string { return tr("classic.activity.listing_credentials") })
	d.registerAliases([]string{"vault_approve", "vault_two_person_status"}, func(params map[string]any) string { return tr("classic.activity.checking_approval") })
	d.registerAliases([]string{"mcp_context7_resolve-library-id"}, func(params map[string]any) string {
		if lib := firstStringParam(params, "libraryName", "library"); lib != "" {
			return tr("classic.activity.resolving_docs_for", truncateStatusText(lib, 30))
		}
		return tr("classic.activity.resolving_docs")
	})
	d.registerAliases([]string{"mcp_context7_query-docs"}, func(params map[string]any) string {
		if lib := firstStringParam(params, "libraryId", "libraryName"); lib != "" {
			return tr("classic.activity.reading_docs_for", truncateStatusText(lib, 30))
		}
		return tr("classic.activity.reading_docs")
	})
	d.registerAliases([]string{"a2a_list_agents"}, func(params map[string]any) string { return tr("classic.activity.listing_peers") })
	d.registerAliases([]string{"a2a_send_message", "a2a_send_streaming_message"}, func(params map[string]any) string { return tr("classic.activity.messaging_peer") })
	d.registerAliases([]string{"a2a_get_task", "a2a_list_tasks", "a2a_subscribe_task"}, func(params map[string]any) string { return tr("classic.activity.checking_peer_tasks") })
	d.registerAliases([]string{"a2a_cancel_task"}, func(params map[string]any) string { return tr("classic.activity.cancelling_peer_task") })
	d.registerAliases([]string{"a2a_fetch_agent_card"}, func(params map[string]any) string { return tr("classic.activity.fetching_peer_card") })
	d.registerAliases([]string{"bulk_update_project_context", "update_project_context"}, func(params map[string]any) string { return tr("classic.activity.updating_project_context") })
	d.registerAliases([]string{"view_project_context"}, func(params map[string]any) string { return tr("classic.activity.reading_project_context") })
	d.registerAliases([]string{"delete_project_context"}, func(params map[string]any) string { return tr("classic.activity.deleting_project_context") })
	d.registerAliases([]string{"browser_click", "browser_drag", "browser_enter_multi_texts", "browser_enter_text", "browser_get_select_options", "browser_navigation", "browser_open_new_tab", "browser_press_key", "browser_restart", "browser_scroll_down", "browser_scroll_up", "browser_select_dropdown_option", "browser_switch_tab", "browser_view_interactive_elements", "browser_wait", "agent_browser"}, func(params map[string]any) string { return tr("classic.activity.using_browser") })
	d.registerAliases([]string{"debug_inspect"}, func(params map[string]any) string { return tr("classic.activity.inspecting_debug") })
	d.registerAliases([]string{"agent_wait_sleep_blocker"}, func(params map[string]any) string { return tr("classic.activity.preventing_wait") })
	d.registerAliases([]string{"inject_system_note", "refocus", "block_next_tool", "halt_peer_loop", "observe_only", "log_concern", "annoyed"}, func(params map[string]any) string { return tr("classic.activity.steering_agent") })
	d.registerAliases([]string{"swarm"}, func(params map[string]any) string { return tr("classic.activity.checking_peers") })
	return d
}
func (d *DefaultToolActivityDescriber) Register(toolName string, describe toolActivityDescriptorFunc) {
	if d == nil || describe == nil || toolName == "" {
		return
	}
	d.descriptors[toolName] = describe
}

func (d *DefaultToolActivityDescriber) registerAliases(toolNames []string, describe toolActivityDescriptorFunc) {
	for _, toolName := range toolNames {
		d.Register(toolName, describe)
	}
}

func (d *DefaultToolActivityDescriber) Describe(toolName string, params map[string]any) string {
	if d != nil {
		if describe := d.descriptors[toolName]; describe != nil {
			return describe(params)
		}
	}
	if toolName == "" {
		return tr("classic.activity.using_tool")
	}
	lower := strings.ToLower(toolName)
	switch {
	case strings.HasPrefix(lower, "mcp_terraform_"):
		return terraformActivityLabel(toolName, params)
	case strings.HasPrefix(lower, "mcp_"):
		return tr("classic.activity.using_mcp")
	case strings.HasPrefix(lower, "a2a_"):
		return tr("classic.activity.working_peer")
	case strings.HasPrefix(lower, "vault_"):
		return tr("classic.activity.using_vault")
	case strings.HasPrefix(lower, "browser_"):
		return tr("classic.activity.using_browser")
	case strings.HasPrefix(lower, "cron"):
		return tr("classic.activity.managing_schedule")
	case strings.Contains(lower, "search") || strings.Contains(lower, "grep"):
		return tr("classic.activity.searching")
	case strings.Contains(lower, "read") || strings.Contains(lower, "get") || strings.Contains(lower, "list"):
		return tr("classic.activity.reading_item", humanizeToolName(toolName))
	case strings.Contains(lower, "write") || strings.Contains(lower, "edit") || strings.Contains(lower, "update") || strings.Contains(lower, "create") || strings.Contains(lower, "delete"):
		return humanizeToolName(toolName)
	default:
		return tr("classic.activity.using_item", humanizeToolName(toolName))
	}
}

// ActivityStateManager owns transient turn activity: phase + running tool set +
// display text. It does not mutate App directly; App applies snapshots to the
// existing spinner/loading/conversation fields.
type ActivityStateManager struct {
	phase     AgentActivityPhase
	phaseText string
	describer ToolActivityDescriber
	tools     []toolActivity
}

func NewActivityStateManager(describer ToolActivityDescriber) *ActivityStateManager {
	if describer == nil {
		describer = NewDefaultToolActivityDescriber()
	}
	return &ActivityStateManager{phase: ActivityPhaseIdle, describer: describer}
}

func (m *ActivityStateManager) SetPhase(phase AgentActivityPhase, label string) {
	if m == nil {
		return
	}
	if phase == "" {
		phase = ActivityPhaseThinking
	}
	// Tool activity has priority over generic thinking/responding updates. Some
	// providers emit content/thinking deltas while a tool is still waiting for its
	// final result; letting those deltas overwrite tool_use makes the visible
	// status flicker between Running <tool> and generic phases.
	if len(m.tools) > 0 && m.phase == ActivityPhaseToolUse && phase != ActivityPhaseIdle && phase != ActivityPhaseCompacting {
		m.phaseText = strings.TrimSpace(label)
		return
	}
	m.phase = phase
	m.phaseText = strings.TrimSpace(label)
	if phase == ActivityPhaseIdle {
		m.tools = nil
	}
}

func (m *ActivityStateManager) BeginTool(callID, toolName string, params map[string]any) {
	if m == nil {
		return
	}
	if callID == "" {
		callID = fmt.Sprintf("%s:%d", toolName, time.Now().UnixNano())
	}
	m.EndTool(callID)
	m.phase = ActivityPhaseToolUse
	m.phaseText = ""
	m.tools = append(m.tools, toolActivity{
		callID:      callID,
		toolName:    toolName,
		description: m.describer.Describe(toolName, params),
		startTime:   time.Now(),
	})
}

func (m *ActivityStateManager) EndTool(callID string) {
	if m == nil || callID == "" {
		return
	}
	for i, act := range m.tools {
		if act.callID == callID {
			m.tools = append(m.tools[:i], m.tools[i+1:]...)
			break
		}
	}
	if len(m.tools) == 0 && m.phase == ActivityPhaseToolUse {
		m.phase = ActivityPhaseThinking
	}
}

func (m *ActivityStateManager) ClearTools() {
	if m == nil {
		return
	}
	m.tools = nil
	if m.phase == ActivityPhaseToolUse {
		m.phase = ActivityPhaseThinking
	}
}

func (m *ActivityStateManager) Snapshot() ActivitySnapshot {
	if m == nil {
		return ActivitySnapshot{Phase: ActivityPhaseIdle, Label: tr("classic.activity.agent_thinking"), ConvStatus: "idle"}
	}
	phase := m.phase
	if phase == "" {
		phase = ActivityPhaseIdle
	}
	label := m.label()
	status, active := legacyConversationStatus(phase)
	return ActivitySnapshot{Phase: phase, Label: label, ConvStatus: status, ConvIsActive: active}
}

func (m *ActivityStateManager) label() string {
	if len(m.tools) > 0 {
		return combineToolActivityText(m.tools)
	}
	if m.phaseText != "" {
		return m.phaseText
	}
	switch m.phase {
	case ActivityPhaseResponding:
		return tr("classic.activity.writing_response")
	case ActivityPhaseCompacting:
		return tr("classic.activity.compacting")
	case ActivityPhasePeer:
		return tr("classic.activity.peer_request")
	case ActivityPhaseIdle:
		return tr("classic.activity.agent_thinking")
	default:
		return tr("classic.activity.agent_thinking")
	}
}

func legacyConversationStatus(phase AgentActivityPhase) (string, bool) {
	switch phase {
	case ActivityPhaseIdle:
		return "idle", false
	case ActivityPhaseResponding:
		return "streaming", true
	default:
		return "thinking", true
	}
}

func combineToolActivityText(tools []toolActivity) string {
	if len(tools) == 0 {
		return tr("classic.activity.agent_thinking")
	}
	readCount := 0
	grepCount := 0
	var others []string
	for _, act := range tools {
		switch act.toolName {
		case "Read":
			readCount++
		case "Grep":
			grepCount++
		default:
			if act.description != "" {
				others = append(others, act.description)
			}
		}
	}

	var parts []string
	if grepCount > 0 {
		if grepCount == 1 {
			parts = append(parts, firstToolDescription(tools, "Grep"))
		} else {
			parts = append(parts, tr("classic.activity.searching_patterns", grepCount))
		}
	}
	if readCount > 0 {
		if readCount == 1 {
			parts = append(parts, firstToolDescription(tools, "Read"))
		} else {
			parts = append(parts, tr("classic.activity.reading_files_count", readCount))
		}
	}
	parts = append(parts, others...)
	if len(parts) == 0 {
		return tr("classic.activity.agent_thinking")
	}

	var b strings.Builder
	if n := len(tools); n > 1 {
		b.WriteString(tr("classic.activity.running_count", n))
	}
	b.WriteString(parts[0])
	for i := 1; i < len(parts); i++ {
		b.WriteString(", ")
		b.WriteString(lowerFirst(parts[i]))
	}
	return b.String()
}

func firstToolDescription(tools []toolActivity, name string) string {
	for _, act := range tools {
		if act.toolName == name && act.description != "" {
			return act.description
		}
	}
	return tr("classic.activity.using_item", name)
}

func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func describeFileMutation(verb string) toolActivityDescriptorFunc {
	return func(params map[string]any) string {
		if fp := stringParam(params, "file_path"); fp != "" {
			return tr("classic.activity.verb_item", verb, lastPathSegment(fp))
		}
		return tr("classic.activity.verb_file", verb)
	}
}

func describeSubAgent(params map[string]any) string {
	for _, key := range []string{"description", "task"} {
		if desc := stringParam(params, key); desc != "" {
			return truncateStatusText(desc, 40)
		}
	}
	return tr("classic.activity.running_subagent")
}

func firstStringParam(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringParam(params, key); value != "" {
			return value
		}
	}
	return ""
}

func humanizeToolName(toolName string) string {
	if toolName == "" {
		return tr("classic.activity.tool")
	}
	name := strings.TrimPrefix(toolName, "mcp_")
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	return strings.TrimSpace(name)
}

func terraformActivityLabel(toolName string, params map[string]any) string {
	workspace := firstStringParam(params, "workspace_name", "workspace", "workspace_id")
	module := firstStringParam(params, "module_name", "module_query", "module_id")
	provider := firstStringParam(params, "provider_name", "name", "service_slug")
	lower := strings.ToLower(toolName)
	switch {
	case strings.Contains(lower, "create_run"):
		if workspace != "" {
			return tr("classic.activity.terraform_run_for", truncateStatusText(workspace, 30))
		}
		return tr("classic.activity.terraform_run")
	case strings.Contains(lower, "workspace"):
		if workspace != "" {
			return tr("classic.activity.terraform_workspace_name", truncateStatusText(workspace, 30))
		}
		return tr("classic.activity.terraform_workspace")
	case strings.Contains(lower, "module"):
		if module != "" {
			return tr("classic.activity.terraform_module_name", truncateStatusText(module, 30))
		}
		return tr("classic.activity.terraform_modules")
	case strings.Contains(lower, "provider"):
		if provider != "" {
			return tr("classic.activity.terraform_provider_name", truncateStatusText(provider, 30))
		}
		return tr("classic.activity.terraform_provider")
	case strings.Contains(lower, "variable"):
		return tr("classic.activity.terraform_variables")
	case strings.Contains(lower, "policy"):
		return tr("classic.activity.terraform_policies")
	case strings.Contains(lower, "stack"):
		return tr("classic.activity.terraform_stacks")
	default:
		return tr("classic.activity.terraform_using")
	}
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if value, ok := params[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func truncateStatusText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// lastPathSegment returns the filename from a path.
func lastPathSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

func (a *App) ensureActivityState() *ActivityStateManager {
	if a.activityState == nil {
		a.activityState = NewActivityStateManager(nil)
	}
	return a.activityState
}

func (a *App) setActivityPhase(phase AgentActivityPhase, label string) {
	a.ensureActivityState().SetPhase(phase, label)
	a.applyActivitySnapshot()
}

func (a *App) beginToolActivity(callID, toolName string, params map[string]any) {
	a.ensureActivityState().BeginTool(callID, toolName, params)
	a.applyActivitySnapshot()
}

func (a *App) endToolActivity(callID string) {
	a.ensureActivityState().EndTool(callID)
	a.applyActivitySnapshot()
}

func (a *App) clearToolActivities() {
	a.ensureActivityState().ClearTools()
	a.applyActivitySnapshot()
}

// updateActivityLabels is kept as the single App-level compatibility hook for
// existing call sites; the source of truth now lives in ActivityStateManager.
func (a *App) updateActivityLabels() {
	a.applyActivitySnapshot()
}

func (a *App) applyActivitySnapshot() {
	snapshot := a.ensureActivityState().Snapshot()
	if a.loadingIndicator != nil {
		a.loadingIndicator.SetText(snapshot.Label)
	}
	if a.spinner != nil {
		a.spinner.SetLabel(snapshot.Label)
	}
	if a.activeConv != nil {
		a.activeConv.Status = snapshot.ConvStatus
		a.activeConv.IsActive = snapshot.ConvIsActive
	}
	a.sidePanelCache.valid = false
}
