package chat

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// MCPPromptResultMsg is sent when an MCP prompt execution completes
type MCPPromptResultMsg struct {
	ServerName string
	PromptName string
	Result     *mcp.PromptResult
	Error      error
}

// handleSlashCommand processes a slash command and returns the command to execute
func (a *App) handleSlashCommand(input string) tea.Cmd {
	// Parse command and args
	input = strings.TrimPrefix(input, "/")
	parts := strings.Fields(input)

	if len(parts) == 0 {
		return nil
	}

	cmdName := parts[0]
	args := parts[1:]

	logDebug("Executing slash command: /%s with args: %v", cmdName, args)

	// Phase 4d: the harness editor is a session-scoped overlay, opened only when
	// an interactive harness is active. Kept as a tiny additive dispatch here so
	// non-harness sessions are entirely unaffected.
	if cmdName == "harness" {
		return a.openHarnessEditor(args)
	}

	// Look up built-in command first
	cmd, ok := a.cmdRegistry.Get(cmdName)
	if ok {
		// Execute builtin command
		cmdToRun := cmd.Execute(args)

		// If command is interactive, set it as active
		if cmd.IsInteractive() {
			cmdWidth := a.width
			if a.showSidePanel && a.width >= MinWidthForSidePanel {
				cmdWidth = a.width - SidePanelWidth
			}
			cmd.Update(tea.WindowSizeMsg{Width: cmdWidth, Height: a.height})
			a.activeCommand = cmd
		}

		// Clear input
		a.textInput.SetValue("")
		a.cmdAutocomplete.Hide()
		a.mentionAutocomplete.Hide()

		return cmdToRun
	}

	// Check if it's a plugin command (format: namespace:command)
	// Plugin commands take priority over MCP prompts
	if strings.Contains(cmdName, ":") && a.sdk != nil && a.sdk.pluginsManager != nil {
		plugin, pluginCmd := a.sdk.pluginsManager.GetCommand(cmdName)
		if pluginCmd != nil {
			logDebug("Found plugin command: /%s from plugin %s", cmdName, plugin.Manifest.Name)
			return a.executePluginCommand(plugin, pluginCmd, args)
		}
	}

	// Check if it's an MCP prompt command (format: serverName:promptName)
	if strings.Contains(cmdName, ":") {
		return a.executeMCPPrompt(cmdName, args)
	}

	// Check for plugin commands without namespace (legacy support)
	if a.sdk != nil && a.sdk.pluginsManager != nil {
		plugin, pluginCmd := a.sdk.pluginsManager.GetCommand(cmdName)
		if pluginCmd != nil {
			logDebug("Found plugin command: /%s from plugin %s", cmdName, plugin.Manifest.Name)
			return a.executePluginCommand(plugin, pluginCmd, args)
		}
	}

	logDebug("Unknown command: /%s", cmdName)
	// Show error notification
	a.addNotification("error", i18n.T("chat_b.slash.unknown_command", cmdName))
	a.textInput.SetValue("")
	return nil
}

// executeMCPPrompt executes an MCP prompt command
func (a *App) executeMCPPrompt(cmdName string, args []string) tea.Cmd {
	// Parse serverName:promptName
	colonIdx := strings.Index(cmdName, ":")
	if colonIdx <= 0 || colonIdx >= len(cmdName)-1 {
		a.addNotification("error", i18n.T("chat_b.slash.invalid_mcp_prompt", cmdName))
		a.textInput.SetValue("")
		return nil
	}

	serverName := cmdName[:colonIdx]
	promptName := cmdName[colonIdx+1:]

	logDebug("Executing MCP prompt: server=%s, prompt=%s, args=%v", serverName, promptName, args)

	// Check if MCP manager is available
	if a.sdk == nil || a.sdk.mcpManager == nil {
		a.addNotification("error", "MCP manager not available")
		a.textInput.SetValue("")
		return nil
	}

	// Check if server is connected
	if !a.sdk.mcpManager.IsServerConnected(serverName) {
		a.addNotification("error", i18n.T("chat_b.slash.server_not_connected", serverName))
		a.textInput.SetValue("")
		return nil
	}

	// Get the prompt metadata to understand expected arguments
	prompts := a.sdk.mcpManager.GetServerPrompts(serverName)
	var prompt *mcp.MCPPrompt
	for _, p := range prompts {
		if p.Name == promptName {
			prompt = p
			break
		}
	}

	if prompt == nil {
		a.addNotification("error", i18n.T("chat_b.slash.prompt_not_found", promptName, serverName))
		a.textInput.SetValue("")
		return nil
	}

	// Build arguments map from positional args
	promptArgs := make(map[string]string)
	for i, arg := range prompt.Arguments {
		if i < len(args) {
			promptArgs[arg.Name] = args[i]
		}
	}

	// Clear input and hide autocomplete
	a.textInput.SetValue("")
	a.cmdAutocomplete.Hide()
	a.mentionAutocomplete.Hide()

	// Execute the prompt asynchronously
	return func() tea.Msg {
		ctx := context.Background()
		result, err := a.sdk.mcpManager.GetPrompt(ctx, serverName, promptName, promptArgs)
		return MCPPromptResultMsg{
			ServerName: serverName,
			PromptName: promptName,
			Result:     result,
			Error:      err,
		}
	}
}

// executePluginCommand executes a plugin command by prepending its instructions to the user's message
func (a *App) executePluginCommand(pluginInterface any, cmdInterface any, args []string) tea.Cmd {
	// Type assert to get the actual command
	cmd, ok := cmdInterface.(*plugins.Command)
	if !ok {
		a.addNotification("error", "Invalid plugin command structure")
		return nil
	}

	plugin, ok := pluginInterface.(*plugins.Plugin)
	if !ok {
		a.addNotification("error", "Invalid plugin structure")
		return nil
	}

	logDebug("Executing plugin command: %s from plugin: %s", cmd.Name, plugin.Manifest.Name)

	// Get the command content (instructions)
	cmdContent := cmd.Content

	// Replace argument placeholders
	argText := strings.Join(args, " ")
	cmdContent = strings.ReplaceAll(cmdContent, "$ARGUMENTS", argText)
	for i, arg := range args {
		placeholder := fmt.Sprintf("$%d", i+1)
		cmdContent = strings.ReplaceAll(cmdContent, placeholder, arg)
	}

	// Create a message that includes both the command instructions and the user's input
	// The command content acts as special instructions for Claude
	fullMessage := fmt.Sprintf("%s\n\nUser request: %s", cmdContent, argText)

	logDebug("Plugin command content length: %d characters", len(cmdContent))

	// Set the full message in the input and send it
	a.textInput.SetValue(fullMessage)

	// Clear autocomplete
	a.cmdAutocomplete.Hide()
	a.mentionAutocomplete.Hide()

	// Send the message
	return a.handleSendMessage()
}
