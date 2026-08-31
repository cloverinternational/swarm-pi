# Workflow Documentation Index

Complete guide to understanding, creating, and using workflows.

---

## 📚 Documentation Files

### 1. **WORKFLOW_YAML_LANGUAGE.md** - Start Here!
The definitive guide to the workflow YAML language itself.

**What it covers:**
- Complete YAML structure and syntax
- Every field explained with examples
- Execution types (sequential, parallel, adversarial)
- Completion criteria
- Steering and quality gates
- Best practices
- Validation rules

**Who should read it:**
- Anyone creating workflows
- Anyone who wants to understand workflows deeply
- Best as a reference document

---

### 2. **WORKFLOW_TEMPLATES.md** - Copy & Paste Ready
Six complete, working workflow templates for common patterns.

**Templates included:**
1. Simple single-agent task
2. Parallel specialists (consensus)
3. Sequential multi-stage (Plan → Execute → Review)
4. Adversarial debate & refinement
5. Hybrid with steering & quality gates
6. Interactive forms with dynamic behavior

**Who should use it:**
- Anyone creating a new workflow
- Start by copying a template, then customize
- Each template is production-ready

---

### 3. **WORKFLOW_CHECKLIST.md** - Verification Tool
Practical checklist to ensure your workflow is correct.

**What it includes:**
- Pre-creation checklist
- Structure verification checklist
- Common mistakes to avoid
- Naming conventions
- Timeout and parameter guidelines
- Troubleshooting quick guide
- Final validation checklist

**Who should use it:**
- Before executing any workflow
- While creating or modifying workflows
- Agents checking other agents' workflows

---

## 🚀 Quick Start Guide

### For Someone Creating a Workflow

1. **Read**: First 2 sections of `WORKFLOW_YAML_LANGUAGE.md` (overview + file structure)
2. **Choose**: Pick a template from `WORKFLOW_TEMPLATES.md` that matches your needs
3. **Copy**: Copy the template YAML to your workflows directory
4. **Customize**: Edit the template for your specific task
5. **Validate**: Use checklist from `WORKFLOW_CHECKLIST.md`
6. **Execute**: Run your workflow

**Time to first workflow**: 20-30 minutes

---

### For Someone Understanding Workflows

1. **Read**: `WORKFLOW_YAML_LANGUAGE.md` completely
2. **Study**: Example workflows in your workflows directory
3. **Reference**: Use `WORKFLOW_TEMPLATES.md` when you need specific patterns
4. **Validate**: Check against `WORKFLOW_CHECKLIST.md`

**Time to understand deeply**: 60-90 minutes

---

### For Someone Reviewing a Workflow

1. **Check**: Use `WORKFLOW_CHECKLIST.md`
2. **Verify**: All sections are present and correct
3. **Understand**: Read the `system_prompt` for each agent
4. **Trace**: Follow the execution flow using `depends_on:`
5. **Reference**: Check `WORKFLOW_YAML_LANGUAGE.md` for any unclear fields

**Time for review**: 10-15 minutes per workflow

---

## 📖 Reading by Topic

### Understanding Execution Flow
- **Sequential Execution**: `WORKFLOW_YAML_LANGUAGE.md` → Execution Types → Sequential
- **Parallel Execution**: `WORKFLOW_YAML_LANGUAGE.md` → Execution Types → Parallel
- **Adversarial Execution**: `WORKFLOW_YAML_LANGUAGE.md` → Execution Types → Adversarial
- **Dependencies**: `WORKFLOW_YAML_LANGUAGE.md` → Groups Section → depends_on

### Understanding Quality Gates
- **Steering**: `WORKFLOW_YAML_LANGUAGE.md` → Steering Section
- **Quality Gates**: `WORKFLOW_YAML_LANGUAGE.md` → Common Patterns → Quality Gates
- **Completion Criteria**: `WORKFLOW_YAML_LANGUAGE.md` → Groups Section → completion

