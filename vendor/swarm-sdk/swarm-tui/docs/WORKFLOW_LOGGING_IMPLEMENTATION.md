# Workflow Logging Implementation Summary

## What Was Implemented

Successfully implemented a comprehensive workflow execution logging system that automatically captures all workflow events, agent actions, tool calls, and errors to structured log files.

## Files Created/Modified

### New Files Created

1. **`internal/chat/workflow_logger.go`** (619 lines)
   - Core logging implementation
   - `WorkflowLogger` struct for managing logs
   - `LogEntry` struct for structured log entries
   - `WorkflowConfig` struct for execution metadata
   - `LoggerAdapter` to integrate with SDK's observability.Logger
   - Methods for logging workflow/group/agent/tool events
   - Automatic file organization and symlink management

2. **`workflow-logs.sh`** (308 lines)
   - Command-line tool for viewing logs
   - Commands: list, latest, show, config, logs, errors, tail
   - Colored output for readability
   - JSON log parsing and formatting

3. **`WORKFLOW_LOGGING.md`** (comprehensive documentation)
   - Usage guide
   - Log structure explanation
   - Debugging examples
   - Best practices
   - API reference

### Modified Files

1. **`internal/chat/workflow_manager.go`**
   - Integrated workflow logger creation
   - Logger initialization before workflow execution
   - Logger adapter wrapping for SDK logger
   - Completion callback to finalize logs
   - Added workspace directory detection

## Log Directory Structure

When you run a workflow, logs are automatically created in:

```
<workspace>/.swarm/workflow-logs/
├── latest -> 20240205-143000_git_staging_workflow/
├── 20240205-143000_git_staging_workflow/
│   ├── config.yaml           # Workflow configuration and metadata
│   ├── execution.log         # Line-delimited JSON log entries
│   ├── full_log.json         # Complete structured log
│   ├── result.json           # Final execution result
│   ├── groups/
│   │   ├── staging.json      # Per-group event logs
│   │   └── review.json
│   └── agents/
│       ├── staging_agent.json  # Per-agent event logs
│       └── review_agent.json
```

## What Gets Logged

### Workflow Level
- ✅ Start/end timestamps
- ✅ Provider and model configuration
- ✅ Input provided
- ✅ Final status (completed/failed/cancelled)
- ✅ Duration
- ✅ Group results
- ✅ Error messages

### Group Level
- ✅ Group start/completion events
- ✅ Execution strategy
- ✅ Agent assignments
- ✅ Group-specific errors

### Agent Level
- ✅ Agent creation and configuration
- ✅ System prompts used
- ✅ Tools assigned
- ✅ Model interactions (via SDK logger)
- ✅ Agent-specific errors

### Tool Level (via SDK logger)
- ✅ Tool invocations
- ✅ Tool parameters
- ✅ Tool outputs
- ✅ Tool errors

## How It Works

### Automatic Integration

1. **Workflow Execution Starts**
   ```go
   // In workflow_manager.go Execute()
   workflowLogger, _ := NewWorkflowLogger(workspaceDir, workflowID, workflowName)
   workflowLogger.Start(ctx, input, currentProvider, currentModel, workflow)
   ```

2. **Logger Wraps SDK Logger**
   ```go
   engineLogger := NewLoggerAdapter(workflowLogger, wm.loader)
   engine := mode.NewWorkflowEngine(workflow, engineLogger, wm.tracer)
   ```

3. **All SDK Logs Captured**
   - LoggerAdapter implements `observability.Logger` interface
   - Forwards all Debug/Info/Warn/Error calls to both:
     - Original SDK logger (console output)
     - WorkflowLogger (file output)

4. **Workflow Completes**
   ```go
   defer func() {
       workflowLogger.Complete(status, result, err)
   }()
   ```

### Log File Generation

- **`config.yaml`**: Created on start, updated on completion
- **`execution.log`**: Appended in real-time as events occur
- **`full_log.json`**: Written on completion with all log entries
- **`result.json`**: Written on completion with workflow result
- **Group/Agent logs**: Extracted and organized on completion
- **`latest` symlink**: Updated to point to most recent run

## Usage Examples

### View Latest Run
```bash
./workflow-logs.sh latest
```

Output:
```
Latest Workflow Run: 20240205-143000_git_staging_workflow

=== Workflow Execution ===

workflow_id:         git_staging_workflow
workflow_name:       Git Staging & Commit Workflow
start_time:          2024-02-05T14:30:00Z
end_time:            2024-02-05T14:35:00Z
duration:            5m0s
status:              ✓ completed
provider:            anthropic
model:               claude-sonnet-4-20250514

=== Groups ===

  ▸ staging
  ▸ review

=== Agents ===

  ▸ staging_agent
  ▸ review_agent
```

### List All Runs
```bash
./workflow-logs.sh list
```

Output:
```
Workflow Execution Runs:

1. 20240205-143000_git_staging_workflow
   Workflow: Git Staging & Commit Workflow
   Status: ✓ completed
   Started: 2024-02-05T14:30:00Z
   Duration: 5m0s

2. 20240205-120000_code_review_workflow
   Workflow: Code Review Workflow
   Status: ✗ failed
   Started: 2024-02-05T12:00:00Z
   Duration: 2m30s

Total runs: 2
```

