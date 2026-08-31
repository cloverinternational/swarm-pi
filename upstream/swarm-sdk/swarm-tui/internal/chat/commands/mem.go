package commands

import (
	"runtime"
	"runtime/debug"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// MemCommand shows live memory statistics and optionally triggers GC.
type MemCommand struct {
	interactive bool
}

// NewMemCommand creates a new /mem command.
func NewMemCommand() *MemCommand {
	return &MemCommand{interactive: false}
}

func (c *MemCommand) Name() string        { return "mem" }
func (c *MemCommand) Description() string { return i18n.T("commands_b.mem.description") }
func (c *MemCommand) Aliases() []string   { return []string{"memory", "heap"} }
func (c *MemCommand) IsInteractive() bool { return c.interactive }

func (c *MemCommand) Execute(args []string) tea.Cmd {
	return func() tea.Msg {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)

		// Format numbers
		heapMB := float64(ms.HeapAlloc) / 1024 / 1024
		sysMB := float64(ms.Sys) / 1024 / 1024
		allocMB := float64(ms.Alloc) / 1024 / 1024

		result := i18n.T(
			"commands_b.mem.stats",
			heapMB, sysMB, allocMB, runtime.NumGoroutine(), ms.NumGC,
			float64(ms.NextGC)/1024/1024,
		)

		// Optional sub-command: /mem gc
		if len(args) > 0 && args[0] == "gc" {
			start := time.Now()
			runtime.GC()
			debug.FreeOSMemory()
			result += i18n.T("commands_b.mem.gc_duration", time.Since(start))
		}

		return MemStatsMsg{Text: result}
	}
}

func (c *MemCommand) Update(msg tea.Msg) (Command, tea.Cmd) { return c, nil }
func (c *MemCommand) View() string                          { return "" }

// MemStatsMsg carries formatted memory stats back to the app.
type MemStatsMsg struct {
	Text string
}