### Understanding Configuration
- **Timeouts**: `WORKFLOW_CHECKLIST.md` → Timeouts Guide
- **Model Selection**: `WORKFLOW_CHECKLIST.md` → Naming Conventions
- **Parameters**: `WORKFLOW_YAML_LANGUAGE.md` → Agents Section → capabilities

### Creating Specific Workflow Types
- **Simple task**: `WORKFLOW_TEMPLATES.md` → Template 1
- **Multiple specialists**: `WORKFLOW_TEMPLATES.md` → Template 2
- **Multi-stage workflow**: `WORKFLOW_TEMPLATES.md` → Template 3
- **Debate & review**: `WORKFLOW_TEMPLATES.md` → Template 4
- **Quality gates**: `WORKFLOW_TEMPLATES.md` → Template 5
- **Interactive forms**: `WORKFLOW_TEMPLATES.md` → Template 6

---

## 🎯 Workflow Patterns at a Glance

### Pattern: Simple Sequential
**When**: One agent, straightforward task
**File**: Look at Template 1 in `WORKFLOW_TEMPLATES.md`
**Doc**: `WORKFLOW_YAML_LANGUAGE.md` → Common Patterns → Sequential Processing

```
Agent 1 → Agent 2 → Agent 3
```

---

### Pattern: Parallel Specialists
**When**: Multiple experts needed, independent analysis
**File**: Look at Template 2 in `WORKFLOW_TEMPLATES.md`
**Doc**: `WORKFLOW_YAML_LANGUAGE.md` → Common Patterns → Parallel Specialists

```
Expert 1 ─┐
Expert 2 ─┼ (all run simultaneously)
Expert 3 ─┘
  ↓
All must agree (consensus)
```

---

### Pattern: Multi-Stage Sequential
**When**: Clear stages with dependencies
**File**: Look at Template 3 in `WORKFLOW_TEMPLATES.md`
**Doc**: `WORKFLOW_YAML_LANGUAGE.md` → Common Patterns → Sequential Processing

```
Plan → Execute → Review
(each depends on previous)
```

---

### Pattern: Debate & Refinement
**When**: Need rigorous critique and improvement
**File**: Look at Template 4 in `WORKFLOW_TEMPLATES.md`
**Doc**: `WORKFLOW_YAML_LANGUAGE.md` → Execution Types → Adversarial

```
Proposal → Critique ⟷ Defense → Refinement
(debate until consensus)
```

---

### Pattern: Quality Gates
**When**: Smart routing and conditional execution
**File**: Look at Template 5 in `WORKFLOW_TEMPLATES.md`
**Doc**: `WORKFLOW_YAML_LANGUAGE.md` → Steering Section

```
Analysis → Steering Decision
           ├─ if good: Finalize
           └─ if bad: Deep Dive
```

---

### Pattern: Interactive Forms
**When**: User-driven workflows with variable behavior
**File**: Look at Template 6 in `WORKFLOW_TEMPLATES.md`
**Doc**: `WORKFLOW_YAML_LANGUAGE.md` → Template Variables

```
User Input Forms → Route to appropriate agents → Format output
```

---

## 🔍 Field Reference Quick Lookup

| Want to know about... | Go to... |
|---|---|
| `id:` field | `WORKFLOW_YAML_LANGUAGE.md` → Top-Level Fields |
| `config:` section | `WORKFLOW_YAML_LANGUAGE.md` → Configuration Section |
| `groups:` | `WORKFLOW_YAML_LANGUAGE.md` → Groups Section |
| `execution:` types | `WORKFLOW_YAML_LANGUAGE.md` → Execution Types |
| `completion:` criteria | `WORKFLOW_YAML_LANGUAGE.md` → Completion Types |
| `agents:` | `WORKFLOW_YAML_LANGUAGE.md` → Agents Section |
| `system_prompt:` | `WORKFLOW_YAML_LANGUAGE.md` → Agent Fields |
| `tools:` | `WORKFLOW_YAML_LANGUAGE.md` → Agent Fields |
| `context_sources:` | `WORKFLOW_YAML_LANGUAGE.md` → Agent Fields |
| `depends_on:` | `WORKFLOW_YAML_LANGUAGE.md` → Group Fields |
| `steering:` | `WORKFLOW_YAML_LANGUAGE.md` → Steering Section |
| Model names | `WORKFLOW_CHECKLIST.md` → Naming Conventions |
| Timeouts | `WORKFLOW_CHECKLIST.md` → Timeouts Guide |
| Temperature | `WORKFLOW_CHECKLIST.md` → Temperature Guide |
| Max tokens | `WORKFLOW_CHECKLIST.md` → Max Tokens Guide |

