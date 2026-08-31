package projectmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"strings"
)

var globalDB *Database
var globalProjectDir string

// InitializeTools sets up the project memory system with the given project directory
func InitializeTools(projectDir string) error {
	globalProjectDir = projectDir
	db, err := NewDatabase(projectDir)
	if err != nil {
		return fmt.Errorf("failed to initialize project memory database: %w", err)
	}
	globalDB = db
	return nil
}

// getDB returns the global database instance, initializing if needed
func getDB() *Database {
	if globalDB == nil && globalProjectDir != "" {
		// Lazy initialization attempt
		if err := InitializeTools(globalProjectDir); err != nil {
			return nil
		}
	}
	return globalDB
}

// ViewProjectContext tool - view project context entries
type ViewProjectContext struct{}

func (t *ViewProjectContext) Name() string {
	return "view_project_context"
}

func (t *ViewProjectContext) Description() string {
	return `View project context entries. Project context is a persistent key-value store of project information.

Parameters:
- context_key (optional): Specific context key to retrieve
- search_query (optional): Search across keys, descriptions, and values  
- show_health_analysis (optional): Show health analysis of context entries
- max_results (optional): Maximum number of results (default: 50)
- sort_by (optional): Sort by 'key', 'last_updated', or 'size' (default: last_updated)`
}

func (t *ViewProjectContext) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"context_key": map[string]any{
				"type":        "string",
				"description": "Specific context key to retrieve (optional)",
			},
			"search_query": map[string]any{
				"type":        "string",
				"description": "Search across keys, descriptions, and values (optional)",
			},
			"show_health_analysis": map[string]any{
				"type":        "boolean",
				"description": "Show health analysis of all context entries (default: false)",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 50)",
			},
			"sort_by": map[string]any{
				"type":        "string",
				"description": "Sort by: key, last_updated, or size (default: last_updated)",
				"enum":        []string{"key", "last_updated", "size"},
			},
		},
	}
}

func (t *ViewProjectContext) Validate(params map[string]any) error {
	if sortBy, ok := params["sort_by"].(string); ok {
		if sortBy != "key" && sortBy != "last_updated" && sortBy != "size" {
			return fmt.Errorf("sort_by must be one of: key, last_updated, size")
		}
	}
	return nil
}

func (t *ViewProjectContext) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	db := getDB()
	if db == nil {
		return nil, fmt.Errorf("project memory database not initialized")
	}

	// Extract parameters
	contextKey, _ := params["context_key"].(string)
	searchQuery, _ := params["search_query"].(string)
	maxResults, _ := params["max_results"].(float64)
	sortBy, _ := params["sort_by"].(string)

	if maxResults == 0 {
		maxResults = 50
	}
	if sortBy == "" {
		sortBy = "last_updated"
	}

	// If specific key requested, return just that entry
	if contextKey != "" {
		entry, err := db.Entry(contextKey)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve context entry: %w", err)
		}
		return tools.NewToolResult(fmt.Sprintf("Context Entry: %s\nValue: %s\nDescription: %s\nLast Updated: %s\nUpdated By: %s",
			entry.Key, entry.Value, entry.Description, entry.UpdatedAt, entry.UpdatedBy)), nil
	}

	// List all entries with optional filtering
	entries, err := db.ListEntries()
	if err != nil {
		return nil, fmt.Errorf("failed to list context entries: %w", err)
	}

	// Filter by search query if provided
	if searchQuery != "" {
		var filtered []ContextEntry
		for _, e := range entries {
			if contains(e.Key, searchQuery) || contains(e.Value, searchQuery) || contains(e.Description, searchQuery) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	// Apply max results limit
	if len(entries) > int(maxResults) {
		entries = entries[:int(maxResults)]
	}

	// Format output
	output := "Project Context Entries:\n\n"
	for i, e := range entries {
		output += fmt.Sprintf("[%d] %s (Updated: %s)\n    Value: %s\n    Description: %s\n\n",
			i+1, e.Key, e.UpdatedAt, truncate(e.Value, 100), e.Description)
	}

	if output == "Project Context Entries:\n\n" {
		output = "No context entries found."
	}

	return tools.NewToolResult(output), nil
}

func (t *ViewProjectContext) IsIdempotent() bool {
	return true
}

func (t *ViewProjectContext) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

func (t *ViewProjectContext) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

func (t *ViewProjectContext) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// UpdateProjectContext tool - update or create a context entry
type UpdateProjectContext struct{}

func (t *UpdateProjectContext) Name() string {
	return "update_project_context"
}

func (t *UpdateProjectContext) Description() string {
	return `Update or create a project context entry. Context is stored persistently and accessible to all agents.

Use this to store:
- Architectural decisions and rationale
- Coding patterns and conventions
- Integration points and APIs
- Database schemas and models
- Key project information

Parameters:
- context_key (required): Unique key for this context entry
- value (required): Value to store (string, object, or array)
- description (optional): Human-readable description
- updated_by (optional): Agent or user ID`
}

func (t *UpdateProjectContext) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"context_key": map[string]any{
				"type":        "string",
				"description": "Unique key for this context entry (required)",
			},
			"value": map[string]any{
				"description": "Value to store (string, object, or array) (required)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Human-readable description (optional)",
			},
			"updated_by": map[string]any{
				"type":        "string",
				"description": "Agent or user ID (optional)",
			},
		},
		"required": []string{"context_key", "value"},
	}
}

