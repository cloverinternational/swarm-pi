# Config Bundle UI/UX Design

## User Stories

### Story 1: View Current Config Source
**As a user, I want to see at a glance whether I'm using global or project config**

**Acceptance Criteria:**
- Tab bar shows config source indicator (🌍 Global or 📦 ProjectName)
- Indicator is always visible when in chat view
- Color/contrast makes it easy to distinguish
- Tooltip or help text explains what the indicator means

### Story 2: Switch Between Global and Project Config
**As a user, I want to quickly switch between global and project config**

**Acceptance Criteria:**
- Single keypress to switch (s/S)
- Visual confirmation of switch (indicator updates)
- Settings reload when config changes
- Previous setting values are preserved

### Story 3: Create New Project Config
**As a user, I want to create a new project config when none exists**

**Acceptance Criteria:**
- Prompt to create when entering directory without config
- Easy creation flow (single keypress to create)
- Config file created with sensible defaults
- Auto-switch to new project config

### Story 4: View Config Details
**As a user, I want to see what settings are in my current config**

**Acceptance Criteria:**
- List view showing config sections
- Expandable details for each section
- Visual comparison between global and project
- Highlight overridden values

### Story 5: Edit Project Config Settings
**As a user, I want to modify my project config settings**

**Acceptance Criteria:**
- Navigate to settings section
- Edit values inline or in editor
- Save changes with confirmation
- Discard changes with confirmation

### Story 6: Compare Global vs Project Config
**As a user, I want to see what differs between global and project config**

**Acceptance Criteria:**
- Side-by-side comparison view
- Highlighted differences
- Clear indication of which values are overridden
- Ability to reset to global values

### Story 7: Auto-Detection Notification
**As a user, I want to be notified when I enter a directory with project config**

**Acceptance Criteria:**
- Banner or toast notification
- Option to switch or dismiss
- Don't show again for this project option
- Clear indication of which config was detected

---

## UI Design

### State Machine

```
┌─────────────────────────────────────────────────────────────┐
│                      Config Bundle UI                        │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  States:                                                     │
│  ┌────────────┐                                              │
│  │   list     │  ← Default entry point                       │
│  └─────┬──────┘                                              │
│        │                                                     │
│        ├──[enter]──→ detail ──→ edit_section                │
│        │                                                     │
│        ├──[c]─────→ create_confirm                           │
│        │                                                     │
│        └──[s]─────→ switch_confirm (if project exists)       │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### Screen Layout: List View

```
┌──────────────────────────────────────────────────────────────┐
│                    Config Source                              │
│                                                              │
│   ┌────────────────────────────────────────────────────────┐ │
│   │  ● Global                                    ✓ active  │ │
│   │     System-wide settings, shared across projects       │ │
│   │     ~/.swarmos/config.json                             │ │
│   └────────────────────────────────────────────────────────┘ │
│                                                              │
│   ┌────────────────────────────────────────────────────────┐ │
│   │  ○ Project: mono                          (available) │ │
│   │     Project-specific settings for this repo            │ │
│   │     .swarm/config.json                                 │ │
│   │                                                        │ │
│   │     ↓ 2 overridden settings                           │ │
│   │     • Theme: light (was: dark)                        │ │
│   │     • DefaultModel: gpt-4 (was: claude-3-5-sonnet)    │ │
│   └────────────────────────────────────────────────────────┘ │
│                                                              │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │ [↑/↓] navigate  [s] switch  [enter] details  [esc] back│ │
│  └─────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

### Screen Layout: Detail View (Split Panel)

```
┌──────────────────────────────────────────────────────────────────────┐
│  Configure Project: mono                                    [esc]  │
├──────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ┌─ Sections ──────────┐┌─ Settings ──────────────────────────────┐ │
│  │                      ││                                          │ │
│  │  ● System      (2)  ││  Theme................... light         │ │
│  │  ○ Agents      (0)  ││  DefaultProvider........ anthropic      │ │
│  │  ○ Profiles     (0) ││  MaxOutputLines......... 200           │ │
│  │  ○ Prompts      (0) ││                                          │ │
│  │  ○ Tools        (0) ││  ── Overridden from Global ───────────  │ │
│  │  ○ Hooks        (0) ││                                          │ │
│  │  ○ Skills       (0) ││  Theme........... dark → light         │ │
│  │  ○ Providers    (0) ││  MaxOutputLines... 100 → 200           │ │
│  │  ○ MCP Servers  (0) ││                                          │ │
│  │                      ││                                          │ │
│  └──────────────────────┘└──────────────────────────────────────────┘ │
│                                                                       │
│  [↑/↓] sections  [→] edit  [r] reset to global  [esc] back           │
└──────────────────────────────────────────────────────────────────────┘
```

