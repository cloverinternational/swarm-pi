package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/core"
)

// registeredSlashCommands is the canonical list of slash commands the server advertises.
var registeredSlashCommands = []SlashCommandInfo{
	{Name: "model", Description: "Show or change the AI model for this session", Args: "[model-id]"},
	{Name: "provider", Description: "Show or change the AI provider for this session", Args: "[provider-name]"},
	{Name: "agent", Description: "Show or switch the active agent profile", Args: "[agent-id]"},
	{Name: "mode", Description: "Show or change the operating mode", Args: "[ask|code|architect]"},
	{Name: "tools", Description: "List available tools, optionally filtered by name", Args: "[filter]"},
	{Name: "clear", Description: "Start a new conversation (clears history for this session)"},
	{Name: "compact", Description: "Compact the conversation history to reduce context size"},
	{Name: "goal", Description: "Set a goal condition that keeps the agent working until met; /goal clear to stop", Args: "<condition>|clear"},
	{Name: "help", Description: "Show all available slash commands"},
}

// ── Dispatcher ────────────────────────────────────────────────────────────────

func (s *Server) handleSlashCommandList() (any, *RPCError) {
	return &SlashCommandListResult{Commands: registeredSlashCommands}, nil
}

func (s *Server) handleSlashCommandRun(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p SlashCommandRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid params", Data: err.Error()}
	}

	// Look up the session (optional for commands like /help).
	var sess *session
	if p.SessionID != "" {
		var ok bool
		sess, ok = s.sessions.get(p.SessionID)
		if !ok {
			return nil, &RPCError{
				Code:    ErrInvalidParams,
				Message: fmt.Sprintf("unknown sessionId: %s", p.SessionID),
			}
		}
	}

	cmd := strings.ToLower(strings.TrimSpace(p.Command))
	args := strings.TrimSpace(p.Args)

	switch cmd {
	case "model":
		return s.runModelCommand(sess, args)
	case "provider":
		return s.runProviderCommand(sess, args)
	case "agent":
		return s.runAgentCommand(sess, args)
	case "mode":
		return s.runModeCommand(sess, args)
	case "tools":
		return s.runToolsCommand(args)
	case "clear":
		return s.runClearCommand(ctx, sess)
	case "compact":
		return s.runCompactCommand(sess)
	case "goal":
		return s.runGoalCommand(sess, args)
	case "help":
		return s.runHelpCommand()
	default:
		return nil, &RPCError{
			Code:    ErrMethodNotFound,
			Message: fmt.Sprintf("unknown slash command: /%s — type /help for a list", cmd),
		}
	}
}

// ── /model ────────────────────────────────────────────────────────────────────

func (s *Server) runModelCommand(sess *session, args string) (any, *RPCError) {
	if args == "" {
		// Show current model.
		current := s.currentModel(sess)
		return &SlashCommandRunResult{Text: fmt.Sprintf("Current model: **%s**\n\nUse `/model <id>` to change it.", current)}, nil
	}
	model := args
	var cfgOpts []ConfigOption
	if sess != nil {
		sess.applyConfig(map[string]string{"model": model})
		if s.engine != nil {
			ev := core.NewInputEvent(core.InputSetModel)
			ev.Content = model
			s.engine.SendEvent(ev)
		}
		cfgOpts = s.buildConfigOptions(sess)
		s.emitConfigOptionUpdate(sess.id, cfgOpts)
	}
	return &SlashCommandRunResult{
		Text:          fmt.Sprintf("✓ Model changed to **%s**", model),
		ConfigOptions: cfgOpts,
	}, nil
}

// ── /provider ─────────────────────────────────────────────────────────────────

func (s *Server) runProviderCommand(sess *session, args string) (any, *RPCError) {
	if args == "" {
		current := s.currentProvider(sess)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Current provider: **%s**\n\n", current))
		if len(s.providers) > 0 {
			sb.WriteString("Available providers:\n")
			for _, p := range s.providers {
				mark := "  "
				if p == current {
					mark = "→ "
				}
				sb.WriteString(fmt.Sprintf("%s%s\n", mark, p))
			}
		}
		sb.WriteString("\nUse `/provider <name>` to switch.")
		return &SlashCommandRunResult{Text: sb.String()}, nil
	}
	prov := args
	var cfgOpts []ConfigOption
	if sess != nil {
		sess.applyConfig(map[string]string{"provider": prov})
		if s.engine != nil {
			ev := core.NewInputEvent(core.InputSetProvider)
			ev.Content = prov
			s.engine.SendEvent(ev)
		}
		cfgOpts = s.buildConfigOptions(sess)
		s.emitConfigOptionUpdate(sess.id, cfgOpts)
	}
	return &SlashCommandRunResult{
		Text:          fmt.Sprintf("✓ Provider changed to **%s**", prov),
		ConfigOptions: cfgOpts,
	}, nil
}

