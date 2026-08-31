// Package chat provides a debug render mode for testing TUI rendering
package chat

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	bashrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/bash"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// DebugRenderApp is a simplified TUI app for testing rendering
type DebugRenderApp struct {
	messages       []Message
	msgViewport    *MessageList
	renderSettings *RenderSettings
	width          int
	height         int
	showThinking   bool
	showFullOutput bool
	helpVisible    bool
	statusMsg      string
	streamingDemo  bool
	streamIndex    int
	streamMaxLines int      // Max lines to show (tail behavior)
	streamLines    []string // Accumulated output lines
}

// NewDebugRenderApp creates a new debug render app
func NewDebugRenderApp() *DebugRenderApp {
	vp := NewMessageList(80, 20)

	return &DebugRenderApp{
		messages:       []Message{},
		msgViewport:    vp,
		renderSettings: NewDefaultRenderSettings(),
		showThinking:   true,
		showFullOutput: false,
		helpVisible:    true,
		statusMsg:      i18n.T("final.render.status_initial"),
	}
}

// Init initializes the debug render app
func (d *DebugRenderApp) Init() tea.Cmd {
	return nil
}

// streamTickMsg is sent periodically during streaming demo
type streamTickMsg struct{}

func localizedRenderBool(value bool) string {
	if value {
		return i18n.T("settings.integrations.voice.on")
	}
	return i18n.T("settings.integrations.voice.off")
}

// Update handles messages for the debug render app
func (d *DebugRenderApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
		d.msgViewport.SetSize(msg.Width-4, msg.Height-6)
		d.updateViewport()
		return d, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return d, tea.Quit

		case "0":
			d.messages = []Message{}
			d.statusMsg = i18n.T("final.render.cleared")
			d.updateViewport()

		case "1":
			d.injectScenario1()
			d.statusMsg = i18n.T("final.render.scenario_1")

		case "2":
			d.injectScenario2()
			d.statusMsg = i18n.T("final.render.scenario_2")

		case "3":
			d.injectScenario3()
			d.statusMsg = i18n.T("final.render.scenario_3")

		case "4":
			d.injectScenario4()
			d.statusMsg = i18n.T("final.render.scenario_4")

		case "5":
			d.injectScenario5()
			d.statusMsg = i18n.T("final.render.scenario_5")

		case "6":
			d.injectScenario6()
			d.statusMsg = i18n.T("final.render.scenario_6")

		case "7":
			d.injectScenario7()
			d.statusMsg = i18n.T("final.render.scenario_7")
			return d, d.startStreaming()

		case "8":
			d.injectScenario8()
			d.statusMsg = i18n.T("final.render.scenario_8")

		case "9":
			d.injectScenario9()
			d.statusMsg = i18n.T("final.render.scenario_9")

		case "d":
			d.injectScenario10()
			d.statusMsg = i18n.T("final.render.scenario_10")

		case "r":
			d.injectScenario11()
			d.statusMsg = i18n.T("final.render.scenario_11")

		case "t":
			d.showThinking = !d.showThinking
			d.statusMsg = i18n.T("final.render.show_thinking", localizedRenderBool(d.showThinking))
			d.updateViewport()

		case "o":
			d.showFullOutput = !d.showFullOutput
			d.statusMsg = i18n.T("final.render.show_full_output", localizedRenderBool(d.showFullOutput))
			d.updateViewport()

		case "h":
			d.helpVisible = !d.helpVisible
			d.updateViewport()

		case "up", "k":
			d.msgViewport.ScrollUp(1)

		case "down", "j":
			d.msgViewport.ScrollDown(1)

		case "pgup":
			d.msgViewport.HalfPageUp()

		case "pgdown":
			d.msgViewport.HalfPageDown()
		}

	case streamTickMsg:
		if d.streamingDemo && d.streamIndex < 100 {
			d.streamIndex++

			// Add new line to stream
			newLine := fmt.Sprintf("[%s] Line %3d: Processing item %d... ████████ OK",
				time.Now().Format("15:04:05.000"), d.streamIndex, d.streamIndex)
			d.streamLines = append(d.streamLines, newLine)

			// Keep only last N lines (tail behavior)
			if len(d.streamLines) > d.streamMaxLines {
				d.streamLines = d.streamLines[len(d.streamLines)-d.streamMaxLines:]
			}

			// Build output string from visible lines
			output := strings.Join(d.streamLines, "\n")
			if d.streamIndex < 100 {
				output += fmt.Sprintf("\n... streaming (%d/100)", d.streamIndex)
			}

			// Update last message's tool result
			if len(d.messages) > 0 {
				lastMsg := &d.messages[len(d.messages)-1]
				for i := len(lastMsg.OrderedBlocks) - 1; i >= 0; i-- {
					if lastMsg.OrderedBlocks[i].Type == "tool_result" {
						lastMsg.OrderedBlocks[i].ToolResult.Output = output
						break
					}
				}
				d.updateViewport()
			}

			d.statusMsg = i18n.T("final.render.streaming_status", d.streamIndex, d.streamMaxLines)

			if d.streamIndex < 100 {
				// 4 lines per second = 250ms per line
				return d, tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
					return streamTickMsg{}
				})
			} else {
				d.streamingDemo = false
				d.statusMsg = i18n.T("final.render.streaming_complete")
			}
		}
	}

	return d, nil
}

