# File Write Tool Implementation Plan

## Source Analysis: Crush Write Tool

This document breaks down the Crush file write tool into atomic requirements, analyzes the LLM user story, identifies edge cases, and provides a sequential implementation plan for SwarmOS SDK.

---

## 1. LLM User Story & Behavior Flow

### What the LLM Sees (Tool Description)

```markdown
Creates or updates files in filesystem for saving/modifying text content.

<usage>
- Provide file path to write
- Include content to write to file
- Tool creates necessary parent directories automatically
</usage>

<features>
- Creates new files or overwrites existing ones
- Auto-creates parent directories if missing
- Checks if file modified since last read for safety
- Avoids unnecessary writes when content unchanged
</features>

<limitations>
- Read file before writing to avoid conflicts
- Cannot append (rewrites entire file)
</limitations>
```

### LLM Decision Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│                    LLM DECIDES TO WRITE A FILE                       │
└─────────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Q1: Did I read this file first?                                      │
│     - If YES → proceed                                               │
│     - If NO (new file) → proceed                                     │
│     - If NO (existing file) → SHOULD use View tool first            │
└─────────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Q2: What parameters do I provide?                                    │
│     - file_path: absolute path (required)                            │
│     - content: full file content (required)                          │
└─────────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     TOOL EXECUTES                                    │
└─────────────────────────────────────────────────────────────────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              ▼                    ▼                    ▼
        ┌─────────┐          ┌─────────┐          ┌─────────┐
        │ SUCCESS │          │  ERROR  │          │ BLOCKED │
        └─────────┘          └─────────┘          └─────────┘
              │                    │                    │
              ▼                    ▼                    ▼
┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
│ LLM sees:        │  │ LLM sees:        │  │ LLM sees:        │
│ - File written   │  │ - Error message  │  │ - Permission     │
│ - Diff stats     │  │ - What went wrong│  │   denied         │
│ - LSP errors     │  │ - Recovery hint  │  │ - User must      │
│                  │  │                  │  │   approve        │
└──────────────────┘  └──────────────────┘  └──────────────────┘
```

### LLM Behavior Expectations

| Scenario | Expected LLM Behavior | Tool Response |
|----------|----------------------|---------------|
| Create new file | Provide path + content | Success + diff |
| Overwrite existing | Read first, then write | Success + diff |
| Write without reading | Write anyway | Error: "modified since last read" |
| Write identical content | Write anyway | Error: "already contains exact content" |
| Write to directory path | Write to path | Error: "Path is a directory" |
| Permission denied | Wait for user | Blocked until approved |

---

## 2. Atomic Requirements Breakdown

### Phase 1: Input Validation (Lines 58-66)

```
REQ-1.1: Validate file_path is not empty
         - Input: params.FilePath
         - Output: Error "file_path is required" if empty
         - Type: Soft error (user-fixable)

REQ-1.2: Validate content is not empty
         - Input: params.Content
         - Output: Error "content is required" if empty
         - Type: Soft error (user-fixable)

REQ-1.3: Resolve path relative to working directory
         - Input: params.FilePath, workingDir
         - Output: Absolute path
         - Function: filepathext.SmartJoin(workingDir, params.FilePath)
```

### Phase 2: File State Checks (Lines 68-87)

```
REQ-2.1: Check if path exists (os.Stat)
         - Input: resolved filePath
         - Output: fileInfo or nil
         - Branches: exists | not exists | error

REQ-2.2: Reject if path is a directory
         - Input: fileInfo.IsDir()
         - Output: Error "Path is a directory, not a file: {path}"
         - Type: Soft error (user-fixable)

REQ-2.3: Check modification time vs last read time (RACE CONDITION PROTECTION)
         - Input: fileInfo.ModTime(), getLastReadTime(filePath)
         - Output: Error if modTime > lastRead
         - Error: "File {path} has been modified since it was last read.
                   Last modification: {modTime}
                   Last read: {lastRead}
                   Please read the file again before modifying it."
         - Type: Soft error (user must re-read file)

REQ-2.4: Check if content is identical (IDEMPOTENCY CHECK)
         - Input: existing file content, params.Content
         - Output: Error if identical
         - Error: "File {path} already contains the exact content. No changes made."
         - Type: Soft error (no-op optimization)

REQ-2.5: Handle stat errors that aren't "not exists"
         - Input: os.Stat error
         - Output: Hard error "error checking file: {err}"
         - Type: Hard error (system issue)
