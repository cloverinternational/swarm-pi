# Workflow Credential Integration - Requirements & Test Specification

## Problem Statement

Workflows currently fail with "no credentials found for provider anthropic" when users authenticate via Claude Code OAuth. The workflow system only checks environment variables, while the TUI has a comprehensive multi-source credential system.

## Root Cause

**Current Workflow Credential Flow:**
```
WorkflowManager.Execute() 
  → WorkflowAgentFactory.LoadCredentialsFromEnvironment()
  → Only checks: ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.
  → ❌ FAILS when using OAuth (no env vars set)
```

**TUI Credential Flow:**
```
buildProvider()
  → getAnthropicAuth() / getOpenAIAuth() / etc.
  → Checks in order:
    1. OAuth tokens (old format)
    2. Account registry (new format)
    3. credentials.json file
    4. Environment variables
  → ✅ WORKS with OAuth, API keys, credentials.json
```

## Solution Requirements

### Functional Requirements

**FR-1: Credential Resolution Parity**
- Workflows MUST use the same credential resolution logic as the TUI
- MUST support all credential sources: OAuth, Account Registry, credentials.json, Environment Variables
- MUST resolve credentials in the same priority order as the TUI

**FR-2: Provider Support**
- MUST support all providers: anthropic, openai, gemini, cerebras, openrouter, z.ai
- MUST handle OAuth providers: anthropic (Claude Code), openai (Codex), gemini
- MUST handle API key providers: cerebras, openrouter, z.ai

**FR-3: Current Provider/Model Resolution**
- MUST correctly resolve `@current` provider to user's active provider
- MUST correctly resolve `@current` model to user's active model
- MUST pass current provider's credentials to workflow factory

**FR-4: Backward Compatibility**
- MUST NOT break existing workflows using environment variables
- MUST NOT require changes to existing workflow YAML files
- MUST continue to support LoadCredentialsFromEnvironment() as fallback

**FR-5: Error Handling**
- MUST provide clear error messages when no credentials found
- MUST indicate which credential sources were checked
- MUST preserve original error context from credential helpers

### Technical Requirements

**TR-1: Code Reuse**
- MUST reuse existing credential resolution functions: getAnthropicAuth(), getOpenAIAuth(), getGeminiAuth(), getOpenAICompatibleAuth()
- MUST NOT duplicate credential resolution logic
- MUST extract credentials from SDK's buildProvider() chain

**TR-2: Minimal Changes**
- SHOULD minimize changes to existing code
- SHOULD use existing interfaces where possible
- SHOULD avoid refactoring unrelated code

**TR-3: Thread Safety**
- MUST be thread-safe for concurrent workflow executions
- MUST NOT cause race conditions in credential access

**TR-4: Security**
- MUST NOT log sensitive credentials (API keys, tokens)
- MUST NOT expose credentials in error messages
- MUST maintain existing security practices

## Test Specification

### Test Environment Setup

**Test Data Required:**
1. OAuth token file: `~/.swarmos/oauth/anthropic_token.json`
2. Account registry: `~/.swarmos/accounts/anthropic/account-*.json`
3. Credentials file: `~/.swarmos/credentials.json`
4. Environment variables: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.
5. Test workflow YAML files

**Test Workflow Files:**
- `workflows/test_oauth.yaml` - Uses `@current` provider
- `workflows/test_explicit.yaml` - Uses explicit provider name
- `workflows/test_multi_provider.yaml` - Uses multiple providers

### Test Cases

#### TC-1: OAuth Authentication (CRITICAL)
**Preconditions:**
- User authenticated with Claude Code OAuth
- OAuth token exists at `~/.swarmos/oauth/anthropic_token.json`
- NO environment variables set
- TUI shows "Claude Code (OAuth) - Claude 4 Opus"

**Test Steps:**
1. Load workflow using `@current` provider
2. Execute workflow with input "What is 2+2?"
3. Monitor workflow execution

**Expected Results:**
- ✅ Workflow starts successfully
- ✅ Agent creates using OAuth credentials
- ✅ No "no credentials found" error
- ✅ Workflow completes with result

**Actual Behavior Before Fix:**
- ❌ Error: "no credentials found for provider anthropic"
- ❌ Workflow fails immediately

#### TC-2: Account Registry Authentication
**Preconditions:**
- New account registry format used
- Account file exists at `~/.swarmos/accounts/anthropic/account-{id}.json`
- NO OAuth tokens in old format
- NO environment variables

**Test Steps:**
1. Load workflow using `anthropic` provider
2. Execute workflow
3. Verify credentials loaded from account registry

**Expected Results:**
- ✅ Workflow loads credentials from account registry
- ✅ Workflow executes successfully
- ✅ Agent uses token from account file

#### TC-3: credentials.json Authentication
**Preconditions:**
- Credentials file at `~/.swarmos/credentials.json` with:
  ```json
  {
    "providers": {
      "anthropic": {
        "api_key": "sk-ant-test-key"
      }
    }
  }
  ```
- NO OAuth tokens
- NO environment variables

**Test Steps:**
1. Load workflow using `anthropic` provider
2. Execute workflow
3. Verify credentials loaded from credentials.json

**Expected Results:**
- ✅ Workflow loads credentials from credentials.json
- ✅ Workflow executes successfully

#### TC-4: Environment Variable Fallback
**Preconditions:**
- Environment variable set: `export ANTHROPIC_API_KEY=sk-ant-test-key`
- NO OAuth tokens
- NO credentials.json

**Test Steps:**
1. Load workflow using `anthropic` provider
2. Execute workflow
3. Verify credentials loaded from environment

