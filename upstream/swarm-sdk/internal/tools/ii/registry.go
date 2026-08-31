// Registry provides convenient access to all ii tools.
// Ported from ii-agent's manager.py
package ii

import (
	"path/filepath"
	"sort"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
)

// GetFileSystemTools returns all file system tools
func GetFileSystemTools(wm *WorkspaceManager) []tools.Tool {
	snapshotDir := filepath.Join(wm.workspacePath, ".swarm", "snapshots")
	return []tools.Tool{
		forge.NewFSRead(wm.workspacePath),
		forge.NewApplyPatchTool(wm.workspacePath),
		forge.NewFSUndo(wm.workspacePath, snapshotDir),
	}
}

// GetProductivityTools returns all productivity tools (task management).
// These tools do not require a WorkspaceManager as they manage in-memory state.
func GetProductivityTools() []tools.Tool {
	return []tools.Tool{NewTaskManageTool()}
}

// GetLegacyTaskTools returns deprecated adapters for callers that still need
// to decode or test historical task-tool contracts. They are intentionally not
// installed in either runtime registry.
func GetLegacyTaskTools() []tools.Tool {
	return legacyTaskToolsWithManager(GetTodoManager())
}

func legacyTaskToolsWithManager(manager *TodoManager) []tools.Tool {
	return []tools.Tool{
		NewTaskCreateToolWithManager(manager),
		NewTaskUpdateToolWithManager(manager),
		NewTaskGetToolWithManager(manager),
		NewTaskListToolWithManager(manager),
	}
}

// RegisterProductivityTools registers TaskManage as the sole live task-record
// tool. Legacy TaskCreate/TaskUpdate/TaskGet/TaskList adapters remain available
// as source-level compatibility helpers, but are not runtime-reachable.
func RegisterProductivityTools(registry tools.Registry) error {
	return registry.Register(NewTaskManageToolWithManager(GetTodoManager()))
}

// DevToolsConfig contains configuration for development tools.
// These tools help with project initialization, deployment, and database operations.
type DevToolsConfig struct {
	// PortExposer is used by the port registration tool to expose local ports.
	// If nil, a default localhost-only exposer is used.
	PortExposer PortExposer

	// DatabaseCredentials contains credentials for the database tool.
	// Required for the database connection tool to function.
	DatabaseCredentials DatabaseCredentials

	// DatabaseServerURL is the URL of the database tool server.
	// If empty, the database connection tool will not be registered.
	DatabaseServerURL string
}

// GetBrowserTools returns all browser automation tools.
// These tools require a BrowserManager for controlling the browser instance.
// Browser tools enable web automation, scraping, and testing capabilities.
func GetBrowserTools(browser *BrowserManager) []tools.Tool {
	return []tools.Tool{
		// Navigation tools
		NewBrowserNavigationTool(browser),
		NewBrowserRestartTool(browser),

		// View/Inspection tools
		NewBrowserViewTool(browser),
		NewBrowserWaitTool(browser),

		// Interaction tools
		NewBrowserClickTool(browser),
		NewBrowserEnterTextTool(browser),
		NewBrowserEnterMultipleTextsTool(browser),
		NewBrowserPressKeyTool(browser),

		// Scrolling tools
		NewBrowserScrollDownTool(browser),
		NewBrowserScrollUpTool(browser),

		// Drag and drop
		NewBrowserDragTool(browser),

		// Dropdown/Select tools
		NewBrowserGetSelectOptionsTool(browser),
		NewBrowserSelectDropdownOptionTool(browser),

		// Tab management
		NewBrowserSwitchTabTool(browser),
		NewBrowserOpenNewTabTool(browser),
	}
}

// GetDevTools returns all development tools (project init, deployment, etc.).
// These tools help with project setup, deployment, and database operations.
func GetDevTools(wm *WorkspaceManager, config *DevToolsConfig) []tools.Tool {
	devTools := []tools.Tool{
		NewFullStackInitTool(wm),
		NewSaveCheckpointTool(wm),
	}

	// Add port registration tool (uses default exposer if none configured)
	if config != nil && config.PortExposer != nil {
		devTools = append(devTools, NewRegisterPortTool(config.PortExposer))
	} else {
		devTools = append(devTools, NewRegisterPortTool(nil))
	}

	// Add database tool only if credentials are configured
	if config != nil && config.DatabaseServerURL != "" {
		devTools = append(devTools, NewGetDatabaseConnectionTool(
			config.DatabaseCredentials,
			config.DatabaseServerURL,
		))
	}

	return devTools
}

// GetAllTools returns all ii tools (file system and productivity tools).
func GetAllTools(wm *WorkspaceManager) []tools.Tool {
	var allTools []tools.Tool
	allTools = append(allTools, GetFileSystemTools(wm)...)
	allTools = append(allTools, GetProductivityTools()...)
	return allTools
}

// GetAllToolsWithDev returns all ii tools including dev tools.
func GetAllToolsWithDev(wm *WorkspaceManager, devConfig *DevToolsConfig) []tools.Tool {
	var allTools []tools.Tool
	allTools = append(allTools, GetFileSystemTools(wm)...)
	allTools = append(allTools, GetProductivityTools()...)
	allTools = append(allTools, GetDevTools(wm, devConfig)...)
	return allTools
}