```

### Phase 3: Directory Creation (Lines 89-92)

```
REQ-3.1: Create parent directories if needed
         - Input: filepath.Dir(filePath)
         - Output: Directories created with 0755 permissions
         - Error: "error creating directory: {err}"
         - Type: Hard error (system issue)
```

### Phase 4: Session & Diff Generation (Lines 94-111)

```
REQ-4.1: Read existing content for diff (if file exists)
         - Input: filePath, fileInfo
         - Output: oldContent string (empty if new file)

REQ-4.2: Get session ID from context
         - Input: ctx
         - Output: sessionID string
         - Error: "session_id is required"
         - Type: Hard error (programming error)

REQ-4.3: Generate unified diff
         - Input: oldContent, newContent, relativePath
         - Output: diff string, additions count, removals count
         - Function: diff.GenerateDiff(old, new, path)
```

### Phase 5: Permission Request (Lines 113-130)

```
REQ-5.1: Request write permission from user
         - Input: CreatePermissionRequest{
             SessionID, Path, ToolCallID, ToolName,
             Action: "write",
             Description: "Create file {path}",
             Params: { FilePath, OldContent, NewContent }
           }
         - Output: boolean (approved/denied)
         - Blocking: YES - waits for user response

REQ-5.2: Return permission denied error if rejected
         - Output: permission.ErrorPermissionDenied
         - Type: Soft error (user-denied)
```

### Phase 6: File Write Operation (Lines 132-135)

```
REQ-6.1: Write file atomically
         - Input: filePath, content bytes, 0644 permissions
         - Function: os.WriteFile(filePath, []byte(content), 0o644)
         - Error: "error writing file: {err}"
         - Type: Hard error (system issue)
```

### Phase 7: Version History (Lines 137-157)

```
REQ-7.1: Check if file exists in history
         - Input: ctx, filePath, sessionID
         - Function: files.GetByPathAndSession()
         - Output: file record or error

REQ-7.2: Create history record if new
         - Input: ctx, sessionID, filePath, oldContent
         - Function: files.Create()
         - Error handling: Log but continue

REQ-7.3: Detect manual user changes
         - Input: file.Content vs oldContent
         - Output: If different, create intermediate version
         - Rationale: User edited file outside of tool

REQ-7.4: Store new version
         - Input: ctx, sessionID, filePath, newContent
         - Function: files.CreateVersion()
         - Error handling: Log but continue
```

### Phase 8: State Recording (Lines 159-160)

```
REQ-8.1: Record file write timestamp
         - Function: recordFileWrite(filePath)
         - Updates: fileRecords[path].writeTime

REQ-8.2: Record file read timestamp (write implies read)
         - Function: recordFileRead(filePath)
         - Updates: fileRecords[path].readTime
         - Rationale: After write, tool "knows" current content
```

### Phase 9: LSP Integration (Lines 162, 166)

```
REQ-9.1: Notify LSP clients of file change
         - Function: notifyLSPs(ctx, lspClients, filePath)
         - Actions: OpenFileOnDemand, NotifyChange, WaitForDiagnostics

REQ-9.2: Collect diagnostics after write
         - Function: getDiagnostics(filePath, lspClients)
         - Output: File diagnostics + project diagnostics
         - Format: <file_diagnostics>...</file_diagnostics>
                   <project_diagnostics>...</project_diagnostics>
                   <diagnostic_summary>X errors, Y warnings</diagnostic_summary>
```

### Phase 10: Response Formatting (Lines 164-173)

```
REQ-10.1: Format success message
          - Format: "<result>\nFile successfully written: {path}\n</result>"

REQ-10.2: Append diagnostics to response
          - Combine: result + getDiagnostics output

REQ-10.3: Attach metadata for UI rendering
          - Metadata: { Diff, Additions, Removals }
          - Purpose: UI can show diff view