**Expected Results:**
- ✅ Workflow loads credentials from environment variable
- ✅ Workflow executes successfully
- ✅ Backward compatibility maintained

#### TC-5: @current Provider Resolution
**Preconditions:**
- TUI configured with provider="ClaudeCode", model="claude-opus-4-20250514"
- OAuth authenticated
- Workflow YAML uses `provider: "@current"` and `model: "@current"`

**Test Steps:**
1. Load workflow with @current values
2. Execute workflow
3. Verify resolution

**Expected Results:**
- ✅ @current resolves to "anthropic" provider
- ✅ @current model resolves to "claude-opus-4-20250514"
- ✅ Credentials loaded using resolved provider name

#### TC-6: Multiple Providers
**Preconditions:**
- Workflow uses multiple agents with different providers
- Agent 1: anthropic (OAuth)
- Agent 2: openai (API key in env)
- Agent 3: gemini (OAuth)

**Test Steps:**
1. Load multi-provider workflow
2. Execute workflow
3. Verify each agent gets correct credentials

**Expected Results:**
- ✅ Agent 1 uses Anthropic OAuth credentials
- ✅ Agent 2 uses OpenAI env variable credentials
- ✅ Agent 3 uses Gemini OAuth credentials
- ✅ All agents execute successfully

#### TC-7: No Credentials Error
**Preconditions:**
- NO credentials available for provider "foobar"
- NO OAuth, NO credentials.json, NO env vars

**Test Steps:**
1. Load workflow using provider "foobar"
2. Execute workflow

**Expected Results:**
- ✅ Clear error message: "failed to get credentials for provider foobar"
- ✅ Error indicates which sources were checked
- ✅ Workflow status shows "failed"

#### TC-8: Credential Priority Order
**Preconditions:**
- ALL credential sources available:
  - OAuth token (sk-ant-oauth-token)
  - credentials.json (sk-ant-file-key)
  - Environment variable (sk-ant-env-key)

**Test Steps:**
1. Execute workflow
2. Verify which credential is used

**Expected Results:**
- ✅ OAuth token used (highest priority)
- ✅ Other sources ignored
- ✅ Priority matches TUI behavior

### Integration Tests

#### IT-1: SDK Integration Test
**Verify:** GetCredentialsForProvider() function works correctly
```go
// Test that helper function returns correct credentials
apiKey, baseURL, isOAuth, err := GetCredentialsForProvider("anthropic")
assert.NoError(t, err)
assert.NotEmpty(t, apiKey)
assert.True(t, isOAuth)
```

#### IT-2: Workflow Factory Test
**Verify:** WorkflowAgentFactory receives and uses credentials
```go
factory := mode.NewWorkflowAgentFactory(baseFactory)
factory.SetCredentials("anthropic", "test-key", "")
creds, err := factory.getCredentials("anthropic")
assert.NoError(t, err)
assert.Equal(t, "test-key", creds.APIKey)
```

#### IT-3: End-to-End Test
**Verify:** Complete workflow execution with OAuth
```go
// Full workflow execution test
execID, err := workflowManager.Execute(ctx, workflowID, input, factory, "anthropic", "claude-opus-4")
assert.NoError(t, err)
// Wait for completion
result := waitForWorkflowCompletion(execID)
assert.Equal(t, mode.WorkflowStatusCompleted, result.Status)
```

## Acceptance Criteria

### Must Have (P0)
- ✅ TC-1: OAuth authentication works for workflows
- ✅ TC-4: Environment variable fallback still works
- ✅ TC-5: @current provider resolution works
- ✅ No regression in existing TUI functionality
- ✅ No security issues (credentials not logged)

### Should Have (P1)
- ✅ TC-2: Account registry support
- ✅ TC-3: credentials.json support
- ✅ TC-6: Multiple providers work
- ✅ TC-7: Clear error messages

### Nice to Have (P2)
- ✅ TC-8: Correct priority order verified
- ✅ Integration tests pass
- ✅ Code documentation updated

## Implementation Checklist

### Phase 1: Extract Credential Resolution
- [ ] Add `GetCredentialsForProvider()` function to `sdk_integration.go`
- [ ] Export credential resolution for reuse
- [ ] Add unit tests for credential resolution

### Phase 2: Update Workflow Manager
- [ ] Modify `Execute()` to use new credential function
- [ ] Pass credentials to WorkflowAgentFactory
- [ ] Preserve backward compatibility

### Phase 3: Testing & Validation
- [ ] Run TC-1 through TC-8
- [ ] Verify no regressions in TUI
- [ ] Test with real OAuth tokens
- [ ] Update documentation

### Phase 4: Code Review
- [ ] Check for security issues
- [ ] Verify thread safety
- [ ] Review error handling
- [ ] Confirm code style consistency

## Success Metrics

**Before Fix:**
- Workflow with OAuth: ❌ 100% failure rate
- Error: "no credentials found for provider anthropic"

**After Fix:**
- Workflow with OAuth: ✅ 100% success rate
- Workflow with API key: ✅ 100% success rate (maintained)
- Workflow with credentials.json: ✅ 100% success rate (new)
- All test cases: ✅ 100% pass rate

## Risk Assessment

**Low Risk:**
- Adding new function for credential extraction (no existing code changes)
- Passing credentials to WorkflowAgentFactory (already has SetCredentials method)

**Medium Risk:**
- Modifying workflow_manager.go Execute() method (active code path)
- Mitigation: Thorough testing with all credential types

**High Risk:**
- None identified

## Rollback Plan

If issues arise:
1. Revert changes to `workflow_manager.go`
2. Keep `GetCredentialsForProvider()` function (harmless if unused)
3. Workflows fall back to environment variable behavior
4. No data loss or corruption risk
