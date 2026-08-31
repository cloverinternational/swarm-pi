// Package commands provides the profile command for performance analysis.
package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// ProfileCommand handles CPU and memory profiling
type ProfileCommand struct {
	mu             sync.Mutex
	cpuFile        *os.File
	cpuProfilePath string
	outputDir      string
	isRunning      bool
	startTime      time.Time

	// UI state
	visible     bool
	selectedIdx int
	lastResult  string
	lastError   string
}

// NewProfileCommand creates a new profile command
func NewProfileCommand() *ProfileCommand {
	home, _ := os.UserHomeDir()
	return &ProfileCommand{
		outputDir:   filepath.Join(home, ".swarmos", "profiles"),
		selectedIdx: 0,
	}
}

func (c *ProfileCommand) Name() string { return "pprof" }
func (c *ProfileCommand) Description() string {
	return i18n.T("commands.profile.description")
}
func (c *ProfileCommand) Aliases() []string   { return []string{"prof", "perf", "profiling"} }
func (c *ProfileCommand) IsInteractive() bool { return true }

func (c *ProfileCommand) Execute(args []string) tea.Cmd {
	// Handle non-interactive execution with args
	if len(args) > 0 {
		switch args[0] {
		case "cpu-start":
			path, err := c.startCPUProfile()
			if err != nil {
				return func() tea.Msg { return ProfileResultMsg{Error: err.Error()} }
			}
			return func() tea.Msg { return ProfileResultMsg{Result: i18n.T("commands.profile.cpu_started", path)} }

		case "cpu-stop":
			path, err := c.stopCPUProfile()
			if err != nil {
				return func() tea.Msg { return ProfileResultMsg{Error: err.Error()} }
			}
			return func() tea.Msg { return ProfileResultMsg{Result: i18n.T("commands.profile.cpu_saved", path)} }

		case "mem":
			path, err := c.writeMemProfile()
			if err != nil {
				return func() tea.Msg { return ProfileResultMsg{Error: err.Error()} }
			}
			return func() tea.Msg { return ProfileResultMsg{Result: i18n.T("commands.profile.memory_saved", path)} }

		case "goroutine":
			path, err := c.writeGoroutineProfile()
			if err != nil {
				return func() tea.Msg { return ProfileResultMsg{Error: err.Error()} }
			}
			return func() tea.Msg { return ProfileResultMsg{Result: i18n.T("commands.profile.goroutine_saved", path)} }

		case "stats":
			stats := c.getMemStatsFormatted()
			return func() tea.Msg { return ProfileResultMsg{Result: stats} }

		case "open":
			if len(args) > 1 {
				return c.openProfile(args[1])
			}
			return func() tea.Msg { return ProfileResultMsg{Error: i18n.T("commands.profile.open_usage")} }
		}
	}

	// Show interactive UI
	c.visible = true
	return nil
}