// startStreaming starts the streaming simulation - counts to 100 at 4 lines/sec
func (d *DebugRenderApp) startStreaming() tea.Cmd {
	d.streamingDemo = true
	d.streamIndex = 0
	d.streamMaxLines = 10 // Show only last 10 lines (tail behavior)
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
		return streamTickMsg{}
	})
}

// View renders the debug render app
func (d *DebugRenderApp) View() tea.View {
	var v tea.View
	var b strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF6B6B")).
		Background(lipgloss.Color("#2D2D2D")).
		Padding(0, 1)

	header := headerStyle.Render(i18n.T("final.render.title"))
	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Italic(true)

	b.WriteString(header)
	b.WriteString("  ")
	b.WriteString(statusStyle.Render(d.statusMsg))
	b.WriteString("\n")

	// Separator
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444")).Render(strings.Repeat("─", d.width))
	b.WriteString(sep)
	b.WriteString("\n")

	// Message viewport
	b.WriteString(d.msgViewport.View())
	b.WriteString("\n")

	// Footer with help
	b.WriteString(sep)
	b.WriteString("\n")

	if d.helpVisible {
		helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
		keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FAB387")).Bold(true)
		descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

		b.WriteString("\n")
		b.WriteString(helpStyle.Render(i18n.T("final.render.scenarios_heading")))
		b.WriteString("\n")
		b.WriteString(keyStyle.Render("  1") + descStyle.Render(i18n.T("final.render.help_simple")))
		b.WriteString(keyStyle.Render("2") + descStyle.Render(i18n.T("final.render.help_tool_call")))
		b.WriteString(keyStyle.Render("3") + descStyle.Render(i18n.T("final.render.help_long_output")))
		b.WriteString(keyStyle.Render("4") + descStyle.Render(i18n.T("final.render.help_multiple_tools")))
		b.WriteString("\n")
		b.WriteString(keyStyle.Render("  5") + descStyle.Render(i18n.T("final.render.help_tool_error")))
		b.WriteString(keyStyle.Render("6") + descStyle.Render(i18n.T("final.render.help_hook")))
		b.WriteString(keyStyle.Render("7") + descStyle.Render(i18n.T("final.render.help_streaming")))
		b.WriteString(keyStyle.Render("8") + descStyle.Render(i18n.T("final.render.help_thinking")))
		b.WriteString("\n")
		b.WriteString(keyStyle.Render("  9") + descStyle.Render(i18n.T("final.render.help_interleaved")))
		b.WriteString(keyStyle.Render("d") + descStyle.Render(i18n.T("final.render.help_diff")))
		b.WriteString(keyStyle.Render("0") + descStyle.Render(i18n.T("final.render.help_clear")))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render(i18n.T("final.render.controls_heading")))
		b.WriteString("\n")
		b.WriteString(keyStyle.Render("  t") + descStyle.Render(i18n.T("final.render.help_toggle_thinking")))
		b.WriteString(keyStyle.Render("o") + descStyle.Render(i18n.T("final.render.help_toggle_output")))
		b.WriteString(keyStyle.Render("h") + descStyle.Render(i18n.T("final.render.help_toggle_help")))
		b.WriteString(keyStyle.Render("↑↓") + descStyle.Render(i18n.T("final.render.help_scroll")))
		b.WriteString(keyStyle.Render("q") + descStyle.Render(i18n.T("final.render.help_quit")))
	} else {
		helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
		b.WriteString(helpStyle.Render(i18n.T("final.render.press_help")))
	}

	v.SetContent(b.String())
	v.AltScreen = true
	return v
}

// updateViewport re-renders messages into the viewport
func (d *DebugRenderApp) updateViewport() {
	content := d.renderMessages()
	d.msgViewport.SetContent(content)
	d.msgViewport.GotoBottom()
}

