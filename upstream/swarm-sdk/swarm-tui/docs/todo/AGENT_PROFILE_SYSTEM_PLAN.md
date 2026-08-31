# Agent Profile System - Detailed Atomic Implementation Plan

## Table of Contents
1. [Problem Statement](#problem-statement)
2. [Requirements Analysis](#requirements-analysis)
3. [System Design (Bottom-Up)](#system-design-bottom-up)
4. [Data Flow](#data-flow)
5. [Implementation Phases](#implementation-phases)
6. [Testing Strategy](#testing-strategy)

---

## Problem Statement

### What We Need
Users want to configure **which model is used for each type of agent** (main, steering, background, sub-agent, thinking, long-context) without hardcoding model selections throughout the codebase.

### Current State
- Model selection is hardcoded or selected globally
- No way to say "use Claude Opus for main agent, but Haiku for background tasks"
- No concept of configuration profiles for different use cases

### Desired State
- Users can create/select **profiles** (e.g., "Quality", "Performance", "Balanced")
- Each profile maps **agent roles** to specific **provider/model combinations**
- Easy switching between profiles without code changes
- Similar to SwarmCode's model pointer system

---

## Requirements Analysis

### Functional Requirements
1. **Profile Management**
   - Create new profiles
   - Edit existing profiles
   - Delete custom profiles
   - Clone profiles
   - Set default profile

2. **Model Pointer System**
   - Map aliases (main, steering, background, etc.) to provider/model
   - Resolve alias → actual model at runtime
   - Override system prompts per role (optional)
   - Configure capabilities per role (max tokens, temperature, etc.)

3. **UI/UX**
   - Settings page for profile management
   - List view of all profiles
   - Edit form for profile configuration
   - Role configuration screen
   - Profile switcher in status bar

4. **Persistence**
   - Save profiles to disk (`~/.swarmos/agent_profiles.json`)
   - Load profiles on startup
   - Auto-save on changes

5. **Integration**
   - SDK agent factory uses active profile
   - Agent creation resolves aliases through active profile
   - Fallback to defaults if profile not configured

### Non-Functional Requirements
1. **Performance**: Profile loading < 50ms
2. **Reliability**: Graceful degradation if profile corrupt
3. **Usability**: Clear error messages, intuitive UI
4. **Maintainability**: Clean separation of concerns

---

## System Design (Bottom-Up)

### Level 1: Data Primitives

#### 1.1 Model Alias (Enumeration)
**What it is**: A symbolic name for an agent role.

**Why we need it**: Decouple agent code from specific models.

**How it works**:
```go
type ModelAlias string

const (
    AliasMain        ModelAlias = "main"
    AliasSteering    ModelAlias = "steering"
    AliasBackground  ModelAlias = "background"
    AliasSubAgent    ModelAlias = "sub_agent"
    AliasThinking    ModelAlias = "thinking"
    AliasLongContext ModelAlias = "long_context"
)
```

**Logic**:
- Simple string enum
- Compile-time constants
- Used as map keys

#### 1.2 Model Pointer (Resolution Target)
**What it is**: The actual provider/model configuration for an alias.

**Why we need it**: Each alias must resolve to concrete provider/model.

**How it works**:
```go
type ModelPointer struct {
    Provider     string              `json:"provider"`      // e.g., "anthropic"
    Model        string              `json:"model"`         // e.g., "claude-opus-4-20250514"
    SystemPrompt string              `json:"system_prompt,omitempty"` // Override
    Capabilities *AgentCapabilities  `json:"capabilities,omitempty"`  // Override
}
```

**Logic**:
- Holds all data needed to create an agent
- JSON serializable for persistence
- Optional fields for customization

#### 1.3 Agent Capabilities (Configuration)
**What it is**: Runtime limits and behavior settings.

**Why we need it**: Different roles need different constraints.

**How it works**:
```go
type AgentCapabilities struct {
    MaxTokens   int     `json:"max_tokens,omitempty"`
    Temperature float64 `json:"temperature,omitempty"`
    MaxTurns    int     `json:"max_turns,omitempty"`
    Timeout     int     `json:"timeout,omitempty"` // seconds
}
```

**Logic**:
- Numeric constraints
- Applied when creating agent
- Omitempty for optional values

---

### Level 2: Profile Data Structure

#### 2.1 Agent Profile (Container)
**What it is**: A named collection of model pointer mappings.

**Why we need it**: Group related configurations together.

**How it works**:
```go
type AgentProfile struct {
    ID          string                      `json:"id"`          // Unique identifier
    Name        string                      `json:"name"`        // Display name
    Description string                      `json:"description,omitempty"`
    Icon        string                      `json:"icon,omitempty"`
    Color       string                      `json:"color,omitempty"`
    IsDefault   bool                        `json:"is_default"`
    Pointers    map[ModelAlias]ModelPointer `json:"pointers"`    // The mappings
    CreatedAt   time.Time                   `json:"created_at"`
    UpdatedAt   time.Time                   `json:"updated_at"`
}
```

**Logic**:
- Map structure for O(1) alias lookup
- Metadata for UI display
- Timestamps for tracking

#### 2.2 Profiles Configuration (Root Storage)
**What it is**: The top-level structure persisted to disk.

**Why we need it**: Store multiple profiles + default selection.

**How it works**:
```go
type ProfilesConfig struct {
    DefaultProfile string         `json:"default_profile"` // ID of default
    Profiles       []AgentProfile `json:"profiles"`
}
```

**Logic**:
- Single source of truth
- DefaultProfile is a pointer (ID) to a profile in Profiles array
- Array for ordered iteration

---

### Level 3: Business Logic Layer

#### 3.1 Profile Manager (CRUD Operations)

**Responsibilities**:
- Load profiles from disk
- Save profiles to disk
- Create/Read/Update/Delete operations
- Get active profile
- Validate profile data

**Core Methods**:

##### 3.1.1 LoadProfiles
**Input**: File path
**Output**: ProfilesConfig, error
**Logic**:
1. Check if file exists
2. If not exists, return built-in defaults
3. Read file bytes
4. Unmarshal JSON → ProfilesConfig
5. Validate structure
6. Return config

**Edge Cases**:
- File doesn't exist → Use defaults
- File corrupt → Return error with helpful message
- Empty file → Use defaults
- Invalid JSON → Return parse error

##### 3.1.2 SaveProfiles
**Input**: ProfilesConfig, file path
**Output**: error
**Logic**:
1. Validate config (at least one profile, default exists)
2. Marshal config → JSON with indentation
3. Create parent directory if not exists
4. Write to temp file
5. Rename temp → actual (atomic write)
6. Return error if any step fails

**Edge Cases**:
- Directory doesn't exist → Create it
- No write permissions → Return permission error
- Disk full → Return write error

##### 3.1.3 CreateProfile
**Input**: Profile data (name, description, etc.)
**Output**: AgentProfile, error
**Logic**:
1. Validate name not empty
2. Generate unique ID (timestamp + random)
3. Initialize Pointers map with defaults
4. Set CreatedAt/UpdatedAt to now
5. Return profile

**Edge Cases**:
- Empty name → Return validation error
- Duplicate ID (very rare) → Regenerate

##### 3.1.4 GetActiveProfile
**Input**: None (uses loaded config)
**Output**: *AgentProfile, error
**Logic**:
1. Get DefaultProfile ID from config
2. Search Profiles array for matching ID
3. If found, return pointer to profile
4. If not found, return first profile (fallback)
5. If no profiles, return error

**Edge Cases**:
- Default not found → Use first profile
- No profiles at all → Return error
- DefaultProfile empty string → Use first profile

##### 3.1.5 ResolveAlias
**Input**: ModelAlias, *AgentProfile
**Output**: ModelPointer, error
**Logic**:
1. Check profile not nil
2. Look up alias in profile.Pointers map
3. If found, return ModelPointer
4. If not found, return error

**Edge Cases**:
- Nil profile → Return error
- Alias not in map → Return "alias not configured" error
- Pointer has empty Provider/Model → Return validation error

---

### Level 4: Settings UI Layer

#### 4.1 State Management

**What we need to track**:
```go
// In types.go State struct
ProfilesState        string    // "list", "action_menu", "edit", "edit_role", "preview"
ProfilesSelected     int       // Index in profiles array (-1 = "New Profile")
ProfilesActionChoice int       // Selected action in menu
ProfilesFormField    int       // Current field in edit form
ProfilesRoleEditing  ModelAlias // Which role is being configured

// Form buffers
ProfilesFormID          string
ProfilesFormName        string
ProfilesFormDescription string
ProfilesFormIcon        string
ProfilesFormColor       string

// Role editing buffers
ProfilesRoleProvider string
ProfilesRoleModel    string
ProfilesRolePrompt   string
```

**Logic**:
- State machine: list → action_menu → edit → edit_role → back
- Selected index tracks current item
- Form buffers hold temporary edits
- Save commits buffers to actual profile

#### 4.2 UI State Machine

**States and Transitions**:

```
list
  ↓ (enter on profile)
action_menu
  ↓ (select "Edit")
edit (profile metadata)
  ↓ (enter on "Configure Roles")
edit_role (specific alias configuration)
  ↓ (esc)
edit
  ↓ (esc)
action_menu
  ↓ (esc)
list
```

**Logic for each state**:

##### State: "list"
**Display**:
- "New Profile" button at top
- List of existing profiles with icons
- Show ✓ next to default profile
- Show model count summary

**Keys**:
- ↑/↓: Navigate
- Enter: Open action menu (or create if on "New Profile")
- n: Quick create
- Esc: Exit to settings menu

**Logic**:
```
if key == "up":
    if ProfilesSelected > -1:
        ProfilesSelected--
    
if key == "down":
    if ProfilesSelected < len(profiles) - 1:
        ProfilesSelected++

if key == "enter":
    if ProfilesSelected == -1:
        // Create new profile
        initializeFormBuffers()
        ProfilesState = "edit"
    else:
        // Open action menu
        ProfilesActionChoice = 0
        ProfilesState = "action_menu"
```

##### State: "action_menu"
**Display**:
- Profile info summary
- Action options: Edit, Set Default, Clone, Delete, Preview

**Keys**:
- ↑/↓: Navigate actions
- Enter: Confirm action
- Esc: Back to list

**Logic**:
```
if key == "enter":
    switch ProfilesActionChoice:
        case 0: // Edit
            loadProfileIntoFormBuffers(selectedProfile)
            ProfilesState = "edit"
        case 1: // Set Default
            setDefaultProfile(selectedProfile.ID)
            saveProfiles()
            ProfilesState = "list"
        case 2: // Clone
            newProfile = cloneProfile(selectedProfile)
            addProfile(newProfile)
            saveProfiles()
            ProfilesState = "list"
        case 3: // Delete
            if !selectedProfile.IsDefault:
                deleteProfile(selectedProfile.ID)
                saveProfiles()
            ProfilesState = "list"
        case 4: // Preview
            ProfilesState = "preview"
```

##### State: "edit"
**Display**:
- Form fields: ID, Name, Description, Icon, Color
- "Configure Roles" button
- Save/Cancel buttons

**Keys**:
- ↑/↓: Navigate fields
- Enter: Edit field / Save
- Esc: Cancel

**Logic**:
```
if key == "enter":
    if ProfilesFormField < 5: // On a text field
        enterEditMode(ProfilesFormField)
    else if ProfilesFormField == 5: // Configure Roles
        ProfilesRoleEditing = AliasMain // Start with first role
        ProfilesState = "edit_role"
    else if ProfilesFormField == 6: // Save
        profile = createProfileFromForm()
        if validate(profile):
            saveProfile(profile)
            ProfilesState = "list"
        else:
            showError()
```

##### State: "edit_role"
**Display**:
- Role name (e.g., "Main Agent")
- Provider dropdown
- Model dropdown (filtered by provider)
- System Prompt textarea
- Capabilities sliders (MaxTokens, Temperature, Timeout)

**Keys**:
- ↑/↓: Navigate fields
- ←/→: Adjust sliders
- Enter: Edit field / Save role
- Esc: Back to edit

**Logic**:
```
if key == "enter":
    if on provider dropdown:
        showProviderOptions()
        cycleProvider()
    else if on model dropdown:
        showModelOptions(selectedProvider)
        cycleModel()
    else if on save:
        pointer = createPointerFromForm()
        profile.Pointers[ProfilesRoleEditing] = pointer
        ProfilesState = "edit"
```

---

### Level 5: SDK Integration Layer

#### 5.1 Agent Factory Modifications

**Current Behavior**:
```go
// Hardcoded model selection
sdk, err := NewSDKIntegration("anthropic", "claude-opus-4-20250514")
```

**Desired Behavior**:
```go
// Profile-based selection
sdk, err := NewSDKIntegrationWithProfile(profileID)
```

**Implementation**:

##### 5.1.1 Add Profile to SDKIntegration
```go
type SDKIntegration struct {
    // ... existing fields
    activeProfile *AgentProfile
    profileMgr    *ProfileManager
}
```

**Logic**:
1. On initialization, load active profile
2. Store reference to profile and manager
3. Use profile for all agent creation

##### 5.1.2 CreateAgentForRole (New Method)
**Input**: ModelAlias (role)
**Output**: *agent.Agent, error

**Logic**:
```go
func (s *SDKIntegration) CreateAgentForRole(ctx context.Context, role ModelAlias) (*agent.Agent, error) {
    // Step 1: Validate profile loaded
    if s.activeProfile == nil {
        return nil, fmt.Errorf("no active profile loaded")
    }
    
    // Step 2: Resolve alias → ModelPointer
    pointer, err := s.profileMgr.ResolveAlias(role, s.activeProfile)
    if err != nil {
        return nil, fmt.Errorf("failed to resolve role %s: %w", role, err)
    }
    
    // Step 3: Create provider config
    providerConfig := provider.Config{
        Name:  pointer.Provider,
        Model: pointer.Model,
    }
    
    // Step 4: Choose factory method based on role
    switch role {
    case AliasMain:
        return s.factory.CreateWorker(ctx, agent.WorkerConfig{
            AgentID:        fmt.Sprintf("main-%d", time.Now().Unix()),
            ProviderConfig: providerConfig,
            SystemPrompt:   pointer.SystemPrompt, // Use override if set
            Tools:          []string{"*"},
            MaxTurns:       getOrDefault(pointer.Capabilities.MaxTurns, 0),
            Timeout:        getOrDefault(pointer.Capabilities.Timeout, 300),
            Temperature:    getOrDefault(pointer.Capabilities.Temperature, 0.7),
        })
        
    case AliasSteering:
        return s.factory.CreateSteering(ctx, agent.SteeringAgentConfig{
            // Similar configuration
        })
        
    case AliasBackground:
        return s.factory.CreateBackground(ctx, agent.BackgroundConfig{
            // Similar configuration
        })
        
    case AliasSubAgent:
        return s.factory.CreateSubAgent(ctx, agent.SubAgentConfig{
            // Similar configuration
        })
        
    default:
        // For thinking/long_context, use Worker with custom config
        return s.factory.CreateWorker(ctx, agent.WorkerConfig{
            // Custom configuration
        })
    }
}
```

**Edge Cases**:
- Nil profile → Error
- Role not configured → Error with helpful message
- Invalid provider → Factory returns error
- Invalid model → Factory returns error

##### 5.1.3 Update Agent Creation Callsites

**Before**:
```go
agent, err := factory.CreateWorker(ctx, config)
```

**After**:
```go
agent, err := sdk.CreateAgentForRole(ctx, AliasMain)
```

**Locations to update**:
- Main agent creation in SDK initialization
- Sub-agent delegation (TaskTool equivalent)
- Background agent spawning
- Steering agent creation

---

### Level 6: Built-in Profiles

#### 6.1 Default Profiles Generator

**Function**: `GenerateBuiltinProfiles() []AgentProfile`

**Logic**:
```go
func GenerateBuiltinProfiles() []AgentProfile {
    profiles := []AgentProfile{
        {
            ID:          "balanced",
            Name:        "Balanced",
            Description: "Optimal balance of quality and speed",
            Icon:        "⚖️",
            Color:       "#3B82F6",
            IsDefault:   true,
            Pointers: map[ModelAlias]ModelPointer{
                AliasMain: {
                    Provider: "anthropic",
                    Model:    "claude-sonnet-4-20250514",
                    Capabilities: &AgentCapabilities{
                        MaxTokens:   32768,
                        Temperature: 0.7,
                        Timeout:     300,
                    },
                },
                // ... all other aliases
            },
            CreatedAt: time.Now(),
            UpdatedAt: time.Now(),
        },
        // Performance profile
        // Quality profile
        // Cost-Optimized profile
    }
    return profiles
}
```

**When to use**:
- First run (no profiles file exists)
- Profiles file corrupt
- User requests "Reset to defaults"

---

## Data Flow

### Profile Loading (Startup)
```
1. App starts
   ↓
2. SettingsManager.NewManager()
   ↓
3. ProfileSettings.NewProfileSettings()
   ↓
4. ProfileManager.LoadProfiles("~/.swarmos/agent_profiles.json")
   ↓
5. File exists?
   YES → Parse JSON
   NO  → GenerateBuiltinProfiles()
   ↓
6. Validate structure
   ↓
7. Store in memory (ProfileSettings.config)
```

### Agent Creation (Runtime)
```
1. User sends message
   ↓
2. SDK needs to create agent
   ↓
3. sdk.CreateAgentForRole(AliasMain)
   ↓
4. profileMgr.ResolveAlias(AliasMain, activeProfile)
   ↓
5. Look up in activeProfile.Pointers[AliasMain]
   ↓
6. Found? Return ModelPointer
   ↓
7. Extract Provider, Model, SystemPrompt, Capabilities
   ↓
8. Call factory.CreateWorker(config)
   ↓
9. Return agent
```

### Profile Switching (User Action)
```
1. User navigates to Settings → Agent Profiles
   ↓
2. Selects profile "Quality"
   ↓
3. Chooses "Set as Default"
   ↓
4. ProfileManager.SetDefaultProfile("quality")
   ↓
5. Update config.DefaultProfile = "quality"
   ↓
6. ProfileManager.SaveProfiles()
   ↓
7. Marshal to JSON
   ↓
8. Write to disk atomically
   ↓
9. Callback: onProfileChange("quality")
   ↓
10. SDK reloads active profile
   ↓
11. Future agents use new profile
```

### Profile Editing (User Action)
```
1. User selects profile "Balanced"
   ↓
2. Chooses "Edit"
   ↓
3. State → "edit"
   ↓
4. Load profile into form buffers
   ↓
5. User navigates to "Configure Roles"
   ↓
6. State → "edit_role", RoleEditing = AliasMain
   ↓
7. Load current pointer for AliasMain
   ↓
8. User changes Provider = "openai", Model = "gpt-4o"
   ↓
9. User saves
   ↓
10. Update profile.Pointers[AliasMain]
   ↓
11. User navigates back, saves profile
   ↓
12. ProfileManager.UpdateProfile(profile)
   ↓
13. Find profile in config.Profiles by ID
   ↓
14. Replace with updated profile
   ↓
15. SaveProfiles()
```

---

## Implementation Phases

### Phase 1: Data Layer (Bottom)
**Goal**: Data structures and JSON persistence work correctly.

**Tasks**:
1. ✅ Create `todo/AGENT_PROFILE_SYSTEM_PLAN.md`
2. Create `internal/chat/settings/agent_profiles_types.go`
   - Define ModelAlias enum
   - Define ModelPointer struct
   - Define AgentProfile struct
   - Define ProfilesConfig struct
3. Create `internal/chat/settings/agent_profiles.go`
   - Implement ProfileManager struct
   - Implement LoadProfiles()
   - Implement SaveProfiles()
   - Implement GenerateBuiltinProfiles()
4. Write unit tests for data layer
   - Test JSON marshal/unmarshal
   - Test validation logic
   - Test edge cases

**Success Criteria**:
- Can create profile in memory
- Can save to JSON file
- Can load from JSON file
- Built-in profiles generate correctly

### Phase 2: Business Logic Layer
**Goal**: CRUD operations work correctly.

**Tasks**:
1. Implement CreateProfile()
2. Implement GetActiveProfile()
3. Implement ResolveAlias()
4. Implement UpdateProfile()
5. Implement DeleteProfile()
6. Implement SetDefaultProfile()
7. Write unit tests for business logic

**Success Criteria**:
- Can create/read/update/delete profiles
- Active profile resolves correctly
- Aliases resolve to model pointers
- Default profile changes persist

### Phase 3: Settings UI Layer
**Goal**: Users can manage profiles through UI.

**Tasks**:
1. Add state fields to `types.go`
2. Create ProfileSettings struct in `agent_profiles.go`
3. Implement Render() method
   - renderList()
   - renderActionMenu()
   - renderEditForm()
   - renderEditRole()
   - renderPreview()
4. Implement HandleKey() method
   - handleListKeys()
   - handleActionMenuKeys()
   - handleEditKeys()
   - handleEditRoleKeys()
5. Wire into SettingsManager
   - Add to NewManager()
   - Add to Render() switch
   - Add to HandleKey() switch
6. Manual UI testing

**Success Criteria**:
- Can navigate all screens
- Can create/edit/delete profiles
- Changes persist on save
- UI is intuitive

### Phase 4: SDK Integration Layer (Top)
**Goal**: Agents use profiles for model selection.

**Tasks**:
1. Add activeProfile to SDKIntegration
2. Implement CreateAgentForRole()
3. Update agent creation callsites
   - Main agent creation
   - Sub-agent creation (if used)
   - Background agent creation (if used)
4. Add profile change callback
5. Test agent creation with different profiles
6. Integration testing

**Success Criteria**:
- Agent creation uses profile
- Switching profiles changes models
- All agent types work
- Fallback works if profile missing

### Phase 5: Polish & Documentation
**Goal**: System is production-ready.

**Tasks**:
1. Add profile switcher to status bar
2. Add error handling and messages
3. Add logging for debugging
4. Write user documentation
5. Update SWARM.md if needed
6. Final testing and bug fixes

**Success Criteria**:
- No crashes or data loss
- Clear error messages
- Good performance
- Documentation complete

---

## Testing Strategy

### Unit Tests

#### Data Layer
```go
func TestProfileMarshaling(t *testing.T) {
    profile := AgentProfile{
        ID: "test",
        Name: "Test Profile",
        Pointers: map[ModelAlias]ModelPointer{
            AliasMain: {Provider: "anthropic", Model: "claude-opus-4"},
        },
    }
    
    // Marshal
    data, err := json.Marshal(profile)
    assert.NoError(t, err)
    
    // Unmarshal
    var loaded AgentProfile
    err = json.Unmarshal(data, &loaded)
    assert.NoError(t, err)
    
    // Compare
    assert.Equal(t, profile.ID, loaded.ID)
    assert.Equal(t, profile.Pointers[AliasMain].Provider, 
                 loaded.Pointers[AliasMain].Provider)
}

func TestLoadProfilesFileNotExists(t *testing.T) {
    mgr := NewProfileManager()
    config, err := mgr.LoadProfiles("/nonexistent/path.json")
    
    assert.NoError(t, err) // Should succeed with defaults
    assert.NotEmpty(t, config.Profiles)
    assert.True(t, len(config.Profiles) >= 4) // Built-ins
}
```

#### Business Logic
```go
func TestResolveAlias(t *testing.T) {
    profile := &AgentProfile{
        Pointers: map[ModelAlias]ModelPointer{
            AliasMain: {Provider: "anthropic", Model: "opus"},
        },
    }
    
    mgr := NewProfileManager()
    
    // Valid alias
    pointer, err := mgr.ResolveAlias(AliasMain, profile)
    assert.NoError(t, err)
    assert.Equal(t, "anthropic", pointer.Provider)
    
    // Invalid alias
    _, err = mgr.ResolveAlias(AliasSteering, profile)
    assert.Error(t, err) // Should error - not configured
}

func TestSetDefaultProfile(t *testing.T) {
    config := ProfilesConfig{
        DefaultProfile: "balanced",
        Profiles: []AgentProfile{
            {ID: "balanced"},
            {ID: "quality"},
        },
    }
    
    mgr := NewProfileManager()
    mgr.config = config
    
    err := mgr.SetDefaultProfile("quality")
    assert.NoError(t, err)
    assert.Equal(t, "quality", mgr.config.DefaultProfile)
}
```

### Integration Tests

#### SDK Integration
```go
func TestAgentCreationWithProfile(t *testing.T) {
    // Create test profile
    profile := &AgentProfile{
        ID: "test",
        Pointers: map[ModelAlias]ModelPointer{
            AliasMain: {
                Provider: "anthropic",
                Model: "claude-sonnet-4-20250514",
            },
        },
    }
    
    // Initialize SDK with profile
    sdk := &SDKIntegration{
        activeProfile: profile,
        profileMgr: NewProfileManager(),
    }
    
    // Create agent
    agent, err := sdk.CreateAgentForRole(context.Background(), AliasMain)
    
    assert.NoError(t, err)
    assert.NotNil(t, agent)
    // Verify agent uses correct model
}
```

### Manual Testing Checklist

#### Settings UI
- [ ] Navigate to Settings → Agent Profiles
- [ ] See list of built-in profiles
- [ ] Create new profile
- [ ] Edit profile metadata
- [ ] Configure each role (all 6 aliases)
- [ ] Clone existing profile
- [ ] Delete custom profile
- [ ] Set default profile
- [ ] Exit and re-enter (persistence)

#### SDK Integration
- [ ] Send message with "Balanced" profile active
- [ ] Verify correct model used
- [ ] Switch to "Quality" profile
- [ ] Send message again
- [ ] Verify different model used
- [ ] Check logs for model selection

#### Error Handling
- [ ] Corrupt profiles file → Should use defaults
- [ ] Delete all profiles → Should recreate defaults
- [ ] Set invalid default → Should fallback gracefully
- [ ] Configure invalid model → Should show error

---

## Edge Cases & Error Handling

### File System Errors
1. **Profiles file corrupt**
   - Detect: JSON parse fails
   - Handle: Log error, use built-in defaults, notify user
   - Recovery: User can reset to defaults

2. **No write permission**
   - Detect: SaveProfiles() fails
   - Handle: Show error message, keep changes in memory
   - Recovery: User fixes permissions, tries again

3. **Disk full**
   - Detect: Write fails
   - Handle: Show error, keep changes in memory
   - Recovery: User frees space, tries again

### Data Validation Errors
1. **Empty profile name**
   - Detect: Validation in CreateProfile()
   - Handle: Show error in UI, prevent save
   - Recovery: User enters valid name

2. **Invalid alias mapping**
   - Detect: ResolveAlias() returns error
   - Handle: Show error, use default model
   - Recovery: User reconfigures profile

3. **Invalid provider/model**
   - Detect: Agent factory returns error
   - Handle: Show error in UI, fallback to default
   - Recovery: User selects valid model

### Runtime Errors
1. **No profiles loaded**
   - Detect: GetActiveProfile() returns nil
   - Handle: Generate defaults, log warning
   - Recovery: Automatic

2. **Default profile missing**
   - Detect: DefaultProfile ID not in Profiles
   - Handle: Use first profile as default
   - Recovery: Automatic

3. **Alias not configured**
   - Detect: ResolveAlias() fails
   - Handle: Use fallback model, log warning
   - Recovery: User configures alias or uses default

---

## Success Metrics

### Functional
- ✅ User can create 5+ custom profiles
- ✅ User can switch between profiles
- ✅ Agents use correct models per profile
- ✅ Changes persist across app restarts

### Performance
- ✅ Profile load time < 50ms
- ✅ Profile save time < 100ms
- ✅ UI renders in < 16ms (60 FPS)

### Reliability
- ✅ No data loss on crash
- ✅ Graceful degradation on errors
- ✅ No crashes from invalid input

### Usability
- ✅ New users understand profiles
- ✅ Clear error messages
- ✅ Intuitive navigation
- ✅ Minimal clicks to switch profiles

---

## Appendix: File Structure

```
swarmos/
├── todo/
│   └── AGENT_PROFILE_SYSTEM_PLAN.md        (this file)
├── internal/chat/settings/
│   ├── agent_profiles_types.go             (data structures)
│   ├── agent_profiles.go                   (business logic + UI)
│   ├── manager.go                          (integration)
│   └── types.go                            (state additions)
├── internal/chat/
│   ├── sdk_integration.go                  (agent factory changes)
│   └── settings_screen.go                  (routing)
└── ~/.swarmos/
    └── agent_profiles.json                 (persisted data)
```

---

## Next Steps

1. ✅ Create this plan document
2. Create git branch `profile-testing`
3. Begin Phase 1: Data Layer implementation
4. Test data layer thoroughly
5. Proceed to Phase 2, then 3, then 4, then 5
6. Final testing and polish
7. Merge to main

---

*Last Updated: 2025-01-XX*
*Author: AI Assistant*
*Status: Planning Complete → Ready for Implementation*
