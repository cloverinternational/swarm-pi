package context

import (
	stdctx "context"
	"crypto/sha256"

	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
)

// MCPProvider defines the MCP calls needed for context injection.
type MCPProvider interface {
	ReadResource(ctx stdctx.Context, serverName, uri string) (*mcp.ResourceContents, error)
	GetPrompt(ctx stdctx.Context, serverName, promptName string, args map[string]string) (*mcp.PromptResult, error)
}

// ContextBlockOptions controls refresh behavior and size limits.
type ContextBlockOptions struct {
	RefreshMode    RefreshMode
	TTLSeconds     int
	RunID          string
	Now            func() time.Time
	MaxSourceChars int
	MaxSourceLines int
	MaxTotalChars  int
}

// AgentMetadata holds runtime operational context injected into every request's
// ephemeral system-prompt block so the agent can reason about its own environment.
type AgentMetadata struct {
	SessionID    string // stable for the lifetime of this SDK session
	ModelName    string // e.g. "claude-opus-4-5"
	ProviderName string // e.g. "anthropic"
	ProfileName  string // active profile name, empty when no profile is set
	ClientType   string // runtime surface: "tui", "headless", "managed", "sdk"
}

// ContextOrchestrator decides when to refresh and renders context blocks.
type ContextOrchestrator struct {
	mu sync.Mutex

	config        ContextConfig
	workspaceRoot string
	loader        ContextLoader
	mcpProvider   MCPProvider
	logger        observability.Logger

	sourceStates map[string]*sourceState
	lastRendered string

	// nestedIndexDirs tracks directories where the agent has read files,
	// so we can discover INDEX.md files in those directories (same pattern
	// Claude Code uses for CLAUDE.md via nestedMemoryAttachmentTriggers).
	nestedIndexDirs   map[string]bool
	injectedIndexDirs map[string]bool // dirs already injected to avoid re-injection

	// agentMeta holds the current agent operational metadata injected into
	// every ephemeral context block. Protected by mu.
	agentMeta AgentMetadata
}

type sourceState struct {
	content       string
	path          string
	hash          string
	lastRefresh   time.Time
	lastRunID     string
	lastFileState fileState
}

type fileState struct {
	modTime time.Time
	size    int64
	exists  bool
}

type resolvedSource struct {
	config      ContextSourceConfig
	refreshMode RefreshMode
	ttlSeconds  int
	cachePolicy CachePolicy
}

type sourceOutput struct {
	source      ContextSource
	cachePolicy CachePolicy
}

type mcpTask struct {
	resolved resolvedSource
	state    *sourceState
	index    int
}

type mcpResult struct {
	task    mcpTask
	content string
	err     error
}

const (
	defaultMaxSourceChars = 4000
	defaultMaxSourceLines = 200
	defaultMaxTotalChars  = 20000
	maxErrorNoteChars     = 200
	maxMCPWarningChars    = 200
	errorContextName      = "contextError"

	defaultMCPTimeout     = 2 * time.Second
	defaultMCPConcurrency = 4
)

// NewContextOrchestrator constructs a new orchestrator.
func NewContextOrchestrator(config ContextConfig, workspaceRoot string, loader ContextLoader, mcpProvider MCPProvider, logger observability.Logger) *ContextOrchestrator {
	if loader == nil {
		loader = NewFileLoaderWithRoot(workspaceRoot)
	}
	if logger == nil {
		logger = observability.NewNopLogger()
	}

	return &ContextOrchestrator{
		config:            config,
		workspaceRoot:     workspaceRoot,
		loader:            loader,
		mcpProvider:       mcpProvider,
		logger:            logger,
		sourceStates:      make(map[string]*sourceState),
		nestedIndexDirs:   make(map[string]bool),
		injectedIndexDirs: make(map[string]bool),
	}
}

// SetAgentMetadata stores runtime agent metadata that is injected into every
// ephemeral context block on the next refresh. Safe to call from any goroutine.
func (o *ContextOrchestrator) SetAgentMetadata(meta AgentMetadata) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.agentMeta = meta
}

// Compile-time check that ContextOrchestrator implements FileReadRegistrar.
var _ builtin.FileReadRegistrar = (*ContextOrchestrator)(nil)

