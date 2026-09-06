# Workflow Troubleshooting Guide

## Common Errors and Fixes

---

### `circular dependency detected in workflow`

**Cause:** Group A depends on Group B which depends on Group A (directly or transitively).

**How to find it:** Run the validator script:
```bash
bash scripts/validate-workflow.sh ./workflows/my_workflow.yaml
```
The script will trace and print the cycle path.

**Fix:** Review your `depends_on` chains. Draw the DAG on paper if needed. Every chain must terminate — there must be at least one group with no `depends_on`.

```yaml
# BROKEN — cycle: a → b → a
- id: a
  depends_on: [b]
- id: b
  depends_on: [a]

# FIXED — linear chain
- id: a
  # no depends_on → starts first
- id: b
  depends_on: [a]
```

---

### `group 'X' depends on non-existent group 'Y'`

**Cause:** A `depends_on` value references a group ID that doesn't exist in the workflow.

**Common causes:**
- Typo in group ID (`analysis` vs `analyse`)
- Group was renamed but `depends_on` wasn't updated
- Referencing a group from a different workflow

**Fix:** Make sure every entry in `depends_on` exactly matches an `id` field in the same workflow (case-sensitive).

---

### `failed to get credentials for provider X`

**Cause:** The workflow references a literal provider (`provider: anthropic`) but no credential is available for it.

**Fixes (in order of preference):**
1. Use `provider: "@current"` + `model: "@current"` to use the user's active provider.
2. Add the provider credential in TUI Settings → Providers.
3. Set the environment variable: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.
4. Use `provider: "@profile"` + `model: "@main"` to defer to profile config.

---

### Tools silently not working (agent can't use Bash/Read/etc.)

**Cause:** Tool names in `tools:` don't exactly match SDK registry names.

**Canonical tool names (case-sensitive):**
```
Bash         Read        Write       Edit
Grep         Task        BackgroundTask
ReadBackgroundCommand    TodoRead    TodoWrite
```

**Common wrong names that won't work:**
```
bash         ← should be Bash
file_read    ← should be Read
file_write   ← should be Write
grep         ← should be Grep
```

The validator script checks tool names against the canonical list. To fix, replace aliases with canonical names.

---

### Workflow times out before completing

**Cause:** `config.max_duration` is too short for the actual work being done.

**Fixes:**
1. Increase `config.max_duration`:
   ```yaml
   config:
     max_duration: 60m
   ```
2. Set per-group timeouts to understand where time is being spent:
   ```yaml
   - id: slow_group
     timeout: 20m
   ```
3. Reduce `max_tokens` for agents that don't need large outputs — this speeds up inference.
4. Switch `execution: sequential` groups with independent agents to `execution: parallel`.

---

### `execution: adversarial` immediately fails or never reaches consensus

**Cause:** Misconfigured completion criteria or agents with incompatible temperature settings.

**Checklist:**
1. `completion.type: consensus` must be set (not `all` or `first`)
2. `completion.threshold` should be between 0.5 and 0.9 — values like `1.0` require unanimous agreement which may never happen
3. At least one agent should have a neutral/moderator role with low temperature (0.2–0.3)
4. Debate agents should have higher temperature (0.6–0.8) to generate diverse perspectives
5. Set a reasonable `timeout` (at least 10–15 minutes for adversarial groups)

---

### Agents don't receive output from previous groups

**Cause:** Context is not being passed correctly.

**Fixes:**
1. Use `context_sources` on the downstream agent:
   ```yaml
   context_sources:
     - groups.my_upstream_group.output
   ```
2. For `execution: sequential` within a group, context is automatically passed from agent to agent — no `context_sources` needed.
3. For `depends_on` inter-group dependencies, the upstream group's output is automatically appended to the downstream group's input context.

---

### YAML parse error on multi-line `system_prompt`

**Cause:** Incorrect YAML block scalar syntax for multi-line strings.

**Fix:** Use `|-` (block scalar, strip trailing newline) or `|` (block scalar, preserve newline):
```yaml
system_prompt: |-
  You are a helpful agent.
  Line 2 of the prompt.
  Line 3.
```
Never use quotes for multi-line prompts — they require escaping newlines as `\n`.

---

### Workflow appears in wrong directory / not found

**Cause:** The workflow file isn't in a scanned directory.

**Scanned directories (in order):**
1. `./workflows/` — project-local
2. `./.workflows/` — project-local (hidden)
3. `~/.swarm/workflows/` — user-global

The TUI scans all `*.yaml` files in these directories on startup. After adding a new file, restart the TUI or use the Workflows tab refresh.

---

## Validator Script Output Reference

```
[OK]   Valid YAML syntax
[OK]   Required fields: id, name, version, groups
[OK]   Group IDs unique: [group_a, group_b, group_c]
[OK]   All depends_on references valid
[OK]   No circular dependencies detected
[OK]   Adversarial groups have consensus completion
[WARN] Unknown tool name 'bash' in group 'X' agent 'Y' — did you mean 'Bash'?
[FAIL] Group 'report' depends on non-existent group 'analyse' — did you mean 'analysis'?
```

Warnings are non-fatal (workflow may still run). FAILs prevent execution.

---

## Debugging Tips

1. **Use dry-run first:** The SDK's `DryRun()` method validates the workflow and estimates execution time without calling any LLMs. The TUI exposes this before execution.
2. **Check execution logs:** Detailed logs per workflow execution are written to `~/.swarm/logs/`.
3. **Start simple:** Build workflows incrementally — start with one group, one agent, no dependencies. Add complexity once the core works.
4. **Use `timeout_behavior: partial`:** This returns results from completed groups even if the workflow times out, making it easier to debug which stage is failing.
5. **Reduce `max_tokens` during testing:** Use small values (2000–4000) while iterating. Switch to full values once the workflow logic is correct.
