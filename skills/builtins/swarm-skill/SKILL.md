---
name: swarm-skill
description: Teaches agents how to author, install, and manage Swarm skills. Covers the SKILL.md format, frontmatter fields, trigger types, hooks, scripts, references, and the difference between local (project-scoped) and global (user-scoped) skill installation at ~/.swarmos/skills/ or .swarm/skills/.
version: 1.0.0
author: Swarm SDK
category: meta
tags:
  - skills
  - authoring
  - meta
  - documentation
enabled: true
priority: 100
---
# Swarm Skill Authoring

A **skill** is a directory that packages instructions, scripts, and references so agents can discover and load them dynamically. Skills are injected into the agent system prompt when active, giving the agent context-specific knowledge and capabilities.

## Skill Types

| Type | Location | Scope |
|------|----------|-------|
| **Global** | `~/.swarmos/skills/<name>/` | All sessions for the current user |
| **Local** | `.swarm/skills/<name>/` in project root | Only sessions inside that project |

Global skills are always available. Local skills are scoped to the project and require the project root's `.swarm/skills/` path to be registered as a search path (see SDK integration below).

## Creating a Skill

### 1. Directory Structure

```
<skill-name>/
├── SKILL.md              # Required: skill definition + instructions
├── references/           # Optional: additional documentation
│   ├── REFERENCE.md
│   └── <topic>.md
├── scripts/              # Optional: executable helpers
│   ├── analyze.sh
│   └── setup.py
├── assets/               # Optional: static resources / templates
└── hooks.json            # Optional: hook configuration
```

The directory name becomes the skill name if no `name` is set in frontmatter. Always prefer setting `name` explicitly.

### 2. SKILL.md Format

Every skill requires a `SKILL.md` file at the root of the skill directory. It has two parts: a **YAML frontmatter block** (between `---` delimiters) and a **Markdown body** containing the agent instructions.

```markdown
---
name: my-skill
description: One-sentence summary of what this skill does and when to use it. Max 1024 chars.
version: 1.0.0
author: Your Name
category: development
tags:
  - tag1
  - tag2
enabled: true
priority: 10
license: MIT
compatibility: swarm >= 1.0
allowed-tools: Write Edit Read Bash
triggers:
  - type: file_pattern
    pattern: "*.go"
  - type: tool
    pattern: Write
  - type: keyword
    pattern: refactor
  - type: mode
    pattern: code
---
# My Skill

This section is the agent instruction body. Write clear, imperative guidance here.
Everything in this block is injected verbatim into the agent system prompt when the
skill is active.

## Capabilities
- What the agent can do with this skill

## Workflow
1. Step one
2. Step two
```

### Frontmatter Field Reference

| Field | Required | Type | Limits | Description |
|-------|----------|------|--------|-------------|
| `name` | ✅ | string | 1–64 chars, `^[a-z0-9]+(-[a-z0-9]+)*$` | Unique identifier. Lowercase alphanumeric + hyphens only. No leading/trailing/consecutive hyphens. |
| `description` | ✅ | string | 1–1024 chars | Human-readable summary. Include trigger phrases so agents know when to activate. |
| `version` | no | string | — | Semver string, e.g. `1.0.0` |
| `author` | no | string | — | Skill author name |
| `category` | no | string | — | Grouping label, e.g. `development`, `testing`, `ops` |
| `tags` | no | []string | — | Discovery tags for search |
| `enabled` | no | bool | — | `true` to make the skill available by default on load |
| `priority` | no | int | — | Load order. Higher = loaded first. Default 0. |
| `license` | no | string | ≤ 256 chars | SPDX license identifier or full name |
| `compatibility` | no | string | ≤ 500 chars | Environment/version requirements |
| `allowed-tools` | no | string | space-delimited | Pre-approved tool names for this skill |
| `triggers` | no | []SkillTrigger | — | Auto-activation rules (see below) |
| `dependencies` | no | []string | — | Names of other skills this one depends on |
| `icon` | no | string | — | Emoji or icon identifier for UI |
| `homepage` | no | string | — | URL for more information |

### Trigger Types

Triggers cause a skill to be automatically activated when matching conditions are detected in the agent context.