// RegisterFileRead records that the agent read a file at the given path.
// The parent directory is tracked so nested INDEX.md files can be discovered
// on the next context refresh (mirrors Claude Code's nestedMemoryAttachmentTriggers).
func (o *ContextOrchestrator) RegisterFileRead(filePath string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	dir := filepath.Dir(filePath)
	if dir == "" || dir == "." {
		return
	}

	// Only track directories that are descendants of the workspace root
	// (no point searching outside the project for project indexes).
	if !strings.HasPrefix(dir, o.workspaceRoot) {
		return
	}

	o.nestedIndexDirs[dir] = true
}

// getNestedIndexMdContent discovers INDEX.md files in directories registered
// via RegisterFileRead, walking from the workspace root toward each directory.
// Already-injected directories are skipped to prevent duplication.
// This mirrors Claude Code's getNestedMemoryAttachmentsForFile pattern.
func (o *ContextOrchestrator) getNestedIndexMdContent() string {
	var parts []string

	for dir := range o.nestedIndexDirs {
		rel, err := filepath.Rel(o.workspaceRoot, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}

		// Walk from workspaceRoot down to dir, checking each intermediate
		// directory for INDEX.md. This mirrors Claude Code's directory
		// traversal from CWD toward the target path.
		dirsToProcess := directoriesBetween(o.workspaceRoot, dir)
		for _, d := range dirsToProcess {
			if o.injectedIndexDirs[d] {
				continue
			}

			indexPath := filepath.Join(d, "INDEX.md")
			content, err := readFileTrimmedLimited(indexPath, defaultMaxSourceChars)
			if err != nil || content == "" {
				continue
			}

			o.injectedIndexDirs[d] = true
			parts = append(parts, fmt.Sprintf(
				"Contents of %s (repository navigation index):\n\n%s",
				indexPath, content,
			))
			if o.logger != nil {
				o.logger.Info(stdctx.Background(), "context.nested_index_md_discovered",
					observability.F("path", indexPath),
					observability.F("size", len(content)),
				)
			}
		}
	}

	// Clear the triggers after processing (like Claude Code's clear)
	o.nestedIndexDirs = make(map[string]bool)

	if len(parts) == 0 {
		return ""
	}

	header := "The following additional repository indexes describe the structure " +
		"of subdirectories you have explored. Use them to navigate without " +
		"filesystem exploration."
	return header + "\n\n" + strings.Join(parts, "\n\n---\n\n")
}

// directoriesBetween returns all directories from root down to target (inclusive),
// ordered parent-to-child. Mirrors Claude Code's getDirectoriesToProcess.
func directoriesBetween(root, target string) []string {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." {
		return []string{root}
	}

	// Split relative path into components
	components := strings.Split(rel, string(filepath.Separator))
	dirs := make([]string, 0, len(components)+1)
	current := root
	dirs = append(dirs, current)
	for _, comp := range components {
		if comp == "" {
			continue
		}
		current = filepath.Join(current, comp)
		dirs = append(dirs, current)
	}
	return dirs
}

// GetContextBlock returns rendered context blocks wrapped in <swarmos_cached_context> and <swarmos_context> tags.
func (o *ContextOrchestrator) GetContextBlock(ctx stdctx.Context, opts ContextBlockOptions) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.loader != nil {
		o.config = o.loader.GetConfig()
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	block, err := o.refresh(ctx, opts, now)
	if err != nil {
		o.logger.Warn(ctx, "context.refresh_failed",
			observability.F("error", err.Error()),
		)
		blockWithError := o.withErrorNote(o.lastRendered, err.Error())
		return blockWithError
	}

	o.lastRendered = block
	return block
}

// IsInjectionEnabled reports whether the injection-kind source with the given
// ID (e.g. SourceIDSkills) is enabled, reading the latest merged config so
// settings-screen toggles take effect without a restart.
func (o *ContextOrchestrator) IsInjectionEnabled(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.loader != nil {
		o.config = o.loader.GetConfig()
	}
	return IsInjectionEnabled(o.config, id)
}

