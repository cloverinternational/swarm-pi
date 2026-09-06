# Responsive Layout Architecture — swarm-tui

## Viewport Breakpoints

```
          XS          SM          MD            LG
     ◄─────────►◄─────────►◄──────────►◄──────────────►
     40       59 60      79 80       99 100          ...
```

| Tier | Width     | Height  | Typical Use                        |
|------|-----------|---------|------------------------------------|
| XS   | 40–59     | 15–19   | Split pane, phone, tiny tmux pane  |
| SM   | 60–79     | 20–24   | Half-screen terminal, small laptop |
| MD   | 80–99     | 25–34   | Standard 80×24 terminal            |
| LG   | 100+      | 35+     | Wide monitor, full-screen terminal |

Height tiers (independent):
| Tier   | Rows  |
|--------|-------|
| Short  | 15–19 |
| Medium | 20–29 |
| Tall   | 30+   |

---

## Screen Inventory & Information Hierarchy

### 1. HOME SCREEN (ScreenHome)

**What it shows:** Tab bar → routed content (Prompt, History, Workflows, Usage, Git, Settings)
**Optional:** Git sidebar (branch, commits, changed files)

#### Content priority (what matters most → least):
1. Input box (prompt tab) — the primary action
2. Tab bar — navigation
3. Resume hint — continue last session
4. Key hints — discoverability
5. Git sidebar — context info

#### Responsive plan:

| Component      | XS (40-59)              | SM (60-79)               | MD (80-99)              | LG (100+)              |
|----------------|-------------------------|--------------------------|-------------------------|-------------------------|
| Tab bar        | Icons only: ❯ H W U G ⚙ | Short labels: PRM HIS WRK | Full labels             | Full + centered         |
| Git sidebar    | Hidden                  | Hidden                   | Hidden (toggle)         | Visible (36 cols)       |
| Input box      | Full width − 2          | Full width − 4           | Max 76, centered        | Max 76, centered        |
| Padding        | Padding(0,1)            | Padding(0,2)             | Padding(0,3)            | Padding(0,3)            |
| Resume hint    | Truncated, no metadata  | Title + ago              | Title + ago + msgs      | Full                    |
| Key hints      | Hidden (too narrow)     | 3 key hints              | All hints               | All hints               |
| Info strip     | Single line: model+branch | Model · branch · files | Full                    | Full                    |

---

### 2. CHAT SCREEN (ScreenChat)

**What it shows:** Header → message viewport → input area
**Optional:** Side panel (right, tokens/mode/activity)

#### Content priority:
1. Message viewport — the conversation
2. Input area — user types here
3. Header (title) — context
4. Side panel — status info

#### Responsive plan:

| Component      | XS (40-59)              | SM (60-79)               | MD (80-99)              | LG (100+)              |
|----------------|-------------------------|--------------------------|-------------------------|-------------------------|
| Side panel     | Hidden                  | Hidden                   | Hidden (toggle at 90)   | Visible (38 cols)       |
| Header         | Title truncated, 1 line | Title + workflow badge   | Full                    | Full                    |
| Header padding | Padding(0,1)            | Padding(0,1)             | Padding(0,2)            | Padding(0,2)            |
| Message width  | width − 2               | width − 4                | width − 4               | chatWidth − 4           |
| Input width    | width − 4               | width − 6                | width − 8               | width − 8               |
| Divider        | Thin (─)                | Thin (─)                 | Full                    | Full                    |
| Overlays       | Full-width modals       | Width − 6                | Width − 10              | ChatWidth − 10          |

---

### 3. CONVERSATIONS SCREEN (ScreenChats)

**What it shows:** Session list + message preview (two-pane or single-pane)

#### Content priority:
1. Session list — pick a conversation
2. Session row: title + time (minimum viable)
3. Message preview — see what's in a session
4. Stats (tokens, cost, tools)
5. Branch filter tabs

#### Responsive plan:

| Component       | XS (40-59)                | SM (60-79)               | MD (80-99)              | LG (100+)               |
|-----------------|---------------------------|--------------------------|-------------------------|--------------------------|
| Layout          | **Single pane** (list only) | **Single pane** (list)  | Two-pane (30/70)        | Two-pane (35/65)         |
| Preview pane    | Hidden → Enter to view    | Hidden → Enter to view   | Right pane              | Right pane               |
| Session row     | Icon + title (truncated)  | Icon + title + time      | Icon + title + model + time | Full (+ tokens)       |
| Filter tabs     | Hidden (use keys 1/2/3)   | Short labels             | Full labels             | Full + padding           |
| Stats line      | Hidden                    | 2 stats max              | 4 stats                 | All stats                |
| Scroll indicator| "↓N"                     | "↓ N more"               | Full                    | Full                     |
| Branch badge    | Hidden                    | Icon only                | Name (truncated)        | Full name                |