### View Errors Only
```bash
./workflow-logs.sh errors 20240205-120000_code_review_workflow
```

Output:
```
Errors for: 20240205-120000_code_review_workflow

[2024-02-05T12:02:30Z]
  Message: Tool execution failed
  Error: command exited with status 1

[2024-02-05T12:02:31Z]
  Message: Workflow execution failed
  Error: agent failed after max retries
```

### Live Tail
```bash
./workflow-logs.sh tail
```

Output (live updating):
```
Tailing latest log: .swarm/workflow-logs/latest/execution.log

[2024-02-05T14:30:00Z] INFO: Workflow execution started: Git Staging & Commit Workflow
[2024-02-05T14:30:01Z] INFO: Group started: Staging & Review
[2024-02-05T14:30:02Z] DEBUG: Tool call: Bash
[2024-02-05T14:30:03Z] INFO: Tool completed successfully
...
```

## Manual Log Inspection

### View Config
```bash
cat .swarm/workflow-logs/latest/config.yaml
```

### View Execution Log (pretty)
```bash
cat .swarm/workflow-logs/latest/execution.log | jq
```

### Search for Errors
```bash
grep ERROR .swarm/workflow-logs/latest/execution.log
```

### Filter by Type
```bash
cat .swarm/workflow-logs/latest/execution.log | jq 'select(.type == "tool")'
```

### View Agent-Specific Logs
```bash
cat .swarm/workflow-logs/latest/agents/staging_agent.json | jq
```

## Benefits

### For Users
- ✅ **Complete audit trail** of workflow executions
- ✅ **Debug failed workflows** by reviewing exact events
- ✅ **Understand workflow behavior** through detailed logs
- ✅ **Track tool usage** and agent actions
- ✅ **Compare runs** to identify differences
- ✅ **Archive important runs** for future reference

### For Developers
- ✅ **Structured logging** (JSON format)
- ✅ **Easy to parse** and analyze programmatically
- ✅ **Per-component logs** (workflow/group/agent)
- ✅ **Timestamped events** for timeline analysis
- ✅ **Error tracking** with context
- ✅ **Integration with SDK logger** (no duplication)

## Privacy & Security

- ✅ **Local only**: Logs stay on your machine
- ✅ **No API keys logged**: Only provider names
- ✅ **Gitignore recommended**: Add `.swarm/` to avoid commits
- ⚠️ **Contains prompts/outputs**: Be aware logs include all workflow data
- ⚠️ **Sensitive data**: Review logs before sharing

## Testing Status

- ✅ **Compiles successfully**: No build errors
- ✅ **Type-safe**: Implements observability.Logger interface
- ✅ **File creation**: Directories and files created automatically
- ✅ **Log viewer**: Shell script tested with realistic data
- ⏳ **Runtime testing**: Needs actual workflow execution to verify

## Next Steps

### To Test
1. Run a workflow from the TUI
2. Check `.swarm/workflow-logs/` for created logs
3. Use `./workflow-logs.sh latest` to view
4. Verify all files are created correctly

### To Enhance
- [ ] Add log rotation (delete old logs)
- [ ] Add compression for archived logs
- [ ] Add web UI for log viewing
- [ ] Add real-time log streaming in TUI
- [ ] Add log export to external systems

## Recommended .gitignore

Add to your `.gitignore`:
```gitignore
# Workflow execution logs
.swarm/workflow-logs/
.swarm/archives/
```

## Quick Reference

### View Commands
```bash
./workflow-logs.sh list           # List all runs
./workflow-logs.sh latest         # Show latest run
./workflow-logs.sh show <run-id>  # Show specific run
./workflow-logs.sh logs <run-id>  # View execution logs
./workflow-logs.sh errors <run-id> # Show only errors
./workflow-logs.sh tail           # Tail latest log (live)
```

### Manual Commands
```bash
# View config
cat .swarm/workflow-logs/latest/config.yaml

# View logs (pretty)
cat .swarm/workflow-logs/latest/execution.log | jq

# Search errors
grep ERROR .swarm/workflow-logs/latest/execution.log

# View specific agent
cat .swarm/workflow-logs/latest/agents/<agent-id>.json | jq

# Archive old logs
tar -czf logs-$(date +%Y%m).tar.gz .swarm/workflow-logs/
```

## Files Summary

| File | Lines | Purpose |
|------|-------|---------|
| `workflow_logger.go` | 619 | Core logging implementation |
| `workflow_manager.go` | +40 | Integration with workflow execution |
| `workflow-logs.sh` | 308 | CLI log viewer tool |
| `WORKFLOW_LOGGING.md` | - | Comprehensive documentation |
| `WORKFLOW_LOGGING_IMPLEMENTATION.md` | - | This summary |

## Compilation Status

✅ **Successfully compiles**: `go build ./cmd/tui-client`

All code has been tested for syntax and type errors. Ready for runtime testing with actual workflow executions!
