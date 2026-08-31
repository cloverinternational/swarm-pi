# Swarm Skill Authoring — Full Reference

## agentskills.io Specification Compliance

Swarm's skill system follows the [agentskills.io](https://agentskills.io) specification for interoperability. Skills authored to that spec are compatible with Swarm with the following notes:

- `name`: `^[a-z0-9]+(-[a-z0-9]+)*$`, max 64 chars (enforced by `ValidateName`)
- `description`: required, max 1024 chars
- `license`: optional, max 256 chars
- `compatibility`: optional, max 500 chars
- `allowed-tools`: space-delimited list of tool names
- `metadata`: arbitrary key-value map for custom fields

## Complete `SkillMetadata` Go Struct

```go
type SkillMetadata struct {
    Name          string            // required
    Description   string            // required
    Version       string
    Author        string
    Category      string
    Tags          []string
    Dependencies  []string
    Enabled       bool
    Priority      int
    Triggers      []SkillTrigger
    Icon          string
    Homepage      string
    License       string            // max 256 chars
    Compatibility string            // max 500 chars
    AllowedTools  string            // space-delimited
    CustomMetadata map[string]string
}
```

## Trigger System — Deep Dive

Triggers are evaluated by `Loader.AutoActivate(ctx ActivationContext)`. The `ActivationContext` carries:

```go
type ActivationContext struct {
    CurrentFile string   // path of file being edited
    CurrentTool string   // tool being invoked
    CurrentMode string   // agent mode string
    Keywords    []string // words extracted from user message
    ProjectType string   // detected project type
}
```

### `file_pattern` trigger

Uses simple glob matching (supports `*` wildcard). Matches against `ActivationContext.CurrentFile`.

```yaml
triggers:
  - type: file_pattern
    pattern: "*.go"           # any Go file
  - type: file_pattern
    pattern: "migrations/*"   # any file in migrations dir
  - type: file_pattern
    pattern: "*.test.ts"      # TypeScript test files
```

### `tool` trigger

Case-insensitive exact match against `ActivationContext.CurrentTool`. Standard tool names: `Write`, `Edit`, `Read`, `Bash`, `MultiEdit`.

```yaml
triggers:
  - type: tool
    pattern: Write
  - type: tool
    pattern: Bash
```

### `keyword` trigger

Substring match (case-insensitive) against each word in `ActivationContext.Keywords`.

```yaml
triggers:
  - type: keyword
    pattern: security audit
  - type: keyword
    pattern: database migration
  - type: keyword
    pattern: deploy
```

### `mode` trigger

Case-insensitive exact match against `ActivationContext.CurrentMode`.

```yaml
triggers:
  - type: mode
    pattern: code
  - type: mode
    pattern: test
```

## Hook System — Deep Dive

### Hook Types

| Hook event | When it fires |
|------------|--------------|
| `PreToolUse` | Before a tool is executed |
| `PostToolUse` | After a tool completes |
| `Stop` | When the agent session ends |
| `SessionStart` | When a new session begins |

### Hook Config Fields

```go
type SkillHookConfig struct {
    Matcher string  // tool name or pattern to match
    Type    string  // "command" or "prompt"
    Command string  // shell command (for type=command)
    Prompt  string  // LLM text (for type=prompt)
    Timeout int     // seconds (for type=command)
}
```

### Full `hooks.json` Example

```json
{
  "PreToolUse": [
    {
      "matcher": "Write",
      "type": "command",
      "command": "scripts/lint-check.sh",
      "timeout": 30
    }
  ],
  "PostToolUse": [
    {
      "matcher": "Write",
      "type": "prompt",
      "prompt": "Check the file just written for any obvious bugs or security issues. Report findings concisely."
    },
    {
      "matcher": "Bash",
      "type": "command",
      "command": "scripts/post-bash-log.sh",
      "timeout": 5
    }
  ],
  "SessionStart": [
    {
      "matcher": "*",
      "type": "command",
      "command": "scripts/init.sh",
      "timeout": 60
    }
  ],
  "Stop": [
    {
      "matcher": "*",
      "type": "command",
      "command": "scripts/cleanup.sh",
      "timeout": 10
    }
  ]
}
```

## Directory Layout Examples

### Minimal skill

```
my-skill/
└── SKILL.md
```

### Full skill with all optional components

```
my-skill/
├── SKILL.md
├── hooks.json
├── references/
│   ├── REFERENCE.md        # primary reference doc
│   ├── api-cheatsheet.md
│   └── error-codes.json
├── scripts/
│   ├── setup.sh            # SessionStart init script
│   ├── analyze.py          # analysis helper
│   └── teardown.sh         # cleanup script
└── assets/
    ├── templates/
    │   └── component.tsx.tmpl
    └── diagrams/
        └── architecture.png
```

## Install Path Quick Reference

| Path | Scope | Auto-discovered |
|------|-------|----------------|
| `~/.swarmos/skills/<name>/` | Global (all sessions, current user) | ✅ Yes — default install dir |
| `~/.swarmos/skills/skills/<name>/` | Global (subdirectory variant) | ✅ Yes — also scanned |
| `.swarm/skills/<name>/` | Local (project-scoped) | ⚠️ Only if path registered via `AddSearchPath` |
| `swarm-sdk/skills/builtins/<name>/` | Built-in (SDK default) | ✅ Yes — embedded at compile time |

## Name Validation Rules

```
Valid:   my-skill, code-review, db-migrate-helper, v2
Invalid: MySkill        (uppercase)
         my_skill       (underscore)
         -my-skill      (leading hyphen)
         my-skill-      (trailing hyphen)
         my--skill      (consecutive hyphens)
         verylongskillnamethatexceeds64characterlimitforthisnamevalidation  (too long)
```

Use `skills.SanitizeName(input)` from the SDK to auto-correct a string into a valid name.

## Programmatic Skill Registration

```go
import "github.com/Swarm-Code/mono/swarm-sdk/skills"

// Register a skill object directly (no disk needed)
registry.RegisterSkill(&skills.Skill{
    Metadata: skills.SkillMetadata{
        Name:        "my-skill",
        Description: "Does something useful.",
        Version:     "1.0.0",
        Enabled:     true,
        Priority:    50,
    },
    Instructions: "Always do X when Y. Prefer Z over W.",
    Path:         "builtin",
})

// Load from a directory path
skill, err := skills.LoadSkill("/path/to/skill-dir")
registry.RegisterSkill(skill)

// Activate it immediately
registry.Activate("my-skill")
```