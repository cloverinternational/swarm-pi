# Agent Profile System - User Guide

## What Are Agent Profiles?

**Agent Profiles** let you use different AI models for different types of work. Instead of one model doing everything, you can assign specialized models to specific tasks:

- **Fast models** for quick subtasks
- **Powerful models** for complex reasoning  
- **Economical models** for background work
- **Long-context models** for large files

## Quick Start

### Accessing Profiles

1. Press **`Ctrl+,`** (or your settings key) to open Settings
2. Navigate to **"Agent Profiles"** in the sidebar
3. Press **Tab** to focus on the content area

### Your First Profile

You'll see 4 built-in profiles ready to use:

- ⚖️ **Balanced** (default) - Good mix of speed and quality
- ⭐ **Quality** - Best models for production work
- 🚀 **Performance** - Fastest models for rapid iteration
- 💰 **Cost-Optimized** - Cheapest models to save money

### Switching Profiles

1. Select a profile with **↑/↓** arrows
2. Press **Enter** to open actions
3. Choose **"Set as Default"**
4. You'll see: ✅ _"Quality is now the default profile"_
5. The status bar updates: `⭐ Quality | Claude Code / ...`

## Understanding Agent Roles

Each profile configures **6 different agent roles**:

### Main Agent
**Purpose:** Handles primary user interactions and conversations  
**When used:** Every message you send  
**Recommended:** Balanced, capable model (Claude Sonnet, GPT-4)

### Steering Agent  
**Purpose:** Quality control - evaluates and guides other agents' work  
**When used:** Complex multi-step tasks  
**Recommended:** Expensive, smart model (Claude Opus, GPT-4o)

