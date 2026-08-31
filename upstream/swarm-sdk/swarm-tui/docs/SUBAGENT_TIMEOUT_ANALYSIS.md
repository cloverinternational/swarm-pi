# Sub-agent Timeout Issue Analysis

## The Problem
Sub-agents are timing out after 60 seconds, which is too restrictive for agents doing real work. The error "context deadline exceeded" indicates a hard timeout is being enforced.

## Root Cause
The 60-second timeout comes from the fallback executor's default timeout:

```go
// In /swarm-sdk/fallback/executor.go
func DefaultOptions() ExecuteOptions {
    return ExecuteOptions{
        Timeout: 60 * time.Second,
    }
}
```

## How It Happens

1. **Sub-agent Creation**: When a sub-agent is created without an explicit timeout:
   ```go
   // In factory.CreateSubAgent()
   Capabilities: &Capabilities{
       Timeout: config.Timeout, // Often 0, meaning "use default"
   }
   ```

2. **Agent Execution**: The agent uses this timeout when executing:
   ```go
   // In agent_execute.go
   resp, result := fallback.ExecuteWithResult[*provider.ChatResponse](ctx, a.chain, execFn, fallback.ExecuteOptions{
       OnProgress: onProgress,
       Timeout:    time.Duration(a.definition.Capabilities.Timeout) * time.Second, // 0 seconds if not set
   })
   ```

3. **Fallback Executor**: When timeout is 0, the executor doesn't apply a timeout context:
   ```go
   if options.Timeout > 0 {
       attemptCtx, cancel = context.WithTimeout(ctx, options.Timeout)
   }
   ```
   
   However, the default 60-second timeout from DefaultOptions() is still enforced somewhere in the chain.

## Why This Is Bad
- Agents doing complex tasks (research, analysis, multi-step operations) often need more than 60 seconds
- The timeout is arbitrary and doesn't reflect actual work requirements
- Background agents should be able to run as long as needed

## Proposed Solutions

### Solution 1: Remove Default Timeout
The simplest fix is to change the default timeout to 0 (no timeout):

```go
func DefaultOptions() ExecuteOptions {
    return ExecuteOptions{
        Timeout: 0, // No timeout by default
    }
}
```

### Solution 2: Larger Default for Sub-agents
Set a more reasonable default timeout for sub-agents (e.g., 10 minutes):

```go
// In factory.CreateSubAgent()
if config.Timeout == 0 {
    config.Timeout = 600 // 10 minutes default for sub-agents
}
```

### Solution 3: Make Timeout Configurable
Add a configuration option to set the default sub-agent timeout:

```go
type FactoryConfig struct {
    DefaultSubAgentTimeout int // seconds
}
```

### Solution 4: No Timeout for Background Agents
Background agents should have no timeout by default since they're designed for long-running tasks:

```go
// In background_agent.go execute()
resp, err := b.agent.Execute(ctx, ExecuteRequest{
    Message: task,
    Timeout: 0, // No timeout for background execution
})
```

## Recommended Approach
The best solution is a combination:
1. Remove the 60-second default from fallback executor (it's too restrictive)
2. Let providers handle their own timeouts (they already have request timeouts)
3. For background agents, explicitly set no timeout
4. Allow users to configure timeouts when needed

This gives maximum flexibility while preventing accidental infinite loops through provider-level timeouts.