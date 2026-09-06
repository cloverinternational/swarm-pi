# WHERE TO PUT WORKFLOW YAML FILES - CORRECT VERSION

## The Correct Answer

Workflows are loaded from **`workflows/` directory in whatever directory you run `swarmos` from.**

NOT from a hardcoded central location.

---

## How It Works

When you run `swarmos`, the application looks for:

```
<current-working-directory>/workflows/
```

**Examples:**

| Current Directory | Workflows Loaded From |
|---|---|
| `/home/user/project1/` | `/home/user/project1/workflows/` |
| `/home/user/project2/` | `/home/user/project2/workflows/` |
| `/tmp/myworkflows/` | `/tmp/myworkflows/workflows/` |
| Any directory | `<that-directory>/workflows/` |

---

## Directory Structure

In YOUR project directory:

```
your-project-directory/
├── workflows/                      ← PUT YOUR YAML FILES HERE
│   ├── my_workflow.yaml
│   ├── code_review.yaml
│   └── research.yaml
│
├── src/
├── README.md
└── ... (other project files)
```

---

## Step by Step

### 1. Create `workflows/` directory in your project

```bash
mkdir -p workflows
```

### 2. Create your workflow file

```bash
cat > workflows/my_workflow.yaml << 'EOF'
id: my_workflow
name: My Workflow
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
        system_prompt: "You are helpful."
        tools: [Bash, Read]
        capabilities:
          max_tokens: 2000
          temperature: 0.5
EOF
```

### 3. Run swarmos from that directory

```bash
cd your-project-directory/
swarmos
```

### 4. Your workflow will be loaded!

The system finds `your-project-directory/workflows/my_workflow.yaml`

---

## Key Points

✅ **Each project has its own `workflows/` directory**
✅ **Workflows are loaded from the current working directory**
✅ **No central registry or hardcoded paths**
✅ **Project-portable - take your directory anywhere**

---

## Real Example

```bash
# Create project structure
mkdir ~/my_research_project
cd ~/my_research_project
mkdir workflows

# Create a workflow
cat > workflows/research.yaml << 'EOF'
id: research_workflow
name: Research Workflow
# ... (YAML content)
EOF

# Run swarmos - it will find ~/my_research_project/workflows/research.yaml
swarmos
```

---

## Important Notes

### ❌ DON'T do this:
- Don't put workflows in `/home/swarm/SwarmCode/TUI/workflows/` expecting them to load everywhere
- That directory is just for that specific project
- It won't load workflows from other directories

### ✅ DO do this:
- Create a `workflows/` directory in YOUR project directory
- Put your YAML files there
- Run `swarmos` from that directory
- Your workflows load automatically

---

## Multiple Projects

If you have multiple projects:

```
~/projects/
├── research-project/
│   └── workflows/
│       ├── research.yaml
│       └── analysis.yaml
│
├── code-review-project/
│   └── workflows/
│       ├── pr_review.yaml
│       └── security_check.yaml
│
└── util-project/
    └── workflows/
        └── helper.yaml
```

When you `cd` to each project and run `swarmos`, it loads that project's workflows.

---

## Validation

To validate a workflow without running it:

```bash
cd your-project-directory/
cd /path/to/swarmos/code/  # Go to TUI repo
./cmd/workflow-cli/workflow-cli -command validate \
  -workflow /path/to/your-project-directory/workflows/my_workflow.yaml
```

---

## Summary

| Aspect | Details |
|---|---|
| **Where to put files** | `<current-working-dir>/workflows/` |
| **File extension** | `.yaml` (not `.yml`) |
| **How they load** | Auto-detected when swarmos starts |
| **Scope** | Per project/directory |
| **No registration needed** | Just create the directory and add files |

---

## That's It!

1. Create `workflows/` in your project directory
2. Add `.yaml` files there
3. Run `swarmos` from that directory
4. Done!

The workflows are loaded from wherever you run swarmos, not from a central location.