func (t *UpdateProjectContext) Validate(params map[string]any) error {
	if _, ok := params["context_key"].(string); !ok {
		return fmt.Errorf("context_key is required and must be a string")
	}
	if _, ok := params["value"]; !ok {
		return fmt.Errorf("value is required")
	}
	return nil
}

func (t *UpdateProjectContext) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	db := getDB()
	if db == nil {
		return nil, fmt.Errorf("project memory database not initialized")
	}

	// Extract parameters
	contextKey, ok := params["context_key"].(string)
	if !ok {
		return nil, fmt.Errorf("context_key is required and must be a string")
	}

	value, ok := params["value"]
	if !ok {
		return nil, fmt.Errorf("value is required")
	}

	// Convert value to JSON string
	var valueStr string
	switch v := value.(type) {
	case string:
		valueStr = v
	case map[string]any, []any:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}
		valueStr = string(data)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}
		valueStr = string(data)
	}

	description, _ := params["description"].(string)
	updatedBy, _ := params["updated_by"].(string)

	// Set the entry
	err := db.SetEntry(contextKey, valueStr, description, updatedBy)
	if err != nil {
		return nil, fmt.Errorf("failed to update context entry: %w", err)
	}

	return tools.NewToolResult(fmt.Sprintf("Successfully updated context entry '%s'", contextKey)), nil
}

func (t *UpdateProjectContext) IsIdempotent() bool {
	return false
}

func (t *UpdateProjectContext) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

func (t *UpdateProjectContext) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

func (t *UpdateProjectContext) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// BulkUpdateProjectContext tool - update multiple context entries
type BulkUpdateProjectContext struct{}

func (t *BulkUpdateProjectContext) Name() string {
	return "bulk_update_project_context"
}

func (t *BulkUpdateProjectContext) Description() string {
	return `Update multiple project context entries atomically.

Parameters:
- updates (required): Array of update objects, each with context_key, value, and optional description
- updated_by (optional): Agent or user ID`
}

func (t *BulkUpdateProjectContext) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"updates": map[string]any{
				"type":        "array",
				"description": "Array of update objects (required)",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"context_key": map[string]any{
							"type":        "string",
							"description": "Unique key",
						},
						"value": map[string]any{
							"description": "Value to store",
						},
						"description": map[string]any{
							"type":        "string",
							"description": "Optional description",
						},
					},
					"required": []string{"context_key", "value"},
				},
			},
			"updated_by": map[string]any{
				"type":        "string",
				"description": "Agent or user ID (optional)",
			},
		},
		"required": []string{"updates"},
	}
}

func (t *BulkUpdateProjectContext) Validate(params map[string]any) error {
	updates, ok := params["updates"].([]any)
	if !ok {
		return fmt.Errorf("updates must be an array")
	}
	if len(updates) == 0 {
		return fmt.Errorf("updates array cannot be empty")
	}
	return nil
}

