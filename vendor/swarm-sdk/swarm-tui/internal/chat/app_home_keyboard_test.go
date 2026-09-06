package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

func TestHomeShiftTabTogglesGitDetailsSidebar(t *testing.T) {
	shiftTab := tea.KeyPressMsg{
		Code: tea.KeyTab,
		Mod:  tea.ModShift,
	}
	if got := shiftTab.String(); got != "shift+tab" {
		t.Fatalf("test key fixture String() = %q, want shift+tab", got)
	}

	for _, inputFocused := range []bool{true, false} {
		t.Run(map[bool]string{true: "input focused", false: "menu focused"}[inputFocused], func(t *testing.T) {
			app := &App{
				screen:               ScreenHome,
				homeButton:           ButtonNewChat,
				homeInputFocused:     inputFocused,
				homeSidebarCollapsed: true,
				commandPalette:       &CommandPalette{},
			}

			_, cmd := app.handleKey(shiftTab)
			if cmd != nil {
				t.Fatal("Shift+Tab returned an unexpected command")
			}
			if app.homeSidebarCollapsed {
				t.Fatal("Shift+Tab did not show the Git details sidebar")
			}
			if app.homeButton != ButtonNewChat {
				t.Fatalf("Shift+Tab changed the selected home tab to %v", app.homeButton)
			}

			_, cmd = app.handleKey(shiftTab)
			if cmd != nil {
				t.Fatal("second Shift+Tab returned an unexpected command")
			}
			if !app.homeSidebarCollapsed {
				t.Fatal("second Shift+Tab did not hide the Git details sidebar")
			}
		})
	}
}

func TestHomeShiftTabPreservesOtherTabNavigation(t *testing.T) {
	shiftTab := tea.KeyPressMsg{
		Code: tea.KeyTab,
		Mod:  tea.ModShift,
	}
	app := &App{
		screen:               ScreenHome,
		homeButton:           ButtonConversations,
		homeSidebarCollapsed: true,
		commandPalette:       &CommandPalette{},
	}

	_, cmd := app.handleKey(shiftTab)
	if cmd != nil {
		t.Fatal("Shift+Tab returned an unexpected command")
	}
	if !app.homeSidebarCollapsed {
		t.Fatal("Shift+Tab opened Git details outside the Prompt tab")
	}
	if app.homeButton != ButtonNewChat {
		t.Fatalf("Shift+Tab did not navigate to the previous home tab: got %v", app.homeButton)
	}
}

func TestHomeShiftTabReachesInlineSettingsNavigation(t *testing.T) {
	shiftTab := tea.KeyPressMsg{
		Code: tea.KeyTab,
		Mod:  tea.ModShift,
	}
	settingsManager := settings.NewManager(
		"", "", false, false, false, false, false, "", "", nil,
	)
	settingsManager.GetState().Focus = settings.FocusContent
	app := &App{
		screen:               ScreenHome,
		homeButton:           ButtonSettings,
		homeSidebarCollapsed: true,
		commandPalette:       &CommandPalette{},
		settingsManager:      settingsManager,
	}

	_, cmd := app.handleKey(shiftTab)
	if cmd != nil {
		t.Fatal("Shift+Tab returned an unexpected command")
	}
	if !app.homeSidebarCollapsed {
		t.Fatal("Shift+Tab opened Git details from the Settings tab")
	}
	if app.homeButton != ButtonSettings {
		t.Fatalf("Shift+Tab unexpectedly left Settings: got %v", app.homeButton)
	}
	if got := settingsManager.GetState().Focus; got != settings.FocusSidebar {
		t.Fatalf("Shift+Tab did not move Settings focus to its sidebar: got %v", got)
	}
}