// renderMessages renders all messages using the same code as the main app
func (d *DebugRenderApp) renderMessages() string {
	var msgLines []string
	th := DefaultTheme

	for _, msg := range d.messages {
		if msg.Role == "user" {
			// User messages: right-aligned with purple border
			borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#CBA6F7"))
			contentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))

			lines := strings.Split(msg.Content, "\n")
			for idx, line := range lines {
				if idx == 0 {
					msgLines = append(msgLines, borderStyle.Render("│")+" "+contentStyle.Render(line))
				} else {
					msgLines = append(msgLines, "  "+contentStyle.Render(line))
				}
			}
			msgLines = append(msgLines, "")

		} else if msg.Role == "assistant" {
			// Assistant messages: left-aligned with green border
			borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1"))

			if len(msg.OrderedBlocks) > 0 {
				// Sort blocks by sequence number (chronological order)
				blocks := make([]MessageBlock, len(msg.OrderedBlocks))
				copy(blocks, msg.OrderedBlocks)
				sort.Slice(blocks, func(i, j int) bool {
					// Sort by sequence - this is the actual order events occurred
					return blocks[i].Sequence < blocks[j].Sequence
				})

				firstContentBlock := true
				for _, block := range blocks {
					switch block.Type {
					case "thinking":
						if d.showThinking {
							thinkingStyle := lipgloss.NewStyle().
								Foreground(lipgloss.Color(th.TextMuted)).
								Italic(true)
							msgLines = append(msgLines, "")
							msgLines = append(msgLines, "    "+thinkingStyle.Render("◆ "+i18n.T("chatui.side.thinking")))
							thinkingWrapped := wrapText(block.Content, d.msgViewport.Width-10)
							for idx, line := range thinkingWrapped {
								if idx == 0 {
									msgLines = append(msgLines, "      "+thinkingStyle.Render("⎿ "+line))
								} else {
									msgLines = append(msgLines, "        "+thinkingStyle.Render(line))
								}
							}
							msgLines = append(msgLines, "")
						}

					case "content":
						renderedLines := renderMarkdownWithWrappingVerbose(block.Content, d.msgViewport.Width-6, th, d.showFullOutput)
						for idx, line := range renderedLines {
							if idx == 0 && firstContentBlock {
								msgLines = append(msgLines, "    "+borderStyle.Render("▌")+" "+line)
								firstContentBlock = false
							} else {
								msgLines = append(msgLines, "      "+line)
							}
						}

					case "tool_call":
						if block.ToolCall != nil {
							tc := block.ToolCall
							level := CollapseLevelCollapsed
							if d.showFullOutput {
								level = CollapseLevelFull
							}
							state := &ToolCallState{
								CallID:        tc.ID,
								ToolName:      tc.Name,
								ToolAPIName:   tc.Name,
								CollapseLevel: level,
							}
							for _, candidate := range msg.OrderedBlocks {
								if candidate.Type == "tool_result" &&
									candidate.ToolResult != nil &&
									candidate.ToolResult.CallID == tc.ID {
									state.HasError = toolResultFailed(candidate.ToolResult)
									break
								}
							}
							toolLine := NewDefaultCollapseWidget().RenderToolHeader(
								state, "•", tc.Parameters, false, d.showFullOutput,
							)

							msgLines = append(msgLines, "")
							msgLines = append(msgLines, "        "+toolLine)
						}

					case "tool_result":
						if block.ToolResult != nil {
							tr := block.ToolResult
							// Find tool name and parameters
							var toolName string
							var toolParams map[string]any
							for _, prevBlock := range msg.OrderedBlocks {
								if prevBlock.Type == "tool_call" && prevBlock.ToolCall != nil {
									if prevBlock.ToolCall.ID == tr.CallID {
										toolName = prevBlock.ToolCall.Name
										toolParams = prevBlock.ToolCall.Parameters
										break
									}
								}
							}

							displayMode := d.renderSettings.GetDisplayModeEnum(toolName)
							maxLines := d.renderSettings.GetMaxLines(toolName)

							resultStyle := lipgloss.NewStyle().
								Foreground(lipgloss.Color(d.renderSettings.GetToolColor(toolName)))
							connectorStyle := lipgloss.NewStyle().
								Foreground(lipgloss.Color(d.renderSettings.Colors.Connector))
							errorStyle := lipgloss.NewStyle().
								Foreground(lipgloss.Color(d.renderSettings.Colors.ToolError))

							if displayMode == DisplayHidden {
								break
							}

							if isBashToolName(toolName) {
								ctx := &toolrender.RenderContext{
									ToolName: toolName,
									Output:   tr.GetOutput(),
									Error:    tr.Error,
									Params:   toolParams,
									Metadata: tr.Metadata,
									CallID:   tr.CallID,
									Width:    d.msgViewport.Width - 12,
									ShowFull: d.showFullOutput,
									BgColor:  DefaultTheme.BG,
								}
								for _, line := range bashrender.New().Render(ctx, nil) {
									msgLines = append(msgLines, "        "+line)
								}
								break
							}

							// Check if this is an edit tool - render diff view
							if IsEditTool(toolName) && tr.Error == "" && toolParams != nil {
								filePath, _ := toolParams["file_path"].(string)
								oldStr, hasOld := toolParams["old_string"].(string)
								newStr, hasNew := toolParams["new_string"].(string)
								content, hasContent := toolParams["content"].(string)

								var diffRendered bool
								if hasOld && hasNew && filePath != "" {
									// Create a temporary collapse widget for rendering
									cw := NewDefaultCollapseWidget()
									diffLines, _ := cw.RenderDiffOutput(nil, filePath, oldStr, newStr, d.msgViewport.Width)
									msgLines = append(msgLines, diffLines...)
									diffRendered = true
								} else if hasContent && filePath != "" && !hasOld {
									cw := NewDefaultCollapseWidget()
									diffLines, _ := cw.RenderDiffOutput(nil, filePath, "", content, d.msgViewport.Width)
									msgLines = append(msgLines, diffLines...)
									diffRendered = true
								}

								if diffRendered {
									break
								}
							}

							if tr.Error != "" {
								errWrapped := wrapTextWithIndent(tr.Error, d.msgViewport.Width-15, "              ")
								if displayMode == DisplayMinimal {
									if len(errWrapped) > 0 {
										msgLines = append(msgLines, "            "+connectorStyle.Render("⎿")+" "+errorStyle.Render(i18n.T("final.render.error_prefix", errWrapped[0])))
									}
								} else if !d.showFullOutput && displayMode == DisplayCompact && len(errWrapped) > maxLines {
									for idx := range maxLines {
										if idx == 0 {
											msgLines = append(msgLines, "            "+connectorStyle.Render("⎿")+" "+errorStyle.Render(i18n.T("final.render.error_prefix", errWrapped[idx])))
										} else {
											msgLines = append(msgLines, errorStyle.Render(errWrapped[idx]))
										}
									}
									truncatedStyle := lipgloss.NewStyle().
										Foreground(lipgloss.Color(d.renderSettings.Colors.Connector)).
										Italic(true)
									msgLines = append(msgLines, "              "+truncatedStyle.Render(i18n.T("final.render.lines_expand", len(errWrapped)-maxLines)))
								} else {
									for idx, line := range errWrapped {
										if idx == 0 {
											msgLines = append(msgLines, "            "+connectorStyle.Render("⎿")+" "+errorStyle.Render(i18n.T("final.render.error_prefix", line)))
										} else {
											msgLines = append(msgLines, errorStyle.Render(line))
										}
									}
								}
							} else {
								outputWrapped := wrapTextWithIndent(tr.Output, d.msgViewport.Width-15, "              ")
								if displayMode == DisplayMinimal {
									if len(outputWrapped) > 0 {
										msgLines = append(msgLines, "            "+connectorStyle.Render("⎿")+" "+resultStyle.Render(outputWrapped[0]))
									}
								} else if !d.showFullOutput && displayMode == DisplayCompact && len(outputWrapped) > maxLines {
									for idx := range maxLines {
										if idx == 0 {
											msgLines = append(msgLines, "            "+connectorStyle.Render("⎿")+" "+resultStyle.Render(outputWrapped[idx]))
										} else {
											msgLines = append(msgLines, resultStyle.Render(outputWrapped[idx]))
										}
									}
									truncatedStyle := lipgloss.NewStyle().
										Foreground(lipgloss.Color(d.renderSettings.Colors.Connector)).
										Italic(true)
									msgLines = append(msgLines, "              "+truncatedStyle.Render(i18n.T("final.render.lines_expand", len(outputWrapped)-maxLines)))
								} else {
									for idx, line := range outputWrapped {
										if idx == 0 {
											msgLines = append(msgLines, "            "+connectorStyle.Render("⎿")+" "+resultStyle.Render(line))
										} else {
											msgLines = append(msgLines, resultStyle.Render(line))
										}
									}
								}
							}
						}

					case "hook_execution":
						if block.HookExecution != nil {
							he := block.HookExecution
							hookStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
							successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
							errorHookStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AA4444"))

							icon := "✓"
							if !he.Success || he.Blocked {
								icon = "✗"
							}

							phaseStr := i18n.T("final.render.pre")
							if he.Phase == "after" {
								phaseStr = i18n.T("final.render.post")
							}

							hookLine := i18n.T("final.render.hook_line", icon, phaseStr, he.HookName)

							if he.Blocked {
								msgLines = append(msgLines, "        "+errorHookStyle.Render(hookLine+i18n.T("final.render.blocked_suffix")))
								if he.Error != "" {
									msgLines = append(msgLines, "          "+errorHookStyle.Render("⎿ "+he.Error))
								}
							} else if !he.Success {
								msgLines = append(msgLines, "        "+errorHookStyle.Render(hookLine))
								if he.Error != "" {
									msgLines = append(msgLines, "          "+errorHookStyle.Render("⎿ "+he.Error))
								}
							} else {
								msgLines = append(msgLines, "        "+hookStyle.Render(hookLine))
								if he.Output != "" {
									msgLines = append(msgLines, "          "+successStyle.Render("⎿ "+he.Output))
								}
							}
						}
					}
				}
			} else {
				// Simple content without blocks
				lines := strings.Split(msg.Content, "\n")
				for idx, line := range lines {
					if idx == 0 {
						msgLines = append(msgLines, "    "+borderStyle.Render("▌")+" "+line)
					} else {
						msgLines = append(msgLines, "      "+line)
					}
				}
			}
			msgLines = append(msgLines, "")
		}
	}

	return strings.Join(msgLines, "\n")
}