**Key design decision:** Below 80 cols, switch to single-pane list. Enter opens full-screen message view. This is intentional, not degraded.

---

### 4. WORKFLOW SCREENS (ScreenWorkflows)

#### Content priority:
1. Workflow list — select a workflow
2. Workflow name + description
3. Category sidebar
4. Detail metadata
5. Edit form fields

#### Responsive plan:

| Component       | XS (40-59)               | SM (60-79)              | MD (80-99)              | LG (100+)               |
|-----------------|--------------------------|-------------------------|-------------------------|--------------------------|
| Layout          | **Single pane** (list)   | **Single pane** (list)  | Two-pane (25/75)        | Two-pane (30/70)         |
| Category sidebar| Hidden (use keys)        | Hidden (use keys)        | Left pane               | Left pane                |
| Workflow item   | Name only                | Name + 1-line desc      | Name + desc + metadata  | Full                     |
| Detail view     | Full-width stacked       | Full-width stacked       | Full-width              | Full-width               |
| Editor tabs     | [1][2][3][4][5]          | Short: Meta Cfg Grp Str | Full names              | Full names               |
| Form labels     | 8 chars max              | 12 chars                | 20 chars                | 25 chars                 |
| Hints bar       | "↑↓ ent esc"            | Abbreviated              | Full                    | Full                     |
| Progress bar    | Min 15 chars             | Width − 8                | Width − 10              | Width − 10               |

---

### 5. SETTINGS (inline in Home)

#### Content priority:
1. Current setting value — what's configured
2. Setting name/label
3. Navigation sidebar
4. Description text
5. Toggle controls

#### Responsive plan:

| Component       | XS (40-59)               | SM (60-79)              | MD (80-99)              | LG (100+)               |
|-----------------|--------------------------|-------------------------|-------------------------|--------------------------|
| Layout          | **Single pane** (stacked)| **Single pane** (stacked)| Two-pane (25/75)       | Two-pane (30/70)         |
| Navigation      | Top bar (horizontal)     | Top bar (horizontal)     | Left sidebar           | Left sidebar (25-40)     |
| Section names   | Icons only               | Short labels             | Full names             | Full + descriptions      |
| Form labels     | 8 chars → value below   | 10 chars → value inline  | 15-20 chars inline     | 20-25 chars inline       |
| Toggle style    | [x] Label                | [x] Label                | [x] Label  desc        | [x] Label  description   |
| Button row      | Stacked vertically       | 2 per row                | Inline                 | Inline                   |
| Hints           | Hidden                   | Abbreviated              | Full                   | Full                     |

**Key design decision:** Below 80 cols, settings sidebar becomes a horizontal tab/dropdown at the top. Content gets full width.

---

### 6. SIDE PANEL (Chat right panel)

#### Content priority:
1. Context usage bar — how full is the context window
2. Model + provider
3. Permission + mode badges
4. Cache status
5. Activity (audit, tasks, agents)
6. Quota

#### Responsive plan:
Side panel only shows at LG (100+ cols). Its internal width is fixed at 38.

| Component       | Internal (38 cols)       |
|-----------------|--------------------------|
| Inner width     | 34 chars (38 − 4 padding)|
| Progress bar    | Full 34-char bar         |
| Badges          | 2-column (17 each)       |
| Activity items  | Truncated at 30 chars    |
| Hint            | "tab: toggle panel"      |

If someone resizes below 90 while panel is open: **force-hide the panel**.

---

### 7. MODALS & OVERLAYS

| Component         | XS (40-59)            | SM (60-79)             | MD+ (80+)              |
|-------------------|-----------------------|------------------------|------------------------|
| Command palette   | Full width − 2        | Width − 6, max 60      | 70, centered           |
| Approval modal    | Full width − 2        | Width − 4              | 84, centered           |
| Model switcher    | Full width − 2        | Width − 6, max 55      | 65, centered           |
| Agent switcher    | Full width − 2        | Width − 6, max 55      | 65, centered           |
| Checkout modal    | Full width − 2        | Width − 6              | 60, centered           |
| Autocomplete      | Input width            | Input width            | Input width             |
| Buttons           | Stacked vertical       | Inline (abbreviated)   | Inline (full labels)    |

---

### 8. FILE VIEWER (ScreenViewer)

| Component     | XS (40-59)            | SM (60-79)             | MD (80-99)             | LG (100+)              |
|---------------|-----------------------|------------------------|------------------------|------------------------|
| Layout        | Content only           | Content only           | Tree(25) + content     | Tree(35) + content     |
| File tree     | Hidden (toggle)        | Hidden (toggle)        | Left pane              | Left pane              |
| Line numbers  | Hidden                 | 4-char                 | 4-char                 | 6-char                 |

