# Workflow Execution Logging System

## Overview
Comprehensive logging system for workflow executions that captures all events, tool calls, agent outputs, and errors to structured log files in `.swarm/workflow-logs/` directory.

## Features

### Automatic Logging
- **Every workflow execution** is automatically logged
- Creates timestamped directory for each run
- Captures workflow configuration, execution timeline, and all events
- Symlink to `latest` run for quick access

### Log Structure
```
.swarm/workflow-logs/
├── latest -> 20240205-143000_git_staging_workflow/
├── 20240205-143000_git_staging_workflow/
│   ├── config.yaml           # Workflow configuration
│   ├── execution.log         # Line-delimited JSON logs
│   ├── full_log.json         # Complete log in structured JSON
│   ├── result.json           # Final execution result
│   ├── groups/               # Per-group logs
│   │   ├── staging.json
│   │   └── review.json
│   └── agents/               # Per-agent logs
│       ├── staging_agent.json
│       └── review_agent.json
└── 20240205-120000_code_review_workflow/
    └── ...
```

### What Gets Logged

#### Workflow Level
- Start/end timestamps
- Provider and model used
- Input provided
- Final status (completed/failed/cancelled)
- Duration
- Group results
- Overall error messages

#### Group Level
- Group start/completion
- Execution strategy (parallel/sequential)
- Agent assignments
- Group-specific errors
- Completion criteria results

#### Agent Level
- Agent creation and configuration
- System prompts used
- Tools assigned
- Model calls and responses
- Thinking processes (if enabled)
- Token usage
- Agent-specific errors

#### Tool Level
- Tool invocations with parameters
- Tool outputs
- Tool execution errors
- Tool execution time

## Usage

### Automatic Logging
Logging happens automatically when you run a workflow. No configuration needed!

When you execute a workflow through the TUI, logs are created in:
```
<your-workspace>/.swarm/workflow-logs/<timestamp>_<workflow-id>/
```

### Viewing Logs

#### Using the Log Viewer Script
```bash
# List all workflow runs
./workflow-logs.sh list

# Show the latest run
./workflow-logs.sh latest

# Show a specific run
./workflow-logs.sh show 20240205-143000_git_staging_workflow

# View execution logs
./workflow-logs.sh logs 20240205-143000_git_staging_workflow

# View only errors
./workflow-logs.sh errors 20240205-143000_git_staging_workflow

# Tail the latest log file (live updates)
./workflow-logs.sh tail
```

#### Manual Viewing

**View config:**
```bash
cat .swarm/workflow-logs/latest/config.yaml
```

**View execution log (pretty JSON):**
```bash
cat .swarm/workflow-logs/latest/execution.log | jq
```

**View specific group logs:**
```bash
cat .swarm/workflow-logs/latest/groups/staging.json | jq
```

**View specific agent logs:**
```bash
cat .swarm/workflow-logs/latest/agents/staging_agent.json | jq
```

**Search for errors:**
```bash
grep ERROR .swarm/workflow-logs/latest/execution.log
```

**Filter by log level:**
```bash
cat .swarm/workflow-logs/latest/execution.log | jq 'select(.level == "ERROR")'
```

## Log Entry Format

Each log entry in `execution.log` is a JSON line with:

```json
{
  "timestamp": "2024-02-05T14:30:00Z",
  "level": "INFO",
  "type": "workflow|group|agent|tool|error",
  "group_id": "staging",
  "group_name": "Staging & Review",
  "agent_id": "staging_agent",
  "agent_name": "Staging Agent",
  "tool_name": "Bash",
  "message": "Tool call: Bash",
  "data": {
    "command": "git status",
    "params": {...}
  },
  "error": "error message if any"
}
```

### Log Levels
- **DEBUG**: Detailed debugging information (tool calls, internal state)
- **INFO**: General informational messages (workflow/group/agent start/completion)
- **WARN**: Warning messages (non-critical issues)
- **ERROR**: Error messages (failures, exceptions)

### Log Types
- **workflow**: Workflow-level events
- **group**: Group-level events
- **agent**: Agent-level events
- **tool**: Tool invocation events
- **error**: Error events

## Configuration File Format

The `config.yaml` file contains:

```yaml
workflow_id: git_staging_workflow
workflow_name: Git Staging & Commit Workflow
start_time: 2024-02-05T14:30:00Z
end_time: 2024-02-05T14:35:00Z
duration: 5m0s
provider: anthropic
model: claude-sonnet-4-20250514
input: "Review and stage changes"
status: completed

groups:
  - id: staging
    name: Staging & Review
    description: Stage changes and review
    execution: sequential
    agents:
      - "Staging Agent (anthropic/claude-sonnet-4-20250514)"
      - "Review Agent (anthropic/claude-sonnet-4-20250514)"

system_prompts:
  staging.staging_agent: "You are a Git staging expert..."
  staging.review_agent: "You are a code reviewer..."

tools:
  staging.staging_agent:
    - "*"
  staging.review_agent:
    - "FileRead"
    - "Grep"

execution_path: /home/user/project/.swarm/workflow-logs/20240205-143000_git_staging_workflow
```

## Debugging Workflows

### Common Debugging Tasks

**Find where workflow failed:**
```bash
./workflow-logs.sh errors <run-id>
```

**Check what tools were called:**
```bash
cat .swarm/workflow-logs/latest/execution.log | jq 'select(.type == "tool")'
```