// ============================================================================
// TEST SCENARIOS
// ============================================================================

func (d *DebugRenderApp) injectScenario1() {
	// Simple user/assistant exchange
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Hello! Can you help me understand how Go generics work?",
		Timestamp: time.Now(),
	})

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "Of course! Go generics (introduced in Go 1.18) allow you to write functions and types that work with different types while maintaining type safety.\n\nHere's a simple example:\n\n```go\nfunc Map[T, U any](slice []T, f func(T) U) []U {\n    result := make([]U, len(slice))\n    for i, v := range slice {\n        result[i] = f(v)\n    }\n    return result\n}\n```\n\nThis `Map` function works with any types T and U!",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Of course! Go generics (introduced in Go 1.18) allow you to write functions and types that work with different types while maintaining type safety.\n\nHere's a simple example:\n\n```go\nfunc Map[T, U any](slice []T, f func(T) U) []U {\n    result := make([]U, len(slice))\n    for i, v := range slice {\n        result[i] = f(v)\n    }\n    return result\n}\n```\n\nThis `Map` function works with any types T and U!"},
		},
	})

	d.updateViewport()
}

func (d *DebugRenderApp) injectScenario2() {
	// Tool call (bash command)
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "What files are in the current directory?",
		Timestamp: time.Now(),
	})

	toolCall := ToolCallDisplay{
		ID:         "call_001",
		Name:       "bash",
		Parameters: map[string]any{"command": "ls -la"},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "Let me check that for you.",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Let me check that for you."},
			{Type: "tool_call", ToolCall: &toolCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID:   "call_001",
				ToolName: "bash",
				Metadata: map[string]any{"exit_code": float64(0)},
				Output: `total 48
drwxr-xr-x   8 user  staff   256 Dec 22 10:30 .
drwxr-xr-x  12 user  staff   384 Dec 22 09:15 ..
-rw-r--r--   1 user  staff   234 Dec 22 10:30 go.mod
-rw-r--r--   1 user  staff  1234 Dec 22 10:30 go.sum
drwxr-xr-x   5 user  staff   160 Dec 22 10:28 cmd
drwxr-xr-x   8 user  staff   256 Dec 22 10:29 internal
drwxr-xr-x   4 user  staff   128 Dec 22 10:27 sdk`,
			}},
			{Type: "content", Content: "The directory contains a Go project with the standard structure."},
		},
	})

	d.updateViewport()
}

