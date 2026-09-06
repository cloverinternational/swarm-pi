package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/filetracker"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/journalstore"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/taskstore"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// journalDaemonInstanceID is a fresh, opaque, process-lifetime identifier
// used to tag every execution-journal record this process writes (see
// journalstore.Open's contract: "a fresh opaque ID for this process
// lifetime (never a PID)"). It is generated lazily, once, via
// crypto/rand -- never derived from os.Getpid() or any other
// process-identity value that could collide across a PID-reuse boundary.
var (
	journalDaemonInstanceIDOnce  sync.Once
	journalDaemonInstanceIDValue string
)

// journalDaemonInstanceID returns this process's opaque journal daemon
// instance ID, minting it on first use.
func journalDaemonInstanceID() string {
	journalDaemonInstanceIDOnce.Do(func() {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			// crypto/rand failing indicates a broken host CSPRNG. There is
			// no safe PID/timestamp fallback per the "never a PID"
			// requirement, so fall back to a fixed, obviously-degenerate
			// sentinel rather than a value that could collide with a real
			// instance ID; the journal remains best-effort at this
			// phase's scope regardless.
			journalDaemonInstanceIDValue = "degraded-csprng-unavailable"
			return
		}
		journalDaemonInstanceIDValue = hex.EncodeToString(buf)
	})
	return journalDaemonInstanceIDValue
}

// journalStoresByDir caches one *journalstore.Store per conversation-scoped
// journal directory for this process's lifetime. journalstore.Open documents
// that opening two Store instances over the same directory concurrently is
// unsafe (single-writer, single-generation); SetupTaskPersistence and
// SaveTasksForConversation can both reach the same conversation's journal
// directory (they derive the same "<conversation metadata dir>/journal"
// path from convID), so both call sites route through
// journalStoreForDir to guarantee at most one *journalstore.Store per
// directory is ever opened, regardless of call order or how many times
// either function runs for the same conversation.
var (
	journalStoresMu    sync.Mutex
	journalStoresByDir = map[string]*journalstore.Store{}
)

// journalStoreForDir returns the (lazily opened, cached-per-process)
// *journalstore.Store rooted at dir, or nil if opening it failed. A failed
// open is logged and NOT cached as a permanent failure: a later call for
// the same dir will retry, since transient failures (e.g. a momentarily
// unwritable disk) should not permanently disable journaling for the
// lifetime of the process. The journal is additive/best-effort at this
// phase's scope (see CONTRACT.md's "Hard invariants"): a nil return here
// must never cause task persistence setup to fail.
func journalStoreForDir(dir string) *journalstore.Store {
	journalStoresMu.Lock()
	defer journalStoresMu.Unlock()

	if js, ok := journalStoresByDir[dir]; ok {
		return js
	}

	js, err := journalstore.Open(dir, journalDaemonInstanceID())
	if err != nil {
		log.Printf("agent: journalstore.Open(%s) failed, continuing without execution journal for this taskstore (task persistence is unaffected): %v", dir, err)
		return nil
	}
	journalStoresByDir[dir] = js
	return js
}

// attachJournalWriter opens (or reuses) the journal store scoped under
// metadataDir's "journal" subdirectory and attaches it to store via
// SetJournalWriter, when opening succeeds. On failure it logs and leaves
// store without a journal writer (nil, the pre-wiring default) rather than
// propagating the error -- journaling is additive/best-effort this phase
// and must never make task persistence newly fragile.
func attachJournalWriter(store *taskstore.Store, metadataDir string) {
	journalDir := filepath.Join(metadataDir, "journal")
	if js := journalStoreForDir(journalDir); js != nil {
		store.SetJournalWriter(js)
	}
}

// SetMessageCallback sets a callback invoked whenever the agent produces a message.
// This allows callers to persist messages to external storage (e.g. a conversation manager).
func (a *Agent) SetMessageCallback(callback MessageCallback) {
	a.mu.Lock()
	a.messageCallback = callback
	a.mu.Unlock()
}

