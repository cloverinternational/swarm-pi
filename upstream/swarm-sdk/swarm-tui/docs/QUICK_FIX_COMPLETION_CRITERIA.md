# Quick Fix: Completion Criteria Mismatch

## Your Current Setup

You have a group named "Parallel Research" with 3 agents running in **parallel**:
- Query Agent
- Email Search Agent  
- RAG Context Agent

With completion type probably: `type: all` (all 3 must succeed)

## The Problem

With `type: all`, **ALL 3 agents must complete**. But if even ONE agent:
- Times out
- Errors
- Takes too long
- Hits a rate limit

Then the **entire group fails**.

## Quick Diagnostic

Check your workflow YAML:

```yaml
config:
  execution: parallel  # ← You have this
  completion:
    type: all  # ← Is this what you have?
```

If `type: all` and one agent is flaky → Group fails

## Immediate Fix: Use `majority` Instead

```yaml
config:
  execution: parallel
  completion:
    type: majority  # ← Changes 1/3 to 2/3
```

Now:
- 2 out of 3 agents must complete
- 1 agent can fail/timeout/error
- Group succeeds when 2 finish

### Results With This Fix

Your situation:
```
Before (type: all):
- Query Agent: ✅
- Email Search Agent: ❌ (timeout/error)
- RAG Context Agent: ✅
Result: 2/3 pass, but needed 3/3 → FAIL

After (type: majority):
- Query Agent: ✅
- Email Search Agent: ❌ (timeout/error)
- RAG Context Agent: ✅
Result: 2/3 pass, need >50% → SUCCESS ✅
```

## Alternative: Use `first`

If you only need **one agent to succeed**:

```yaml
completion:
  type: first  # Just one agent needs to finish
```

## How To Choose

| Type | Use When | Your Case |
|------|----------|-----------|
| `all` | All agents are critical | ❌ Not for you |
| `majority` | Most agents matter | ✅ BEST FOR YOU |
| `first` | Any answer is fine | ❓ Maybe |
| `consensus` | Need agreement | ❓ Maybe |

## What I Recommend

Change to:

```yaml
config:
  execution: parallel
  completion:
    type: majority  # Allows 1 failure
```

This will:
- ✅ Let Email Search Agent fail without breaking workflow
- ✅ Keep Query + RAG agents' output
- ✅ Advance to next layer
- ✅ Show which agent failed in logs

Then you can debug why Email Search Agent is timing out or erroring.

## The Real Root Cause

I suspect the **Email Search Agent** is:
1. **Timing out** - Gmail API search is slow
2. **Hitting rate limits** - Gmail API limits
3. **Erroring** - Invalid search query or credentials
4. **Still running** - Group times out before it completes

Using `majority` lets you work around this while you debug.

---

**Try this fix first, it will likely solve your immediate problem!**

