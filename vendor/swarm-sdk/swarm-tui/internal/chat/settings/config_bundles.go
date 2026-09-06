package settings

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ConfigBundlesSettings manages agent configuration bundles for easy sharing and switching
type ConfigBundlesSettings struct {
	bundleManager *BundleManager
	profileMgr    *ProfileManager
	agentSettings *AgentsSettings

	// State
	state         string // "list", "export", "import", "confirm_apply", "confirm_delete"
	bundles       []BundleInfo
	selectedIndex int
	errorMsg      string
	successMsg    string

	// Form inputs
	exportName        textinput.Model
	exportDescription textinput.Model
	importFilename    textinput.Model
	focusedInput      int // 0=name, 1=description
}

// NewConfigBundlesSettings creates a new config bundles settings handler
func NewConfigBundlesSettings(profileMgr *ProfileManager, agentSettings *AgentsSettings) *ConfigBundlesSettings {
	nameInput := textinput.New()
	nameInput.Placeholder = i18n.T("settings.config_bundles.placeholder.name")
	nameInput.CharLimit = 50
	nameInput.SetWidth(40)

	descInput := textinput.New()
	descInput.Placeholder = i18n.T("settings.config_bundles.placeholder.description")
	descInput.CharLimit = 200
	descInput.SetWidth(40)

	importInput := textinput.New()
	importInput.Placeholder = i18n.T("settings.config_bundles.placeholder.import")
	importInput.CharLimit = 200
	importInput.SetWidth(40)

	s := &ConfigBundlesSettings{
		bundleManager:     NewBundleManager(),
		profileMgr:        profileMgr,
		agentSettings:     agentSettings,
		state:             "list",
		exportName:        nameInput,
		exportDescription: descInput,
		importFilename:    importInput,
	}

	s.refreshBundles()
	return s
}

// refreshBundles reloads the list of available bundles
func (s *ConfigBundlesSettings) refreshBundles() {
	bundles, err := s.bundleManager.ListBundles()
	if err != nil {
		s.errorMsg = i18n.T("settings.config_bundles.error.list", err)
		s.bundles = []BundleInfo{}
	} else {
		s.bundles = bundles
		s.errorMsg = ""
	}

	// Reset selection if out of bounds
	if s.selectedIndex >= len(s.bundles) {
		s.selectedIndex = 0
	}
}

// IsInNestedState returns true if config bundles is in a sub-screen (export, import, confirm).
func (s *ConfigBundlesSettings) IsInNestedState() bool {
	return s.state != "list"
}

// IsEditingText returns true if config bundles is in a text-editing state.
func (s *ConfigBundlesSettings) IsEditingText() bool {
	return s.state == "export" || s.state == "import"
}

// Update handles key presses and updates for config bundles settings
func (s *ConfigBundlesSettings) Update(msg tea.Msg) tea.Cmd {
	switch s.state {
	case "list":
		return s.updateList(msg)
	case "export":
		return s.updateExport(msg)
	case "import":
		return s.updateImport(msg)
	case "confirm_apply":
		return s.updateConfirmApply(msg)
	case "confirm_delete":
		return s.updateConfirmDelete(msg)
	}
	return nil
}

// updateList handles list view updates
func (s *ConfigBundlesSettings) updateList(msg tea.Msg) tea.Cmd {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "up", "k":
			if s.selectedIndex > 0 {
				s.selectedIndex--
			}
		case "down", "j":
			if s.selectedIndex < len(s.bundles)-1 {
				s.selectedIndex++
			}
		case "e":
			// Export current config
			s.state = "export"
			s.exportName.Focus()
			s.exportDescription.Blur()
			s.focusedInput = 0
			s.errorMsg = ""
			s.successMsg = ""
			return textinput.Blink
		case "i":
			// Import bundle
			s.state = "import"
			s.importFilename.Focus()
			s.errorMsg = ""
			s.successMsg = ""
			return textinput.Blink
		case "enter":
			// Apply selected bundle
			if len(s.bundles) > 0 {
				s.state = "confirm_apply"
				s.errorMsg = ""
				s.successMsg = ""
			}
		case "d", "delete":
			// Delete selected bundle
			if len(s.bundles) > 0 {
				s.state = "confirm_delete"
				s.errorMsg = ""
				s.successMsg = ""
			}
		case "r":
			// Refresh list
			s.refreshBundles()
			s.successMsg = i18n.T("settings.config_bundles.success.refreshed")
		}
	}
	return nil
}