// SetCompactionPersistCallback installs the host callback used to durably save
// a compacted generation produced inside the agent loop.
func (a *Agent) SetCompactionPersistCallback(callback CompactionPersistCallback) {
	a.mu.Lock()
	a.compactionPersistCallback = callback
	a.mu.Unlock()
}

// SetIntermediateCallback sets a callback for real-time updates during execution.
// This allows UIs to display tool calls, results, and thinking as they happen.
func (a *Agent) SetIntermediateCallback(callback IntermediateCallback) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.intermediateCallback = callback
}

// SetHeadlessMode sets whether the agent is running in headless mode (without UI).
// When true, the agent will not attempt interactive operations like showing
// profile picker modals during fallback chain exhaustion.
func (a *Agent) SetHeadlessMode(headless bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.isHeadless = headless
}

// IsHeadless reports whether the agent is running in headless mode (no UI).
// Interactive TUI sessions are false; `swarm -p` / IPC / daemon runs are true.
func (a *Agent) IsHeadless() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.isHeadless
}

// IntermediateCallback returns the currently registered intermediate callback, or nil.
// Used by wrappers (e.g. BackgroundAgent) that need to chain an additional callback
// without discarding the one already set.
func (a *Agent) IntermediateCallback() IntermediateCallback {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.intermediateCallback
}

// SetMessageInjector sets a callback for injecting user messages between agent turns.
// The injector is called at the start of each turn inside executeLoop.
// Returning nil or an empty slice means no messages to inject.
func (a *Agent) SetMessageInjector(injector MessageInjector) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messageInjector = injector
}

// SetRichMessageInjector sets a callback for injecting canonical messages between turns.
func (a *Agent) SetRichMessageInjector(injector RichMessageInjector) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.richMessageInjector = injector
}

// SetEphemeralSystemFn registers a function that returns an ephemeral suffix
// appended to the system prompt on every provider request.
//
// DEPRECATED: Ephemeral system prompt injection breaks cache, is invisible to
// the model, and not persistent. Use hooks that return ContinueWithMessage()
// instead. Kept for backward compatibility — no new code should use this.
//
// The suffix is NEVER stored in conversation history and is NEVER rendered in
// the TUI — it is invisible to the user. The agent can read it but should not
// acknowledge it explicitly.
//
// Passing nil removes any existing fn.
func (a *Agent) SetEphemeralSystemFn(fn func([]*conversation.Message) string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ephemeralSystemFn = fn
}

// GetEphemeralSystemFn returns the currently registered ephemeral system
// prompt function, or nil if none is set.
//
// DEPRECATED: See SetEphemeralSystemFn.
func (a *Agent) GetEphemeralSystemFn() func([]*conversation.Message) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ephemeralSystemFn
}

// SetToolFilter sets a filter function for deferred tool loading.
// When set, buildProviderTools will pass all candidate tools through this
// filter before including them in the ChatRequest. This enables the
// DeferredRegistry to strip deferred tools from the context, saving tokens.
//
// Pass nil to disable filtering (all tools will be sent to the provider).
func (a *Agent) SetToolFilter(filter ToolFilterFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolFilter = filter
}

// SetToolHints replaces the provider-visible tool allow-list.
//
// Sub-agent launchers use this after copying tools from a parent registry so
// the definition advertises the exact concrete names that were successfully
// registered on the child. Callers must configure hints before Execute starts.
//
// Mid-execution updates are also safe: the whole slice is swapped under
// a.mu.Lock and buildProviderTools snapshots it under a.mu.RLock, so a turn
// being built concurrently observes either the complete old list or the
// complete new one, never a partially-updated set. (The client's closed
// harness path relies on this to widen hints as an MCP server finishes
// registering its tools.)
//
// CAUTION: an empty/nil list is stored as nil, and nil hints mean "ALL
// registry tools" to buildProviderTools. Callers that mean "no tools" must
// pass a non-empty sentinel that matches no registered tool.
func (a *Agent) SetToolHints(toolHints []string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(toolHints) == 0 {
		a.definition.ToolHints = nil
		return
	}
	a.definition.ToolHints = append([]string(nil), toolHints...)
}

// SetHooksManager sets the hooks manager for emitting tool execution events.
// This enables hooks to be triggered before/after tool calls.
func (a *Agent) SetHooksManager(hm HooksManager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hooksManager = hm
}

