package settings

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func newProviderDeleteTestManager() (*Manager, *ModelSettings) {
	model := &ModelSettings{
		state: "manage_providers",
		providers: []commands.Provider{
			{Name: "ClaudeCode", DisplayName: "Claude Code (OAuth)"},
		},
	}
	profiles := &SectionModelProfiles{
		state: MPStateManageProviders,
		model: model,
	}
	state := NewState()
	state.Focus = FocusContent
	state.SelectedSection = SectionModels
	return &Manager{
		state:         state,
		modelProfiles: profiles,
	}, model
}

func openProviderDeleteConfirmation(t *testing.T, manager *Manager, model *ModelSettings) {
	t.Helper()
	manager.HandleKey("d", nil)
	if model.state != "confirm_delete" {
		t.Fatalf("model state after d = %q, want confirm_delete", model.state)
	}
}

func TestModelProfilesProviderDeleteConfirmationRoutesSelectionKeys(t *testing.T) {
	manager, model := newProviderDeleteTestManager()
	openProviderDeleteConfirmation(t, manager, model)

	manager.HandleKey("right", nil)
	if model.confirmSelected != 1 {
		t.Fatalf("selection after right = %d, want Yes (1)", model.confirmSelected)
	}

	manager.HandleKey("left", nil)
	if model.confirmSelected != 0 {
		t.Fatalf("selection after left = %d, want No (0)", model.confirmSelected)
	}
}

func TestModelProfilesProviderDeleteConfirmationRoutesQuickKeys(t *testing.T) {
	t.Run("n cancels", func(t *testing.T) {
		manager, model := newProviderDeleteTestManager()
		openProviderDeleteConfirmation(t, manager, model)

		manager.HandleKey("n", nil)
		if model.state != "manage_providers" {
			t.Fatalf("model state after n = %q, want manage_providers", model.state)
		}
		if len(model.providers) != 1 {
			t.Fatalf("provider count after n = %d, want 1", len(model.providers))
		}
	})

	t.Run("y deletes", func(t *testing.T) {
		manager, model := newProviderDeleteTestManager()
		openProviderDeleteConfirmation(t, manager, model)

		manager.HandleKey("y", nil)
		if model.state != "manage_providers" {
			t.Fatalf("model state after y = %q, want manage_providers", model.state)
		}
		if len(model.providers) != 0 {
			t.Fatalf("provider count after y = %d, want 0", len(model.providers))
		}
	})
}

func TestModelProfilesProviderDeleteConfirmationKeepsHInDialog(t *testing.T) {
	manager, model := newProviderDeleteTestManager()
	openProviderDeleteConfirmation(t, manager, model)
	model.confirmSelected = 1

	manager.HandleKey("h", nil)

	if model.confirmSelected != 0 {
		t.Fatalf("selection after h = %d, want No (0)", model.confirmSelected)
	}
	if manager.state.Focus != FocusContent {
		t.Fatalf("focus after h = %v, want content focus", manager.state.Focus)
	}
}
