# Sub-Agent Permission Inheritance Fix

## Problem
Sub-agents created via the `Task` tool were unable to execute certain operations (e.g., bash commands, file writes) despite having the tools registered in their tool registry. The sub-agents would report permission errors like:
- "Bash commands are not permitted"
- "No direct file writing tool is available"

## Root Cause Analysis

### Investigation Steps
1. **Tool Registry Check**: Confirmed sub-agents DID have tools (bash, read, write, etc.) in their registry
2. **Permission System Trace**: Found that tool registries have an associated `PermissionChecker`
3. **Code Flow Analysis**:
   - Parent agent has `SimpleRegistry` with permissive `PermissionChecker`
   - Factory creates sub-agent with NEW `SimpleRegistry`
   - New registry gets DEFAULT restrictive `PermissionChecker` (line 54 of `registry_impl.go`)
   - `DelegateTaskTool` copies tools BUT NOT the permission checker
   - Sub-agent ends up with tools but restrictive permissions

### Key Code Locations
- **Registry Creation**: `SDK/tools/registry_impl.go:52-55`
  ```go
  func NewSimpleRegistry(...) *SimpleRegistry {
      return &SimpleRegistry{
          permissions: NewSimplePermissionChecker(nil), // <- Default restrictive checker
          ...
      }
  }
  ```

- **Tool Copying**: `SDK/tools/builtin/delegate_task.go:373-428`
  - Copies tools from parent registry
  - BUT missing permission checker inheritance

## The Fix

**File**: `/home/rincon/swarm/SDK/tools/builtin/delegate_task.go`  
**Location**: After line 427 (after tool copying completes)

```go
// Copy permission checker from parent registry to sub-agent registry
// This ensures sub-agents inherit the same permission policies as the parent
if parentSimpleReg, ok := t.parentToolReg.(*tools.SimpleRegistry); ok {
    if subSimpleReg, ok := subToolReg.(*tools.SimpleRegistry); ok {
        if parentChecker := parentSimpleReg.GetPermissionChecker(); parentChecker != nil {
            subSimpleReg.SetPermissionChecker(parentChecker)
            t.logger.Debug(ctx, "delegate_task.permission_checker_inherited",
                observability.Field{Key: "agent", Value: subAgent.ID()})
        }
    }
}
```

### Why Type Assertions?
The `Registry` interface doesn't expose `GetPermissionChecker()` or `SetPermissionChecker()`. These methods exist only on the `SimpleRegistry` concrete type. Type assertions allow us to access these methods safely.

## Testing Results

### Before Fix
```bash
$ swarm -p "Use Task with implementation-agent. Create file test.txt"
# Sub-agent reports: "Bash commands are not permitted"
# File not created
```

### After Fix  
```bash
$ swarm -p "Use Task with implementation-agent. Create /tmp/test.txt"
# ✅ Sub-agent successfully creates file
# ✅ Content verified: File exists with correct content
```

### Verified For
- ✅ `implementation-agent` - Can execute bash and create files
- ✅ `code-reviewer` - Can execute bash for code analysis
- ✅ `research-agent` - Full bash access maintained
- ✅ `general-assistant` - Inherits all parent permissions

## Impact
- **Sub-agents now inherit parent permission policies**
- **No breaking changes** - existing behavior improved
- **Backward compatible** - graceful fallback if registries aren't SimpleRegistry
- **Proper logging** - debug logs when permission checker is inherited

## Related Issues
- Unimplemented TODO in `factory.go:registerTools()` - wildcards don't expand
- Permission checker not part of Registry interface contract
- No tests for sub-agent permission inheritance

## Future Improvements
1. **Interface Enhancement**: Add permission methods to `Registry` interface
2. **Testing**: Add comprehensive sub-agent permission inheritance tests  
3. **Documentation**: Update SDK docs to explain permission inheritance model
4. **Wildcard Expansion**: Implement the TODO in `registerTools()` for completeness

## Files Modified
- ✅ `/home/rincon/swarm/SDK/tools/builtin/delegate_task.go` (11 lines added)
- 📄 Backup: `/home/rincon/swarm/SDK/tools/builtin/delegate_task.go.backup`

## Commit Info
```
Fix: Sub-agent permission inheritance

Sub-agents now properly inherit permission policies from their parent
agent's tool registry. Previously, sub-agents were created with default
restrictive permission checkers even though tools were copied.

This fix ensures sub-agents can execute bash commands, write files, and
perform all operations their parent agent is permitted to do.
```

---

**Date**: 2026-01-28  
**Author**: Debug Session  
**Status**: ✅ Fixed & Verified