// callIntermediateCallback safely invokes the intermediate callback if set.
func (a *Agent) callIntermediateCallback(ctx context.Context, update IntermediateUpdate) {
	a.mu.RLock()
	cb := a.intermediateCallback
	a.mu.RUnlock()
	if cb != nil {
		if err := cb(ctx, update); err != nil {
			a.logger.Warn(ctx, "intermediate_callback_failed",
				observability.F("update_type", update.UpdateType()),
				observability.F("error", err.Error()))
		}
	}
}

// ID returns the agent's unique identifier.
func (a *Agent) ID() string {
	return a.definition.ID
}

// Name returns the agent's name.
func (a *Agent) Name() string {
	return a.definition.Name
}

// ToolRegistry returns the agent's tool registry.
// This allows external code to register additional tools after agent creation.
func (a *Agent) ToolRegistry() tools.Registry {
	return a.toolReg
}

// BrowserFamilyContext returns opaque inherited runtime authority. A zero
// value means this is an unbound top-level agent; authority may still be
// supplied on the trusted Go context of an individual Execute call.
func (a *Agent) BrowserFamilyContext() chrome.FamilyContext {
	return a.browserFamily
}

// Provider returns the agent's active provider.
func (a *Agent) Provider() provider.Provider {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.provider
}

// A2ARuntime returns the A2A runtime if this agent is A2A-enabled.
func (a *Agent) A2ARuntime() *a2a.Runtime {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.a2aRuntime
}

// SetModel updates the agent's model for the next execution.
func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.Model = model
}

// SetID overrides the agent's ID.
// This is needed for parallel sub-agent safety: when multiple Task calls use
// the same agent_id or preset, the factory produces agents with identical IDs.
// Callers must override with a unique, call-scoped ID so the TUI can
// distinguish parallel sub-agents' streaming updates.
func (a *Agent) SetID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.ID = id
}

// SetProvider updates the agent's provider (useful for token refresh).
// Also updates definition.Provider so error logs reflect the live provider name.
func (a *Agent) SetProvider(prov provider.Provider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.provider = prov
	if prov != nil {
		a.definition.Provider = prov.Name()
	}
}

// SetCredentialStore sets the credential store for multi-token rotation.
// When set, the agent will try multiple credentials per provider before
// falling back to the next provider in the chain.
func (a *Agent) SetCredentialStore(cs *CredentialStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.credentialStore = cs
}

// SetChain updates the agent's fallback chain.
// Set to nil to disable chain-based execution and use the direct provider path.
// Set to a chain to enable pool-based execution with fallback.
func (a *Agent) SetChain(chain *fallback.Chain) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chain = chain
}

// SetNoFallback toggles strict single-model execution. When enabled, the
// fallback chain is collapsed to its primary entry: no fallback models are
// attempted, so a failure surfaces the REAL primary error instead of cascading
// into unrelated providers. Use this when the caller has explicitly pinned a
// provider/model and wants its error verbatim.
func (a *Agent) SetNoFallback(noFallback bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.noFallback = noFallback
}

// SetConfiguredContextWindow sets the context window from the user's model config.
// This value takes priority over anything the provider's Capabilities() returns,
// so user config always wins — no hardcoded SDK value can override it.
func (a *Agent) SetConfiguredContextWindow(cw int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.configuredContextWindow = cw
}

// ConfiguredContextWindow returns the explicitly configured context window, or 0 if not set.
func (a *Agent) ConfiguredContextWindow() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.configuredContextWindow
}

// SetSystemPrompt updates the agent's system prompt.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.SystemPrompt = prompt
}

// SystemPrompt returns the agent's current system prompt.
func (a *Agent) SystemPrompt() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.definition.SystemPrompt
}

// SetPromptTraceWriter installs a writer that receives a [PROMPT PROVENANCE]
// banner just before every provider call. When w is nil (the default), no
// banner is rendered and the per-turn prompttrace.Collector is never
// attached to the request context — so contribution sites pay nothing.
//
// Typical wiring: the caller enabling --raw also calls this with os.Stderr
// (the same destination DebugTransport writes to). The banner appears
// immediately before the REQUEST BODY dump.
func (a *Agent) SetPromptTraceWriter(w io.Writer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.promptTraceWriter = w
}

