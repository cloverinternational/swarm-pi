// Package commands provides the slash command system for SwarmOS.
// Each command is a separate file implementing the Command interface.
package commands

import (
	tea "charm.land/bubbletea/v2"
)

// Command represents a slash command that can be executed in the chat.
type Command interface {
	// Name returns the command name (without the leading /)
	Name() string

	// Description returns a short description for autocomplete
	Description() string

	// Aliases returns alternative names for this command
	Aliases() []string

	// Execute runs the command with given arguments
	Execute(args []string) tea.Cmd

	// View renders the command's UI (if interactive)
	View() string

	// Update handles messages for interactive commands
	Update(msg tea.Msg) (Command, tea.Cmd)

	// IsInteractive returns true if the command needs interactive UI
	IsInteractive() bool
}

// SubcommandProvider is an optional interface for commands that support subcommands.
// Commands implementing this can provide autocomplete suggestions for their subcommands.
type SubcommandProvider interface {
	// Subcommands returns available subcommands and their descriptions
	Subcommands() []Subcommand

	// ArgumentCompletions returns completions for a specific subcommand's arguments
	ArgumentCompletions(subcommand string) []string
}

// Subcommand represents a subcommand with its name and description
type Subcommand struct {
	Name        string
	Description string
}

// Registry manages all available commands
type Registry struct {
	commands map[string]Command
	aliases  map[string]string // alias -> command name
}

// NewRegistry creates a new command registry
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]Command),
		aliases:  make(map[string]string),
	}
}

// Register adds a command to the registry
func (r *Registry) Register(cmd Command) {
	name := cmd.Name()
	r.commands[name] = cmd

	// Register aliases
	for _, alias := range cmd.Aliases() {
		r.aliases[alias] = name
	}
}

// Get retrieves a command by name or alias
func (r *Registry) Get(name string) (Command, bool) {
	// Try direct lookup
	if cmd, ok := r.commands[name]; ok {
		return cmd, true
	}

	// Try alias lookup
	if cmdName, ok := r.aliases[name]; ok {
		return r.commands[cmdName], true
	}

	return nil, false
}

// All returns all registered commands
func (r *Registry) All() []Command {
	cmds := make([]Command, 0, len(r.commands))
	for _, cmd := range r.commands {
		cmds = append(cmds, cmd)
	}
	return cmds
}

// Match returns commands that match the given prefix
func (r *Registry) Match(prefix string) []Command {
	if prefix == "" {
		return r.All()
	}

	var matches []Command
	for name, cmd := range r.commands {
		// Check command name
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			matches = append(matches, cmd)
			continue
		}

		// Check aliases
		for _, alias := range cmd.Aliases() {
			if len(alias) >= len(prefix) && alias[:len(prefix)] == prefix {
				matches = append(matches, cmd)
				break
			}
		}
	}
	return matches
}