// ── /agent ────────────────────────────────────────────────────────────────────

func (s *Server) runAgentCommand(sess *session, args string) (any, *RPCError) {
	if args == "" {
		current := s.currentAgentID(sess)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Active agent: **%s**\n\n", func() string {
			if current == "" {
				return "(default)"
			}
			return current
		}()))
		if len(s.agentOptions) > 0 {
			sb.WriteString("Available agents:\n")
			for _, a := range s.agentOptions {
				mark := "  "
				if a.ID == current {
					mark = "→ "
				}
				line := fmt.Sprintf("%s%s", mark, a.Name)
				if a.Description != "" {
					line += fmt.Sprintf(" — %s", a.Description)
				}
				sb.WriteString(line + "\n")
			}
		} else {
			sb.WriteString("No agent profiles configured.")
		}
		sb.WriteString("\nUse `/agent <id>` to switch.")
		return &SlashCommandRunResult{Text: sb.String()}, nil
	}
	agentID := args
	var cfgOpts []ConfigOption
	if sess != nil {
		sess.applyConfig(map[string]string{"agent": agentID})
		if s.engine != nil {
			s.engine.SetActiveAgent(agentID)
		}
		cfgOpts = s.buildConfigOptions(sess)
		s.emitConfigOptionUpdate(sess.id, cfgOpts)
	}
	return &SlashCommandRunResult{
		Text:          fmt.Sprintf("✓ Agent switched to **%s**", agentID),
		ConfigOptions: cfgOpts,
	}, nil
}

// ── /mode ─────────────────────────────────────────────────────────────────────

func (s *Server) runModeCommand(sess *session, args string) (any, *RPCError) {
	if args == "" {
		current := s.currentModeID()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Current mode: **%s**\n\n", current))
		sb.WriteString("Available modes:\n")
		for _, m := range availableModes {
			mark := "  "
			if m.ID == current {
				mark = "→ "
			}
			sb.WriteString(fmt.Sprintf("%s%s — %s\n", mark, m.ID, m.Description))
		}
		sb.WriteString("\nUse `/mode <id>` to switch.")
		return &SlashCommandRunResult{Text: sb.String()}, nil
	}
	modeID := strings.ToLower(args)
	// Validate
	valid := false
	for _, m := range availableModes {
		if m.ID == modeID {
			valid = true
			break
		}
	}
	if !valid {
		return nil, &RPCError{
			Code:    ErrInvalidParams,
			Message: fmt.Sprintf("unknown mode %q — valid modes: ask, code, architect", modeID),
		}
	}
	var cfgOpts []ConfigOption
	if sess != nil {
		sess.applyConfig(map[string]string{"mode": modeID})
		if s.engine != nil {
			ev := core.NewInputEvent(core.InputSetMode)
			ev.Content = acpModeToEngineMode(modeID)
			s.engine.SendEvent(ev)
		}
		cfgOpts = s.buildConfigOptions(sess)
		s.emitConfigOptionUpdate(sess.id, cfgOpts)
	}
	return &SlashCommandRunResult{
		Text:          fmt.Sprintf("✓ Mode changed to **%s**", modeID),
		ConfigOptions: cfgOpts,
	}, nil
}

// ── /tools ────────────────────────────────────────────────────────────────────

func (s *Server) runToolsCommand(filter string) (any, *RPCError) {
	names := s.toolNames
	// If the engine has an up-to-date registry, prefer that.
	if s.engine != nil {
		if reg := s.engine.GetToolRegistry(); len(reg.ToolNames) > 0 {
			names = reg.ToolNames
		}
	}
	if len(names) == 0 {
		return &SlashCommandRunResult{Text: "No tools registered."}, nil
	}
	// Sort and optionally filter.
	sort.Strings(names)
	filter = strings.ToLower(strings.TrimSpace(filter))
	var matched []string
	for _, n := range names {
		if filter == "" || strings.Contains(strings.ToLower(n), filter) {
			matched = append(matched, n)
		}
	}
	if len(matched) == 0 {
		return &SlashCommandRunResult{
			Text: fmt.Sprintf("No tools matching %q.", filter),
		}, nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**%d tools**", len(matched)))
	if filter != "" {
		sb.WriteString(fmt.Sprintf(" matching %q", filter))
	}
	sb.WriteString(":\n\n")
	for _, n := range matched {
		sb.WriteString("• " + n + "\n")
	}
	return &SlashCommandRunResult{Text: sb.String()}, nil
}

// ── /clear ────────────────────────────────────────────────────────────────────

func (s *Server) runClearCommand(ctx context.Context, sess *session) (any, *RPCError) {
	if sess == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "/clear requires a sessionId"}
	}
	if s.engine == nil {
		return &SlashCommandRunResult{Text: "✓ Conversation cleared."}, nil
	}
	// Create a fresh conversation and update the session.
	newConvID, err := s.engine.CreateConversation(ctx, core.CreateConversationOptions{})
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "failed to create conversation", Data: err.Error()}
	}
	sess.mu.Lock()
	sess.convID = newConvID
	sess.mu.Unlock()
	s.engine.SendEvent(core.NewSwitchConvEvent(newConvID))
	return &SlashCommandRunResult{Text: "✓ New conversation started."}, nil
}