**See agent responses:**
```bash
cat .swarm/workflow-logs/latest/agents/<agent-id>.json | jq
```

**Check group execution order:**
```bash
cat .swarm/workflow-logs/latest/execution.log | jq 'select(.type == "group") | {time: .timestamp, group: .group_name, msg: .message}'
```

**View timeline:**
```bash
cat .swarm/workflow-logs/latest/execution.log | jq -r '[.timestamp, .level, .type, .message] | @tsv'
```

## Integration with TUI

The logging system is automatically integrated into the TUI workflow execution:

1. When you execute a workflow (`w` → select workflow → `r` for run)
2. Logs are created in `.swarm/workflow-logs/`
3. Console shows log path on execution start
4. All workflow events automatically logged
5. On completion, symlink to `latest` is updated

## Performance

- **Minimal overhead**: Async logging with buffering
- **Structured JSON**: Easy to parse and analyze
- **Compressed archives**: Old logs can be compressed (future feature)
- **Rotation**: Automatic cleanup of old logs (configurable, future feature)

## Privacy & Security

- **Local only**: Logs stay on your machine in `.swarm/workflow-logs/`
- **Gitignore**: Add `.swarm/` to your `.gitignore` to avoid committing logs
- **Sensitive data**: Be aware logs contain prompts, tool outputs, and API responses
- **Credentials**: API keys are NOT logged (only provider names)

## Best Practices

1. **Add to .gitignore**: 
   ```bash
   echo ".swarm/" >> .gitignore
   ```

2. **Review logs after failures**:
   ```bash
   ./workflow-logs.sh latest
   ./workflow-logs.sh errors <run-id>
   ```

3. **Archive old logs**:
   ```bash
   tar -czf workflow-logs-archive-$(date +%Y%m).tar.gz .swarm/workflow-logs/
   ```

4. **Clean up old logs**:
   ```bash
   find .swarm/workflow-logs/ -type d -mtime +30 -exec rm -rf {} +
   ```

## Troubleshooting

### Logs not being created
- Check if workflow execution started successfully
- Verify write permissions in workspace directory
- Check console for "workflow execution logging to" message

### Log files empty
- Workflow may have failed immediately
- Check `config.yaml` for status
- Look for errors in TUI console

### Can't find logs
- Logs are created in the directory where you run the TUI
- Check `.swarm/workflow-logs/` in your current workspace
- Use `find . -name "workflow-logs"` to locate

### Symlink "latest" not working
- Symlink only created after workflow completes
- Check if logs directory has any runs
- Manual: `ls -la .swarm/workflow-logs/latest`

## Future Enhancements

- [ ] Web UI for log viewing
- [ ] Log rotation and automatic cleanup
- [ ] Export logs to external systems (Elasticsearch, CloudWatch, etc.)
- [ ] Real-time log streaming in TUI
- [ ] Log compression for old runs
- [ ] Configurable log levels per component
- [ ] Log filtering and search UI
- [ ] Workflow execution replay from logs

## Examples

### Example 1: Debug Failed Workflow

```bash
# List runs to find the failed one
./workflow-logs.sh list

# Show the run details
./workflow-logs.sh show 20240205-143000_git_staging_workflow

# View errors
./workflow-logs.sh errors 20240205-143000_git_staging_workflow

# Check specific agent logs
cat .swarm/workflow-logs/20240205-143000_git_staging_workflow/agents/staging_agent.json | jq
```

### Example 2: Monitor Live Execution

```bash
# In terminal 1: Run workflow from TUI

# In terminal 2: Tail logs
./workflow-logs.sh tail
```

### Example 3: Compare Two Runs

```bash
# Compare configs
diff .swarm/workflow-logs/run1/config.yaml .swarm/workflow-logs/run2/config.yaml

# Compare results
diff <(cat .swarm/workflow-logs/run1/result.json | jq) <(cat .swarm/workflow-logs/run2/result.json | jq)
```

## Log Retention

By default, logs are kept indefinitely. Recommendations:

- **Keep last 30 days**: Essential for debugging recent issues
- **Archive monthly**: Compress and archive logs older than 30 days
- **Delete old archives**: Remove archives older than 6 months

```bash
# Archive script example
#!/bin/bash
ARCHIVE_DIR=".swarm/archives"
mkdir -p "$ARCHIVE_DIR"
find .swarm/workflow-logs/ -type d -name "202*" -mtime +30 -exec \
    tar -czf "$ARCHIVE_DIR/{}.tar.gz" {} \; -exec rm -rf {} \;
```

## API (for Advanced Users)

The logging system can be used programmatically:

```go
import "github.com/Swarm-Code/mono/swarmos-tui/internal/chat"

// Create logger
logger, err := chat.NewWorkflowLogger(workspaceDir, workflowID, workflowName)

// Start logging
logger.Start(ctx, input, provider, model, workflow)

// Log events
logger.LogWorkflowEvent("INFO", "Custom event", map[string]interface{}{"key": "value"})
logger.LogGroupEvent(groupID, groupName, "INFO", "Group started", nil)
logger.LogAgentEvent(groupID, agentID, agentName, "DEBUG", "Agent thinking", nil)
logger.LogToolCall(groupID, agentID, "Bash", map[string]interface{}{"cmd": "ls"})
logger.LogError(err, "Tool execution failed", nil)

// Complete logging
logger.Complete(status, result, err)
```