```

---

## 3. Edge Cases & Error Scenarios

### Input Edge Cases

| Edge Case | Current Handling | Recommended |
|-----------|-----------------|-------------|
| Empty file_path | Error: "file_path is required" | ✅ Correct |
| Empty content | Error: "content is required" | ⚠️ Should allow (create empty file) |
| Relative path | SmartJoin resolves to absolute | ✅ Correct |
| Path with `..` | SmartJoin handles | ⚠️ Add security check |
| Unicode in path | OS handles | ✅ Correct |
| Very long path | OS limit | ⚠️ Add explicit check |

### File State Edge Cases

| Edge Case | Current Handling | Recommended |
|-----------|-----------------|-------------|
| File is directory | Error: "Path is a directory" | ✅ Correct |
| File is symlink | os.Stat follows symlink | ⚠️ Consider Lstat |
| File is read-only | os.WriteFile fails | ⚠️ Better error message |
| File locked by process | os.WriteFile fails | ⚠️ Better error message |
| Disk full | os.WriteFile fails | ⚠️ Better error message |
| Permission denied (OS) | os.WriteFile fails | ⚠️ Better error message |

### Race Condition Edge Cases

| Edge Case | Current Handling | Recommended |
|-----------|-----------------|-------------|
| User edits between read/write | modTime check catches | ✅ Correct |
| Another process edits | modTime check catches | ✅ Correct |
| File deleted between read/write | Write creates new | ⚠️ Warn user |
| Rapid successive writes | May miss race | ⚠️ Add file locking |

### Content Edge Cases

| Edge Case | Current Handling | Recommended |
|-----------|-----------------|-------------|
| Binary content | Writes as-is | ⚠️ Warn if binary detected |
| Huge file (>100MB) | Writes, may OOM | ⚠️ Add size limit |
| Null bytes in content | Writes as-is | ✅ Correct |
| Mixed line endings | Writes as-is | ⚠️ Normalize option |

### Session/History Edge Cases

| Edge Case | Current Handling | Recommended |
|-----------|-----------------|-------------|
| Missing session ID | Hard error | ✅ Correct |
| History service down | Logs, continues | ✅ Correct |
| Conflicting history | Creates intermediate | ✅ Correct |

### LSP Edge Cases

| Edge Case | Current Handling | Recommended |
|-----------|-----------------|-------------|
| No LSP available | Skip diagnostics | ✅ Correct |
| LSP timeout | 5 second wait | ✅ Correct |
| LSP crashes | Logged, continues | ✅ Correct |
| File not in LSP scope | LSP ignores | ✅ Correct |

---

## 4. Dependencies Required

### Core Dependencies

```go
// Standard library
import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"
)
```

### Internal Dependencies (Must Create)

```
1. File Tracking Service (file.go)
   - fileRecord struct { path, readTime, writeTime }
   - recordFileRead(path)
   - recordFileWrite(path)
   - getLastReadTime(path) time.Time
   - Thread-safe map with RWMutex

2. Diff Generation Service
   - GenerateDiff(old, new, path) (diff string, additions int, removals int)
   - Unified diff format

3. History Service Interface
   - GetByPathAndSession(ctx, path, sessionID) (File, error)
   - Create(ctx, sessionID, path, content) (File, error)
   - CreateVersion(ctx, sessionID, path, content) (Version, error)

4. Permission Service Interface
   - Request(CreatePermissionRequest) bool
   - CreatePermissionRequest struct

5. LSP Client Interface (Optional)
   - HandlesFile(path) bool
   - OpenFileOnDemand(ctx, path) error
   - NotifyChange(ctx, path) error
   - WaitForDiagnostics(ctx, timeout)
   - GetDiagnostics() map[URI][]Diagnostic

6. Path Utilities
   - SmartJoin(base, path) string
   - PathOrPrefix(path, prefix) string
```

---

## 5. Sequential Implementation Plan

### Step 1: File Tracking Service
**Files to create:** `sdk/tools/internal/filetracking/tracker.go`

```go
// Atomic unit: Track file read/write times for race condition detection
type FileTracker struct {
    records map[string]FileRecord
    mu      sync.RWMutex
}

type FileRecord struct {
    Path      string
    ReadTime  time.Time
    WriteTime time.Time
}

func (t *FileTracker) RecordRead(path string)
func (t *FileTracker) RecordWrite(path string)
func (t *FileTracker) GetLastReadTime(path string) time.Time
```

**Test cases:**
- Record read, verify time stored
- Record write, verify time stored
- Concurrent access safety
- Zero time for untracked files

---

### Step 2: Diff Generation
**Files to create:** `sdk/tools/internal/diff/diff.go`

```go
// Atomic unit: Generate unified diff between two strings
func GenerateDiff(oldContent, newContent, filePath string) (diff string, additions int, removals int)
```

**Test cases:**
- Empty old (new file)
- Empty new (delete all)
- Single line change
- Multi-line change
- No changes

---

### Step 3: History Service Interface
**Files to create:** `sdk/tools/history/interface.go`

```go
// Atomic unit: Version history for rollback support
type Service interface {
    GetByPathAndSession(ctx context.Context, path, sessionID string) (*File, error)
    Create(ctx context.Context, sessionID, path, content string) (*File, error)
    CreateVersion(ctx context.Context, sessionID, path, content string) (*Version, error)
}