func (o *ContextOrchestrator) refresh(ctx stdctx.Context, opts ContextBlockOptions, now func() time.Time) (string, error) {
	maxSourceChars := opts.MaxSourceChars
	if maxSourceChars <= 0 {
		maxSourceChars = defaultMaxSourceChars
	}
	maxSourceLines := opts.MaxSourceLines
	if maxSourceLines <= 0 {
		maxSourceLines = defaultMaxSourceLines
	}
	maxTotalChars := opts.MaxTotalChars
	if maxTotalChars <= 0 {
		maxTotalChars = defaultMaxTotalChars
	}

	config := o.config
	defaults := config.Defaults
	if opts.RefreshMode != "" {
		defaults.RefreshMode = opts.RefreshMode
	}
	if opts.TTLSeconds != 0 {
		ttl := opts.TTLSeconds
		defaults.TTLSeconds = &ttl
	}

	home, _ := os.UserHomeDir()
	workDir := o.workspaceRoot
	if workDir == "" {
		workDir = "."
	}

	sources := NormalizeSourceConfigs(config.Sources)
	outputs := make([]sourceOutput, 0, len(sources))
	mcpTasks := make([]mcpTask, 0)

	for _, source := range sources {
		if !source.Enabled || source.ID == "" {
			continue
		}
		// Injection-kind sources gate prompt contributors at their own
		// injection sites (skills XML, workspace env, etc.) — they never
		// render content into the context block.
		if source.Kind == SourceKindInjection {
			continue
		}
		resolved := resolveSource(source, defaults)
		if IsMCPSourceConfig(source) {
			state := o.stateForSource(source.ID)
			outputIndex := len(outputs)
			if shouldRefreshSource(resolved.refreshMode, resolved.ttlSeconds, opts.RunID, state, now) {
				mcpTasks = append(mcpTasks, mcpTask{resolved: resolved, state: state, index: outputIndex})
				outputs = append(outputs, sourceOutput{
					source:      ContextSource{Name: sourceContextName(resolved.config), Path: state.path},
					cachePolicy: resolved.cachePolicy,
				})
			} else if strings.TrimSpace(state.content) != "" {
				outputs = append(outputs, sourceOutput{
					source:      ContextSource{Name: sourceContextName(resolved.config), Path: state.path, Content: state.content},
					cachePolicy: resolved.cachePolicy,
				})
			}
			continue
		}

		output, ok := o.refreshNonMCPSource(resolved, opts.RunID, now, workDir, home)
		if ok {
			outputs = append(outputs, output)
		}
	}

	if len(mcpTasks) > 0 {
		timeout := resolveMCPTimeout(defaults)
		concurrency := resolveMCPConcurrency(defaults)
		o.refreshMCPSources(ctx, mcpTasks, outputs, opts.RunID, now, timeout, concurrency)
	}

	cachedSources := make([]ContextSource, 0, len(outputs))
	ephemeralSources := make([]ContextSource, 0, len(outputs)+1) // +1 for nested INDEX.md
	for _, output := range outputs {
		if strings.TrimSpace(output.source.Content) == "" {
			continue
		}
		if output.cachePolicy == CacheEphemeral {
			ephemeralSources = append(ephemeralSources, output.source)
		} else {
			cachedSources = append(cachedSources, output.source)
		}
	}

	// Discover INDEX.md files from directories where the agent has read files.
	// This is the nested traversal pattern from Claude Code: when the agent
	// reads a file in a subdirectory, we inject that directory's INDEX.md
	// as ephemeral context on the next refresh.
	indexEnabled := true
	if source, _, ok := FindSourceByID(config.Sources, SourceIDIndexMd); ok {
		indexEnabled = source.Enabled
	}
	if indexEnabled {
		if nestedIndex := o.getNestedIndexMdContent(); nestedIndex != "" {
			ephemeralSources = append(ephemeralSources, ContextSource{
				Name:    "nestedIndexMd",
				Content: nestedIndex,
			})
		}
	}

	cachedContent := renderSources(cachedSources, "", maxSourceChars, maxSourceLines)
	ephemeralContent := renderSources(ephemeralSources, "", maxSourceChars, maxSourceLines)

	remaining := maxTotalChars
	cachedContent, remaining = applyBudget(cachedContent, remaining)
	if remaining > 0 {
		ephemeralContent, remaining = applyBudget(ephemeralContent, remaining)
	} else {
		ephemeralContent = ""
	}

	blocks := make([]string, 0, 2)
	if strings.TrimSpace(cachedContent) != "" {
		blocks = append(blocks, CachedContextStartTag+"\n"+cachedContent+"\n"+CachedContextEndTag)
	}
	if strings.TrimSpace(ephemeralContent) != "" {
		blocks = append(blocks, ContextStartTag+"\n"+ephemeralContent+"\n"+ContextEndTag)
	}
	block := strings.TrimSpace(strings.Join(blocks, "\n\n"))

	o.logger.Info(ctx, "context.refreshed",
		observability.F("source_count", len(outputs)),
		observability.F("block_length", len(block)),
	)

	return block, nil
}

