// Package chatui provides a modular, testable chat UI implementation for the Swarm TUI.
//
// Architecture Overview:
//
// The chatui package follows a clean architecture pattern with clear separation
// of concerns across multiple sub-packages:
//
//   - types/      Core domain types (Message, Conversation, State, Events)
//   - theme/      Styling concerns (colors, pre-allocated styles)
//   - renderer/   Rendering logic (state -> strings)
//   - viewport/   Viewport management (scrolling, selection, caching)
//   - layout/     Layout calculations (widths, multi-panel splits)
//   - state/      State management (Bubbletea Model pattern)
//   - components/ Reusable UI components (input, statusbar, scrollbar)
//
// Usage:
//
//	panel := chatui.NewPanel(width, height,
//	    chatui.WithTheme(myTheme),
//	    chatui.WithThinking(true),
//	)
//
//	// Use as Bubbletea Model
//	p := tea.NewProgram(panel)
//
//	// Add messages
//	panel.AppendMessage(msg)
//
//	// Handle streaming
//	panel.StreamChunk(content, block)
//
// Multi-Panel Support:
//
// The layout/ package provides multi-panel layout management for split views:
//
//	manager := chatui.NewManager(width, height)
//	manager.SplitHorizontal(0.5) // 50/50 split
//	manager.FocusPanel(1)        // Focus right panel
//
// Feature Flag:
//
// Enable the new chat UI with the SWARM_USE_CHATUI=1 environment variable.
// This allows gradual migration while maintaining the existing implementation.
package chatui