// ── /compact ──────────────────────────────────────────────────────────────────

func (s *Server) runCompactCommand(sess *session) (any, *RPCError) {
	if sess == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "/compact requires a sessionId"}
	}
	if s.engine != nil {
		ev := core.NewInputEvent(core.InputClearHistory)
		s.engine.SendEvent(ev)
	}
	return &SlashCommandRunResult{Text: "✓ Conversation history compacted."}, nil
}

// ── /help ─────────────────────────────────────────────────────────────────────

func (s *Server) runHelpCommand() (any, *RPCError) {
	var sb strings.Builder
	sb.WriteString("**Swarm slash commands**\n\n")
	for _, cmd := range registeredSlashCommands {
		sb.WriteString(fmt.Sprintf("`/%s", cmd.Name))
		if cmd.Args != "" {
			sb.WriteString(" " + cmd.Args)
		}
		sb.WriteString("`")
		if cmd.Description != "" {
			sb.WriteString(" — " + cmd.Description)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n**@ mentions**\n\n")
	sb.WriteString("Type `@` in the prompt to embed files, selections, or terminal output directly into your message.\n")
	return &SlashCommandRunResult{Text: sb.String()}, nil
}

// ── /goal ─────────────────────────────────────────────────────────────────────

func (s *Server) runGoalCommand(sess *session, args string) (any, *RPCError) {
	if s.goalHook == nil {
		return &SlashCommandRunResult{Text: "Goal tracking is unavailable in this session (no goal hook wired)."}, nil
	}

	args = strings.TrimSpace(args)

	if args == "" {
		// Show current goal status from the live hook.
		goal := s.goalHook.GetGoal()
		if goal == nil || goal.State == "cleared" {
			return &SlashCommandRunResult{Text: "No goal set. Usage: `/goal <condition>` or `/goal clear`"}, nil
		}
		text := fmt.Sprintf("Goal active: %s\nState: %s", goal.Condition, goal.State)
		if goal.LastReason != "" {
			text += fmt.Sprintf("\nLast check: %s", goal.LastReason)
		}
		return &SlashCommandRunResult{Text: text}, nil
	}

	if strings.ToLower(args) == "clear" {
		s.goalHook.Clear()
		return &SlashCommandRunResult{Text: "Goal cleared."}, nil
	}

	// Set new goal via the hook (enforces trusted-workspace + length guards).
	if err := s.goalHook.SetGoal(args); err != nil {
		return &SlashCommandRunResult{Text: err.Error()}, nil
	}
	return &SlashCommandRunResult{Text: fmt.Sprintf("Goal set: %s\nClaude will keep working until this condition is met. `/goal clear` to stop.", args)}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (s *Server) currentModel(sess *session) string {
	if sess != nil {
		if _, model, _, _ := sess.getConfig(); model != "" {
			return model
		}
	}
	if s.config != nil {
		if cfg := s.config.GetConfig(); cfg != nil && cfg.DefaultModel != "" {
			return cfg.DefaultModel
		}
	}
	return "claude-sonnet-4-6"
}

func (s *Server) currentProvider(sess *session) string {
	if sess != nil {
		if prov, _, _, _ := sess.getConfig(); prov != "" {
			return prov
		}
	}
	if s.config != nil {
		if cfg := s.config.GetConfig(); cfg != nil && cfg.DefaultProvider != "" {
			return cfg.DefaultProvider
		}
	}
	return "anthropic"
}

func (s *Server) currentAgentID(sess *session) string {
	if sess != nil {
		if _, _, agentID, _ := sess.getConfig(); agentID != "" {
			return agentID
		}
	}
	if s.engine != nil {
		return s.engine.GetActiveAgent()
	}
	return ""
}

// emitConfigOptionUpdate sends a config_option_update session notification
// so that ZED ACP clients can refresh their configuration dropdowns.
func (s *Server) emitConfigOptionUpdate(sessionID string, cfgOpts []ConfigOption) {
	s.sendSessionUpdate(sessionID, SessionUpdate{
		SessionUpdateType: "config_option_update",
		ConfigOptions:     cfgOpts,
	})
}
