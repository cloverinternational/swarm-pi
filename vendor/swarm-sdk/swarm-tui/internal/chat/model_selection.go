package chat

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (app *App) applyModelSelection(provider, model, providerDisplay, modelDisplay string) error {
	if app.sdk != nil && app.sdk.harnessGoverned() {
		app.addNotification("error", i18n.T("residual.init.harness_model_switch_disabled"))
		return fmt.Errorf("model switching is disabled while the session is governed by a harness")
	}
	nextContextWindow := app.modelContextWindow
	if app.sdk != nil {
		if err := app.sdk.SwitchProvider(provider, model); err != nil {
			logDebug("Failed to switch provider: %v", err)
			return err
		}
		nextContextWindow = app.sdk.GetModelContextWindow()
	}

	app.currentProvider = provider
	app.currentModel = model
	app.currentProviderDisplay = providerDisplay
	app.currentModelDisplay = modelDisplay
	app.modelContextWindow = nextContextWindow
	if app.metrics != nil {
		app.metrics.SetCurrentModel(model, modelDisplay)
		logDebug("[METRICS] Switched metrics context to model=%s", model)
	}
	logDebug("[MODEL] SWITCH model=%s contextWindow=%d provider=%s",
		model, app.modelContextWindow, provider)
	return nil
}