// updateExport handles export form updates
func (s *ConfigBundlesSettings) updateExport(msg tea.Msg) tea.Cmd {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "esc":
			s.state = "list"
			s.exportName.SetValue("")
			s.exportDescription.SetValue("")
			return nil
		case "tab":
			s.focusedInput = (s.focusedInput + 1) % 2
			if s.focusedInput == 0 {
				s.exportName.Focus()
				s.exportDescription.Blur()
			} else {
				s.exportName.Blur()
				s.exportDescription.Focus()
			}
			return textinput.Blink
		case "enter":
			// Perform export
			return s.performExport()
		}
	}

	// Update focused input
	var cmd tea.Cmd
	if s.focusedInput == 0 {
		s.exportName, cmd = s.exportName.Update(msg)
	} else {
		s.exportDescription, cmd = s.exportDescription.Update(msg)
	}
	return cmd
}

// performExport exports current config as a bundle
func (s *ConfigBundlesSettings) performExport() tea.Cmd {
	name := strings.TrimSpace(s.exportName.Value())
	description := strings.TrimSpace(s.exportDescription.Value())

	if name == "" {
		s.errorMsg = i18n.T("settings.config_bundles.error.name_required")
		return nil
	}

	// Create bundle
	var profiles *ProfilesConfig
	if s.profileMgr != nil {
		pCopy := *s.profileMgr.GetConfig()
		profiles = &pCopy
	}

	var customAgents *CustomAgentConfig
	if s.agentSettings != nil {
		customAgents = &s.agentSettings.config
	}

	bundle, err := s.bundleManager.CreateBundle(name, description, profiles, customAgents)
	if err != nil {
		s.errorMsg = i18n.T("settings.config_bundles.error.create", err)
		return nil
	}

	// Export to file
	filename := sanitizeFilename(name)
	if err := s.bundleManager.ExportBundle(bundle, filename); err != nil {
		s.errorMsg = i18n.T("settings.config_bundles.error.export", err)
		return nil
	}

	// Success
	s.successMsg = i18n.T("settings.config_bundles.success.exported", filename)
	s.state = "list"
	s.exportName.SetValue("")
	s.exportDescription.SetValue("")
	s.refreshBundles()

	return nil
}

// updateImport handles import form updates
func (s *ConfigBundlesSettings) updateImport(msg tea.Msg) tea.Cmd {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "esc":
			s.state = "list"
			s.importFilename.SetValue("")
			return nil
		case "enter":
			// Perform import (just load, don't apply yet)
			return s.performImport()
		}
	}

	var cmd tea.Cmd
	s.importFilename, cmd = s.importFilename.Update(msg)
	return cmd
}

// performImport imports a bundle file
func (s *ConfigBundlesSettings) performImport() tea.Cmd {
	filename := strings.TrimSpace(s.importFilename.Value())

	if filename == "" {
		s.errorMsg = i18n.T("settings.config_bundles.error.filename_required")
		return nil
	}

	// Import bundle (validates but doesn't apply)
	_, err := s.bundleManager.ImportBundle(filename)
	if err != nil {
		s.errorMsg = i18n.T("settings.config_bundles.error.import", err)
		return nil
	}

	// Success
	s.successMsg = i18n.T("settings.config_bundles.success.imported")
	s.state = "list"
	s.importFilename.SetValue("")
	s.refreshBundles()

	return nil
}

// updateConfirmApply handles apply confirmation
func (s *ConfigBundlesSettings) updateConfirmApply(msg tea.Msg) tea.Cmd {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "y", "enter":
			// Apply bundle
			return s.performApply()
		case "n", "esc":
			s.state = "list"
		}
	}
	return nil
}

// performApply applies the selected bundle
func (s *ConfigBundlesSettings) performApply() tea.Cmd {
	if s.selectedIndex >= len(s.bundles) {
		s.errorMsg = i18n.T("settings.config_bundles.error.no_selection")
		s.state = "list"
		return nil
	}

	bundleInfo := s.bundles[s.selectedIndex]
	bundle, err := s.bundleManager.ImportBundle(bundleInfo.Filename)
	if err != nil {
		s.errorMsg = i18n.T("settings.config_bundles.error.load", err)
		s.state = "list"
		return nil
	}

	// Apply bundle
	if err := s.bundleManager.ApplyBundle(bundle, s.profileMgr, s.agentSettings); err != nil {
		s.errorMsg = i18n.T("settings.config_bundles.error.apply", err)
		s.state = "list"
		return nil
	}

	s.successMsg = i18n.T("settings.config_bundles.success.applied", bundle.Name)
	s.state = "list"
	return nil
}

