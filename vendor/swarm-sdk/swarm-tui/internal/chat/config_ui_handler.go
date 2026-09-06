package chat

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// CommandArgumentOptions implements commands.ArgumentOptionProvider. Values are
// derived from the current SDK/settings state on every autocomplete refresh, so
// changes made in Settings appear immediately.
func (a *App) CommandArgumentOptions(commandName string) []commands.ArgumentOption {
	switch commandName {
	case "profile":
		return a.profileArgumentOptions()
	case "agents":
		return a.agentArgumentOptions()
	case "prompt":
		return a.promptArgumentOptions()
	case "compaction":
		return []commands.ArgumentOption{{
			Label:       "Open Settings → Compaction",
			Description: "Configure thresholds, model, and automatic compaction",
			Action:      true,
		}}
	case "providers":
		return []commands.ArgumentOption{{
			Label:       "Open Settings → Models & Providers",
			Description: "Manage providers, model profiles, and model configuration",
			Action:      true,
		}}
	default:
		return nil
	}
}

func (a *App) profileArgumentOptions() []commands.ArgumentOption {
	if a.sdk == nil {
		return unavailableArgumentOption("Profiles are still loading")
	}
	pm := a.sdk.GetProfileManager()
	if pm == nil {
		return unavailableArgumentOption("Profile manager is unavailable")
	}

	activeID := ""
	if active, err := pm.GetActiveProfile(); err == nil && active != nil {
		activeID = active.ID
	}
	defaultID := ""
	if config := pm.GetConfig(); config != nil {
		defaultID = config.DefaultProfile
	}

	profiles := pm.ListProfiles()
	options := make([]commands.ArgumentOption, 0, len(profiles))
	for _, profile := range profiles {
		badges := make([]string, 0, 2)
		if profile.ID == activeID {
			badges = append(badges, "current")
		}
		if profile.ID == defaultID {
			badges = append(badges, "default")
		}
		options = append(options, commands.ArgumentOption{
			Value:       profile.ID,
			Label:       profile.Name,
			Description: profile.Description,
			Badges:      badges,
		})
	}
	if len(options) == 0 {
		return unavailableArgumentOption("No model profiles configured")
	}
	return options
}

func (a *App) agentArgumentOptions() []commands.ArgumentOption {
	if a.settingsManager == nil || a.settingsManager.GetAgentsSettings() == nil {
		return unavailableArgumentOption("Agent settings are still loading")
	}
	agentSettings := a.settingsManager.GetAgentsSettings()
	defaultID := agentSettings.GetDefaultAgentID()
	agents := agentSettings.GetAgents()
	options := make([]commands.ArgumentOption, 0, len(agents))
	for _, agentEntry := range agents {
		badges := make([]string, 0, 2)
		if agentEntry.ID == defaultID {
			badges = append(badges, "default")
		}
		if agentEntry.Builtin {
			badges = append(badges, "builtin")
		}
		options = append(options, commands.ArgumentOption{
			Value:       agentEntry.ID,
			Label:       agentEntry.Name,
			Description: agentEntry.Description,
			Badges:      badges,
		})
	}
	if len(options) == 0 {
		return unavailableArgumentOption("No sub-agents configured")
	}
	return options
}

func (a *App) promptArgumentOptions() []commands.ArgumentOption {
	if a.settingsManager == nil || a.settingsManager.GetSystemPromptSettings() == nil {
		return unavailableArgumentOption("System prompts are still loading")
	}
	promptSettings := a.settingsManager.GetSystemPromptSettings()
	activeName := promptSettings.GetActivePromptName()
	prompts := promptSettings.GetPrompts()
	options := make([]commands.ArgumentOption, 0, len(prompts))
	for _, prompt := range prompts {
		badges := make([]string, 0, 2)
		if prompt.Name == activeName {
			badges = append(badges, "active")
		}
		if prompt.Builtin {
			badges = append(badges, "builtin")
		}
		options = append(options, commands.ArgumentOption{
			Value:       prompt.Name,
			Label:       prompt.Name,
			Description: oneLinePreview(prompt.Content, 96),
			Badges:      badges,
		})
	}
	if len(options) == 0 {
		return unavailableArgumentOption("No system prompts configured")
	}
	return options
}

func unavailableArgumentOption(message string) []commands.ArgumentOption {
	return []commands.ArgumentOption{{
		Label:       message,
		Description: "Press Enter to open the command UI",
		Action:      true,
	}}
}

func oneLinePreview(content string, limit int) string {
	preview := strings.Join(strings.Fields(content), " ")
	runes := []rune(preview)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return preview
}