func (o *ContextOrchestrator) refreshNonMCPSource(resolved resolvedSource, runID string, now func() time.Time, workDir, home string) (sourceOutput, bool) {
	source := resolved.config
	state := o.stateForSource(source.ID)

	if resolved.refreshMode == RefreshOnChange && resolved.config.Kind == SourceKindFile {
		return o.refreshFileOnChange(resolved, runID, now, workDir, home)
	}

	if !shouldRefreshSource(resolved.refreshMode, resolved.ttlSeconds, runID, state, now) {
		if strings.TrimSpace(state.content) == "" {
			return sourceOutput{}, false
		}
		return sourceOutput{
			source:      ContextSource{Name: sourceContextName(source), Path: state.path, Content: state.content},
			cachePolicy: resolved.cachePolicy,
		}, true
	}

	content, path := loadSourceContent(source, workDir, home, now)
	state.content = content
	state.path = path
	state.hash = hashString(content)
	state.lastRefresh = now()
	if runID != "" {
		state.lastRunID = runID
	}

	if strings.TrimSpace(content) == "" {
		return sourceOutput{}, false
	}

	return sourceOutput{
		source:      ContextSource{Name: sourceContextName(source), Path: path, Content: content},
		cachePolicy: resolved.cachePolicy,
	}, true
}

func (o *ContextOrchestrator) refreshFileOnChange(resolved resolvedSource, runID string, now func() time.Time, workDir, home string) (sourceOutput, bool) {
	source := resolved.config
	state := o.stateForSource(source.ID)

	path, stat, exists := resolveFileSourcePath(source.ID, workDir, home)
	if !exists {
		state.content = ""
		state.path = ""
		state.hash = ""
		state.lastFileState = fileState{exists: false}
		state.lastRefresh = now()
		if runID != "" {
			state.lastRunID = runID
		}
		return sourceOutput{}, false
	}

	if state.path == path && fileStateEqual(state.lastFileState, stat) {
		// Stat looks identical — do a cheap content-hash check to guard against
		// sub-second writes that don't bump the mtime (common in fast tests and
		// on filesystems with 1-second mtime granularity).
		content, err := readFileTrimmedForSource(path, source.ID)
		if err != nil {
			// File exists (stat succeeded) but is unreadable (e.g. chmod 000).
			// Preserve the cached content so the caller keeps seeing the last
			// known value rather than a blank screen.
			if strings.TrimSpace(state.content) == "" {
				return sourceOutput{}, false
			}
			return sourceOutput{
				source:      ContextSource{Name: sourceContextName(source), Path: state.path, Content: state.content},
				cachePolicy: resolved.cachePolicy,
			}, true
		}
		if hashString(content) == state.hash {
			// Truly unchanged — serve from cache.
			if strings.TrimSpace(state.content) == "" {
				return sourceOutput{}, false
			}
			return sourceOutput{
				source:      ContextSource{Name: sourceContextName(source), Path: state.path, Content: state.content},
				cachePolicy: resolved.cachePolicy,
			}, true
		}
		// Content changed despite identical stat — update and return new content.
		state.path = path
		state.lastFileState = stat
		state.lastRefresh = now()
		if runID != "" {
			state.lastRunID = runID
		}
		state.content = content
		state.hash = hashString(content)
		if strings.TrimSpace(state.content) == "" {
			return sourceOutput{}, false
		}
		return sourceOutput{
			source:      ContextSource{Name: sourceContextName(source), Path: state.path, Content: state.content},
			cachePolicy: resolved.cachePolicy,
		}, true
	}

	content, err := readFileTrimmedForSource(path, source.ID)
	if err != nil {
		state.content = ""
		state.path = ""
		state.hash = ""
		state.lastFileState = fileState{exists: false}
		state.lastRefresh = now()
		if runID != "" {
			state.lastRunID = runID
		}
		return sourceOutput{}, false
	}

	contentHash := hashString(content)
	state.path = path
	state.lastFileState = stat
	state.lastRefresh = now()
	if runID != "" {
		state.lastRunID = runID
	}

	if contentHash != state.hash {
		state.content = content
		state.hash = contentHash
	}

	if strings.TrimSpace(state.content) == "" {
		return sourceOutput{}, false
	}

	return sourceOutput{
		source:      ContextSource{Name: sourceContextName(source), Path: state.path, Content: state.content},
		cachePolicy: resolved.cachePolicy,
	}, true
}

