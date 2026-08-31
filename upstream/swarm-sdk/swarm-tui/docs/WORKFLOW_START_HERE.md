# 🚀 Workflow Creation - START HERE

You want to create a workflow? Start here.

---

## IMPORTANT: Where to Put Your Files

**Create a `workflows/` directory in YOUR PROJECT DIRECTORY and put `.yaml` files there.**

```
your-project/
├── workflows/              ← CREATE THIS
│   └── my_workflow.yaml    ← PUT YOUR YAML HERE
├── src/
└── README.md
```

When you run `swarmos` from your project directory, it automatically loads workflows from `workflows/` folder.

**For detailed instructions:** See `@docs/WHERE_TO_PUT_WORKFLOW_YAML.md`

---

## The One-Minute Summary

A **workflow** is a YAML file that defines how multiple AI agents work together to solve a problem.

**Three key concepts:**
1. **Groups** - Collections of agents working together
2. **Execution** - How agents work (sequential, parallel, or debating)
3. **Completion** - When a group is done (all succeed, consensus, etc.)

**That's it.** Everything else is details.

---

## Five-Minute Quick Start

### 1. Pick Your Pattern

Which best describes what you need?

```
┌─ ONE AGENT does everything?
│  → Use: Simple Sequential (Template 1)
│
├─ MULTIPLE EXPERTS analyze independently?
│  → Use: Parallel Specialists (Template 2)
│
├─ PLAN → EXECUTE → REVIEW stages?
│  → Use: Multi-Stage (Template 3)
│
├─ Need RIGOROUS DEBATE & IMPROVEMENT?
│  → Use: Adversarial (Template 4)
│
├─ Need SMART ROUTING & QUALITY GATES?
│  → Use: Hybrid with Steering (Template 5)
│
└─ Need USER INPUT & DYNAMIC BEHAVIOR?
   → Use: Interactive Forms (Template 6)
```

### 2. Copy the Template

Go to `WORKFLOW_TEMPLATES.md` and copy the template that matches your pattern.

### 3. Customize (10 minutes)

Change these things:
- `id:` → your workflow name
- `name:` → your display name
- Every `[TASK DESCRIPTION]` → your actual task
- Every `[SPECIALIST DOMAIN]` → actual specialties
- `provider:` and `model:` → your AI providers

### 4. Validate (1 minute)

Check your file against `WORKFLOW_CHECKLIST.md` - just scan the checklist and verify.

### 5. Execute

Run through your workflow runner. Done!

**Total time: 20-30 minutes**

---

## The Documents (Know What You Need)

| Document | Purpose | Read When |
|---|---|---|
| **WORKFLOW_YAML_LANGUAGE.md** | Complete YAML syntax reference | Creating or debugging workflows |
| **WORKFLOW_TEMPLATES.md** | Copy-paste ready templates | Creating a new workflow (START HERE!) |
| **WORKFLOW_CHECKLIST.md** | Validation checklist | Before executing |
| **WORKFLOW_DOCUMENTATION_INDEX.md** | Map of all docs | Looking for something specific |

---

## Real-World Examples

### Example 1: Code Review
"I want multiple experts to review code, each from their angle"

**Pattern**: Parallel Specialists (Template 2)
**Agents**: Security Expert, Performance Expert, Code Quality Expert
**Execution**: All run at the same time
**Completion**: Must all agree (consensus)

### Example 2: Feature Planning
"Plan the feature, implement it, review the result"

**Pattern**: Multi-Stage (Template 3)
**Groups**: 
  1. Planning (create detailed plan)
  2. Implementation (build the plan)
  3. Review (verify quality)
**Execution**: Each stage waits for previous

### Example 3: Architecture Design
"Need everyone to debate and agree before proceeding"

**Pattern**: Adversarial (Template 4)
**Agents**: Proposer, Skeptic, Supporter
**Execution**: Debate until consensus
**Flow**: Proposal → Critique → Defense → Refinement

### Example 4: Research
"Get research from different angles, combine into report"

**Pattern**: Parallel Specialists (Template 2)
**Agents**: Technical, Business, Risk researchers
**Execution**: All research at same time
**Completion**: All complete, then synthesize

---

## Anatomy of a Workflow (What Goes Where)

```yaml
# ========== IDENTIFICATION ==========
id: my_workflow          # Unique name
name: My Workflow        # Display name
version: 1.0.0          # Version

# ========== GLOBAL SETTINGS ==========
config:
  max_duration: "30m"   # Workflow timeout

# ========== THE REAL STUFF ==========
groups:                # Collections of agents
  - id: stage_1        # First group
    execution: parallel # How they work together
    agents:            # The AI agents
      - id: agent_1
        system_prompt: "You are..."  # Agent instructions
        model: gpt-4   # Which AI model
        tools: [...]   # What they can do

  - id: stage_2        # Second group
    depends_on: [stage_1]  # Depends on first group
    # ... more config
```