func (c *ProfileCommand) View() string {
	if !c.visible {
		return ""
	}

	// Menu options
	options := []struct {
		key   string
		label string
		desc  string
	}{
		{"1", i18n.T("commands.profile.menu.start_cpu"), i18n.T("commands.profile.menu.start_cpu_desc")},
		{"2", i18n.T("commands.profile.menu.stop_cpu"), i18n.T("commands.profile.menu.stop_cpu_desc")},
		{"3", i18n.T("commands.profile.menu.memory"), i18n.T("commands.profile.menu.memory_desc")},
		{"4", i18n.T("commands.profile.menu.goroutine"), i18n.T("commands.profile.menu.goroutine_desc")},
		{"5", i18n.T("commands.profile.menu.memory_stats"), i18n.T("commands.profile.menu.memory_stats_desc")},
		{"6", i18n.T("commands.profile.menu.open_dir"), i18n.T("commands.profile.menu.open_dir_desc")},
		{"q", i18n.T("commands.profile.menu.close"), i18n.T("commands.profile.menu.close_desc")},
	}

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Text))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	resultStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Success))

	errorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Error))

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("commands.profile.title")))
	lines = append(lines, "")

	// Status
	var statusLine string
	if c.isRunning {
		duration := time.Since(c.startTime).Round(time.Second)
		statusLine = selectedStyle.Render(i18n.T("commands.profile.cpu_active", duration))
	} else {
		statusLine = dimStyle.Render(i18n.T("commands.profile.cpu_inactive"))
	}
	lines = append(lines, statusLine)
	lines = append(lines, "")

	// Menu
	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == c.selectedIdx {
			cursor = "→ "
			style = selectedStyle
		}
		line := cursor + style.Render(fmt.Sprintf("[%s] %s", opt.key, opt.label))
		lines = append(lines, line)
		lines = append(lines, "    "+dimStyle.Render(opt.desc))
	}

	// Result/Error
	if c.lastResult != "" {
		lines = append(lines, "")
		lines = append(lines, resultStyle.Render("✓ "+c.lastResult))
	}
	if c.lastError != "" {
		lines = append(lines, "")
		lines = append(lines, errorStyle.Render("✗ "+c.lastError))
	}

	// Help
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.profile.navigation_hint")))
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.profile.saved_to", c.outputDir)))
	lines = append(lines, dimStyle.Render(i18n.T("commands.profile.view_with")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2).
		Width(60)

	return boxStyle.Render(content)
}

func (c *ProfileCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		c.lastResult = ""
		c.lastError = ""

		switch msg.String() {
		case "q", "esc":
			c.visible = false
			return c, nil

		case "up", "k":
			if c.selectedIdx > 0 {
				c.selectedIdx--
			}

		case "down", "j":
			if c.selectedIdx < 6 {
				c.selectedIdx++
			}

		case "enter":
			return c, c.executeSelected()

		case "1":
			c.selectedIdx = 0
			return c, c.executeSelected()
		case "2":
			c.selectedIdx = 1
			return c, c.executeSelected()
		case "3":
			c.selectedIdx = 2
			return c, c.executeSelected()
		case "4":
			c.selectedIdx = 3
			return c, c.executeSelected()
		case "5":
			c.selectedIdx = 4
			return c, c.executeSelected()
		case "6":
			c.selectedIdx = 5
			return c, c.executeSelected()
		}

	case ProfileResultMsg:
		if msg.Error != "" {
			c.lastError = msg.Error
		} else {
			c.lastResult = msg.Result
		}
	}

	return c, nil
}

func (c *ProfileCommand) executeSelected() tea.Cmd {
	switch c.selectedIdx {
	case 0: // Start CPU
		path, err := c.startCPUProfile()
		if err != nil {
			return func() tea.Msg { return ProfileResultMsg{Error: err.Error()} }
		}
		return func() tea.Msg { return ProfileResultMsg{Result: i18n.T("commands.profile.cpu_started", path)} }

	case 1: // Stop CPU
		path, err := c.stopCPUProfile()
		if err != nil {
			return func() tea.Msg { return ProfileResultMsg{Error: err.Error()} }
		}
		return func() tea.Msg { return ProfileResultMsg{Result: i18n.T("commands.profile.cpu_saved", path)} }

	case 2: // Memory
		path, err := c.writeMemProfile()
		if err != nil {
			return func() tea.Msg { return ProfileResultMsg{Error: err.Error()} }
		}
		return func() tea.Msg { return ProfileResultMsg{Result: i18n.T("commands.profile.memory_saved", path)} }

	case 3: // Goroutine
		path, err := c.writeGoroutineProfile()
		if err != nil {
			return func() tea.Msg { return ProfileResultMsg{Error: err.Error()} }
		}
		return func() tea.Msg { return ProfileResultMsg{Result: i18n.T("commands.profile.goroutine_saved", path)} }

	case 4: // Stats
		stats := c.getMemStatsFormatted()
		return func() tea.Msg { return ProfileResultMsg{Result: stats} }

	case 5: // Open dir
		return c.openProfilesDir()

	case 6: // Close
		c.visible = false
	}

	return nil
}