---

### 9. GIT SCREEN (ScreenGit)

| Component     | XS (40-59)            | SM (60-79)             | MD+ (80+)              |
|---------------|-----------------------|------------------------|------------------------|
| Header        | "GIT [esc]"           | "GIT [esc] [r]"        | Full hints             |
| Panel         | Full width − 2        | Full width − 2         | Full width − 2         |

---

## Implementation: Viewport Helper

A single source of truth for responsive decisions:

```go
package chat

// ViewportTier represents a responsive breakpoint
type ViewportTier int

const (
    TierXS ViewportTier = iota  // 40-59 cols
    TierSM                      // 60-79 cols
    TierMD                      // 80-99 cols
    TierLG                      // 100+ cols
)

type HeightTier int

const (
    HeightShort  HeightTier = iota  // 15-19 rows
    HeightMedium                     // 20-29 rows
    HeightTall                       // 30+ rows
)

// Viewport provides responsive layout decisions
type Viewport struct {
    Width  int
    Height int
    Tier   ViewportTier
    HTier  HeightTier
}

func NewViewport(w, h int) Viewport {
    v := Viewport{Width: w, Height: h}
    switch {
    case w >= 100:
        v.Tier = TierLG
    case w >= 80:
        v.Tier = TierMD
    case w >= 60:
        v.Tier = TierSM
    default:
        v.Tier = TierXS
    }
    switch {
    case h >= 30:
        v.HTier = HeightTall
    case h >= 20:
        v.HTier = HeightMedium
    default:
        v.HTier = HeightShort
    }
    return v
}

// Layout helpers — every screen uses these instead of ad-hoc math

func (v Viewport) Pad() int {
    switch v.Tier {
    case TierXS: return 1
    case TierSM: return 2
    default:     return 3
    }
}

func (v Viewport) InnerW() int {
    return max(20, v.Width - v.Pad()*2)
}

func (v Viewport) ShowSidebar() bool {
    return v.Tier >= TierLG
}

func (v Viewport) ShowTwoPane() bool {
    return v.Tier >= TierMD
}

func (v Viewport) SidebarWidth() int {
    switch v.Tier {
    case TierMD: return min(25, v.Width/4)
    case TierLG: return min(40, v.Width/4)
    default:     return 0
    }
}

func (v Viewport) ContentWidth() int {
    sw := v.SidebarWidth()
    if sw == 0 {
        return v.InnerW()
    }
    return max(30, v.Width - sw - 1)
}

func (v Viewport) ModalWidth(preferred int) int {
    maxW := v.Width - 2
    if v.Tier == TierXS {
        return maxW
    }
    margin := 2 * v.Pad()
    if preferred > maxW - margin {
        return maxW - margin
    }
    return preferred
}

func (v Viewport) FormLabelWidth() int {
    switch v.Tier {
    case TierXS: return 8
    case TierSM: return 12
    case TierMD: return 18
    default:     return 24
    }
}

func (v Viewport) TruncateHints(full string, abbreviated string) string {
    if v.Tier <= TierSM {
        return abbreviated
    }
    return full
}
```

---

## Migration Strategy

### Phase 1: Add Viewport helper (non-breaking)
- Create `viewport.go` with the `Viewport` struct and helpers
- Add `vp Viewport` field to `App`, updated on resize
- No rendering changes yet

### Phase 2: Fix critical negative-dimension bugs
- Replace all `width - N` with `max(minVal, width - N)`
- Use `vp.Pad()` instead of hardcoded Padding(0,3)
- Use `vp.ModalWidth(preferred)` for all modals

### Phase 3: Implement single-pane fallbacks
- Conversations: single-pane list below MD
- Settings: horizontal nav below MD
- Workflows: single-pane list below MD
- File viewer: hide tree below MD

### Phase 4: Responsive tab bar
- XS: icon-only tabs
- SM: abbreviated labels
- MD+: full labels

### Phase 5: Smart content density
- Responsive form labels (vp.FormLabelWidth())
- Responsive hints (vp.TruncateHints())
- Progressive stats disclosure (show fewer stats at narrow widths)

---

## Design Principles

1. **Never overflow** — content must fit within the viewport at every size
2. **Progressive disclosure** — show less info at small sizes, not broken info
3. **Single-pane is a feature** — a focused list view at XS/SM is better than a crushed two-pane
4. **Padding scales with space** — 1 at XS, 2 at SM, 3 at MD+
5. **Modals go full-width at XS** — no margins to waste at tiny sizes
6. **Every width subtraction uses max()** — no negative dimensions ever
7. **Sidebar is a luxury** — only shows when there's room for both sidebar AND content