func (d *DebugRenderApp) injectScenario3() {
	// Tool with long output (truncation test)
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Show me a long output to test truncation",
		Timestamp: time.Now(),
	})

	var longOutput strings.Builder
	for i := 1; i <= 50; i++ {
		longOutput.WriteString(fmt.Sprintf("Line %02d: This is line number %d of the output, demonstrating truncation behavior\n", i, i))
	}

	toolCall := ToolCallDisplay{
		ID:         "call_002",
		Name:       "bash",
		Parameters: map[string]any{"command": "seq 1 50 | while read n; do echo \"Line $n: This is...\"; done"},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "tool_call", ToolCall: &toolCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID:   "call_002",
				ToolName: "bash",
				Output:   longOutput.String(),
				Metadata: map[string]any{"exit_code": float64(0)},
			}},
			{Type: "content", Content: "As you can see, this output has 50 lines. In compact mode, it gets truncated."},
		},
	})

	d.updateViewport()
}

func (d *DebugRenderApp) injectScenario4() {
	// Multiple tool calls in sequence
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Create a file and then read it back",
		Timestamp: time.Now(),
	})

	writeCall := ToolCallDisplay{
		ID:         "call_003",
		Name:       "file_write",
		Parameters: map[string]any{"file_path": "/tmp/test.txt", "content": "Hello, World!"},
	}

	readCall := ToolCallDisplay{
		ID:         "call_004",
		Name:       "file_read",
		Parameters: map[string]any{"file_path": "/tmp/test.txt"},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "I'll create the file first:"},
			{Type: "tool_call", ToolCall: &writeCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_003",
				Output: "Successfully wrote 13 bytes to /tmp/test.txt",
			}},
			{Type: "content", Content: "Now let me read it back:"},
			{Type: "tool_call", ToolCall: &readCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_004",
				Output: "Hello, World!",
			}},
			{Type: "content", Content: "The file was created and contains the expected content."},
		},
	})

	d.updateViewport()
}