// SetPromptTraceSeedFn registers a callback that runs at the start of every
// request, after the per-request prompttrace.Collector is attached to the
// context. The callback can replay startup-time contributions (skills
// injection, dream contract injection, headless custom prompt overrides)
// into the freshly-attached collector by calling prompttrace.From(ctx).
// Append/AppendChars/AppendMsg. Without this hook, those contributions
// would never appear in the banner because they ran long before any
// per-request ctx existed.
func (a *Agent) SetPromptTraceSeedFn(fn func(ctx context.Context)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.promptTraceSeedFn = fn
}

// SetAutoCompactionConfig updates the agent's auto-compaction configuration.
func (a *Agent) SetAutoCompactionConfig(config AutoCompactionConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.autoCompactionConfig = config
}

// AutoCompactionConfig returns the agent's current auto-compaction configuration.
func (a *Agent) AutoCompactionConfig() AutoCompactionConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autoCompactionConfig
}

// SetCompactionService sets the full compaction service on the agent.
// This allows the agent to own compaction as a first-class capability.
func (a *Agent) SetCompactionService(svc *compaction.Service) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.compactionService = svc
}

// CompactionService returns the agent's compaction service.
func (a *Agent) CompactionService() *compaction.Service {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.compactionService
}

// ManualCompact performs manual compaction on the given conversation using the agent's
// compaction service. This enables external code (like the TUI) to trigger compaction
// on sub-agents and main agents using the same unified interface.
//
// The compaction includes full context preservation: mode state, todos, file accesses,
// and MCP servers. Returns the compaction result including the summary and token counts.
//
// If no compaction service is set, returns an error.
func (a *Agent) ManualCompact(ctx context.Context, conv *conversation.Conversation) (*compaction.CompactionResult, error) {
	svc := a.CompactionService()
	if svc == nil {
		return nil, fmt.Errorf("compaction.service_not_configured: agent %s has no compaction service", a.ID())
	}
	// Build the full compaction context from the agent's current state
	compCtx, err := a.BuildCompactionContext()
	if err != nil {
		return nil, fmt.Errorf("failed to build compaction context: %w", err)
	}
	// Perform manual compaction (manual=true, preserves context)
	return svc.CompactWithContext(ctx, conv, true, compCtx)
}

// MicroCompactor returns the agent's micro-compactor for stats/configuration.
func (a *Agent) MicroCompactor() *compaction.MicroCompactor {
	return a.microCompactor
}

// SetConversationJSONPath forwards the conversation JSON file path to the
// micro-compactor so that per-turn pointer messages include the path.
// Call this whenever the active conversation changes (e.g. after compaction).
func (a *Agent) SetConversationJSONPath(path string) {
	if a.microCompactor != nil {
		a.microCompactor.SetConversationPath(path)
	}
}

// RecordFileAccess records a file access on the compaction service (if set).
// This is a convenience method so SDK consumers don't need to reach into the service.
func (a *Agent) RecordFileAccess(path string, isWrite bool) {
	if svc := a.CompactionService(); svc != nil {
		svc.RecordFileAccess(path, isWrite)
	}
}

// SetFileTracker sets the file access recorder for the agent.
// The recorder tracks which files are read/modified during tool execution,
// enabling the compaction system to recover important files into context.
func (a *Agent) SetFileTracker(tracker *filetracker.Recorder) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fileTracker = tracker
}

// FileTracker returns the agent's file access recorder.
func (a *Agent) FileTracker() *filetracker.Recorder {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.fileTracker
}

// SetTaskStore sets the persistent task store for the agent.
// The task store persists todos to disk, surviving compaction and enabling
// task sharing across conversation branches.
func (a *Agent) SetTaskStore(store *taskstore.Store) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.taskStore = store
}

