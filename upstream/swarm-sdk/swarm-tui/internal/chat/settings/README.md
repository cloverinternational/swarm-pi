# Settings System

A modular, extensible settings system for SwarmOS with a side panel navigation interface.

## Architecture

The settings system is organized into separate files for easy maintenance and extension:

```
internal/chat/settings/
├── README.md           # This file
├── types.go           # Core types and section definitions
├── manager.go         # Settings manager and navigation
├── general.go         # General app settings
├── display.go         # Display/rendering settings
├── model.go           # Model/provider selection
├── auth.go            # Authentication settings
└── advanced.go        # Advanced configuration
```

## UI Layout

```
┌─────────────────────────────────────────────────────────────┐
│ SETTINGS          │  GENERAL SETTINGS                       │
│ ──────────────────│  Configure application behavior         │
│ ● General         │                                          │
│ ● Model & Provider│  ▶ Theme                                │
│ ● MCP Servers     │    Color scheme for the interface       │
│ ● Display         │    < Default >                          │
│ ● Authentication  │                                          │
│ ● Advanced        │  ▶ Font Size                            │
│                   │    Text size in the interface           │
│                   │    ====|----------  14                  │
└─────────────────────────────────────────────────────────────┘
 Dark sidebar (25%)   Content area (75%)
```

## Adding a New Settings Section

### Step 1: Define the Section in `types.go`

```go
// Add to the Section enum
const (
    SectionGeneral Section = iota
    SectionModel
    SectionMCP
    SectionDisplay
    SectionAuth
    SectionAdvanced
    SectionYourNew  // Add your new section here
)

// Add to the Sections slice
var Sections = []SectionInfo{
    {SectionGeneral, "General", "General application settings", "●"},
    // ... existing sections ...
    {SectionYourNew, "Your Section", "Description of your section", "●"},
}
```

### Step 2: Create a New Settings File

Create `internal/chat/settings/yournew.go`:

```go
package settings

import (
    "github.com/charmbracelet/lipgloss/v2"
)

// YourNewSettings handles your custom settings
type YourNewSettings struct {
    items []Item
}

// NewYourNewSettings creates a new settings handler
func NewYourNewSettings() *YourNewSettings {
    return &YourNewSettings{
        items: []Item{
            {
                Key:         "yournew.setting1",
                Label:       "Setting Name",
                Description: "What this setting does",
                Type:        InputTypeToggle,
                Value:       true,
            },
            {
                Key:         "yournew.setting2",
                Label:       "Another Setting",
                Description: "Another description",
                Type:        InputTypeDropdown,
                Value:       "Option1",
                Options:     []string{"Option1", "Option2", "Option3"},
            },
        },
    }
}

// GetItems returns all settings items
func (y *YourNewSettings) GetItems() []Item {
    return y.items
}

// Render renders the settings view
func (y *YourNewSettings) Render(width, height int, state *State, theme interface{}) string {
    th := theme.(Theme)
    var lines []string

    // Header
    headerStyle := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.Primary)).
        Bold(true).
        Padding(1, 2)
    header := headerStyle.Render("YOUR SECTION NAME")
    lines = append(lines, header)

    // Subtitle
    subtitleStyle := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.TextMuted)).
        Italic(true).
        Padding(0, 2)
    subtitle := subtitleStyle.Render("Description of this settings section")
    lines = append(lines, subtitle, "")

    // Render settings items
    for i, item := range y.items {
        isSelected := i == state.SelectedItem
        settingRow := renderSettingRow(item, isSelected, width-8, th)
        lines = append(lines, settingRow, "")
    }

    // Hints
    hintStyle := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.TextMuted)).
        Italic(true).
        Padding(1, 2)
    hints := hintStyle.Render("↑/↓ navigate • enter edit • space toggle • esc back")
    lines = append(lines, "", hints)

    content := lipgloss.JoinVertical(lipgloss.Left, lines...)

    containerStyle := lipgloss.NewStyle().
        Width(width).
        Height(height).
        Padding(0, 0)

    return containerStyle.Render(content)
}
```

### Step 3: Register in `manager.go`

Add your settings handler to the Manager struct:

```go
type Manager struct {
    state *State

    // Section handlers
    general  *GeneralSettings
    display  *DisplaySettings
    model    *ModelSettings
    auth     *AuthSettings
    advanced *AdvancedSettings
    yournew  *YourNewSettings  // Add your handler
    
    // ... callbacks ...
}
```

Initialize in `NewManager`:

```go
func NewManager(provider, model string, showThinking, showFullToolOutput, richAnimations, copySelectionShortcut, autoCopySelectionOnMouse bool) *Manager {
    return &Manager{
        state:    NewState(),
        general:  NewGeneralSettings(),
        display:  NewDisplaySettings(showThinking, showFullToolOutput, richAnimations, copySelectionShortcut, autoCopySelectionOnMouse),
        model:    NewModelSettings(provider, model),
        auth:     NewAuthSettings(provider, true),
        advanced: NewAdvancedSettings(),
        yournew:  NewYourNewSettings(),  // Initialize your handler
    }
}
```

Add to the Render switch:

```go
switch m.state.SelectedSection {
case SectionGeneral:
    content = m.general.Render(contentWidth, height, m.state, th)
// ... other cases ...
case SectionYourNew:
    content = m.yournew.Render(contentWidth, height, m.state, th)
}
```

Add keyboard handler:

```go
switch m.state.SelectedSection {
case SectionGeneral:
    return m.handleGeneralKey(key)
// ... other cases ...
case SectionYourNew:
    return m.handleYourNewKey(key)
}

// Add the handler function
func (m *Manager) handleYourNewKey(key string) tea.Cmd {
    items := m.yournew.GetItems()

    switch key {
    case "up", "k":
        if m.state.SelectedItem > 0 {
            m.state.SelectedItem--
        }
    case "down", "j":
        if m.state.SelectedItem < len(items)-1 {
            m.state.SelectedItem++
        }
    case " ", "space":
        if m.state.SelectedItem < len(items) {
            item := items[m.state.SelectedItem]
            if item.Type == InputTypeToggle {
                // Toggle the value
                item.Value = !item.Value.(bool)
            }
        }
    case "enter":
        // Handle enter key for your section
    }

    return nil
}
```

## Available Input Types

### Toggle Switch
```go
{
    Type:  InputTypeToggle,
    Value: true,  // bool
}
// Renders as: [X] ON  or  [ ] OFF
```

### Dropdown
```go
{
    Type:    InputTypeDropdown,
    Value:   "Default",  // string
    Options: []string{"Default", "Dark", "Light"},
}
// Renders as: < Default >
```

### Number Input
```go
{
    Type:  InputTypeNumber,
    Value: 4,  // int
    Min:   2,
    Max:   8,
    Step:  1,
}
// Renders as: [ 4 ]  < >
```

### Slider
```go
{
    Type:  InputTypeSlider,
    Value: 14,  // int
    Min:   10,
    Max:   24,
    Step:  1,
}
// Renders as: =====|----------  14
```

### Text Input
```go
{
    Type:  InputTypeText,
    Value: "some text",  // string
}
// Renders as: [some text|]  (with cursor)
```

### Button
```go
{
    Type:    InputTypeButton,
    Label:   "Click Me",
    OnClick: func() error { /* action */ },
}
// Renders as: [Click Me]
```

## Organizing Settings with Categories

Group related settings within a section using the `Category` field:

```go
items := []Item{
    // First category (no category name = default group)
    {Key: "general.theme", Label: "Theme", ...},
    {Key: "general.fontSize", Label: "Font Size", ...},
    
    // New category
    {
        Key:      "editor.wordWrap",
        Label:    "Word Wrap",
        Category: "Editor",  // Category header will be displayed
        ...
    },
    {
        Key:      "editor.lineNumbers",
        Label:    "Line Numbers",
        Category: "Editor",  // Same category
        ...
    },
}
```

Renders as:
```
GENERAL SETTINGS

  ▶ Theme
    Color scheme for the interface
    < Default >

  ▶ Font Size
    Text size in the interface
    =====|----------  14

── Editor ──

  ▶ Word Wrap
    Wrap long lines
    [X] ON
```

## Keyboard Navigation

### Sidebar Navigation
- `↑`/`↓` or `j`/`k` - Move between sections
- `Enter` - Select section (switches to content area)

### Content Navigation  
- `↑`/`↓` or `j`/`k` - Navigate between settings
- `←`/`→` or `h`/`l` - Adjust values (sliders, numbers)
- `Space` - Toggle boolean settings
- `Enter` - Edit text/activate buttons
- `Esc` - Return to home screen

### Global Keys
- `Esc` - Exit settings

## Theming

The settings system automatically adapts to the app theme. Available theme colors:

```go
type Theme struct {
    Primary   string  // Accent color for selected items
    Accent    string  // Secondary accent
    Text      string  // Primary text color
    TextDim   string  // Secondary text color
    TextMuted string  // Dimmed text (descriptions, hints)
    BG        string  // Base background
    BGLight   string  // Elevated background for inputs
    BGLighter string  // Hover/selected background
    Border    string  // Border color
    Success   string  // Success/enabled color (green)
    Warning   string  // Warning color (yellow)
    Error     string  // Error color (red)
}
```

Sidebar has a fixed dark background (`#151B24`) to create visual separation.

## Best Practices

1. **One section per file** - Keep each settings category in its own file
2. **Clear descriptions** - Write helpful descriptions for each setting
3. **Group related settings** - Use categories to organize settings within a section
4. **Consistent naming** - Use dot notation for keys: `section.subsection.setting`
5. **Provide defaults** - Always set sensible default values
6. **Add hints** - Include keyboard hints at the bottom of each section
7. **Uppercase headers** - Use uppercase for section titles for consistency
8. **No emojis** - Use simple ASCII characters (●, |, =, -, etc.)

## Example: Complete Settings Section

See `general.go` for a complete, working example that demonstrates:
- Multiple input types (toggle, dropdown, slider, number)
- Category grouping
- Proper rendering
- Keyboard navigation
- Theme integration

## Testing Your Settings

1. Build the app: `go build -o swarmos ./cmd/swarmos`
2. Run the app: `./swarmos`
3. Click the Settings button or press `s` on home screen
4. Navigate to your new section using `↑`/`↓`
5. Test keyboard navigation and controls

## Common Issues

### Setting values don't persist
- The current implementation uses in-memory state
- To persist settings, add save/load logic to your settings handler
- Hook into `OnChange` callbacks to write to config files

### Control not responding
- Ensure you've added the keyboard handler in `manager.go`
- Check that the input type matches the expected value type
- Verify the handler is called in the section switch statement

### Layout issues
- Sidebar is fixed at 25% width (min 25, max 40 chars)
- Content area adapts to remaining width
- Test at different terminal sizes

## Future Enhancements

Potential additions to the settings system:
- [ ] Persistent settings storage (TOML/JSON)
- [ ] Setting validation and error display
- [ ] Search/filter settings
- [ ] Settings reset/defaults button
- [ ] Import/export settings
- [ ] Keyboard shortcuts customization
- [ ] Real-time setting preview