// handleOpenConfigUI reacts to a commands.OpenConfigUIMsg emitted by a config
// slash command (/profile, /agents, /prompt, /compaction, /providers). It opens
// the matching overlay switcher or Settings section. When the command carries a
// direct-apply argument (e.g. "/profile prod"), it applies the change without
// opening any UI.
//
// This lives in the chat package (not commands) because it needs *App plus both
// the commands and settings packages; commands must not import settings.
func (a *App) handleOpenConfigUI(msg commands.OpenConfigUIMsg) {
	arg := strings.TrimSpace(msg.Arg)
	switch msg.Target {
	case "profile":
		if arg != "" {
			a.applyProfileByArg(arg)
		} else {
			a.executeCommand("switch_profile") // opens a.profileSwitcher
		}
	case "agents":
		if arg != "" {
			a.setDefaultAgentByArg(arg)
		} else {
			a.executeCommand("switch_agent") // opens a.agentSwitcher
		}
	case "prompt":
		if arg != "" {
			a.setActivePromptByArg(arg)
		} else {
			a.executeCommand("switch_prompt") // opens a.promptSwitcher
		}
	case "compaction":
		a.openSettingsSection(settings.SectionCompaction)
	case "providers":
		a.openSettingsSection(settings.SectionModels)
	default:
		a.addNotification("error", "Unknown config target: "+msg.Target)
		return
	}
	a.viewNeedsRefresh = true
}

// openSettingsSection navigates to the Home → Settings tab and selects the given
// section, matching how the command palette opens settings.
func (a *App) openSettingsSection(section settings.Section) {
	a.executeCommand("settings") // sets ScreenHome + ButtonSettings + refreshes data
	if a.settingsManager != nil {
		a.settingsManager.SelectSection(section)
	}
}

// applyProfileByArg switches the active model profile directly by profile ID
// (case-insensitive). Bare /profile opens the overlay for name-based picking;
// this fast path matches against the available profile IDs. It mirrors the
// success path used by the profile-switcher Enter handler so the current model
// display stays in sync with the newly-activated profile.
func (a *App) applyProfileByArg(arg string) {
	if a.sdk == nil {
		a.addNotification("error", "Profile system not ready")
		return
	}
	pm := a.sdk.GetProfileManager()
	if pm == nil {
		a.addNotification("error", "No profile manager available")
		return
	}
	profiles := pm.ListProfiles()
	ids := make([]string, 0, len(profiles))
	id := ""
	for _, profile := range profiles {
		ids = append(ids, profile.ID)
		if strings.EqualFold(profile.ID, arg) {
			id = profile.ID
			break
		}
	}
	// IDs are authoritative, but accepting an exact case-insensitive display
	// name makes the advertised `/profile <name|id>` fast path truthful.
	if id == "" {
		var nameMatches []string
		for _, profile := range profiles {
			if strings.EqualFold(profile.Name, arg) {
				nameMatches = append(nameMatches, profile.ID)
			}
		}
		if len(nameMatches) == 1 {
			id = nameMatches[0]
		} else if len(nameMatches) > 1 {
			a.addNotification("error", fmt.Sprintf(
				"Profile name %q is ambiguous; use an ID (%s)",
				arg, strings.Join(nameMatches, ", ")))
			return
		}
	}
	if id == "" {
		a.addNotification("error", fmt.Sprintf("Profile not found: %s (available: %s)", arg, strings.Join(ids, ", ")))
		return
	}
	if err := a.sdk.SwitchProfile(id); err != nil {
		a.addNotification("error", fmt.Sprintf("Profile switch failed: %v", err))
		return
	}
	if provider, model, err := a.sdk.GetModelForRole(settings.AliasMain); err == nil && a.settingsManager != nil {
		a.settingsManager.GetModelSettings().SetModel(provider, model)
		a.currentProvider = provider
		a.currentModel = model
		a.currentProviderDisplay = provider
		a.currentModelDisplay = model
		a.modelContextWindow = a.sdk.GetModelContextWindow()
	}
	a.addNotification("success", "Switched to profile: "+id)
}

// setDefaultAgentByArg sets the default sub-agent directly (used by
// "/agents <id>").
func (a *App) setDefaultAgentByArg(arg string) {
	if a.settingsManager == nil {
		a.addNotification("error", "Settings not ready")
		return
	}
	as := a.settingsManager.GetAgentsSettings()
	if as == nil {
		a.addNotification("error", "Agents settings unavailable")
		return
	}
	if err := as.SetDefaultAgent(arg); err != nil {
		a.addNotification("error", fmt.Sprintf("Set agent failed: %v", err))
		return
	}
	a.addNotification("success", "Default agent: "+arg)
}

// setActivePromptByArg activates a system prompt directly (used by
// "/prompt <name>").
func (a *App) setActivePromptByArg(arg string) {
	if a.settingsManager == nil {
		a.addNotification("error", "Settings not ready")
		return
	}
	sp := a.settingsManager.GetSystemPromptSettings()
	if sp == nil {
		a.addNotification("error", "System prompt settings unavailable")
		return
	}
	if err := sp.SetActivePrompt(arg); err != nil {
		a.addNotification("error", fmt.Sprintf("Set prompt failed: %v", err))
		return
	}
	a.addNotification("success", "System prompt: "+arg)
}