func (d *DebugRenderApp) injectScenario5() {
	// Tool with error result
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Try to read a file that doesn't exist",
		Timestamp: time.Now(),
	})

	toolCall := ToolCallDisplay{
		ID:         "call_005",
		Name:       "file_read",
		Parameters: map[string]any{"file_path": "/nonexistent/path/file.txt"},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Let me try to read that file:"},
			{Type: "tool_call", ToolCall: &toolCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_005",
				Output: "",
				Error:  "ENOENT: no such file or directory, open '/nonexistent/path/file.txt'\nThe file does not exist. Please check the path and try again.\nStack trace:\n  at Object.openSync (fs.js:497:3)\n  at Object.readFileSync (fs.js:393:35)",
			}},
			{Type: "content", Content: "As expected, the file doesn't exist. Let me suggest some alternatives..."},
		},
	})

	d.updateViewport()
}

func (d *DebugRenderApp) injectScenario6() {
	// Hook execution (pre/post hooks)
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Run a command with security hooks",
		Timestamp: time.Now(),
	})

	toolCall := ToolCallDisplay{
		ID:         "call_006",
		Name:       "bash",
		Parameters: map[string]any{"command": "rm -rf /tmp/cache"},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Running the cleanup command with security validation:"},
			{Type: "tool_call", ToolCall: &toolCall},
			{Type: "hook_execution", HookExecution: &HookExecutionDisplay{
				HookName: "security-validator",
				ToolName: "bash",
				Phase:    "before",
				Success:  true,
				Output:   "Command validated: safe to execute",
			}},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_006",
				Output: "Removed 47 files from /tmp/cache",
			}},
			{Type: "hook_execution", HookExecution: &HookExecutionDisplay{
				HookName: "audit-logger",
				ToolName: "bash",
				Phase:    "after",
				Success:  true,
				Output:   "Logged to /var/log/swarmos/audit.log",
			}},
			{Type: "content", Content: "Cache cleared successfully, and the action was logged."},
		},
	})

	// Also show a blocked hook
	blockedCall := ToolCallDisplay{
		ID:         "call_007",
		Name:       "bash",
		Parameters: map[string]any{"command": "rm -rf /"},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Now trying a dangerous command:"},
			{Type: "tool_call", ToolCall: &blockedCall},
			{Type: "hook_execution", HookExecution: &HookExecutionDisplay{
				HookName: "security-validator",
				ToolName: "bash",
				Phase:    "before",
				Success:  false,
				Blocked:  true,
				Error:    "BLOCKED: Destructive command 'rm -rf /' is not allowed",
			}},
			{Type: "content", Content: "The command was blocked by security hooks. This protects your system from accidental destruction."},
		},
	})

	d.updateViewport()
}

func (d *DebugRenderApp) injectScenario7() {
	// Streaming simulation - count to 100, show last 10 lines
	d.streamLines = nil     // Reset accumulated lines
	d.showFullOutput = true // Bypass render truncation during streaming

	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Run a long process that outputs 100 lines",
		Timestamp: time.Now(),
	})

	toolCall := ToolCallDisplay{
		ID:         "call_008",
		Name:       "bash",
		Parameters: map[string]any{"command": "for i in $(seq 1 100); do echo \"Processing $i\"; sleep 0.25; done"},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Running the process (streaming output, showing last 10 lines)..."},
			{Type: "tool_call", ToolCall: &toolCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_008",
				Output: "Starting...", // Will be updated incrementally
			}},
		},
	})

	d.updateViewport()
}