---

## ❓ Common Questions

### "How do I create a new workflow?"
→ Start with `WORKFLOW_TEMPLATES.md`, pick a template, customize it.

### "How do I validate my workflow?"
→ Use `WORKFLOW_CHECKLIST.md` to verify everything.

### "What does `depends_on:` do?"
→ See `WORKFLOW_YAML_LANGUAGE.md` → Group Fields → depends_on

### "How do I make agents work together?"
→ See `WORKFLOW_YAML_LANGUAGE.md` → Execution Types

### "How do I route workflows based on quality?"
→ See `WORKFLOW_YAML_LANGUAGE.md` → Steering Section, or Template 5

### "How do I handle user input?"
→ See Template 6 in `WORKFLOW_TEMPLATES.md`

### "What's the difference between execution types?"
→ See `WORKFLOW_YAML_LANGUAGE.md` → Common Patterns

### "How do I set timeouts?"
→ See `WORKFLOW_CHECKLIST.md` → Timeouts Guide

### "Which model should I use?"
→ Use tier names (gpt-4, claude-opus, etc.), see `WORKFLOW_CHECKLIST.md` → Naming Conventions

### "How do I debug a failing workflow?"
→ See `WORKFLOW_CHECKLIST.md` → Troubleshooting Quick Guide

---

## 📋 File Organization

```
docs/
├── WORKFLOW_YAML_LANGUAGE.md        ← The language spec
├── WORKFLOW_TEMPLATES.md             ← Copy & paste templates
├── WORKFLOW_CHECKLIST.md             ← Validation checklist
├── WORKFLOW_DOCUMENTATION_INDEX.md   ← This file
└── README.md                         ← General project README
```

---

## 🎓 Learning Path

### Path 1: "I want to create a workflow NOW"
1. Read: `WORKFLOW_TEMPLATES.md` introduction
2. Do: Copy Template 1
3. Do: Customize for your task
4. Do: Validate with `WORKFLOW_CHECKLIST.md`
5. Do: Execute

**Time**: 20 minutes

---

### Path 2: "I want to understand workflows"
1. Read: `WORKFLOW_YAML_LANGUAGE.md` (complete)
2. Read: `WORKFLOW_TEMPLATES.md` (skim all templates)
3. Study: Existing workflows in your directory
4. Practice: Create a simple workflow
5. Refer: `WORKFLOW_CHECKLIST.md` while working

**Time**: 2-3 hours

---

### Path 3: "I want to master advanced patterns"
1. Read: `WORKFLOW_YAML_LANGUAGE.md` (complete)
2. Study: Templates 4, 5, 6 in `WORKFLOW_TEMPLATES.md`
3. Read: Steering section in `WORKFLOW_YAML_LANGUAGE.md`
4. Experiment: Create workflows with steering rules
5. Review: Others' workflows using `WORKFLOW_CHECKLIST.md`

**Time**: 4-5 hours

---

## 🔧 Maintenance

These docs are:
- **Project-agnostic**: Work in any directory
- **Version-neutral**: Use tier-based model names, not version IDs
- **Language-focused**: Explain the YAML structure and semantics
- **Example-rich**: Extensive code examples throughout

---

## 📞 Getting Help

1. **What field does this do?** → `WORKFLOW_YAML_LANGUAGE.md`
2. **How do I create X pattern?** → `WORKFLOW_TEMPLATES.md`
3. **Is my workflow correct?** → `WORKFLOW_CHECKLIST.md`
4. **I'm confused about execution flow** → `WORKFLOW_YAML_LANGUAGE.md` → Common Patterns

---

Done! Now you have complete, universal workflow documentation.