func (o *ContextOrchestrator) refreshMCPSources(ctx stdctx.Context, tasks []mcpTask, outputs []sourceOutput, runID string, now func() time.Time, timeout time.Duration, concurrency int) {
	if len(tasks) == 0 {
		return
	}

	if concurrency <= 0 {
		concurrency = defaultMCPConcurrency
	}
	if timeout <= 0 {
		timeout = defaultMCPTimeout
	}

	results := make(chan mcpResult, len(tasks))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, task := range tasks {
		wg.Add(1)
		go func(task mcpTask) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			mcpSource, _ := MCPSourceFromConfig(task.resolved.config)
			fetchCtx, cancel := stdctx.WithTimeout(ctx, timeout)
			defer cancel()

			content, err := o.fetchMCPContent(fetchCtx, mcpSource)
			results <- mcpResult{task: task, content: content, err: err}
		}(task)
	}

	wg.Wait()
	close(results)

	for result := range results {
		source := result.task.resolved.config
		state := result.task.state
		content := strings.TrimSpace(result.content)
		if result.err != nil {
			mcpSource, _ := MCPSourceFromConfig(source)
			content = formatMCPWarning(mcpSource, result.err)
		}
		if content == "" {
			content = "MCP source returned empty content."
		}

		state.content = content
		state.hash = hashString(content)
		state.lastRefresh = now()
		if runID != "" {
			state.lastRunID = runID
		}

		output := sourceOutput{
			source:      ContextSource{Name: sourceContextName(source), Content: content},
			cachePolicy: result.task.resolved.cachePolicy,
		}

		if result.task.index >= 0 && result.task.index < len(outputs) {
			outputs[result.task.index] = output
		}
	}
}

func resolveSource(source ContextSourceConfig, defaults ContextDefaults) resolvedSource {
	resolved := resolvedSource{config: source}
	resolved.refreshMode = resolveRefreshMode(source, defaults)
	resolved.ttlSeconds = resolveTTLSeconds(source, defaults)
	resolved.cachePolicy = resolveCachePolicy(source, defaults)
	return resolved
}

func resolveRefreshMode(source ContextSourceConfig, defaults ContextDefaults) RefreshMode {
	mode := source.RefreshMode
	if mode == "" || mode == RefreshInherit {
		mode = defaults.RefreshMode
	}
	if mode == "" || mode == RefreshInherit || !isValidRefreshMode(mode) {
		mode = RefreshEveryMessage
	}
	if mode == RefreshOnChange && source.Kind != SourceKindFile {
		mode = RefreshEveryMessage
	}
	return mode
}

func resolveTTLSeconds(source ContextSourceConfig, defaults ContextDefaults) int {
	if source.TTLSeconds != nil {
		return *source.TTLSeconds
	}
	if defaults.TTLSeconds != nil {
		return *defaults.TTLSeconds
	}
	return 0
}

func resolveCachePolicy(source ContextSourceConfig, defaults ContextDefaults) CachePolicy {
	policy := source.CachePolicy
	if policy == "" || policy == CacheInherit {
		policy = defaults.CachePolicy
	}
	if policy == "" || policy == CacheInherit || !isValidCachePolicy(policy) {
		policy = CacheCached
	}
	return policy
}

func resolveMCPTimeout(defaults ContextDefaults) time.Duration {
	if defaults.MCPTimeoutMS != nil && *defaults.MCPTimeoutMS > 0 {
		return time.Duration(*defaults.MCPTimeoutMS) * time.Millisecond
	}
	return defaultMCPTimeout
}

func resolveMCPConcurrency(defaults ContextDefaults) int {
	if defaults.MCPConcurrency != nil && *defaults.MCPConcurrency > 0 {
		return *defaults.MCPConcurrency
	}
	return defaultMCPConcurrency
}

func shouldRefreshSource(mode RefreshMode, ttlSeconds int, runID string, state *sourceState, now func() time.Time) bool {
	switch mode {
	case RefreshEveryTurn:
		return true
	case RefreshEveryMessage:
		if runID == "" {
			return true
		}
		return state.lastRunID != runID
	case RefreshTTL:
		if ttlSeconds <= 0 {
			return true
		}
		if state.lastRefresh.IsZero() {
			return true
		}
		return now().Sub(state.lastRefresh) >= time.Duration(ttlSeconds)*time.Second
	default:
		return true
	}
}

