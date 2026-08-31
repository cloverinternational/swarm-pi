package chat

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

func TestSidePanelVaultChipTracksProviderState(t *testing.T) {
	prev := vault.GetDefaultVaultProvider()
	defer vault.SetDefaultVaultProvider(prev)

	app := &App{
		width:              120,
		height:             40,
		showSidePanel:      true,
		modelContextWindow: 128000,
		theme:              DefaultTheme,
	}
	panel := NewSidePanel(SidePanelWidth, app.height)

	vault.SetDefaultVaultProvider(nil)
	locked := panel.Render(app)
	if !strings.Contains(locked, "🔒 vault") {
		t.Fatalf("locked sidebar does not contain vault chip:\n%s", locked)
	}

	storage := vault.NewMemoryStorage()
	v := vault.NewVault(storage, vault.VaultConfig{Enabled: true})
	exec := vault.NewExecutor(v, vault.VaultConfig{Enabled: true}, nil)
	vault.SetDefaultVaultProvider(vault.NewVaultProvider(exec, v, ""))
	unlocked := panel.Render(app)
	if !strings.Contains(unlocked, "🔓 vault") {
		t.Fatalf("unlocked sidebar does not contain vault chip:\n%s", unlocked)
	}
	if locked == unlocked {
		t.Fatal("sidebar cache did not invalidate when vault state changed")
	}
}