// Profile operations

func (c *ProfileCommand) startCPUProfile() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cpuFile != nil {
		return "", fmt.Errorf("%s", i18n.T("commands.profile.already_running"))
	}

	if err := os.MkdirAll(c.outputDir, 0755); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.create_directory_failed"), err)
	}

	timestamp := time.Now().Format("20060102-150405")
	c.cpuProfilePath = filepath.Join(c.outputDir, fmt.Sprintf("cpu-%s.prof", timestamp))

	f, err := os.Create(c.cpuProfilePath)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.create_cpu_file_failed"), err)
	}

	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.start_cpu_failed"), err)
	}

	c.cpuFile = f
	c.isRunning = true
	c.startTime = time.Now()

	return c.cpuProfilePath, nil
}

func (c *ProfileCommand) stopCPUProfile() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cpuFile == nil {
		return "", fmt.Errorf("%s", i18n.T("commands.profile.not_running"))
	}

	pprof.StopCPUProfile()
	c.cpuFile.Close()
	path := c.cpuProfilePath
	c.cpuFile = nil
	c.isRunning = false

	return path, nil
}

func (c *ProfileCommand) writeMemProfile() (string, error) {
	if err := os.MkdirAll(c.outputDir, 0755); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.create_directory_failed"), err)
	}

	timestamp := time.Now().Format("20060102-150405")
	path := filepath.Join(c.outputDir, fmt.Sprintf("mem-%s.prof", timestamp))

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.create_memory_file_failed"), err)
	}
	defer f.Close()

	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.write_memory_failed"), err)
	}

	return path, nil
}

func (c *ProfileCommand) writeGoroutineProfile() (string, error) {
	if err := os.MkdirAll(c.outputDir, 0755); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.create_directory_failed"), err)
	}

	timestamp := time.Now().Format("20060102-150405")
	path := filepath.Join(c.outputDir, fmt.Sprintf("goroutine-%s.prof", timestamp))

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.create_goroutine_file_failed"), err)
	}
	defer f.Close()

	if err := pprof.Lookup("goroutine").WriteTo(f, 2); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("commands.profile.write_goroutine_failed"), err)
	}

	return path, nil
}

func (c *ProfileCommand) getMemStatsFormatted() string {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return i18n.T(
		"commands.profile.memory_stats",
		formatBytes(stats.Alloc),
		formatBytes(stats.TotalAlloc),
		formatBytes(stats.Sys),
		stats.NumGC,
		runtime.NumGoroutine(),
	)
}

func (c *ProfileCommand) openProfilesDir() tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", c.outputDir)
		case "linux":
			cmd = exec.Command("xdg-open", c.outputDir)
		case "windows":
			cmd = exec.Command("explorer", c.outputDir)
		default:
			return ProfileResultMsg{Error: i18n.T("commands.profile.unsupported_os")}
		}
		if err := cmd.Start(); err != nil {
			return ProfileResultMsg{Error: err.Error()}
		}
		return ProfileResultMsg{Result: i18n.T("commands.profile.opened", c.outputDir)}
	}
}

func (c *ProfileCommand) openProfile(path string) tea.Cmd {
	return func() tea.Msg {
		// Try to launch pprof web interface
		cmd := exec.Command("go", "tool", "pprof", "-http=localhost:8080", path)
		if err := cmd.Start(); err != nil {
			return ProfileResultMsg{Error: i18n.T("commands.profile.open_failed", err)}
		}
		return ProfileResultMsg{Result: i18n.T("commands.profile.pprof_opened")}
	}
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ProfileResultMsg is sent when a profile operation completes
type ProfileResultMsg struct {
	Result string
	Error  string
}
