package commands

import (
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/update"
)

// InitRegistry initializes the command registry with all built-in commands
// If updater is provided, the /update command will be registered
func InitRegistry(updater *update.Updater) *Registry {
	reg := NewRegistry()

	// Register all commands here
	reg.Register(NewMemCommand()) // Live memory stats
	reg.Register(NewAuthCommand())
	reg.Register(NewModelCommand())
	reg.Register(NewRenderCommand())
	reg.Register(NewMCPCommand())
	reg.Register(NewThinkingCommand())
	reg.Register(NewHooksCommand())
	// reg.Register(NewDoctorCommand()) // TODO: implement doctor command
	reg.Register(NewCompactCommand())
	reg.Register(NewClearCommand())
	reg.Register(NewMicroCompactCommand())
	reg.Register(NewContextCommand()) // /context, /audit — context composition breakdown (incl. hidden/ephemeral)
	reg.Register(NewProfileCommand()) // /pprof — performance profiler (CPU/mem/goroutine); aliases prof, perf

	// Config-surface slash commands: open an existing overlay switcher or a
	// Settings section without leaving chat (see config_ui.go). /profile is the
	// model-profile switcher (distinct from /pprof above).
	reg.Register(NewProfileSwitchCommand())   // /profile — model profile switcher
	reg.Register(NewAgentsCommand())          // /agents, /subagents — sub-agent switcher
	reg.Register(NewPromptCommand())          // /prompt, /systemprompt — system prompt switcher
	reg.Register(NewCompactionCommand())      // /compaction — Settings: Compaction
	reg.Register(NewProvidersCommand())       // /providers, /provider — Settings: Models/providers
	reg.Register(NewModeCommand())            // Operating mode switch (PLAN/ACT/AUTO)
	reg.Register(NewSkillCommand())           // Skill management: list, enable, disable, search, install
	reg.Register(NewPluginCommand())          // Plugin management (Claude Code-compatible): list, enable, disable, install
	reg.Register(NewCommandsBrowser())        // Browse all commands (built-in and plugin)
	reg.Register(NewVoiceCommand())           // Voice input toggle
	reg.Register(NewClaudeImportCommand())    // Import conversations from Claude Code format
	reg.Register(NewClaudeExportCommand())    // Export current conversation to Claude Code format
	reg.Register(NewBugCommand())             // Submit a bug report with full client state to Sentry
	reg.Register(NewReindexCommand())         // Generate titles for conversations missing them
	reg.Register(NewCodeModeCommand())        // Toggle code mode (JavaScript sandbox for batched tool calls)
	reg.Register(NewRefreshPreviewsCommand()) // Regenerate previews for conversations with broken/system text
	reg.Register(NewSwarmCommand())           // A2A swarm status and peer management
	reg.Register(NewThemeCommand())           // Theme selector with interactive menu
	reg.Register(NewAutoModeCommand())        // Toggle auto-mode classifier
	reg.Register(NewAttachCommand())          // Attach to a running agent daemon (tmux-like)
	reg.Register(NewDetachCommand())          // Detach from the attached daemon (it keeps running)
	reg.Register(NewLoopCommand())            // Schedule a recurring task (/loop 5m task)
	reg.Register(NewGoalCommand())            // Set / show / clear the active goal stop-hook
	reg.Register(NewProtectCommand())         // Protect git main branch: block edits, force worktree
	reg.Register(NewWorkspaceCommand())       // Select or create the execution worktree

	// Register update command if updater is available
	if updater != nil {
		reg.Register(NewUpdateCommand(updater))
	}

	// Cache command removed - now integrated into settings screen (/render)

	// Add more commands by creating new files and registering them here
	// Example:
	// reg.Register(NewConfigCommand())
	// reg.Register(NewAgentsCommand())

	return reg
}