### Screen Layout: Create Project Config

```
┌──────────────────────────────────────────────────────────────┐
│                    Create Project Config                      │
├──────────────────────────────────────────────────────────────┤
│                                                               │
│   No project config found in this directory.                 │
│                                                               │
│   Creating a project config allows you to:                    │
│   • Override global settings for this project                │
│   • Add project-specific agents and tools                    │
│   • Configure project-specific MCP servers                    │
│                                                               │
│   Location: .swarm/config.json                                │
│                                                               │
│   ┌─────────────────────────────────────────────────────────┐│
│   │ [enter] create  [esc] cancel                             ││
│   └─────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────┘
```

### Screen Layout: Switch Confirmation

```
┌──────────────────────────────────────────────────────────────┐
│                    Switch Config Source                       │
├──────────────────────────────────────────────────────────────┤
│                                                               │
│   Switch from Global to Project: mono?                        │
│                                                               │
│   ┌─────────────────────────────────────────────────────────┐│
│   │  Global                    Project: mono                ││
│   │  ─────────                 ─────────────                ││
│   │  Theme: dark               Theme: light                 ││
│   │  MaxOutputLines: 100       MaxOutputLines: 200          ││
│   │                                                          ││
│   │  2 settings will be overridden                           ││
│   └─────────────────────────────────────────────────────────┘│
│                                                               │
│   ┌─────────────────────────────────────────────────────────┐│
│   │ [enter] switch  [esc] cancel                             ││
│   └─────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────┘
```

### Screen Layout: Auto-Detection Toast

```
┌──────────────────────────────────────────────────────────────┐
│  📦 Project config detected: mono                            │
│                                                               │
│  [s] switch to project  [esc] dismiss  [d] don't show again  │
└──────────────────────────────────────────────────────────────┘
```

---

## Visual Design System

### Colors (matching existing theme)

| Element | Global | Project | Active State |
|---------|--------|---------|--------------|
| Icon | 🌍 | 📦 | ✓ checkmark |
| Border | `th.Border` | `th.Border` | `th.Primary` |
| Background | `th.BG` | `th.BG` | `th.BGLighter` |
| Text | `th.Text` | `th.Text` | `th.Primary` |
| Muted | `th.TextMuted` | `th.TextMuted` | `th.TextDim` |
| Success | `th.Success` | `th.Success` | - |
| Override indicator | - | `th.Accent` | - |

### Card Style (matching profile UI)

```go
cardStyle := lipgloss.NewStyle().
    Border(lipgloss.RoundedBorder()).
    BorderForeground(lipgloss.Color(th.Border)).
    Width(cardWidth).
    Padding(0, 1)

selectedStyle := lipgloss.NewStyle().
    Border(lipgloss.RoundedBorder()).
    BorderForeground(lipgloss.Color(th.Primary)).
    Background(lipgloss.Color(th.BGLighter)).
    Width(cardWidth).
    Padding(0, 1)
```

### Key Hint Style

```go
k := func(key string) string {
    return lipgloss.NewStyle().
        Background(lipgloss.Color(th.BGLight)).
        Foreground(lipgloss.Color(th.Text)).
        Padding(0, 1).
        Bold(true).
        Render(key)
}
```

---

## Keyboard Navigation

| Key | Context | Action |
|-----|---------|--------|
| `↑/↓` | List | Navigate between configs |
| `↑/↓` | Detail | Navigate between sections |
| `←/→` | Detail | Navigate between panels |
| `s` | List | Switch to selected config |
| `enter` | List | Enter detail view |
| `enter` | Create | Confirm creation |
| `enter` | Switch | Confirm switch |
| `c` | List | Create new project config |
| `r` | Detail | Reset section to global |
| `e` | Detail | Edit section settings |
| `esc` | Any | Go back / cancel |
| `?` | Any | Show help |

---

## Responsive Design

### Wide Screen (>80 cols)
- Split panel with section list and details
- Full keyboard hints
- Side-by-side comparison

### Medium Screen (60-80 cols)
- Stacked layout
- Abbreviated hints
- Collapsed comparison

### Narrow Screen (<60 cols)
- Single column
- Minimal hints (just essential keys)
- No comparison view

---

## Implementation Checklist

- [ ] ConfigSourceSettings struct with state machine
- [ ] List view with Global/Project cards
- [ ] Detail view with section list
- [ ] Split panel for section editing
- [ ] Create confirmation dialog
- [ ] Switch confirmation dialog
- [ ] Auto-detection toast notification
- [ ] Keyboard navigation
- [ ] Responsive layout
- [ ] Integration with TabBar indicator
- [ ] Integration with settings menu