type File struct {
    ID        string
    SessionID string
    Path      string
    Content   string
    CreatedAt time.Time
}

type Version struct {
    ID        string
    FileID    string
    Content   string
    CreatedAt time.Time
}
```

**Implementations:**
- In-memory (for testing)
- SQLite (for persistence)
- No-op (for minimal deployments)

---

### Step 4: Permission Service Enhancement
**Files to modify:** `sdk/tools/permission.go`

```go
// Enhance existing permission system
type WritePermissionParams struct {
    FilePath   string `json:"file_path"`
    OldContent string `json:"old_content,omitempty"`
    NewContent string `json:"new_content,omitempty"`
}
```

---

### Step 5: Write Tool Core Implementation
**Files to create:** `sdk/tools/builtin/file_write_v2.go`

Implementation order:
1. Parameter validation
2. Path resolution
3. File state checks (exists, directory, race condition)
4. Content identity check
5. Directory creation
6. Permission request
7. File write
8. History recording
9. State tracking
10. Response formatting

---

### Step 6: LSP Integration (Optional)
**Files to create:** `sdk/tools/internal/lsp/client.go`

```go
// Atomic unit: LSP client interface for diagnostics
type Client interface {
    HandlesFile(path string) bool
    NotifyChange(ctx context.Context, path string) error
    GetDiagnostics() map[string][]Diagnostic
}
```

---

### Step 7: Tool Prompt/Description
**Files to create:** `sdk/tools/builtin/file_write_v2.md`

```markdown
Creates or updates files in filesystem.

<usage>
- Provide file path to write
- Include content to write to file
- Tool creates parent directories automatically
</usage>

<features>
- Creates new files or overwrites existing
- Auto-creates parent directories
- Checks if file modified since last read (prevents conflicts)
- Avoids unnecessary writes when content unchanged
- Tracks version history for rollback
- Shows LSP diagnostics after write (if available)
</features>

<limitations>
- Read file before writing to avoid conflicts
- Cannot append (rewrites entire file)
- Large files (>10MB) may be slow
</limitations>

<tips>
- Use View tool first to examine existing files
- Use LS tool to verify location for new files
- Check diagnostics in response for errors
</tips>
```

---

### Step 8: Integration Tests

```go
// Test scenarios
func TestFileWrite_NewFile(t *testing.T)
func TestFileWrite_OverwriteExisting(t *testing.T)
func TestFileWrite_RaceConditionBlocked(t *testing.T)
func TestFileWrite_IdenticalContentSkipped(t *testing.T)
func TestFileWrite_DirectoryPath(t *testing.T)
func TestFileWrite_PermissionDenied(t *testing.T)
func TestFileWrite_HistoryCreated(t *testing.T)
func TestFileWrite_ManualEditDetected(t *testing.T)
func TestFileWrite_ParentDirCreated(t *testing.T)
```

---

## 6. Implementation Checklist

### Phase 1: Foundation (Week 1)
- [ ] Create file tracking service
- [ ] Create diff generation utility
- [ ] Define history service interface
- [ ] Create in-memory history implementation

### Phase 2: Core Tool (Week 2)
- [ ] Implement parameter validation
- [ ] Implement file state checks
- [ ] Implement race condition protection
- [ ] Implement content identity check
- [ ] Implement directory creation
- [ ] Implement file write operation

### Phase 3: Integration (Week 3)
- [ ] Integrate permission service
- [ ] Integrate history service
- [ ] Integrate file tracking
- [ ] Add response metadata (diff, additions, removals)

### Phase 4: Polish (Week 4)
- [ ] Add LSP integration (optional)
- [ ] Write comprehensive tests
- [ ] Create tool description/prompt
- [ ] Documentation

---

## 7. Success Metrics

| Metric | Target | Measurement |
|--------|--------|-------------|
| Race condition prevention | 100% | No overwrites of user changes |
| LLM success rate | >95% | Tool calls resulting in correct file state |
| Error message clarity | High | LLM can self-correct from error messages |
| History coverage | 100% | All writes tracked in history |
| Rollback capability | Full | Any version restorable |

---

## 8. Migration Path

For existing SwarmOS SDK users:

1. Keep existing `file_write` tool unchanged
2. Add new `file_write_v2` tool with enhanced features
3. Deprecate `file_write` after validation period
4. Rename `file_write_v2` to `file_write` in next major version
