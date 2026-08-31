# How to Access Workflow Documentation

The workflow documentation is now available in the `docs/` directory and can be accessed by agents using the `@` symbol in the chat interface.

---

## Quick Reference

### To Reference Documentation in Chat

Use the `@` symbol followed by the filename:

```
@docs/WORKFLOW_START_HERE.md
@docs/WORKFLOW_YAML_LANGUAGE.md
@docs/WORKFLOW_TEMPLATES.md
@docs/WORKFLOW_CHECKLIST.md
@docs/WORKFLOW_DOCUMENTATION_INDEX.md
```

---

## Available Documentation

### 1. Start Here
```
@docs/WORKFLOW_START_HERE.md
```
5-minute introduction with decision tree for choosing patterns

### 2. YAML Language Reference
```
@docs/WORKFLOW_YAML_LANGUAGE.md
```
Complete specification of all YAML fields and options

### 3. Copy-Paste Templates
```
@docs/WORKFLOW_TEMPLATES.md
```
6 production-ready templates for common patterns

### 4. Validation Checklist
```
@docs/WORKFLOW_CHECKLIST.md
```
Pre-execution verification checklist

### 5. Documentation Index
```
@docs/WORKFLOW_DOCUMENTATION_INDEX.md
```
Master index and navigation guide

### 6. Summary
```
@docs/WORKFLOW_DOCUMENTATION_SUMMARY.md
```
Overview of all documentation

---

## For Agents: Quick Workflow Creation

1. **Read**: `@docs/WORKFLOW_START_HERE.md` (5 min)
2. **Pick**: A template from `@docs/WORKFLOW_TEMPLATES.md`
3. **Copy**: The template code
4. **Customize**: For your task
5. **Validate**: Check against `@docs/WORKFLOW_CHECKLIST.md`
6. **Execute**: Run the workflow

---

## File Locations

All documentation files are in: `/home/swarm/SwarmCode/TUI/docs/`

Files starting with `WORKFLOW_`:
- `WORKFLOW_START_HERE.md` ⭐ BEGIN HERE
- `WORKFLOW_YAML_LANGUAGE.md` - Complete spec
- `WORKFLOW_TEMPLATES.md` - Copy & paste
- `WORKFLOW_CHECKLIST.md` - Validation
- `WORKFLOW_DOCUMENTATION_INDEX.md` - Master index
- `WORKFLOW_DOCUMENTATION_SUMMARY.md` - Summary

---

## Using Documentation with Tools

### Read a Doc File
```
In chat, type:
@docs/WORKFLOW_START_HERE.md

The system will load and display the file content.
```

### Copy a Template
```
1. Open: @docs/WORKFLOW_TEMPLATES.md
2. Find the template you need
3. Copy the YAML code block
4. Paste into your workflow file
```

### Validate Your Work
```
1. Open: @docs/WORKFLOW_CHECKLIST.md
2. Use the checklist to verify your workflow
3. Reference: @docs/WORKFLOW_YAML_LANGUAGE.md for specific fields
```

---

## Documentation Hierarchy

```
START HERE (5 min)
    ↓
YAML_LANGUAGE (complete reference)
    ↓
TEMPLATES (choose pattern)
    ↓
CHECKLIST (validate)
    ↓
DOCUMENTATION_INDEX (find things)
```

---

## Search Tips

All documentation files contain:
- **Table of contents** - Jump to sections
- **Index/references** - Find specific fields
- **Examples** - Copy-paste code
- **Cross-references** - Links between docs

---

## For Teams

Point your team to:
```
@docs/WORKFLOW_START_HERE.md
```

Then they can reference any other doc as needed:
```
@docs/WORKFLOW_TEMPLATES.md        # For templates
@docs/WORKFLOW_YAML_LANGUAGE.md    # For details
@docs/WORKFLOW_CHECKLIST.md        # For validation
```

---

## File Structure

```
/home/swarm/SwarmCode/TUI/docs/
├── WORKFLOW_START_HERE.md               ← BEGIN HERE
├── WORKFLOW_YAML_LANGUAGE.md            ← Full reference
├── WORKFLOW_TEMPLATES.md                ← 6 templates
├── WORKFLOW_CHECKLIST.md                ← Validation
├── WORKFLOW_DOCUMENTATION_INDEX.md      ← Navigation
└── WORKFLOW_DOCUMENTATION_SUMMARY.md    ← Summary
```

---

## Key Points

✅ **Project-Agnostic** - Works in any directory
✅ **Generic References** - Tier-based model names (gpt-4, claude-opus)
✅ **YAML Language-Focused** - Not tool-specific
✅ **Copy-Paste Ready** - 6 production templates
✅ **Comprehensive** - Beginner to advanced

---

## Quick Links for Common Tasks

| Task | File to Use |
|------|------------|
| Create first workflow | `@docs/WORKFLOW_START_HERE.md` |
| Choose a pattern | `@docs/WORKFLOW_START_HERE.md` (decision tree) |
| Find a template | `@docs/WORKFLOW_TEMPLATES.md` |
| Understand a YAML field | `@docs/WORKFLOW_YAML_LANGUAGE.md` |
| Validate workflow | `@docs/WORKFLOW_CHECKLIST.md` |
| Find documentation | `@docs/WORKFLOW_DOCUMENTATION_INDEX.md` |

---

Done! Documentation is ready to use with `@docs/` references.
