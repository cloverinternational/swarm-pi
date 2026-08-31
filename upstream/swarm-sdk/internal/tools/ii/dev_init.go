// Project initialization tool for creating fullstack web applications from templates.
// Ported from ii-agent's init_tool.py
package ii

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	// FullStackInitToolName is the unique identifier for this tool
	FullStackInitToolName = "fullstack_project_init"

	// FullStackInitToolDisplayName is the human-readable name
	FullStackInitToolDisplayName = "Initialize application template"
)

// fullStackInitDescription provides detailed documentation for the tool
const fullStackInitDescription = `Initializes a complete fullstack web application from pre-configured templates with modern development tools and best practices.

## Overview
This tool scaffolds production-ready fullstack applications with automated dependency management, testing infrastructure, and deployment configurations. Choose from optimized templates that include modern UI components, authentication, database integration, and comprehensive testing setups.

## Available Frameworks (default: nextjs-shadcn)

### nextjs-shadcn
Modern TypeScript fullstack with premium UI components
- Frontend: Next.js 14+ (App Router), TypeScript, Tailwind CSS, shadcn/ui components
- Build Tools: Bun package manager, Biome linter/formatter, Jest testing
- Features: Server-side rendering, built-in authentication (NextAuth), Prisma ORM, advanced animations (Framer Motion), real-time features (Socket.io)
- Use Case: Enterprise applications, content management systems, e-commerce platforms

### react-shadcn-python
Fullstack JavaScript + Python with FastAPI backend
- Frontend: React + Vite, JavaScript, Tailwind CSS, shadcn/ui components
- Backend: FastAPI, SQLAlchemy, Pydantic, comprehensive testing suite
- Build Tools: Bun (frontend), pip (backend), automated testing with pytest
- Features: REST API, JWT authentication, database migrations, OpenAPI documentation
- Use Case: API-driven applications, data dashboards, microservices architecture

## Development Guidelines

### Backend Standards
- Testing Requirements: Comprehensive test coverage for all endpoints and business logic
  * Unit tests for all functions and classes
  * Integration tests for API endpoints
  * Edge case and error handling coverage
  * All tests must pass before deployment
- API Design: Follow RESTful principles with OpenAPI documentation
- Security: Input validation, authentication, authorization, SQL injection prevention

### Frontend Standards
- UI/UX: Modern, responsive design using Tailwind CSS utility classes
- Component Architecture: Reusable, composable React components
- State Management: Context API or external libraries as needed
- Performance: Code splitting, lazy loading, optimized builds
- Accessibility: WCAG compliance, semantic HTML, proper ARIA labels

### Deployment Configuration
- Default Ports:
  * Backend: 8080 (auto-increment if unavailable)
  * Frontend: 3000 (auto-increment if unavailable)
- Environment: Development, staging, and production configurations
- Monitoring: Error tracking, performance monitoring, logging

### Debugging Best Practices
- API Testing: Test all endpoints with appropriate HTTP clients
- Error Analysis: Monitor console output and application logs
- Documentation: Consult framework documentation and community resources
- Incremental Development: Test components and features iteratively

## Post-Initialization Steps
1. Navigate to project directory: cd <project_name>
2. Install dependencies (automatically handled by tool)
3. Start development servers
4. Review generated documentation and project structure
5. Begin feature development following established patterns

## Quality Assurance
- All templates include pre-configured linting and formatting
- Automated testing infrastructure is ready for immediate use
- Security best practices are implemented by default
- Performance optimizations are built into the build process`

// FullStackInitTool implements project initialization functionality
type FullStackInitTool struct {
	workspaceManager *WorkspaceManager
}

// NewFullStackInitTool creates a new FullStackInitTool
func NewFullStackInitTool(wm *WorkspaceManager) *FullStackInitTool {
	return &FullStackInitTool{workspaceManager: wm}
}

// Name returns the tool name
func (t *FullStackInitTool) Name() string {
	return FullStackInitToolName
}

// DisplayName returns the human-readable display name
func (t *FullStackInitTool) DisplayName() string {
	return FullStackInitToolDisplayName
}

// Description returns the tool description
func (t *FullStackInitTool) Description() string {
	return fullStackInitDescription
}

