# 🔥 GIT HEATMAP ANALYSIS - TUI PROJECT
## Problem Zones & High-Change Areas (Last 12 Months)

---

## 🚨 CRITICAL ZONE: Top 5 Problem Files

These files have the highest combination of commits, bug fixes, and code churn:

| File | Commits | Bug Fixes | Churn | Status |
|------|---------|-----------|-------|--------|
| **internal/chat/app.go** | 167 | 97 | 42K lines | 🔴 CRITICAL |
| **internal/chat/sdk_integration.go** | 85 | 50 | 8.7K lines | 🔴 CRITICAL |
| **headless/ipc/server.go** | 43 | 17 | 4.2K lines | 🟠 HIGH |
| **internal/chat/settings/manager.go** | 42 | 22 | 2.4K lines | 🟠 HIGH |
| **internal/chat/chat_state.go** | 31 | 18 | 2.2K lines | 🟡 MEDIUM |

### 📊 Key Insights:
- **internal/chat/app.go**: 58% of commits are bug fixes - extremely unstable
  - Split into multiple files (app_*.go) but git tracks it as single file
  - Has been refactored extensively but remains problematic
  - 4 authors working on it = coordination issues

- **internal/chat/sdk_integration.go**: Bridge between UI and SDK
  - 59% of commits are fixes
  - Integration point = high complexity
  - 6,796 lines - needs refactoring

---

## 🔥 HEAT ZONES BY DIRECTORY

Files changed in last year by directory:

| Directory | Changed Files | Heat Level |
|-----------|---------------|------------|
| **internal/chat** | 843 | 🔴🔴🔴🔴🔴 |
| **internal/chat/settings** | 262 | 🔴🔴🔴 |
| **internal/chat/commands** | 149 | 🔴🔴 |
| **headless/ipc** | 117 | 🔴🔴 |
| **headless/core** | 91 | 🔴 |
| **sdk/tools/ii** | 72 | 🔴 |
| **sdk/provider/anthropic** | 71 | 🔴 |

---

## 💥 HIGH CHURN FILES (Most Lines Changed)

Files with highest total line changes (additions + deletions):

| File | Total Churn | Commits | Churn/Commit |
|------|-------------|---------|--------------|
| internal/chat/app.go | 42,000 | 167 | 251 |
| internal/chat/sdk_integration.go | 8,722 | 85 | 103 |
| internal/chat/settings/model.go | 8,553 | 27 | 317 |
| internal/chat/commands/model.go | 6,121 | 20 | 306 |
| internal/chat/settings/mcp.go | 5,027 | 18 | 279 |
| headless/ipc/server.go | 4,239 | 43 | 99 |

---

## 🐛 BUG FIX HOTSPOTS (Recent 3 Months)

Files with most bug fix commits recently:

| File | Recent Fixes | Total Commits | Fix % |
|------|--------------|---------------|-------|
| internal/chat/app.go | 93 | 167 | 56% |
| internal/chat/sdk_integration.go | 43 | 85 | 51% |
| internal/chat/settings/manager.go | 18 | 42 | 43% |
| internal/chat/chat_state.go | 18 | 31 | 58% |
| cmd/swarmos/main.go | 14 | 22 | 64% |
| internal/chat/sidepanel.go | 13 | 28 | 46% |

---

## 🎯 COMPLEXITY HOTSPOTS (High Score = Churn × Commits)

These files have both high change frequency AND high churn (complexity indicator):

| File | Score | Churn | Commits | Size |
|------|-------|-------|---------|------|
| headless/core/config.go | 344 | 1,811 | 19 | Medium |
| headless/cmd/ipc-server/main.go | 335 | 1,523 | 22 | Medium |
| cmd/swarmos/main.go | 221 | 923 | 24 | Small |
| headless/cloudsync/manager.go | 83 | 1,673 | 5 | Large |

---

## 📈 TRENDING ISSUES

### Patterns Observed:
1. **UI/SDK Integration Layer** - Constant issues
   - Files: sdk_integration.go, app.go, bridge.go
   - Root cause: Complex state synchronization

2. **Settings Management** - Frequent changes
   - Files: settings/*.go
   - Root cause: Expanding feature set, UI refactors

3. **IPC Communication** - Stability issues
   - Files: headless/ipc/*.go
   - Root cause: Protocol changes, error handling

4. **Provider Translation** - Regular fixes
   - Files: sdk/provider/*/translate.go
   - Root cause: API changes from providers

---

## 🎨 VISUALIZATION SUMMARY

```
PROBLEM INTENSITY HEATMAP:

internal/chat/
├── app.go                      🔴🔴🔴🔴🔴 (167 commits, 97 fixes)
├── sdk_integration.go          🔴🔴🔴🔴   (85 commits, 50 fixes)
├── chat_state.go              🔴🔴🔴     (31 commits, 18 fixes)
├── settings/
│   ├── manager.go             🔴🔴🔴     (42 commits, 22 fixes)
│   ├── model.go               🔴🔴       (27 commits, 12 fixes)
│   └── mcp.go                 🔴🔴       (18 commits, 12 fixes)
└── commands/
    ├── model.go               🔴🔴       (20 commits, 10 fixes)
    └── mcp.go                 🔴         (14 commits, 11 fixes)

headless/
├── ipc/
│   ├── server.go              🔴🔴🔴     (43 commits, 17 fixes)
│   └── protocol.go            🔴🔴       (24 commits, 10 fixes)
└── core/
    ├── config.go              🔴🔴       (18 commits, 8 fixes)
    └── engine.go              🔴🔴       (15 commits, 9 fixes)

sdk/
├── agent/agent.go             🔴🔴       (18 commits, 11 fixes)
└── provider/
    └── anthropic/
        └── translate.go       🔴🔴       (13 commits, 10 fixes)
```

---

## 💡 RECOMMENDATIONS

### Immediate Actions:
1. **Refactor internal/chat/app.go** 
   - Already split into app_*.go files but needs cleaner separation
   - Consider state machine pattern
   - Add integration tests

2. **Stabilize SDK Integration**
   - Add comprehensive error handling
   - Implement circuit breaker pattern
   - Better state validation

3. **Settings Architecture Review**
   - Too many changes indicate unclear requirements
   - Consolidate settings types
   - Add schema validation

### Long-term:
1. Add architectural decision records (ADRs)
2. Implement feature flags for risky changes
3. Increase test coverage in hot zones
4. Consider breaking apart monolithic files

---

**Generated:** $(date)
**Period:** Last 12 months
**Total Commits Analyzed:** 610 (main) + 94 (SDK) = 704