func (o *ContextOrchestrator) stateForSource(id string) *sourceState {
	if id == "" {
		return &sourceState{}
	}
	state, ok := o.sourceStates[id]
	if !ok {
		state = &sourceState{}
		o.sourceStates[id] = state
	}
	return state
}

func sourceContextName(source ContextSourceConfig) string {
	switch source.ID {
	case SourceIDGlobalClaudeMd:
		return "globalClaudeMd"
	case SourceIDGlobalSwarmMd:
		return "globalSwarmMd"
	case SourceIDProjectClaudeMd:
		return "claudeMd"
	case SourceIDProjectSwarmMd:
		return "swarmMd"
	case SourceIDAgentsMd:
		return "agentsMd"
	case SourceIDIndexMd:
		return "indexMd"
	case SourceIDGitStatus:
		return "gitStatus"
	case SourceIDProjectName:
		return "projectName"
	case SourceIDCurrentDate:
		return "currentDate"
	}

	if IsMCPSourceConfig(source) {
		if mcpSource, ok := MCPSourceFromConfig(source); ok {
			return formatMCPContextName(mcpSource)
		}
	}

	if source.ID != "" {
		return sanitizeContextName(source.ID)
	}
	return "context"
}

func resolveFileSourcePath(sourceID, workDir, home string) (string, fileState, bool) {
	var candidates []string
	switch sourceID {
	case SourceIDGlobalClaudeMd:
		candidates = []string{filepath.Join(home, ".claude", "CLAUDE.md")}
	case SourceIDGlobalSwarmMd:
		candidates = []string{filepath.Join(home, ".swarm", "SWARM.md")}
	case SourceIDProjectClaudeMd:
		candidates = []string{
			filepath.Join(workDir, ".claude", "CLAUDE.md"),
			filepath.Join(workDir, "CLAUDE.md"),
		}
	case SourceIDProjectSwarmMd:
		candidates = []string{
			filepath.Join(workDir, ".swarm", "SWARM.md"),
			filepath.Join(workDir, "SWARM.md"),
		}
	case SourceIDAgentsMd:
		candidates = []string{
			filepath.Join(workDir, ".swarm", "AGENTS.md"),
			filepath.Join(workDir, ".claude", "AGENTS.md"),
			filepath.Join(workDir, "AGENTS.md"),
		}
	case SourceIDIndexMd:
		candidates = []string{
			filepath.Join(workDir, "INDEX.md"),
		}
	default:
		return "", fileState{}, false
	}

	for _, path := range candidates {
		info, err := os.Stat(path)
		if err == nil {
			return path, fileState{modTime: info.ModTime(), size: info.Size(), exists: true}, true
		}
	}

	return "", fileState{}, false
}

func fileStateEqual(a, b fileState) bool {
	return a.exists == b.exists && a.size == b.size && a.modTime.Equal(b.modTime)
}

func loadSourceContent(source ContextSourceConfig, workDir, home string, now func() time.Time) (string, string) {
	switch source.ID {
	case SourceIDGlobalClaudeMd, SourceIDGlobalSwarmMd, SourceIDProjectClaudeMd, SourceIDProjectSwarmMd, SourceIDAgentsMd, SourceIDIndexMd:
		path, _, exists := resolveFileSourcePath(source.ID, workDir, home)
		if !exists {
			return "", ""
		}
		content, err := readFileTrimmedForSource(path, source.ID)
		if err != nil {
			return "", ""
		}
		return content, path
	case SourceIDGitStatus:
		return getGitStatus(workDir), ""
	case SourceIDProjectName:
		projectName := filepath.Base(workDir)
		if projectName == "" || projectName == "." {
			return "", ""
		}
		return projectName, ""
	case SourceIDCurrentDate:
		return nowFunc().Format("2006-01-02"), ""
	default:
		return "", ""
	}
}

func readFileTrimmed(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readFileTrimmedLimited(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, maxBytes)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(string(buf[:n])), nil
}

func readFileTrimmedForSource(path string, sourceID string) (string, error) {
	if sourceID == SourceIDIndexMd {
		return readFileTrimmedLimited(path, defaultMaxSourceChars)
	}
	return readFileTrimmed(path)
}