func (t *BulkUpdateProjectContext) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	db := getDB()
	if db == nil {
		return nil, fmt.Errorf("project memory database not initialized")
	}

	// Extract updates array
	updatesRaw, ok := params["updates"].([]any)
	if !ok {
		return nil, fmt.Errorf("updates must be an array")
	}

	updatedBy, _ := params["updated_by"].(string)

	successful := 0
	failed := 0
	var errors []string

	for _, updateRaw := range updatesRaw {
		updateMap, ok := updateRaw.(map[string]any)
		if !ok {
			failed++
			errors = append(errors, "Invalid update object format")
			continue
		}

		contextKey, ok := updateMap["context_key"].(string)
		if !ok {
			failed++
			errors = append(errors, "Missing context_key in update object")
			continue
		}

		value, ok := updateMap["value"]
		if !ok {
			failed++
			errors = append(errors, fmt.Sprintf("Missing value for key %s", contextKey))
			continue
		}

		// Convert value to string
		var valueStr string
		switch v := value.(type) {
		case string:
			valueStr = v
		default:
			data, _ := json.Marshal(v)
			valueStr = string(data)
		}

		description, _ := updateMap["description"].(string)

		// Update the entry
		if err := db.SetEntry(contextKey, valueStr, description, updatedBy); err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("Failed to update %s: %v", contextKey, err))
		} else {
			successful++
		}
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Bulk update completed: %d successful, %d failed", successful, failed))
	if len(errors) > 0 {
		output.WriteString("\n\nErrors:\n")
		for _, err := range errors {
			output.WriteString(fmt.Sprintf("  - %s\n", err))
		}
	}

	return tools.NewToolResult(output.String()), nil
}

func (t *BulkUpdateProjectContext) IsIdempotent() bool {
	return false
}

func (t *BulkUpdateProjectContext) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

func (t *BulkUpdateProjectContext) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

func (t *BulkUpdateProjectContext) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// DeleteProjectContext tool - delete a context entry
type DeleteProjectContext struct{}

func (t *DeleteProjectContext) Name() string {
	return "delete_project_context"
}

func (t *DeleteProjectContext) Description() string {
	return `Delete a project context entry.

Parameters:
- context_key (required): Key of the entry to delete`
}

func (t *DeleteProjectContext) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"context_key": map[string]any{
				"type":        "string",
				"description": "Key of the entry to delete (required)",
			},
		},
		"required": []string{"context_key"},
	}
}

func (t *DeleteProjectContext) Validate(params map[string]any) error {
	if _, ok := params["context_key"].(string); !ok {
		return fmt.Errorf("context_key is required and must be a string")
	}
	return nil
}

func (t *DeleteProjectContext) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	db := getDB()
	if db == nil {
		return nil, fmt.Errorf("project memory database not initialized")
	}

	// Extract parameters
	contextKey, ok := params["context_key"].(string)
	if !ok {
		return nil, fmt.Errorf("context_key is required and must be a string")
	}

	// Delete the entry
	err := db.DeleteEntry(contextKey)
	if err != nil {
		return nil, fmt.Errorf("failed to delete context entry: %w", err)
	}

	return tools.NewToolResult(fmt.Sprintf("Successfully deleted context entry '%s'", contextKey)), nil
}

func (t *DeleteProjectContext) IsIdempotent() bool {
	return false
}

func (t *DeleteProjectContext) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

func (t *DeleteProjectContext) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

func (t *DeleteProjectContext) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// Helper function to check if a string contains a substring (case-insensitive)
func contains(haystack, needle string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if match := true; match {
			for j := 0; j < len(needle); j++ {
				a, b := haystack[i+j], needle[j]
				if a >= 'A' && a <= 'Z' {
					a += 32
				}
				if b >= 'A' && b <= 'Z' {
					b += 32
				}
				if a != b {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// Helper function to truncate a string to a maximum length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// RegisterAllTools registers all project memory tools with the registry
func RegisterAllTools(registry tools.Registry) error {
	toolsList := []tools.Tool{
		&ViewProjectContext{},
		&UpdateProjectContext{},
		&BulkUpdateProjectContext{},
		&DeleteProjectContext{},
	}

	for _, tool := range toolsList {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", tool.Name(), err)
		}
	}
	return nil
}
