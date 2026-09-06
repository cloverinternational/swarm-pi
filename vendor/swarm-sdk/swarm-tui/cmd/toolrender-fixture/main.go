package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat"
)

func main() {
	program := tea.NewProgram(chat.NewToolSidebarFixture())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tool render fixture: %v\n", err)
		os.Exit(1)
	}
}