func getGitStatus(workDir string) string {
	gitDir := filepath.Join(workDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return ""
	}

	var result strings.Builder

	absPath, err := filepath.Abs(workDir)
	if err != nil {
		absPath = workDir
	}
	result.WriteString("Working Directory: " + absPath + "\n\n")

	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = workDir
	branchOutput, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(branchOutput))
	result.WriteString("Current branch: " + branch + "\n")

	cmd = exec.Command("git", "log", "--oneline", "-5", "--format=%h %s")
	cmd.Dir = workDir
	commitsOutput, err := cmd.Output()
	if err == nil {
		commits := strings.TrimSpace(string(commitsOutput))
		if commits != "" {
			result.WriteString("\nLast 5 commits:\n" + commits + "\n")
		}
	}

	cmd = exec.Command("git", "status", "--short")
	cmd.Dir = workDir
	statusOutput, err := cmd.Output()
	if err != nil {
		return result.String()
	}
	status := strings.TrimSpace(string(statusOutput))
	if status != "" {
		result.WriteString("\nStatus:\n" + status + "\n")
	}

	cmd = exec.Command("git", "diff", "--cached", "--name-status")
	cmd.Dir = workDir
	stagedFilesOutput, err := cmd.Output()
	if err == nil {
		stagedFiles := strings.TrimSpace(string(stagedFilesOutput))
		if stagedFiles != "" {
			result.WriteString("\nStaged files:\n" + stagedFiles + "\n")
		}
	}

	return result.String()
}

func applyBudget(content string, remaining int) (string, int) {
	if remaining <= 0 || strings.TrimSpace(content) == "" {
		if remaining < 0 {
			remaining = 0
		}
		return "", remaining
	}
	if len(content) <= remaining {
		return content, remaining - len(content)
	}
	return truncateWithNote(content, remaining, "\n... (context truncated)"), 0
}

func renderSources(sources []ContextSource, errorNote string, maxSourceChars, maxSourceLines int) string {
	if len(sources) == 0 && errorNote == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("As you answer the user's questions, you can use the following context:\n")

	for _, src := range sources {
		if strings.TrimSpace(src.Content) == "" {
			continue
		}
		content, truncated := trimSourceContent(src.Content, maxSourceChars, maxSourceLines)
		b.WriteString("<context name=\"")
		b.WriteString(src.Name)
		b.WriteString("\">\n")
		b.WriteString(content)
		if truncated {
			b.WriteString("\n... (truncated)")
		}
		b.WriteString("\n</context>\n")
	}

	if errorNote != "" {
		b.WriteString("<context name=\"")
		b.WriteString(errorContextName)
		b.WriteString("\">\n")
		b.WriteString(errorNote)
		b.WriteString("\n</context>\n")
	}

	return strings.TrimSpace(b.String())
}

func trimSourceContent(content string, maxChars, maxLines int) (string, bool) {
	truncated := false
	trimmed := strings.TrimRight(content, "\n")

	if maxLines > 0 {
		lines := strings.Split(trimmed, "\n")
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			truncated = true
		}
		trimmed = strings.Join(lines, "\n")
	}

	if maxChars > 0 && len(trimmed) > maxChars {
		trimmed = trimmed[:maxChars]
		truncated = true
	}

	return strings.TrimRight(trimmed, "\n"), truncated
}

func (o *ContextOrchestrator) withErrorNote(block, errMsg string) string {
	note := formatErrorNote(errMsg)
	if note == "" {
		return block
	}
	if block == "" {
		content := renderSources(nil, note, defaultMaxSourceChars, defaultMaxSourceLines)
		if content == "" {
			return ""
		}
		return ContextStartTag + "\n" + content + "\n" + ContextEndTag
	}

	clean := removeContextErrorBlock(block)
	endIdx := strings.LastIndex(clean, ContextEndTag)
	if endIdx == -1 {
		errorContent := renderSources(nil, note, defaultMaxSourceChars, defaultMaxSourceLines)
		if errorContent == "" {
			return clean
		}
		return strings.TrimSpace(clean + "\n\n" + ContextStartTag + "\n" + errorContent + "\n" + ContextEndTag)
	}

	errorBlock := "\n<context name=\"" + errorContextName + "\">\n" + note + "\n</context>\n"
	return clean[:endIdx] + errorBlock + clean[endIdx:]
}

func formatErrorNote(msg string) string {
	note := strings.TrimSpace(strings.ReplaceAll(msg, "\n", " "))
	if note == "" {
		return ""
	}
	if len(note) > maxErrorNoteChars {
		note = note[:maxErrorNoteChars]
	}
	return "Context refresh failed: " + note
}

