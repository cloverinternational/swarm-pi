// Template processor system for initializing project templates.
// Ported from ii-agent's template_processor module.
package ii

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// TemplateProcessor defines the interface for project template processors.
// Each processor handles initialization for a specific framework/template combination.
type TemplateProcessor interface {
	// TemplateName returns the name of the template (used for lookup and file paths)
	TemplateName() string

	// InstallDependencies installs the project dependencies
	InstallDependencies() error

	// CopyProjectTemplate copies the template files to the project directory
	CopyProjectTemplate() error

	// StartUpProject performs complete project initialization
	StartUpProject() error

	// GetProjectRule returns the project-specific rules and guidelines
	ProjectRule() string
}

// BaseProcessor provides common functionality for all template processors.
// Implements the Template Method pattern for project initialization.
type BaseProcessor struct {
	// ProjectDir is the absolute path to the project directory
	ProjectDir string

	// templateName is the name of the template to use
	templateName string

	// projectRule contains the project-specific rules and guidelines
	projectRule string

	// templatesRoot is the root directory containing all templates
	templatesRoot string
}

// NewBaseProcessor creates a new BaseProcessor with the given configuration.
func NewBaseProcessor(projectDir, templateName, projectRule string) *BaseProcessor {
	return &BaseProcessor{
		ProjectDir:    projectDir,
		templateName:  templateName,
		projectRule:   projectRule,
		templatesRoot: getTemplatesRoot(),
	}
}

// TemplateName returns the template name
func (b *BaseProcessor) TemplateName() string {
	return b.templateName
}

// GetProjectRule returns the project-specific rules
func (b *BaseProcessor) ProjectRule() string {
	if b.projectRule == "" {
		return "No project rules defined"
	}
	return b.projectRule
}

// CopyProjectTemplate copies the template files to the project directory.
// This is a final method that should not be overridden.
func (b *BaseProcessor) CopyProjectTemplate() error {
	templatePath := filepath.Join(b.templatesRoot, ".templates", b.templateName)

	// Check if template exists
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		return fmt.Errorf("template '%s' not found at %s", b.templateName, templatePath)
	}

	// Use appropriate copy command for the OS
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// On Windows, use xcopy
		cmd = exec.Command("xcopy", templatePath, b.ProjectDir, "/E", "/I", "/Y")
	} else {
		// On Unix-like systems, use cp -rf
		cmd = exec.Command("sh", "-c", fmt.Sprintf("cp -rf %s/* .", templatePath))
		cmd.Dir = b.ProjectDir
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to copy project template: %s. Output: %s", err, string(output))
	}

	return nil
}

// StartUpProject performs the complete project initialization.
// This is a template method that calls CopyProjectTemplate and InstallDependencies.
// Subclasses must implement InstallDependencies.
func (b *BaseProcessor) StartUpProject(installer func() error) error {
	if err := b.CopyProjectTemplate(); err != nil {
		return fmt.Errorf("failed to start up project: %w", err)
	}

	if installer != nil {
		if err := installer(); err != nil {
			return fmt.Errorf("failed to start up project: %w", err)
		}
	}

	return nil
}

// getTemplatesRoot returns the root directory containing templates.
// This walks up from the current file location to find the project root.
func getTemplatesRoot() string {
	// First check if TEMPLATES_ROOT environment variable is set
	if root := os.Getenv("II_TEMPLATES_ROOT"); root != "" {
		return root
	}

	// Try to find templates relative to current working directory
	cwd, err := os.Getwd()
	if err == nil {
		// Walk up looking for .templates directory
		current := cwd
		for {
			templatesDir := filepath.Join(current, ".templates")
			if info, err := os.Stat(templatesDir); err == nil && info.IsDir() {
				return current
			}

			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}

	// Default to current directory
	return cwd
}

// SetTemplatesRoot sets the root directory for templates (useful for testing)
func (b *BaseProcessor) SetTemplatesRoot(root string) {
	b.templatesRoot = root
}

// runCommand executes a command and returns the combined output
func runCommand(command string, cwd string) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Dir = cwd

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command failed: %s. Output: %s", err, string(output))
	}
	return string(output), nil
}

// ProcessorFactory is a function that creates a TemplateProcessor for a given project directory
type ProcessorFactory func(projectDir string) TemplateProcessor

// WebProcessorRegistry provides a registry-based factory for template processors.
// Supports dynamic registration of processors for different frameworks.
type WebProcessorRegistry struct {
	mu       sync.RWMutex
	registry map[string]ProcessorFactory
}

// Global registry instance
var globalProcessorRegistry = &WebProcessorRegistry{
	registry: make(map[string]ProcessorFactory),
}

// GetProcessorRegistry returns the global processor registry
func GetProcessorRegistry() *WebProcessorRegistry {
	return globalProcessorRegistry
}

// Register adds a processor factory for the given framework name.
// This method is thread-safe.
func (r *WebProcessorRegistry) Register(frameworkName string, factory ProcessorFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registry[frameworkName] = factory
}

