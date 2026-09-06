package chat

import (
	"slices"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

var permissionLevelOrder = []tools.PermissionLevel{
	tools.LevelAlwaysAsk,
	tools.LevelBalanced,
	tools.LevelPermissive,
	tools.LevelYOLO,
}

func normalizePermissionLevel(level tools.PermissionLevel) tools.PermissionLevel {
	if slices.Contains(permissionLevelOrder, level) {
		return level
	}
	return tools.LevelBalanced
}

func nextPermissionLevel(level tools.PermissionLevel) tools.PermissionLevel {
	normalized := normalizePermissionLevel(level)
	for i, candidate := range permissionLevelOrder {
		if candidate == normalized {
			return permissionLevelOrder[(i+1)%len(permissionLevelOrder)]
		}
	}
	return tools.LevelBalanced
}

func permissionLevelLabel(level tools.PermissionLevel) string {
	switch level {
	case tools.LevelAlwaysAsk:
		return i18n.T("classic_chat_2.permission.always_ask")
	case tools.LevelPermissive:
		return i18n.T("classic_chat_2.permission.permissive")
	case tools.LevelYOLO:
		return i18n.T("classic_chat_2.permission.yolo")
	default:
		return i18n.T("classic_chat_2.permission.balanced")
	}
}

func permissionLevelColor(level tools.PermissionLevel, th Theme) string {
	switch level {
	case tools.LevelAlwaysAsk:
		return th.Warning
	case tools.LevelPermissive:
		return th.Success
	case tools.LevelYOLO:
		return th.Error
	case tools.LevelBalanced:
		return th.Primary
	default:
		return th.TextMuted
	}
}

func (a *App) currentPermissionLevel() tools.PermissionLevel {
	if a.permissionConfig == nil {
		return tools.LevelBalanced
	}
	return normalizePermissionLevel(a.permissionConfig.Config().Level)
}

func (a *App) cyclePermissionLevel() {
	if a.permissionConfig == nil && a.sdk == nil {
		a.addNotification("error", i18n.T("classic_chat_2.permission.config_unavailable"))
		return
	}

	current := a.currentPermissionLevel()
	next := nextPermissionLevel(current)

	// If the user is manually cycling permissions while in AUTO mode, they are
	// intentionally overriding the AUTO-managed YOLO level.  Exit AUTO mode so
	// they don't get YOLO silently re-applied on the next cycle and so the
	// preAutoPermissionLevel is not mistakenly restored to YOLO.
	if a.operatingMode == mode.ModeAuto {
		// Clear the saved level so restore-on-exit doesn't snap back to YOLO.
		a.preAutoPermissionLevel = next
		logDebug("[MODE] Permission cycled manually in AUTO mode — preAutoPermissionLevel updated to %s", next)
	}

	if a.sdk != nil {
		if err := a.sdk.UpdatePermissionLevel(next); err != nil {
			a.addNotification("error", i18n.T("classic_chat_2.permission.update_failed", err))
			return
		}
	} else if a.permissionConfig != nil {
		cfg := a.permissionConfig.Config()
		cfg.Level = next
		a.permissionConfig.SetConfig(cfg)
		if err := a.permissionConfig.Save(); err != nil {
			a.addNotification("error", i18n.T("classic_chat_2.permission.save_failed", err))
			return
		}
	}

	if a.permissionBroker != nil && a.permissionConfig != nil {
		a.permissionBroker.SetConfig(a.permissionConfig.Config())
	}

	if a.chatScreen != nil {
		a.chatScreen.SetPermissionLevel(string(next))
	}

	a.sidePanelCache.valid = false
	a.addNotification("success", i18n.T("classic_chat_2.permission.current", permissionLevelLabel(next)))
}