---

## Four Common Mistakes (Don't Do These)

❌ **Mistake 1: Vague system prompts**
```yaml
system_prompt: "Review the code"  # TOO VAGUE
```
✅ **Fix:**
```yaml
system_prompt: |
  You are a security reviewer.
  Find: vulnerability, auth issues, data protection concerns.
  Rate severity: Low/Medium/High/Critical
```

---

❌ **Mistake 2: Hardcoded model versions**
```yaml
model: gpt-4-20240314         # Will go obsolete!
model: claude-opus-20240229   # Will go obsolete!
```
✅ **Fix:**
```yaml
model: gpt-4           # Tier name, stays current
model: claude-opus     # Tier name, stays current
```

---

❌ **Mistake 3: No tool access**
```yaml
agents:
  - id: worker
    tools: []  # Empty! Agent can't do anything
```
✅ **Fix:**
```yaml
agents:
  - id: worker
    tools: [Bash, Read, Grep]  # Agent needs tools
```

---

❌ **Mistake 4: Unrealistic timeouts**
```yaml
groups:
  - timeout: "1m"  # Way too short for analysis!
```
✅ **Fix:**
```yaml
groups:
  - timeout: "10m"  # Realistic time
```

---

## Decision Tree: Which Pattern Do I Need?

```
START HERE
    │
    ├─ Is it ONE agent?
    │  ├─ YES → Simple Sequential (Template 1)
    │  └─ NO → Continue...
    │
    ├─ Do agents work independently?
    │  ├─ YES → Parallel Specialists (Template 2)
    │  └─ NO → Continue...
    │
    ├─ Is there a clear pipeline? (A → B → C)
    │  ├─ YES → Multi-Stage (Template 3)
    │  └─ NO → Continue...
    │
    ├─ Do agents need to debate?
    │  ├─ YES → Adversarial (Template 4)
    │  └─ NO → Continue...
    │
    ├─ Do you need smart routing?
    │  ├─ YES → Hybrid with Steering (Template 5)
    │  └─ NO → Continue...
    │
    └─ Does user provide input?
       ├─ YES → Interactive Forms (Template 6)
       └─ NO → Re-evaluate...
```

---

## The Simplest Workflow (Copy This)

Here's the absolute minimum working workflow:

```yaml
id: hello_workflow
name: Hello Workflow
version: 1.0.0

config:
  max_duration: "10m"

groups:
  - id: main
    execution: sequential
    timeout: "8m"
    completion:
      type: all
    agents:
      - id: worker
        name: Worker
        provider: openai
        model: gpt-4
        system_prompt: "Complete the task."
        tools: [Bash, Read]
        capabilities:
          max_tokens: 2000
          temperature: 0.5
```

Copy this, change `id:` and `name:`, customize `system_prompt:`, save it, and you have a workflow.

---

## Field Meanings (Just the Important Ones)

| Field | Means | Example |
|---|---|---|
| `id:` | Unique name | `my_workflow` |
| `name:` | Display title | `My Awesome Workflow` |
| `execution:` | How agents work | `sequential`, `parallel` |
| `provider:` | AI provider | `openai`, `anthropic` |
| `model:` | AI model | `gpt-4`, `claude-opus` |
| `system_prompt:` | Agent instructions | "You are a..." |
| `tools:` | Agent capabilities | `[Bash, Read, Grep]` |
| `max_tokens:` | Response length | `4000` |
| `temperature:` | Creativity (0=precise, 1=creative) | `0.5` |
| `depends_on:` | Which group runs first | `["stage1"]` |
| `threshold:` | Agreement needed | `0.7` (70%) |

---

## When You Get Stuck

1. **Read the field explanation** in `WORKFLOW_YAML_LANGUAGE.md`
2. **Check a template** in `WORKFLOW_TEMPLATES.md`
3. **Validate** against `WORKFLOW_CHECKLIST.md`
4. **Compare** to existing workflows in your directory

---

## You're Ready!

Now:
1. Go to `WORKFLOW_TEMPLATES.md`
2. Pick a template
3. Copy the code
4. Customize it
5. Validate with checklist
6. Execute

**You now know everything you need.** The rest is details.

---

### More Info?

- `WORKFLOW_YAML_LANGUAGE.md` - Deep dive on YAML syntax
- `WORKFLOW_TEMPLATES.md` - More template examples
- `WORKFLOW_CHECKLIST.md` - Validation before running
- `WORKFLOW_DOCUMENTATION_INDEX.md` - Complete index

---

**Good luck! 🚀**