// Create creates a processor instance for the given framework.
// Returns an error if the framework is not registered.
func (r *WebProcessorRegistry) Create(frameworkName, projectDir string) (TemplateProcessor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	factory, ok := r.registry[frameworkName]
	if !ok {
		available := r.listFrameworksLocked()
		return nil, fmt.Errorf("unknown framework '%s'. Available: %s", frameworkName, strings.Join(available, ", "))
	}

	return factory(projectDir), nil
}

// ListFrameworks returns a list of all registered framework names.
func (r *WebProcessorRegistry) ListFrameworks() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.listFrameworksLocked()
}

// listFrameworksLocked returns frameworks without acquiring lock (caller must hold lock)
func (r *WebProcessorRegistry) listFrameworksLocked() []string {
	frameworks := make([]string, 0, len(r.registry))
	for name := range r.registry {
		frameworks = append(frameworks, name)
	}
	return frameworks
}

// IsRegistered checks if a framework is registered
func (r *WebProcessorRegistry) IsRegistered(frameworkName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.registry[frameworkName]
	return ok
}

// NextJSShadcnProcessor handles Next.js + shadcn/ui project initialization
type NextJSShadcnProcessor struct {
	*BaseProcessor
}

// NewNextJSShadcnProcessor creates a new Next.js + shadcn processor
func NewNextJSShadcnProcessor(projectDir string) *NextJSShadcnProcessor {
	return &NextJSShadcnProcessor{
		BaseProcessor: NewBaseProcessor(projectDir, "nextjs-shadcn", nextjsShadcnDeploymentRule(projectDir)),
	}
}

// InstallDependencies installs dependencies using bun
func (p *NextJSShadcnProcessor) InstallDependencies() error {
	_, err := runCommand("bun install", p.ProjectDir)
	if err != nil {
		return fmt.Errorf("failed to install dependencies automatically: %w. Please fix the error and run `bun install` in the project directory manually", err)
	}
	return nil
}

// StartUpProject performs complete project initialization
func (p *NextJSShadcnProcessor) StartUpProject() error {
	return p.BaseProcessor.StartUpProject(p.InstallDependencies)
}

// CopyProjectTemplate delegates to BaseProcessor
func (p *NextJSShadcnProcessor) CopyProjectTemplate() error {
	return p.BaseProcessor.CopyProjectTemplate()
}

// nextjsShadcnDeploymentRule returns the project rules for Next.js + shadcn projects
func nextjsShadcnDeploymentRule(projectPath string) string {
	return fmt.Sprintf(`
Project directory %s created successfully. Application code is in %s/src. File tree:
%s/
│   ├── .gitignore              # Git ignore file
│   ├── biome.json              # Biome linter/formatter configuration
│   ├── bun.lock               # Lock file for dependencies
│   ├── components.json         # shadcn/ui configuration
│   ├── eslint.config.mjs       # ESLint configuration
│   ├── next-env.d.ts           # Next.js TypeScript declarations
│   ├── next.config.js          # Next.js configuration
│   ├── package.json            # Project dependencies and scripts
│   ├── postcss.config.mjs      # PostCSS configuration
│   ├── README.md               # Project documentation
│   ├── __tests__/              # Jest test directory
│   ├── src/                    # Source code directory
│   │   ├── app/                # Next.js App Router directory
│   │   │   ├── ClientBody.tsx  # Client-side body component
│   │   │   ├── globals.css     # Global styles
│   │   │   ├── layout.tsx      # Root layout component
│   │   │   ├── page.tsx        # Home page component
│   │   └── lib/                # Utility functions and libraries
│   │   │   └── utils.ts        # Utility functions
│   │   └── components/         # Components directory
│   │       └── ui/             # shadcn/ui components
│   │           └── button.tsx  # Button component
│   ├── tailwind.config.ts      # Tailwind CSS configuration
    └── tsconfig.json           # TypeScript configuration

IMPORTANT NOTE: This project is built with TypeScript(tsx) and Next.js App Router.
Add components with 'cd %s && bunx shadcn@latest add -y -o'. Import components with '@/' alias. Note, 'toast' is deprecated, use 'sonner' instead. Before editing, run 'cd %s && bun install' to install dependencies. Run 'cd %s && bun run dev' to start the dev server ASAP to catch any runtime errors. Remember that all terminal commands must be run from the project directory.
Any database operations must be done with Prisma ORM.
Authentication must be done with NextAuth. Use bcrypt for password hashing.
Use Chart.js for charts. Moveable for Draggable, Resizable, Scalable, Rotatable, Warpable, Pinchable, Groupable, Snappable components.
Use AOS for scroll animations. React-Player for video player.
Advance animations must be done with Framer Motion, Anime.js, and React Three Fiber.
Before writing the frontend integration, you must write an openapi spec for the backend then you must write test for all the expected http requests and responses using supertest (already installed).
Run the test by running 'bun test'. Any backend operations must pass all test before you begin your deployment
The integration must follow the api contract strictly. Your predecessor was killed because he did not follow the api contract.

IMPORTANT: All the todo list must be done before you can return to the user.

If you need to use websocket, follow this guide: https://socket.io/how-to/use-with-nextjs
You must use socket.io and (IMPORTANT) socket.io-client for websocket.
Socket.io rules:
"Separate concerns, sanitize data, handle failures gracefully"

    NEVER send objects with circular references or function properties
    ALWAYS validate data serializability before transmission
    SEPARATE connection management from business logic storage
    SANITIZE all data crossing network boundaries
    CLEANUP resources and event listeners to prevent memory leaks
    HANDLE network failures, timeouts, and reconnections
    VALIDATE all incoming data on both client and server
    TEST with multiple concurrent connections under load

APPLIES TO: Any real-time system (WebSockets, Server-Sent Events, WebRTC, polling)


Banned libraries (will break with this template): Quill
`, projectPath, projectPath, projectPath, projectPath, projectPath, projectPath)
}

