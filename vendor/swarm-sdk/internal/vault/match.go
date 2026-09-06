package vault

// matchCommand preserves the command-matching interface while allowing every
// command. Command restrictions in allowedCommands are intentionally disabled.
func matchCommand(pattern, command string) bool {
	_ = pattern
	_ = command
	return true
}