// updateConfirmDelete handles delete confirmation
func (s *ConfigBundlesSettings) updateConfirmDelete(msg tea.Msg) tea.Cmd {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "y", "enter":
			// Delete bundle
			return s.performDelete()
		case "n", "esc":
			s.state = "list"
		}
	}
	return nil
}

// performDelete deletes the selected bundle
func (s *ConfigBundlesSettings) performDelete() tea.Cmd {
	if s.selectedIndex >= len(s.bundles) {
		s.errorMsg = i18n.T("settings.config_bundles.error.no_selection")
		s.state = "list"
		return nil
	}

	bundleInfo := s.bundles[s.selectedIndex]
	if err := s.bundleManager.DeleteBundle(bundleInfo.Filename); err != nil {
		s.errorMsg = i18n.T("settings.config_bundles.error.delete", err)
		s.state = "list"
		return nil
	}

	s.successMsg = i18n.T("settings.config_bundles.success.deleted", bundleInfo.Name)
	s.state = "list"
	s.refreshBundles()
	return nil
}

// Render renders the config bundles settings UI
func (s *ConfigBundlesSettings) Render(width, height int) string {
	s.exportName.Placeholder = i18n.T("settings.config_bundles.placeholder.name")
	s.exportDescription.Placeholder = i18n.T("settings.config_bundles.placeholder.description")
	s.importFilename.Placeholder = i18n.T("settings.config_bundles.placeholder.import")

	var raw string
	switch s.state {
	case "list":
		raw = s.renderList(width, height)
	case "export":
		raw = s.renderExport(width, height)
	case "import":
		raw = s.renderImport(width, height)
	case "confirm_apply":
		raw = s.renderConfirmApply(width, height)
	case "confirm_delete":
		raw = s.renderConfirmDelete(width, height)
	default:
		return ""
	}
	// Constrain output to width so no line overflows
	return lipgloss.NewStyle().
		Width(maxInt(20, width)).
		MaxHeight(maxInt(5, height)).
		Render(raw)
}

// renderList renders the bundle list view
func (s *ConfigBundlesSettings) renderList(width, height int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	normalStyle := lipgloss.NewStyle()
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))

	var content strings.Builder

	// Title
	title := i18n.T("settings.config_bundles.title")
	if width < 30 {
		title = i18n.T("settings.config_bundles.title.compact")
	}
	content.WriteString(titleStyle.Render(title))
	content.WriteString("\n\n")

	// Description
	desc := i18n.T("settings.config_bundles.description")
	if len(desc) > maxInt(20, width-2) {
		desc = i18n.T("settings.config_bundles.description.compact")
	}
	content.WriteString(desc + "\n\n")

	// Error/Success messages
	if s.errorMsg != "" {
		content.WriteString(errorStyle.Render("✗ " + s.errorMsg))
		content.WriteString("\n\n")
	}
	if s.successMsg != "" {
		content.WriteString(successStyle.Render("✓ " + s.successMsg))
		content.WriteString("\n\n")
	}

	// Bundle list
	if len(s.bundles) == 0 {
		emptyMsg := i18n.T("settings.config_bundles.empty")
		if width < 55 {
			emptyMsg = i18n.T("settings.config_bundles.empty.compact")
		}
		content.WriteString(normalStyle.Render(emptyMsg))
	} else {
		content.WriteString(headerStyle.Render(i18n.T("settings.config_bundles.available")))
		content.WriteString("\n\n")

		for i, bundle := range s.bundles {
			style := normalStyle
			prefix := "  "
			if i == s.selectedIndex {
				style = selectedStyle
				prefix = "❯ "
			}

			// Format depends on available width
			line := fmt.Sprintf("%s%s", prefix, bundle.Name)
			if width >= 60 && bundle.Description != "" {
				line += fmt.Sprintf(" - %s", bundle.Description)
			}
			if width >= 50 {
				line += fmt.Sprintf(" (%s)", bundle.CreatedAt.Format("2006-01-02"))
			}

			// Truncate to width
			if len(line) > maxInt(20, width-2) {
				line = line[:maxInt(17, width-5)] + "..."
			}

			content.WriteString(style.Render(line))
			content.WriteString("\n")
		}
	}

	content.WriteString("\n")

	// Help text
	if width >= 75 {
		content.WriteString(headerStyle.Render(i18n.T("settings.config_bundles.keys")))
		content.WriteString("\n")
		content.WriteString(i18n.T("settings.config_bundles.help.full.1"))
		content.WriteString(i18n.T("settings.config_bundles.help.full.2"))
	} else if width >= 45 {
		content.WriteString(headerStyle.Render(i18n.T("settings.config_bundles.keys")))
		content.WriteString("\n")
		content.WriteString(i18n.T("settings.config_bundles.help.compact.1"))
		content.WriteString(i18n.T("settings.config_bundles.help.compact.2"))
	}
	// Below 45: no help text, too narrow

	return content.String()
}