// ReactShadcnPythonProcessor handles React + shadcn + Python FastAPI project initialization
type ReactShadcnPythonProcessor struct {
	*BaseProcessor
}

// NewReactShadcnPythonProcessor creates a new React + shadcn + Python processor
func NewReactShadcnPythonProcessor(projectDir string) *ReactShadcnPythonProcessor {
	return &ReactShadcnPythonProcessor{
		BaseProcessor: NewBaseProcessor(projectDir, "react-shadcn-python", reactShadcnPythonDeploymentRule(projectDir)),
	}
}

// InstallDependencies installs frontend (bun) and backend (pip) dependencies
func (p *ReactShadcnPythonProcessor) InstallDependencies() error {
	frontendDir := filepath.Join(p.ProjectDir, "frontend")
	backendDir := filepath.Join(p.ProjectDir, "backend")

	// Install frontend dependencies
	_, err := runCommand("bun install", frontendDir)
	if err != nil {
		return fmt.Errorf("failed to install frontend dependencies automatically: %w. Please fix the error and run `bun install` in the frontend directory manually", err)
	}

	// Install backend dependencies
	_, err = runCommand("pip install -r requirements.txt", backendDir)
	if err != nil {
		return fmt.Errorf("failed to install backend dependencies automatically: %w. Please fix the error and run `pip install -r requirements.txt` in the backend directory manually", err)
	}

	return nil
}

// StartUpProject performs complete project initialization
func (p *ReactShadcnPythonProcessor) StartUpProject() error {
	return p.BaseProcessor.StartUpProject(p.InstallDependencies)
}

// CopyProjectTemplate delegates to BaseProcessor
func (p *ReactShadcnPythonProcessor) CopyProjectTemplate() error {
	return p.BaseProcessor.CopyProjectTemplate()
}

// reactShadcnPythonDeploymentRule returns the project rules for React + Python projects
func reactShadcnPythonDeploymentRule(projectPath string) string {
	return fmt.Sprintf(`Successfully initialized codebase:
%s
├── backend/
│   ├── README.md
│   ├── requirements.txt
│   └── src/
│       ├── __init__.py
│       ├── main.py
│       └── tests/
│           └── __init__.py
└── frontend
    ├── README.md
    ├── bun.lock
    ├── components.json
    ├── eslint.config.js
    ├── index.html
    ├── package.json
    ├── public/
    │   └── _redirects
    ├── src/
    │   ├── App.tsx
    │   ├── components/
    │   │   └── ui
    │   │       └── button.tsx
    │   ├── index.css
    │   ├── lib/
    │   │   └── utils.ts
    │   ├── main.tsx
    │   └── vite-env.d.ts
    ├── tsconfig.app.json
    ├── tsconfig.json
    ├── tsconfig.node.json
    └── vite.config.ts

Installed dependencies:
- Frontend: bun install
- Backend: pip install -r requirements.txt

Backend dependencies:
fastapi
uvicorn
sqlalchemy
python-dotenv
pydantic
pydantic-settings
pytest
pytest-asyncio
httpx
openai
bcrypt
python-jose[cryptography]
python-multipart
cryptography
requests

Utilize the Shadcn UI library for the frontend. Add components with 'bunx shadcn@latest add -y -o'. Import components with '@/' alias. Note, 'toast' is deprecated, use 'sonner' instead.`, projectPath)
}

// init registers the default processors
func init() {
	registry := GetProcessorRegistry()

	registry.Register("nextjs-shadcn", func(projectDir string) TemplateProcessor {
		return NewNextJSShadcnProcessor(projectDir)
	})

	registry.Register("react-shadcn-python", func(projectDir string) TemplateProcessor {
		return NewReactShadcnPythonProcessor(projectDir)
	})
}

// GetAvailableFrameworks returns a list of all available framework names
func GetAvailableFrameworks() []string {
	return GetProcessorRegistry().ListFrameworks()
}

// CreateProcessor creates a template processor for the given framework
func CreateProcessor(frameworkName, projectDir string) (TemplateProcessor, error) {
	return GetProcessorRegistry().Create(frameworkName, projectDir)
}
