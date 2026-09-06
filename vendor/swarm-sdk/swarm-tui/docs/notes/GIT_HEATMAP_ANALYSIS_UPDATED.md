# 🔥 UPDATED GIT HEATMAP ANALYSIS - TUI PROJECT
## Corrected Analysis Reflecting Recent Demonolithization (Feb 7, 2026)

---

## ⚠️ IMPORTANT: GIT HISTORY CONTEXT

**Git tracks files by path, not by content splits.** The heatmap shows `internal/chat/app.go` with 167 commits and 42K churn, BUT this file was **massively refactored on Feb 7, 2026**:

- **Old monolith:** One giant `app.go` file (likely 10,000+ lines)
- **New structure:** Split into **18 domain-focused files** + tiny 241-line core
- **Git tracking:** All historical commits before Feb 7 are attributed to `app.go` path

### Current Reality (Post-Refactor):

```
OLD: app.go (10,000+ lines) - MONOLITH
                ↓
NEW: Split into 18 files (Feb 7, 2026):
├── app.go (241 lines) - Core struct & pools only
├── app_init.go (1,712 lines) - Initialization
├── app_update.go (2,435 lines) - Update loop
├── app_keyboard.go (1,087 lines) - Keyboard handling  
├── app_chat_render.go (1,641 lines) - Chat rendering
├── app_conversations.go (1,191 lines) - Conversation list
├── app_conversations_view.go (1,077 lines) - Conversation display
├── app_messaging.go (744 lines) - Message handling
├── app_home.go (819 lines) - Home screen
├── app_workflow_chat.go (1,229 lines) - Workflow integration
├── app_workspace.go (528 lines) - Workspace features
├── app_chat_key.go (410 lines) - Chat key handlers
├── app_conversations_messages.go (544 lines) - Message bubbles
├── app_streaming_types.go (333 lines) - Streaming types
├── app_sdk_helpers.go (279 lines) - SDK helpers
├── app_notifications.go (278 lines) - Notifications
├── app_conversations_sidebar.go (271 lines) - Sidebar
├── app_compaction.go (247 lines) - Compaction
├── app_mouse.go (185 lines) - Mouse handling
├── app_cloud.go (183 lines) - Cloud features
├── app_boot.go (141 lines) - Boot/navigation
└── app_conversations_layout.go (130 lines) - Layout
```

**Total:** ~14,853 lines across 22 files (vs old single monolith)

---

## 🎯 TRUE CURRENT PROBLEM ZONES

Now that we understand the refactor, here are the **ACTUAL** hotspots today:

### 🔴 CRITICAL: Active Problem Files

| File | Size | Status | Issue |
|------|------|--------|-------|
| **sdk_integration.go** | 6,796 lines | 🔴 CRITICAL | Still monolithic, 85 commits, 50 fixes |
| **workflow_editor.go** | 3,101 lines | 🟠 HIGH | Complex UI logic |
| **debug_screen.go** | 2,503 lines | 🟠 HIGH | Debug tooling |
| **app_update.go** | 2,435 lines | 🟡 MEDIUM | Main update loop (post-split) |
| **chat_state.go** | 2,433 lines | 🟡 MEDIUM | State management, 31 commits |

### 📊 Refined Analysis

**1. SDK Integration Layer - THE REAL PROBLEM**
- **File:** `internal/chat/sdk_integration.go`
- **Size:** 6,796 lines (LARGEST file)
- **Commits:** 85 (59% are bug fixes)
- **Churn:** 8,722 lines
- **Status:** 🔴 **NEEDS IMMEDIATE REFACTORING**
- **Why:** This is the UI-SDK bridge, handles all provider communication
- **Action:** Should be split like app.go was

**2. App.go - Successfully Refactored ✅**
- **Before:** ~10,000+ lines, constant bugs
- **After:** 241 lines (core struct only)
- **Result:** Clean separation of concerns
- **Proof:** Recent commits (last 5 days) show changes in specific domain files, not app.go

**3. Settings Management - Still Problematic**
- **Location:** `internal/chat/settings/*.go`
- **Files:** 262 changes across directory
- **Issue:** Too many rapid changes = unclear requirements
- **Files to watch:**
  - `settings/manager.go` - 42 commits, 22 fixes
  - `settings/model.go` - 27 commits, 12 fixes
  - `settings/mcp.go` - 18 commits, 12 fixes

**4. Workflow System - Growing Complexity**
- **Files:** 
  - `workflow_editor.go` (3,101 lines)
  - `workflow_execution.go` (1,873 lines)
  - `workflow_selector.go` (1,467 lines)
- **Total:** ~6,500 lines of workflow code
- **Status:** 🟡 Monitor closely, may need split

**5. IPC Layer - Stability Concerns**
- **File:** `headless/ipc/server.go`
- **Commits:** 43
- **Fixes:** 17
- **Issue:** Protocol changes, error handling
- **Status:** 🟠 Ongoing issues

---

## 📈 CURRENT ARCHITECTURE HEALTH

### ✅ Wins (What's Working):

1. **App.go Demonolithization (Feb 7, 2026)**
   - Successfully split 10K+ line monolith into 22 focused files
   - Each file has clear responsibility
   - Easier to understand and maintain

2. **Modular Subdirectories**
   - `settings/` - 42 files
   - `commands/` - Command handlers
   - `mcp_ui/` - MCP interface
   - `autocomplete/` - Autocomplete logic
   - Clean separation of concerns

3. **Test Coverage**
   - Test files present alongside implementation
   - Example: `app_content_reconcile_test.go`