// GetAllToolsWithBrowser returns all ii tools including browser tools.
// Use this when you need browser automation capabilities.
func GetAllToolsWithBrowser(wm *WorkspaceManager, browser *BrowserManager) []tools.Tool {
	var allTools []tools.Tool
	allTools = append(allTools, GetFileSystemTools(wm)...)
	allTools = append(allTools, GetProductivityTools()...)
	allTools = append(allTools, GetBrowserTools(browser)...)
	return allTools
}

// GetAllToolsWithDevAndBrowser returns all ii tools including dev and browser tools.
// This is the most comprehensive set of tools for full agent functionality
// including web automation capabilities.
func GetAllToolsWithDevAndBrowser(wm *WorkspaceManager, devConfig *DevToolsConfig, browser *BrowserManager) []tools.Tool {
	var allTools []tools.Tool
	allTools = append(allTools, GetFileSystemTools(wm)...)
	allTools = append(allTools, GetProductivityTools()...)
	allTools = append(allTools, GetDevTools(wm, devConfig)...)
	allTools = append(allTools, GetBrowserTools(browser)...)
	return allTools
}

// ToolRegistry manages ii tool registration
type ToolRegistry struct {
	tools            map[string]tools.Tool
	workspaceManager *WorkspaceManager
	browserManager   *BrowserManager
}

// NewToolRegistry creates a new tool registry.
func NewToolRegistry(workspacePath string) (*ToolRegistry, error) {
	wm, err := NewWorkspaceManager(workspacePath)
	if err != nil {
		return nil, err
	}

	registry := &ToolRegistry{
		tools:            make(map[string]tools.Tool),
		workspaceManager: wm,
	}

	// Register all tools
	for _, tool := range GetAllTools(wm) {
		registry.tools[tool.Name()] = tool
	}

	return registry, nil
}

// NewToolRegistryWithDev creates a registry with development tools.
func NewToolRegistryWithDev(workspacePath string, devConfig *DevToolsConfig) (*ToolRegistry, error) {
	wm, err := NewWorkspaceManager(workspacePath)
	if err != nil {
		return nil, err
	}

	registry := &ToolRegistry{
		tools:            make(map[string]tools.Tool),
		workspaceManager: wm,
	}

	// Register all tools including dev tools
	for _, tool := range GetAllToolsWithDev(wm, devConfig) {
		registry.tools[tool.Name()] = tool
	}

	return registry, nil
}

// Get retrieves a live registered tool by name.
func (r *ToolRegistry) Get(name string) (tools.Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// List returns all registered tool names.
// Names are sorted alphabetically for consistent ordering (required for cache stability).
func (r *ToolRegistry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// All returns all registered tools.
// Tools are returned in alphabetically sorted order by name (required for cache stability).
func (r *ToolRegistry) All() []tools.Tool {
	// Get sorted names first
	names := r.List()
	toolList := make([]tools.Tool, 0, len(names))
	for _, name := range names {
		if tool, ok := r.tools[name]; ok {
			toolList = append(toolList, tool)
		}
	}
	return toolList
}

// RegisterTool adds a custom tool to the registry
func (r *ToolRegistry) RegisterTool(tool tools.Tool) {
	r.tools[tool.Name()] = tool
}

// WorkspaceManager returns the workspace manager
func (r *ToolRegistry) WorkspaceManager() *WorkspaceManager {
	return r.workspaceManager
}

// BrowserManager returns the browser manager (may be nil if browser tools not registered)
func (r *ToolRegistry) BrowserManager() *BrowserManager {
	return r.browserManager
}

// Cleanup releases resources used by the registry.
// This should be called when the registry is no longer needed,
// particularly to clean up browser instances.
func (r *ToolRegistry) Cleanup() {
	if r.browserManager != nil {
		r.browserManager.Stop()
	}
}

// NewToolRegistryWithBrowser creates a registry with browser automation tools.
// The browser is not started automatically - call browser.Start() when needed.
func NewToolRegistryWithBrowser(workspacePath string, browserConfig *BrowserConfig) (*ToolRegistry, error) {
	wm, err := NewWorkspaceManager(workspacePath)
	if err != nil {
		return nil, err
	}

	browser := NewBrowserManager(browserConfig)

	registry := &ToolRegistry{
		tools:            make(map[string]tools.Tool),
		workspaceManager: wm,
		browserManager:   browser,
	}

	// Register all tools including browser tools
	for _, tool := range GetAllToolsWithBrowser(wm, browser) {
		registry.tools[tool.Name()] = tool
	}

	return registry, nil
}

// NewToolRegistryWithDevAndBrowser creates a registry with all tools including dev and browser.
// This is the most comprehensive registry for full agent functionality.
func NewToolRegistryWithDevAndBrowser(workspacePath string, devConfig *DevToolsConfig, browserConfig *BrowserConfig) (*ToolRegistry, error) {
	wm, err := NewWorkspaceManager(workspacePath)
	if err != nil {
		return nil, err
	}

	browser := NewBrowserManager(browserConfig)

	registry := &ToolRegistry{
		tools:            make(map[string]tools.Tool),
		workspaceManager: wm,
		browserManager:   browser,
	}

	// Register all tools including dev and browser tools
	for _, tool := range GetAllToolsWithDevAndBrowser(wm, devConfig, browser) {
		registry.tools[tool.Name()] = tool
	}

	return registry, nil
}