### Background Agent
**Purpose:** Long-running async tasks that don't block the UI  
**When used:** Compiling, testing, large file operations  
**Recommended:** Mid-tier model (doesn't need to be fastest)

### Sub-Agent
**Purpose:** Quick specialized tasks delegated from main agent  
**When used:** Code formatting, simple searches, quick edits  
**Recommended:** Fast, cheap model (Haiku, Llama 3.3)

### Thinking Agent
**Purpose:** Deep reasoning and planning for complex problems  
**When used:** Architectural decisions, debugging complex issues  
**Recommended:** Expensive model with extended thinking (Opus)

### Long Context Agent
**Purpose:** Processing large files or conversations (100k+ tokens)  
**When used:** Analyzing entire codebases, long documents  
**Recommended:** Model with large context window (Sonnet, Gemini)

## Creating Custom Profiles

### Method 1: Clone and Customize

1. Select a built-in profile (e.g., "Balanced")
2. Press **Enter** → **Clone**
3. Profile is copied as "Balanced (Copy)"
4. Select the copy → **Edit**
5. Modify settings → **Configure Roles**
6. Adjust models for each role
7. Save and set as default

### Method 2: Create from Scratch

1. Navigate to **"+ New Profile"** at the top
2. Press **Enter**
3. Fill in:
   - **Name:** e.g., "Production"
   - **Description:** When to use this profile
   - **Icon:** Single emoji (⚙️, 🎯, etc.)
   - **Color:** Hex color code (#3B82F6)
4. **Configure Roles** → Set provider/model for each role
5. **Save**

## Configuring Roles

For each role, you can configure:

### Basic Settings
- **Provider:** anthropic, openai, cerebras, etc.
- **Model:** Specific model (claude-opus-4, gpt-4o, etc.)
- **System Prompt:** Optional custom instructions

### Advanced Capabilities
- **Max Tokens:** Output length limit (0 = provider default)
- **Temperature:** Creativity (0.0 = deterministic, 1.0 = creative)
- **Max Turns:** Conversation limit (0 = unlimited)
- **Timeout:** Seconds before giving up (300 = 5 minutes)

## Example Workflows

### Developer with Budget Constraints
**Profile: "Economical"**
- Main: Haiku (cheap, fast enough)
- Steering: Sonnet (good quality check)
- Background: Haiku (doesn't matter if slow)
- Sub-Agent: Llama 3.3 (free tier!)
- Thinking: Sonnet (for hard problems)
- Long Context: Sonnet (large window)

### Enterprise Production Work
**Profile: "Zero Errors"**
- Main: Opus (best quality)
- Steering: Opus (double-check everything)
- Background: Sonnet (balanced)
- Sub-Agent: Sonnet (no cheap mistakes)
- Thinking: Opus (extended thinking enabled)
- Long Context: Opus (handle anything)

### Rapid Prototyping
**Profile: "Speed Demon"**
- Main: Llama 3.3 (instant responses)
- Steering: Sonnet (catch major issues)
- Background: Llama 3.3 (who cares)
- Sub-Agent: Llama 3.3 (go fast!)
- Thinking: Llama 3.3 (quick ideas)
- Long Context: Sonnet (when needed)

## Tips & Tricks

### Smart Cloning
💡 **Tip:** Clone the closest built-in profile to your needs, then tweak 1-2 roles instead of configuring from scratch.

### Profile Switching
You can switch profiles mid-conversation! The next agent created will use the new profile's settings.

### Testing Profiles
Create a "Test" profile to experiment with new models without affecting your working profile.

### Model Selection
- **Anthropic Claude:** Best for coding, writing, complex tasks
- **OpenAI GPT:** Good all-rounders, strong at creative tasks  
- **Cerebras Llama:** Free tier, very fast, good for simple tasks
- **Google Gemini:** Long context windows, good for large files

### Validation
The system validates:
- ✅ Profile names can't be empty
- ✅ Can't delete the last profile
- ✅ Can't delete the default profile (change default first)
- ✅ Invalid models show clear error messages

## Keyboard Shortcuts

### List View
- **↑/↓ or k/j:** Navigate profiles
- **Enter:** Open action menu
- **n:** Quick new profile
- **Esc:** Back to settings

### Action Menu
- **↑/↓:** Navigate options
- **Enter:** Execute action
- **Esc:** Cancel

### Edit Forms
- **↑/↓:** Navigate fields
- **Enter:** Edit field or save
- **Esc:** Cancel and go back
- **Backspace:** Delete character
- **←/→:** Move cursor
- **Home/End:** Jump to start/end

## Troubleshooting

### Profile Not Loading
**Symptom:** Status bar doesn't show profile  
**Fix:** Check `~/.swarmos/agent_profiles.json` exists and is valid JSON

### Changes Not Saving
**Symptom:** Edits disappear after closing settings  
**Fix:** Press **Save** button in edit form (field 6)

### Role Configuration Missing
**Symptom:** Role shows "(not set)"  
**Fix:** That role hasn't been configured yet. Enter provider/model and save.

### Invalid Model Error
**Symptom:** Error when creating agent  
**Fix:** Check model name matches exactly (case-sensitive). See provider docs for valid models.

## File Locations

- **Profiles:** `~/.swarmos/agent_profiles.json`
- **Format:** JSON with profile array and default ID
- **Backup:** Copy this file to save your profiles!

## Advanced: JSON Structure

```json
{
  "default_profile": "balanced",
  "profiles": [
    {
      "id": "balanced",
      "name": "Balanced",
      "description": "Good mix of speed and quality",
      "icon": "⚖️",
      "color": "#3B82F6",
      "is_default": true,
      "pointers": {
        "main": {
          "provider": "anthropic",
          "model": "claude-sonnet-4-20250514",
          "capabilities": {
            "max_tokens": 32768,
            "temperature": 0.7,
            "timeout": 300
          }
        }
      }
    }
  ]
}
```

You can edit this file directly if you prefer!

## Best Practices

1. **Start with built-ins** - They're well-tuned for common use cases
2. **Clone before customizing** - Keep the originals as reference
3. **Name profiles clearly** - "Work", "Personal", "Testing" etc.
4. **Document your profiles** - Use the description field!
5. **Test before deploying** - Try new profiles on non-critical work first
6. **Back up your config** - Copy `agent_profiles.json` before major changes

## Getting Help

- **In-app hints:** Blue info boxes guide first-time users
- **Error messages:** Show exactly what went wrong
- **Success messages:** Confirm every action worked
- **This guide:** Reference for all features

---

**Happy profiling!** 🚀

The profile system lets you optimize for exactly what you need: speed, quality, cost, or anything in between. Experiment and find your perfect setup!