```yaml
triggers:
  # Activate when the current file matches a glob pattern
  - type: file_pattern
    pattern: "*.go"

  # Activate when a specific tool is being used (exact match, case-insensitive)
  - type: tool
    pattern: Write

  # Activate when a keyword appears in the user message (substring match)
  - type: keyword
    pattern: database migration

  # Activate when the agent is in a specific mode (exact match, case-insensitive)
  - type: mode
    pattern: code
```

Multiple triggers use OR logic — any matching trigger activates the skill.

### Hooks (hooks.json)

Skills can register lifecycle hooks via a `hooks.json` file:

```json
{
  "PreToolUse": [
    {
      "matcher": "Write",
      "type": "command",
      "command": "scripts/pre-write-check.sh",
      "timeout": 10
    }
  ],
  "PostToolUse": [
    {
      "matcher": "Write",
      "type": "prompt",
      "prompt": "Review the file just written for security issues."
    }
  ],
  "Stop": [],
  "SessionStart": [
    {
      "matcher": "*",
      "type": "command",
      "command": "scripts/setup.sh"
    }
  ]
}
```

Hook types:
- `command` — runs a shell command; `command` field sets the executable path relative to the skill dir
- `prompt` — injects an LLM sub-prompt; `prompt` field sets the text

## Installing Skills

### Global Skill (available in all sessions)

```bash
# Create the skill directory under the global install path
mkdir -p ~/.swarmos/skills/my-skill/references
# Write your SKILL.md
cat > ~/.swarmos/skills/my-skill/SKILL.md << 'EOF'
---
name: my-skill
description: My custom skill for doing X.
version: 1.0.0
enabled: true
---
# My Skill
Your instructions here.
EOF
```

### Local Skill (project-scoped)

```bash
# Create inside your project root
mkdir -p .swarm/skills/my-skill
cat > .swarm/skills/my-skill/SKILL.md << 'EOF'
---
name: my-skill
description: Project-local skill for this repo.
version: 1.0.0
enabled: true
---
# My Skill
Instructions scoped to this project.
EOF
```

To make local skills discoverable, register the project path in the SDK:

```go
skillsManager.GetLoader().Registry.AddSearchPath(".swarm/skills")
// Then re-discover:
skillsManager.GetLoader().Registry.DiscoverAll()
```

## Managing Skills

### Slash commands (TUI / headless)

```
/skill list                  List all available skills
/skill info <name>           Show skill details and instructions
/skill activate <name>       Enable a skill for the current session
/skill deactivate <name>     Disable a skill
/skill active                Show all currently active skills
/skill search <query>        Search local registry and marketplace
/skill install <name>        Install from marketplace to ~/.swarmos/skills/
/skill uninstall <name>      Remove an installed skill
/skill update                Refresh marketplace index
```

### Go SDK

```go
// Get the manager (already initialized at startup)
mgr := sdk.GetSkillsManager()

// List all skills
skills := mgr.GetAllSkills()

// Activate / deactivate
mgr.EnableSkill("my-skill")
mgr.DisableSkill("my-skill")

// Check if active
active := mgr.IsSkillActive("my-skill")

// Get active instructions (for system prompt injection)
instructions := mgr.GetActiveInstructions()

// Auto-activate based on context triggers
mgr.AutoActivate(skills.ActivationContext{
    CurrentFile: "main.go",
    CurrentTool: "Write",
    Keywords:    []string{"refactor"},
})
```

## Skill Authoring Best Practices

1. **Write instructions for the agent, not the user.** The body is injected into the system prompt — use imperative voice ("Always validate input", "Prefer X over Y").
2. **Keep descriptions concise but trigger-rich.** The description is used for search and display; pack in relevant keywords.
3. **Use triggers to auto-activate.** Agents shouldn't have to think about enabling skills — triggers fire them contextually.
4. **Put heavy reference material in `references/`.** Keep `SKILL.md` body focused; link to detailed docs in `references/`.
5. **Version your skills.** Increment version when instructions change so users know to update.
6. **Use `priority`** to control load order when skills have dependencies (higher priority loads first).
7. **Test locally first.** Drop the skill in `~/.swarmos/skills/` and run `/skill list` to verify it's discovered correctly.

## SDK Default Skills

Default skills are embedded directly in the `swarm-sdk` binary using `go:embed`. They are registered at loader initialization and are always available without installation. To add a new default skill:

1. Create the directory under `swarm-sdk/skills/builtins/<skill-name>/`
2. Add a valid `SKILL.md`
3. The `defaults.go` embed covers the whole `builtins/` tree automatically