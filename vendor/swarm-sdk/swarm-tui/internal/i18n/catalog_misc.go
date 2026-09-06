package i18n

func init() {
	Register("misc", map[string]string{
		"misc.tabbar.prompt":   "PROMPT",
		"misc.tabbar.history":  "HISTORY",
		"misc.tabbar.usage":    "USAGE",
		"misc.tabbar.settings": "SETTINGS",
		"misc.tabbar.global":   "Global",
		"misc.diff.no_changes": "no changes",
		"misc.diff.more_lines": "  ... +%d more lines",
	}, map[string]string{
		"misc.tabbar.prompt":   "CONSULTA",
		"misc.tabbar.history":  "HISTORIAL",
		"misc.tabbar.usage":    "USO",
		"misc.tabbar.settings": "CONFIGURACIÓN",
		"misc.tabbar.global":   "Global",
		"misc.diff.no_changes": "sin cambios",
		"misc.diff.more_lines": "  ... +%d líneas más",
	})
}