// renderExport renders the export form
func (s *ConfigBundlesSettings) renderExport(width, height int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	labelStyle := lipgloss.NewStyle().Bold(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	var content strings.Builder

	content.WriteString(titleStyle.Render(i18n.T("settings.config_bundles.export.title")))
	content.WriteString("\n\n")

	if s.errorMsg != "" {
		content.WriteString(errorStyle.Render("✗ " + s.errorMsg))
		content.WriteString("\n\n")
	}

	// Name input
	content.WriteString(labelStyle.Render(i18n.T("settings.config_bundles.name")))
	content.WriteString("\n")
	content.WriteString(s.exportName.View())
	content.WriteString("\n\n")

	// Description input
	content.WriteString(labelStyle.Render(i18n.T("settings.config_bundles.description_label")))
	content.WriteString("\n")
	content.WriteString(s.exportDescription.View())
	content.WriteString("\n\n")

	// Help
	content.WriteString(i18n.T("settings.config_bundles.export.help"))

	return content.String()
}

// renderImport renders the import form
func (s *ConfigBundlesSettings) renderImport(width, height int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	labelStyle := lipgloss.NewStyle().Bold(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	var content strings.Builder

	content.WriteString(titleStyle.Render(i18n.T("settings.config_bundles.import.title")))
	content.WriteString("\n\n")

	if s.errorMsg != "" {
		content.WriteString(errorStyle.Render("✗ " + s.errorMsg))
		content.WriteString("\n\n")
	}

	content.WriteString(labelStyle.Render(i18n.T("settings.config_bundles.filename")))
	content.WriteString("\n")
	content.WriteString(s.importFilename.View())
	content.WriteString("\n\n")

	content.WriteString(i18n.T("settings.config_bundles.import.instructions"))
	content.WriteString(i18n.T("settings.config_bundles.import.validation"))

	content.WriteString(i18n.T("settings.config_bundles.import.help"))

	return content.String()
}

// renderConfirmApply renders apply confirmation dialog
func (s *ConfigBundlesSettings) renderConfirmApply(width, height int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))

	var content strings.Builder

	if s.selectedIndex < len(s.bundles) {
		bundle := s.bundles[s.selectedIndex]
		content.WriteString(titleStyle.Render(i18n.T("settings.config_bundles.apply.title")))
		content.WriteString("\n\n")
		content.WriteString(i18n.T("settings.config_bundles.bundle", bundle.Name))
		content.WriteString(i18n.T("settings.config_bundles.description_value", bundle.Description))
		content.WriteString(i18n.T("settings.config_bundles.created", bundle.CreatedAt.Format("2006-01-02 15:04")))
		content.WriteString(i18n.T("settings.config_bundles.apply.merge"))
		content.WriteString(i18n.T("settings.config_bundles.apply.replace"))
		content.WriteString(i18n.T("settings.config_bundles.apply.help"))
	}

	return content.String()
}

// renderConfirmDelete renders delete confirmation dialog
func (s *ConfigBundlesSettings) renderConfirmDelete(width, height int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))

	var content strings.Builder

	if s.selectedIndex < len(s.bundles) {
		bundle := s.bundles[s.selectedIndex]
		content.WriteString(titleStyle.Render(i18n.T("settings.config_bundles.delete.title")))
		content.WriteString("\n\n")
		content.WriteString(i18n.T("settings.config_bundles.bundle", bundle.Name))
		content.WriteString(i18n.T("settings.config_bundles.file", bundle.Filename))
		content.WriteString(i18n.T("settings.config_bundles.delete.permanent"))
		content.WriteString(i18n.T("settings.config_bundles.delete.irreversible"))
		content.WriteString(i18n.T("settings.config_bundles.delete.help"))
	}

	return content.String()
}

// sanitizeFilename converts a name to a safe filename
func sanitizeFilename(name string) string {
	// Replace spaces and special chars with hyphens
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	// Remove non-alphanumeric except hyphens
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	filename := result.String()
	// Remove duplicate hyphens
	for strings.Contains(filename, "--") {
		filename = strings.ReplaceAll(filename, "--", "-")
	}
	// Trim hyphens from ends
	filename = strings.Trim(filename, "-")
	// Add timestamp for uniqueness
	timestamp := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s-%s", filename, timestamp)
}