// Parameters returns the JSON schema for tool parameters
func (t *FullStackInitTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project_name": map[string]any{
				"type":        "string",
				"description": "A name for your project (lowercase, no spaces, use hyphens - if needed). Example: `my-app`, `todo-app`",
			},
			"framework": map[string]any{
				"type":        "string",
				"description": "The framework to use for the project",
				"enum":        GetAvailableFrameworks(),
			},
		},
		"required": []string{"project_name", "framework"},
	}
}

// Execute initializes a new project from a template
func (t *FullStackInitTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "fullstack_init.context_cancelled")
	default:
	}

	// Extract parameters
	projectName, ok := params["project_name"].(string)
	if !ok || projectName == "" {
		return nil, sdkerr.Permanent("fullstack_init.missing_project_name", "project_name is required")
	}

	framework, ok := params["framework"].(string)
	if !ok || framework == "" {
		return nil, sdkerr.Permanent("fullstack_init.missing_framework", "framework is required")
	}

	// Determine project directory
	var projectDir string
	if t.workspaceManager != nil {
		projectDir = filepath.Join(t.workspaceManager.WorkspacePath(), projectName)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, sdkerr.Wrap(err, "fullstack_init.cwd_failed")
		}
		projectDir = filepath.Join(cwd, projectName)
	}

	// Check if project directory already exists
	if _, err := os.Stat(projectDir); err == nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Project directory %s already exists, please choose a different project name", projectDir)), nil
	}

	// Create project directory
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return nil, sdkerr.Wrap(err, "fullstack_init.mkdir_failed")
	}

	// Check context before template processing
	select {
	case <-ctx.Done():
		// Clean up the created directory
		os.RemoveAll(projectDir)
		return nil, sdkerr.Wrap(ctx.Err(), "fullstack_init.context_cancelled")
	default:
	}

	// Create template processor
	processor, err := CreateProcessor(framework, projectDir)
	if err != nil {
		os.RemoveAll(projectDir) // Clean up on failure
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to create processor: %s", err.Error())), nil
	}

	// Start up the project
	if err := processor.StartUpProject(); err != nil {
		os.RemoveAll(projectDir) // Clean up on failure
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to start up project in %s: %s", projectDir, err.Error())), nil
	}

	// Build project metadata
	projectMetadata := map[string]any{
		"type":              "fullstack_project_metadata",
		"project_name":      projectName,
		"framework":         framework,
		"project_directory": projectDir,
		"template":          processor.TemplateName(),
		"project_rule":      processor.ProjectRule(),
	}

	// Create result with metadata
	result := tools.NewToolResult(fmt.Sprintf(
		"Successfully initialized fullstack web application in %s. Framework: %s.",
		projectDir, framework,
	))
	result.WithMetadata("project_metadata", projectMetadata)

	return result, nil
}

// Validate checks if the given parameters are valid
func (t *FullStackInitTool) Validate(params map[string]any) error {
	projectName, ok := params["project_name"].(string)
	if !ok || projectName == "" {
		return fmt.Errorf("project_name is required")
	}

	framework, ok := params["framework"].(string)
	if !ok || framework == "" {
		return fmt.Errorf("framework is required")
	}

	// Check if framework is available
	if !GetProcessorRegistry().IsRegistered(framework) {
		return fmt.Errorf("unknown framework '%s'. Available: %v", framework, GetAvailableFrameworks())
	}

	return nil
}

// IsIdempotent returns false as project creation is not idempotent
func (t *FullStackInitTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool creates files
func (t *FullStackInitTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions
func (t *FullStackInitTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileWrite, tools.PermissionBashExecute}
}

// SupportedContentTypes returns the content types this tool can produce
func (t *FullStackInitTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use
func (t *FullStackInitTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns confirmation details for project initialization
func (t *FullStackInitTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	projectName, _ := params["project_name"].(string)
	framework, _ := params["framework"].(string)
	return &ConfirmationDetails{
		Type:    ConfirmationTypeBash,
		Message: fmt.Sprintf("Initialize new %s project named '%s'", framework, projectName),
	}
}

// Metadata returns the tool metadata
func (t *FullStackInitTool) Metadata() map[string]any {
	return map[string]any{
		"category": "development",
		"tags":     []string{"project", "init", "fullstack", "template"},
	}
}