func removeContextErrorBlock(block string) string {
	startTag := "<context name=\"" + errorContextName + "\">"
	for {
		start := strings.Index(block, startTag)
		if start == -1 {
			break
		}
		end := strings.Index(block[start:], "</context>")
		if end == -1 {
			break
		}
		endIdx := start + end + len("</context>")
		block = block[:start] + block[endIdx:]
	}
	return block
}

func truncateWithNote(content string, maxChars int, note string) string {
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}

	if len(note) >= maxChars {
		return note[:maxChars]
	}

	return content[:maxChars-len(note)] + note
}

func (o *ContextOrchestrator) fetchMCPContent(ctx stdctx.Context, source MCPContextSource) (string, error) {
	if o.mcpProvider == nil {
		return "", fmt.Errorf("MCP provider unavailable")
	}

	switch source.Kind {
	case MCPSourceResource:
		if source.URI == "" {
			return "", fmt.Errorf("missing resource URI")
		}
		result, err := o.mcpProvider.ReadResource(ctx, source.ServerName, source.URI)
		if err != nil {
			return "", err
		}
		return resourceContentsToText(result), nil
	case MCPSourcePrompt:
		if source.PromptName == "" {
			return "", fmt.Errorf("missing prompt name")
		}
		result, err := o.mcpProvider.GetPrompt(ctx, source.ServerName, source.PromptName, source.PromptArgs)
		if err != nil {
			return "", err
		}
		return promptResultToText(result), nil
	default:
		return "", fmt.Errorf("unknown MCP source kind")
	}
}

func resourceContentsToText(resource *mcp.ResourceContents) string {
	if resource == nil {
		return ""
	}
	if strings.TrimSpace(resource.Text) != "" {
		return resource.Text
	}
	if len(resource.Contents) > 0 {
		var b strings.Builder
		for _, item := range resource.Contents {
			text := strings.TrimSpace(item.Text)
			if text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(text)
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	if resource.Blob != "" {
		if resource.MimeType != "" {
			return fmt.Sprintf("[binary resource omitted: %s]", resource.MimeType)
		}
		return "[binary resource omitted]"
	}
	return ""
}

func promptResultToText(result *mcp.PromptResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	for _, message := range result.Messages {
		text := promptContentToText(message.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(text)
	}
	return b.String()
}

func promptContentToText(content mcp.PromptContent) string {
	if strings.TrimSpace(content.Text) != "" {
		return content.Text
	}
	if content.Resource != nil {
		if strings.TrimSpace(content.Resource.Text) != "" {
			return content.Resource.Text
		}
		if content.Resource.URI != "" {
			return fmt.Sprintf("[resource: %s]", content.Resource.URI)
		}
	}
	if content.Data != "" {
		if content.MimeType != "" {
			return fmt.Sprintf("[binary content omitted: %s]", content.MimeType)
		}
		return "[binary content omitted]"
	}
	return ""
}

func formatMCPContextName(source MCPContextSource) string {
	normalized := NormalizeMCPSource(source)
	name := normalized.ID
	if name == "" {
		name = "mcp_source"
	}
	return sanitizeContextName(name)
}

func sanitizeContextName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "mcp_source"
	}
	replacer := strings.NewReplacer(
		"\"", "",
		"<", "",
		">", "",
		"\n", "_",
		"\r", "_",
		"\t", "_",
		" ", "_",
	)
	name = replacer.Replace(name)
	return name
}

func formatMCPWarning(source MCPContextSource, err error) string {
	msg := strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
	if len(msg) > maxMCPWarningChars {
		msg = msg[:maxMCPWarningChars]
	}
	return fmt.Sprintf("MCP source %s unavailable: %s", describeMCPSource(source), msg)
}

func describeMCPSource(source MCPContextSource) string {
	label := strings.TrimSpace(source.Label)
	if label != "" {
		return label
	}
	identifier := ""
	switch source.Kind {
	case MCPSourceResource:
		identifier = source.URI
	case MCPSourcePrompt:
		identifier = source.PromptName
	}
	if identifier != "" {
		return fmt.Sprintf("%s:%s", source.ServerName, identifier)
	}
	if source.ServerName != "" {
		return source.ServerName
	}
	return "unknown"
}

func hashString(input string) string {
	if input == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