// TaskStore returns the agent's persistent task store.
// If the store hasn't been initialized and a conversationID is set,
// it will be lazily created using the default storage path.
func (a *Agent) TaskStore() *taskstore.Store {
	a.mu.RLock()
	store := a.taskStore
	convID := a.conversationID
	storagePath := a.storagePath
	a.mu.RUnlock()
	// Return existing store
	if store != nil {
		return store
	}
	// Lazily create store if conversationID is set
	if convID == "" {
		return nil
	}
	// Create metadata directory path
	var metadataDir string
	if storagePath != "" {
		metadataDir = filepath.Join(storagePath, convID, "metadata")
	} else {
		// Use default path under the canonical ~/.swarm root.
		metadataDir = filepath.Join(paths.ConversationsDir(), convID, "metadata")
	}
	// Create or load the store
	newStore, err := taskstore.Load(taskstore.Config{
		ConversationID: convID,
		MetadataDir:    metadataDir,
	})
	if err != nil {
		// Log error but don't fail - return nil
		return nil
	}
	// Store it
	a.mu.Lock()
	a.taskStore = newStore
	a.mu.Unlock()
	return newStore
}

// SyncTasksToDisk persists the current task state to disk.
// This should be called before compaction and on task changes.
func (a *Agent) SyncTasksToDisk() error {
	store := a.TaskStore()
	if store == nil {
		return nil
	}
	return store.Save()
}

// SetupTaskPersistence loads the task store for the given conversation, wires
// it into the agent, and restores any persisted tasks into the global
// ii.TodoManager. Safe to call before agent.Execute().
//
// If convID is empty this is a no-op.
func (a *Agent) SetupTaskPersistence(convID string) error {
	if convID == "" {
		return nil
	}

	metadataDir := filepath.Join(paths.ConversationsDir(), convID, "metadata")

	store, err := taskstore.Load(taskstore.Config{
		ConversationID: convID,
		MetadataDir:    metadataDir,
	})
	if err != nil {
		return fmt.Errorf("taskstore: failed to load task store for conv %s: %w", convID, err)
	}
	attachJournalWriter(store, metadataDir)
	a.SetTaskStore(store)

	todoManager := ii.GetTodoManager()
	if todoManager == nil {
		return nil
	}

	todoManager.SetSyncer(taskstore.NewTodoSyncer(store))

	// Restore persisted tasks — or clear stale in-memory state when there are none.
	tasks := store.GetActiveTasks()
	if len(tasks) > 0 {
		if err := taskstore.RestoreTodos(store, todoManager); err != nil {
			return fmt.Errorf("taskstore: failed to restore tasks for conv %s: %w", convID, err)
		}
	} else {
		todoManager.ClearTodos()
	}

	return nil
}

// SaveTasksForConversation explicitly saves the current tasks from the global
// TodoManager to the taskstore for the specified conversation ID.
// This must be called BEFORE switching to a different conversation to ensure
// tasks are persisted correctly.
//
// The issue this solves: TodoManager is a global singleton. When switching
// conversations, the syncer is changed to the new conversation's taskstore.
// This means tasks for the OLD conversation would be lost. By calling this
// method before switching, we explicitly save the tasks to the correct store.
func (a *Agent) SaveTasksForConversation(convID string) error {
	if convID == "" {
		return nil
	}

	tm := ii.GetTodoManager()
	if tm == nil {
		return nil
	}

	// Get current tasks
	tasks := tm.Todos()
	if len(tasks) == 0 {
		return nil
	}

	// Load the taskstore for this conversation
	metadataDir := filepath.Join(paths.ConversationsDir(), convID, "metadata")

	store, err := taskstore.Load(taskstore.Config{
		ConversationID: convID,
		MetadataDir:    metadataDir,
	})
	if err != nil {
		return fmt.Errorf("taskstore: failed to load task store for conv %s: %w", convID, err)
	}
	attachJournalWriter(store, metadataDir)

	// Sync tasks to the store
	return taskstore.SyncTodos(store, tasks)
}

// ToolCallsTotal returns the total number of tool calls executed since the
// current Execute() call started. The value resets to zero at the beginning
// of every Execute() invocation. This is safe to call concurrently.
func (a *Agent) ToolCallsTotal() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.toolCallsTotal
}