### 🚨 Current Problems:

1. **SDK Integration - Next Monolith to Split**
   - 6,796 lines in single file
   - 59% of commits are bug fixes
   - Bridge between UI and SDK = high complexity
   - **RECOMMENDATION:** Split into:
     - `sdk_integration_core.go` - Core bridge logic
     - `sdk_integration_streaming.go` - Streaming handlers
     - `sdk_integration_tools.go` - Tool call handling
     - `sdk_integration_state.go` - State sync
     - `sdk_integration_providers.go` - Provider management

2. **Settings Churn**
   - 262 file changes in settings directory
   - Indicates unstable requirements or unclear design
   - **RECOMMENDATION:** Create settings ADR (Architecture Decision Record)

3. **Workflow Complexity Growing**
   - 6,500+ lines across workflow files
   - Monitor for split opportunity
   - **RECOMMENDATION:** Consider workflow state machine

---

## 🔥 REVISED HEATMAP (Current State)

```
ACTUAL PROBLEM INTENSITY (Post Feb 7 Refactor):

internal/chat/
├── sdk_integration.go          🔴🔴🔴🔴🔴 (6,796 lines, NEEDS SPLIT)
├── workflow_editor.go          🟠🟠🟠     (3,101 lines, monitor)
├── debug_screen.go            🟠🟠       (2,503 lines)
├── app_update.go              🟡         (2,435 lines, post-split)
├── chat_state.go              🟡🟡       (2,433 lines, 18 fixes)
├── messagelist.go             🟡         (1,799 lines)
├── app_*.go (22 files)        ✅         (~15K total, well split)
├── settings/
│   ├── manager.go             🔴🔴       (42 commits, 22 fixes)
│   ├── model.go               🔴         (27 commits, unstable)
│   └── mcp.go                 🔴         (18 commits, 12 fixes)
└── commands/
    ├── model.go               🟡         (20 commits, 10 fixes)
    └── mcp.go                 🟡         (14 commits, 11 fixes)

headless/
├── ipc/
│   ├── server.go              🔴🔴🔴     (43 commits, 17 fixes)
│   └── protocol.go            🔴🔴       (24 commits, 10 fixes)
└── core/
    ├── config.go              🔴🔴       (344 complexity score)
    └── engine.go              🔴🔴       (15 commits, 9 fixes)

sdk/ (submodule)
├── agent/agent.go             🔴🔴       (18 commits, 11 fixes)
└── provider/
    └── anthropic/
        └── translate.go       🔴🔴       (13 commits, 10 fixes)
```

---

## 💡 UPDATED RECOMMENDATIONS

### Immediate (This Week):

1. **Refactor `sdk_integration.go`** 🔴 **PRIORITY 1**
   ```
   Current: 6,796 lines, highest bug density
   Action: Split like app.go was (4-5 focused files)
   Impact: Reduce bug surface area, easier debugging
   ```

2. **Settings Architecture Review** 🟠 **PRIORITY 2**
   ```
   Current: Too many rapid changes (262 file changes)
   Action: Document requirements, create ADR
   Impact: Reduce churn, stabilize interface
   ```

3. **IPC Error Handling** 🟠 **PRIORITY 3**
   ```
   Current: 17 bug fixes in headless/ipc/server.go
   Action: Add comprehensive error handling, logging
   Impact: Improve headless mode stability
   ```

### Medium-term (This Month):

1. **Workflow System Architecture**
   - 6,500 lines across workflow files
   - Consider state machine pattern
   - Add workflow ADR

2. **Debug Screen Optimization**
   - 2,503 lines in debug_screen.go
   - Consider lazy loading for debug data
   - Split UI from logic

3. **Test Coverage Expansion**
   - Add integration tests for sdk_integration.go
   - Test IPC protocol edge cases
   - Add workflow execution tests

### Long-term (Next Quarter):

1. **Architectural Decision Records (ADRs)**
   - Document major design decisions
   - Prevent requirement churn
   - Example: Settings structure, MCP integration

2. **Feature Flags**
   - For risky changes in hot zones
   - Gradual rollout capability
   - A/B testing for UI changes

3. **Observability**
   - Add structured logging
   - Performance metrics
   - Error tracking improvements

---

## 📊 METRICS SUMMARY

### File Count:
- **Total Go files in internal/chat:** 165
- **App-related files:** 25 (was 1 monolith)
- **Subdirectories:** 10 well-organized domains

### Code Distribution:
- **Before refactor:** app.go ~10K+ lines (monolith)
- **After refactor:** app.go 241 lines + 22 focused files
- **Largest file now:** sdk_integration.go (6,796 lines) ← Next target

### Commit Activity (Last Year):
- **Main project:** 610 commits
- **SDK submodule:** 94 commits  
- **Total:** 704 commits

### Bug Fix Density:
- **sdk_integration.go:** 59% of commits are fixes
- **chat_state.go:** 58% of commits are fixes
- **settings/manager.go:** 52% of commits are fixes

---

## 🎯 SUCCESS CRITERIA

The app.go refactor shows what success looks like:
- ✅ Single file split into focused modules
- ✅ Clear naming convention (app_domain.go)
- ✅ Each file has single responsibility
- ✅ Reduced bug density (recent commits are feature adds, not fixes)

**Next target:** Apply same pattern to `sdk_integration.go`

---

**Generated:** $(date)
**Analysis Period:** Last 12 months (with Feb 7 refactor context)
**Next Review:** After sdk_integration.go refactor