func (d *DebugRenderApp) injectScenario8() {
	// Extended thinking block
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Solve this complex algorithm problem",
		Timestamp: time.Now(),
	})

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		Thinking:  "Let me think through this step by step...\n\nFirst, I need to consider the time complexity requirements. The problem asks for O(n log n) which suggests we might need sorting or a divide-and-conquer approach.\n\nLooking at the constraints:\n- n can be up to 10^5\n- Values can be negative\n- We need to find the maximum subarray sum\n\nThis is actually Kadane's algorithm! But wait, there's a twist - we also need to track the indices. Let me reconsider...\n\nActually, I think we can modify Kadane's algorithm to track start and end indices while maintaining the O(n) complexity.",
		OrderedBlocks: []MessageBlock{
			{Type: "thinking", Content: "Let me think through this step by step...\n\nFirst, I need to consider the time complexity requirements. The problem asks for O(n log n) which suggests we might need sorting or a divide-and-conquer approach.\n\nLooking at the constraints:\n- n can be up to 10^5\n- Values can be negative\n- We need to find the maximum subarray sum\n\nThis is actually Kadane's algorithm! But wait, there's a twist - we also need to track the indices. Let me reconsider...\n\nActually, I think we can modify Kadane's algorithm to track start and end indices while maintaining the O(n) complexity."},
			{Type: "content", Content: "I've analyzed this problem carefully. Here's my solution using a modified Kadane's algorithm:\n\n```go\nfunc maxSubArray(nums []int) (maxSum, start, end int) {\n    maxSum = nums[0]\n    currentSum := nums[0]\n    tempStart := 0\n    \n    for i := 1; i < len(nums); i++ {\n        if currentSum < 0 {\n            currentSum = nums[i]\n            tempStart = i\n        } else {\n            currentSum += nums[i]\n        }\n        \n        if currentSum > maxSum {\n            maxSum = currentSum\n            start = tempStart\n            end = i\n        }\n    }\n    return\n}\n```\n\nThis runs in O(n) time with O(1) extra space!"},
		},
	})

	d.updateViewport()
}

func (d *DebugRenderApp) injectScenario9() {
	// Complex interleaved (text + tools + hooks)
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Analyze the codebase and make changes",
		Timestamp: time.Now(),
	})

	grepCall := ToolCallDisplay{
		ID:         "call_009",
		Name:       "grep",
		Parameters: map[string]any{"pattern": "TODO", "path": "src/"},
	}

	readCall := ToolCallDisplay{
		ID:         "call_010",
		Name:       "file_read",
		Parameters: map[string]any{"file_path": "src/main.go"},
	}

	writeCall := ToolCallDisplay{
		ID:         "call_011",
		Name:       "file_write",
		Parameters: map[string]any{"file_path": "src/main.go", "content": "..."},
	}

	bashCall := ToolCallDisplay{
		ID:         "call_012",
		Name:       "bash",
		Parameters: map[string]any{"command": "go build ./..."},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		Thinking:  "This is a complex task. I should:\n1. First search for TODOs\n2. Read the relevant files\n3. Make changes\n4. Verify the build works",
		OrderedBlocks: []MessageBlock{
			{Type: "thinking", Content: "This is a complex task. I should:\n1. First search for TODOs\n2. Read the relevant files\n3. Make changes\n4. Verify the build works"},
			{Type: "content", Content: "Let me start by finding all TODO comments in the codebase:"},
			{Type: "tool_call", ToolCall: &grepCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_009",
				Output: "src/main.go:15: // TODO: Add error handling\nsrc/main.go:42: // TODO: Implement caching\nsrc/utils.go:8: // TODO: Document this function",
			}},
			{Type: "content", Content: "I found 3 TODOs. Let me read the main file to understand the context:"},
			{Type: "tool_call", ToolCall: &readCall},
			{Type: "hook_execution", HookExecution: &HookExecutionDisplay{
				HookName: "file-access-log",
				ToolName: "file_read",
				Phase:    "before",
				Success:  true,
				Output:   "Access granted",
			}},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_010",
				Output: "package main\n\nimport (\n    \"fmt\"\n    \"os\"\n)\n\nfunc main() {\n    // TODO: Add error handling\n    data := loadData()\n    fmt.Println(data)\n}",
			}},
			{Type: "content", Content: "Now I'll add the error handling:"},
			{Type: "tool_call", ToolCall: &writeCall},
			{Type: "hook_execution", HookExecution: &HookExecutionDisplay{
				HookName: "backup-hook",
				ToolName: "file_write",
				Phase:    "before",
				Success:  true,
				Output:   "Backup created: src/main.go.bak",
			}},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_011",
				Output: "Successfully wrote 245 bytes",
			}},
			{Type: "hook_execution", HookExecution: &HookExecutionDisplay{
				HookName: "git-stage",
				ToolName: "file_write",
				Phase:    "after",
				Success:  true,
				Output:   "Staged: src/main.go",
			}},
			{Type: "content", Content: "Let me verify the build still works:"},
			{Type: "tool_call", ToolCall: &bashCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_012",
				Output: "Build successful!",
			}},
			{Type: "content", Content: "All done! I've:\n1. Found and addressed the TODO for error handling\n2. Created a backup before editing\n3. Verified the build passes\n\nThe changes have been staged for commit."},
		},
	})

	d.updateViewport()
}

// injectScenario10 tests the diff view for edit operations
func (d *DebugRenderApp) injectScenario10() {
	// File edit with diff view
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Fix the typo in the greeting function",
		Timestamp: time.Now(),
	})

	editCall := ToolCallDisplay{
		ID:   "call_020",
		Name: "Edit",
		Parameters: map[string]any{
			"file_path": "src/greetings.go",
			"old_string": `func Greet(name string) string {
	if name == "" {
		return "Hello, World!"
	}
	return "Hello, " + name + "!"
}`,
			"new_string": `func Greet(name string) string {
	if name == "" {
		return "Hello, World!"
	}
	// Add proper greeting with formatted output
	greeting := fmt.Sprintf("Hello, %s!", name)
	return greeting
}`,
		},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "I'll fix the greeting function:"},
			{Type: "tool_call", ToolCall: &editCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_020",
				Output: "Successfully edited src/greetings.go",
			}},
			{Type: "content", Content: "Fixed! The greeting function now uses fmt.Sprintf for cleaner output."},
		},
	})

	// Also add a Write tool example (new file creation)
	writeCall := ToolCallDisplay{
		ID:   "call_021",
		Name: "Write",
		Parameters: map[string]any{
			"file_path": "src/utils/helpers.go",
			"content": `package utils

import "strings"

// TrimAndLower trims whitespace and converts to lowercase
func TrimAndLower(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Contains checks if a string contains a substring
func Contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
`,
		},
	}

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Let me also create a helpers file:"},
			{Type: "tool_call", ToolCall: &writeCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_021",
				Output: "Successfully created src/utils/helpers.go",
			}},
			{Type: "content", Content: "Created the helpers file with utility functions."},
		},
	})

	d.updateViewport()
}

// injectScenario11 tests the Read tool with syntax highlighting
func (d *DebugRenderApp) injectScenario11() {
	d.messages = append(d.messages, Message{
		Role:      "user",
		Content:   "Show me the main.go file",
		Timestamp: time.Now(),
	})

	readCall := ToolCallDisplay{
		ID:   "call_030",
		Name: "Read",
		Parameters: map[string]any{
			"file_path": "cmd/server/main.go",
		},
	}

	// Sample Go file content to demonstrate syntax highlighting
	goFileContent := `package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Server configuration constants
const (
	DefaultPort    = 8080
	DefaultTimeout = 30 * time.Second
)

// Server represents our HTTP server
type Server struct {
	port    int
	handler http.Handler
	timeout time.Duration
}

// NewServer creates a new server instance
func NewServer(port int) *Server {
	return &Server{
		port:    port,
		handler: nil,
		timeout: DefaultTimeout,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	// Build the address string
	addr := fmt.Sprintf(":%d", s.port)

	// Create the HTTP server
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.handler,
		ReadTimeout:  s.timeout,
		WriteTimeout: s.timeout,
	}

	fmt.Printf("Server starting on port %d\n", s.port)
	return srv.ListenAndServe()
}

func main() {
	// Get port from environment or use default
	port := DefaultPort
	if p := os.Getenv("PORT"); p != "" {
		// Parse port from string
		fmt.Sscanf(p, "%d", &port)
	}

	server := NewServer(port)
	if err := server.Start(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}`

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "Here's the main.go file with the server implementation:"},
			{Type: "tool_call", ToolCall: &readCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_030",
				Output: goFileContent,
			}},
			{Type: "content", Content: "This file contains a simple HTTP server implementation with configurable port and timeout."},
		},
	})

	// Also show Python file example
	pythonReadCall := ToolCallDisplay{
		ID:   "call_031",
		Name: "Read",
		Parameters: map[string]any{
			"file_path": "scripts/deploy.py",
		},
	}

	pythonContent := `#!/usr/bin/env python3
"""Deployment script for the application."""

import os
import sys
import subprocess
from typing import List, Optional

# Configuration
DEFAULT_ENV = "staging"
VALID_ENVS = ["dev", "staging", "prod"]

class DeploymentError(Exception):
    """Custom exception for deployment failures."""
    pass

def run_command(cmd: List[str]) -> str:
    """Run a shell command and return output."""
    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            check=True
        )
        return result.stdout
    except subprocess.CalledProcessError as e:
        raise DeploymentError(f"Command failed: {e.stderr}")

def deploy(env: str = DEFAULT_ENV) -> None:
    """Deploy to the specified environment."""
    if env not in VALID_ENVS:
        raise ValueError(f"Invalid environment: {env}")

    print(f"Deploying to {env}...")

    # Build the application
    run_command(["make", "build"])

    # Run tests
    if env == "prod":
        run_command(["make", "test"])

    # Deploy
    run_command(["kubectl", "apply", "-f", f"k8s/{env}/"])
    print("Deployment complete!")

if __name__ == "__main__":
    env = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_ENV
    deploy(env)`

	d.messages = append(d.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "And here's the Python deployment script:"},
			{Type: "tool_call", ToolCall: &pythonReadCall},
			{Type: "tool_result", ToolResult: &ToolResultDisplay{
				CallID: "call_031",
				Output: pythonContent,
			}},
			{Type: "content", Content: "The deployment script supports different environments and includes proper error handling."},
		},
	})

	d.updateViewport()
}
