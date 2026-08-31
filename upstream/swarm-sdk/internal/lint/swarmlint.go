// Package main implements swarmlint - a static analysis tool for Swarm SDK contract enforcement.
//
// Swarm SDK contracts define invariants that MUST be maintained for correct behavior.
// This linter detects violations at compile time, before code runs.
//
// Contracts enforced:
//   - OrderedBlocks access: Use GetOrderedBlocks() instead of direct field access
//   - Message construction: Use factory methods instead of direct struct literals
//   - JSON-RPC client: Use official client instead of custom implementations
//   - Public API documentation: Require contract comments on public functions
//
// Usage:
//
//	go install github.com/Swarm-Code/mono/swarm-sdk/internal/lint/cmd/swarmlint@latest
//	swarmlint ./...
package lint

import (
	"flag"
	"go/ast"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Analyzer is the main swarmlint analyzer.
var Analyzer = &analysis.Analyzer{
	Name:     "swarmlint",
	Doc:      "enforce Swarm SDK contracts at compile time",
	Flags:    flag.FlagSet{},
	Run:      run,
	Requires: []*analysis.Analyzer{}, // No dependencies
}

// ContractViolationPrefix is the standard prefix for all contract violation messages.
const ContractViolationPrefix = "CONTRACT VIOLATION: "

// Violation types and their messages.
var (
	// OrderedBlocks violations
	ErrOrderedBlocksDirectAccess = ContractViolationPrefix +
		"Use GetOrderedBlocks() instead of direct .OrderedBlocks access. " +
		"Direct access may return blocks out of sequence order. " +
		"See: https://swarmcode.ai/docs/sdk/contracts#ordered-blocks"

	// Message construction violations
	ErrMessageDirectConstruction = ContractViolationPrefix +
		"Use NewMessage() factory or MessageBuilder instead of direct Message{} construction. " +
		"Direct construction bypasses validation and ordering guarantees. " +
		"See: https://swarmcode.ai/docs/sdk/contracts#message-construction"

	// JSON-RPC client violations
	ErrCustomWebSocketClient = ContractViolationPrefix +
		"Use official swarm-sdk/client instead of custom WebSocket JSON-RPC implementation. " +
		"Custom clients may not handle reconnection, auth, and error cases correctly. " +
		"See: https://swarmcode.ai/docs/sdk/contracts#jsonrpc-client"

	// Documentation violations
	ErrMissingContractDoc = ContractViolationPrefix +
		"Public API missing contract documentation. " +
		"Add a comment explaining the contract invariant. " +
		"See: https://swarmcode.ai/docs/sdk/contracts#documentation"

	// Settings API violations
	ErrDirectSwarmOSConfigAccess = ContractViolationPrefix +
		"Use sdkclient.CoreConfig with ToCoreConfig()/ApplyCoreConfig() instead of direct SwarmOSConfig field access. " +
		"Direct access bypasses SDK validation and defaults. " +
		"See: https://swarmcode.ai/docs/sdk/contracts#settings-api"

	ErrDirectCoreConfigMutation = ContractViolationPrefix +
		"Use SettingsManager.Save() instead of direct CoreConfig field mutation. " +
		"Direct mutation bypasses persistence and validation. " +
		"See: https://swarmcode.ai/docs/sdk/contracts#settings-api"
)

// Config holds linter configuration.
type Config struct {
	// CheckOrderedBlocks enables checking for direct OrderedBlocks access.
	CheckOrderedBlocks bool

	// CheckMessageConstruction enables checking for direct Message{} literals.
	CheckMessageConstruction bool

	// CheckCustomClient enables checking for custom JSON-RPC client implementations.
	CheckCustomClient bool

	// CheckDocumentation enables checking for contract documentation on public APIs.
	CheckDocumentation bool

	// CheckSettingsAPI enables checking for proper SDK settings API usage.
	// Enforces using CoreConfig instead of direct SwarmOSConfig access.
	CheckSettingsAPI bool

	// Allowlist for packages that can use internal APIs (tests, internal packages).
	AllowedPackages []string
}

// DefaultConfig returns the default linter configuration.
func DefaultConfig() *Config {
	return &Config{
		CheckOrderedBlocks:       true,
		CheckMessageConstruction: true,
		CheckCustomClient:        true,
		CheckDocumentation:       false, // Opt-in for now
		CheckSettingsAPI:         true,  // Enforce SDK settings usage
		AllowedPackages:          []string{"_test", "internal/", "lint/"},
	}
}

func init() {
	// Add flags for configuration
	Analyzer.Flags.BoolVar(&DefaultConfig().CheckOrderedBlocks, "ordered-blocks", true, "check for direct OrderedBlocks access")
	Analyzer.Flags.BoolVar(&DefaultConfig().CheckMessageConstruction, "message-construction", true, "check for direct Message{} construction")
	Analyzer.Flags.BoolVar(&DefaultConfig().CheckCustomClient, "custom-client", true, "check for custom JSON-RPC client usage")
	Analyzer.Flags.BoolVar(&DefaultConfig().CheckDocumentation, "documentation", false, "check for contract documentation")
	Analyzer.Flags.BoolVar(&DefaultConfig().CheckSettingsAPI, "settings-api", true, "check for proper SDK settings API usage (enforces CoreConfig)")
}

func run(pass *analysis.Pass) (any, error) {
	cfg := DefaultConfig()

	// Skip internal SDK packages - they implement the contracts
	if isInternalSDKPackage(pass.Pkg.Path()) {
		return nil, nil
	}

	// Skip allowed packages (tests, internal)
	if isAllowedPackage(pass.Pkg.Path(), cfg.AllowedPackages) {
		return nil, nil
	}

	for _, file := range pass.Files {
		// Skip generated files
		if isGeneratedFile(file) {
			continue
		}

		// Skip test files - they need to set up test data
		if isTestFile(pass, file) {
			continue
		}

		// Run all detectors
		ast.Inspect(file, func(n ast.Node) bool {
			if cfg.CheckOrderedBlocks {
				detectOrderedBlocksAccess(pass, n)
			}
			if cfg.CheckMessageConstruction {
				detectDirectMessageConstruction(pass, n)
			}
			if cfg.CheckCustomClient {
				detectCustomClientImplementation(pass, n, file)
			}
			if cfg.CheckDocumentation {
				detectMissingContractDocumentation(pass, n)
			}
			if cfg.CheckSettingsAPI {
				detectDirectSwarmOSConfigAccess(pass, n)
			}
			return true
		})
	}

	return nil, nil
}

// =============================================================================
// Detector: OrderedBlocks Direct Access
// =============================================================================

// detectOrderedBlocksAccess detects direct access to .OrderedBlocks field.
// Contract: Agents MUST use GetOrderedBlocks() to ensure correct ordering.
func detectOrderedBlocksAccess(pass *analysis.Pass, n ast.Node) {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return
	}

	// Check if accessing .OrderedBlocks
	if sel.Sel.Name != "OrderedBlocks" {
		return
	}

	// Check if this is a method call (e.g., GetOrderedBlocks())
	// We want field access, not method calls
	if isMethodCallContext(sel) {
		return
	}

	// Check if the type is conversation.Message or *conversation.Message
	if !isMessageType(pass, sel.X) {
		return
	}

	// Allow assignment to OrderedBlocks (for construction/serialization)
	// We only want to catch READ access
	if isAssignmentToField(pass, sel) {
		return
	}

	pass.Reportf(sel.Sel.Pos(), "%s", ErrOrderedBlocksDirectAccess)
}

// isMethodCallContext checks if this selector is part of a method call.
func isMethodCallContext(sel *ast.SelectorExpr) bool {
	// If parent is a CallExpr with this as the function, it's a method call
	// This function is called from ast.Inspect, so we can't check parent directly
	// Instead, we check if the selector name starts with "Get"
	return strings.HasPrefix(sel.Sel.Name, "Get")
}

// isMessageType checks if the expression is of type conversation.Message.
func isMessageType(pass *analysis.Pass, expr ast.Expr) bool {
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok {
		return false
	}

	// Check for conversation.Message and *conversation.Message
	typeStr := tv.Type.String()
	return strings.HasSuffix(typeStr, "conversation.Message") ||
		strings.HasSuffix(typeStr, "*conversation.Message")
}

// isAssignmentToField checks if this selector is the target (LHS) of an
// assignment, which is allowed for construction/serialization (e.g.
// `msg.OrderedBlocks = ...`). Only READ access to OrderedBlocks is a violation.
func isAssignmentToField(pass *analysis.Pass, sel *ast.SelectorExpr) bool {
	for _, file := range pass.Files {
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if found {
				return false
			}
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				if lhs == sel {
					found = true
					return false
				}
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// =============================================================================
// Detector: Direct Message Construction
// =============================================================================

// detectDirectMessageConstruction detects direct construction of Message{} structs.
// Contract: Agents MUST use factory methods for Message construction.
func detectDirectMessageConstruction(pass *analysis.Pass, n ast.Node) {
	composite, ok := n.(*ast.CompositeLit)
	if !ok {
		return
	}

	// Check if this is a Message type
	typeName := getTypeName(pass, composite.Type)
	if typeName == "" {
		return
	}

	// Check for conversation.Message construction
	if !isConversationMessage(typeName) {
		return
	}

	// Allow construction in specific contexts:
	// - Inside factory methods (NewMessage, MessageBuilder.Build)
	// - Inside Clone() method
	// - Inside JSON unmarshaling
	if isAllowedConstructionContext(pass, composite) {
		return
	}

	// Check if OrderedBlocks is being set directly in the literal
	hasOrderedBlocks := false
	for _, elt := range composite.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == "OrderedBlocks" {
			hasOrderedBlocks = true
			break
		}
	}

	if hasOrderedBlocks {
		pass.Reportf(composite.Pos(), "%s", ErrMessageDirectConstruction)
	}
}

// getTypeName extracts the type name from an expression.
func getTypeName(pass *analysis.Pass, expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return getTypeName(pass, t.X)
	default:
		return ""
	}
}

// isConversationMessage checks if the type name is Message from conversation package.
func isConversationMessage(typeName string) bool {
	return typeName == "Message" || typeName == "conversation.Message"
}

// isAllowedConstructionContext checks if we're inside an allowed construction context.
func isAllowedConstructionContext(pass *analysis.Pass, composite *ast.CompositeLit) bool {
	// Check if we're inside a function that's allowed to construct Messages
	// This would require walking up the AST, which is complex in ast.Inspect
	// For now, we check file-level patterns

	// Allow in files that define factory methods
	// This is a simplified check
	return false
}

// =============================================================================
// Detector: Custom JSON-RPC Client
// =============================================================================

// detectCustomClientImplementation detects usage of custom JSON-RPC implementations.
// Contract: Agents MUST use the official swarm-sdk/client for JSON-RPC communication.
func detectCustomClientImplementation(pass *analysis.Pass, n ast.Node, file *ast.File) {
	// Check imports for suspicious WebSocket/JSON-RPC packages
	importSpec, ok := n.(*ast.ImportSpec)
	if !ok {
		return
	}

	importPath := strings.Trim(importSpec.Path.Value, `"\"`)

	// Detect WebSocket imports outside of official client
	if isWebSocketImport(importPath) {
		// Check if this file is in the official client package
		if !isOfficialClientPackage(pass.Pkg.Path()) {
			pass.Reportf(importSpec.Pos(),
				"%s\n  Import: %s\n  Recommendation: Use swarm-sdk/client for JSON-RPC communication",
				ErrCustomWebSocketClient, importPath)
		}
	}

	// Detect gorilla/websocket direct usage for JSON-RPC
	if strings.Contains(importPath, "gorilla/websocket") {
		// Check if there's also a JSON-RPC-like usage pattern
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Check for websocket.Dial or similar
			if isWebSocketDial(call) && !isOfficialClientPackage(pass.Pkg.Path()) {
				pass.Reportf(call.Pos(),
					"%s\n  Detected: WebSocket dial for potential JSON-RPC usage\n  Recommendation: Use client.New() instead",
					ErrCustomWebSocketClient)
			}
			return true
		})
	}
}

// isWebSocketImport checks if an import path is a WebSocket library.
func isWebSocketImport(importPath string) bool {
	websocketImports := []string{
		"github.com/gorilla/websocket",
		"golang.org/x/net/websocket",
		"github.com/gobwas/ws",
		"nhooyr.io/websocket",
	}

	for _, wp := range websocketImports {
		if strings.HasPrefix(importPath, wp) {
			return true
		}
	}
	return false
}

// isWebSocketDial checks if a call expression is a WebSocket dial.
func isWebSocketDial(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	dialMethods := []string{"Dial", "DialContext", "NewClient"}
	return slices.Contains(dialMethods, sel.Sel.Name)
}

// isOfficialClientPackage checks if the package is the official client package.
func isOfficialClientPackage(pkgPath string) bool {
	return strings.HasSuffix(pkgPath, "/client") ||
		strings.Contains(pkgPath, "/internal/") ||
		strings.Contains(pkgPath, "swarm-sdk/client") ||
		strings.Contains(pkgPath, "swarm-sdk/ws")
}

// =============================================================================
// Detector: Missing Contract Documentation
// =============================================================================

// detectMissingContractDocumentation checks for missing contract documentation.
// Contract: Public APIs MUST document their invariants.
func detectMissingContractDocumentation(pass *analysis.Pass, n ast.Node) {
	// Check function declarations
	fn, ok := n.(*ast.FuncDecl)
	if !ok {
		return
	}

	// Only check exported functions
	if !fn.Name.IsExported() {
		return
	}

	// Skip test functions and main
	if strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "main" {
		return
	}

	// Check if function has contract-related keywords in documentation
	if hasContractDocumentation(fn.Doc) {
		return
	}

	// Check if this is a contract-relevant function
	// (accesses Message, uses client, etc.)
	if !isContractRelevantFunction(pass, fn) {
		return
	}

	pass.Reportf(fn.Name.Pos(),
		"%s\n  Function: %s\n  Add documentation explaining the contract invariant",
		ErrMissingContractDoc, fn.Name.Name)
}

// hasContractDocumentation checks if a function has contract documentation.
func hasContractDocumentation(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}

	contractKeywords := []string{
		"CONTRACT:",
		"Contract:",
		"contract:",
		"MUST:",
		"REQUIRED:",
		"INVARIANT:",
	}

	for _, comment := range doc.List {
		for _, keyword := range contractKeywords {
			if strings.Contains(comment.Text, keyword) {
				return true
			}
		}
	}
	return false
}

// isContractRelevantFunction checks if a function is contract-relevant.
func isContractRelevantFunction(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	// Check if function signature involves Message or Client types
	if fn.Type.Params == nil {
		return false
	}

	for _, param := range fn.Type.Params.List {
		typeStr := getTypeString(pass, param.Type)
		if strings.Contains(typeStr, "Message") ||
			strings.Contains(typeStr, "Client") {
			return true
		}
	}

	return false
}

// getTypeString gets the string representation of a type.
func getTypeString(pass *analysis.Pass, expr ast.Expr) string {
	if tv, ok := pass.TypesInfo.Types[expr]; ok {
		return tv.Type.String()
	}
	return ""
}

// =============================================================================
// Helper Functions
// =============================================================================

// isInternalSDKPackage returns true for packages that implement SDK internals.
// These packages are allowed to access OrderedBlocks directly and construct Messages.
func isInternalSDKPackage(pkgPath string) bool {
	internalPackages := []string{
		"swarm-sdk/conversation",
		"swarm-sdk/client",
		"swarm-sdk/internal",
		"swarm-sdk/ws",
	}
	for _, p := range internalPackages {
		if strings.Contains(pkgPath, p) {
			return true
		}
	}
	return false
}

// isTestFile checks if a file is a test file (ending in _test.go).
// Test files are exempt from Message construction checks since they need to set up test data.
func isTestFile(pass *analysis.Pass, file *ast.File) bool {
	// Get the file position
	pos := pass.Fset.Position(file.Pos())
	filename := pos.Filename
	return strings.HasSuffix(filename, "_test.go")
}

// isAllowedPackage checks if a package path matches any allowed pattern.
func isAllowedPackage(pkgPath string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(pkgPath, pattern) {
			return true
		}
	}
	return false
}

// isGeneratedFile checks if a file is generated.
func isGeneratedFile(file *ast.File) bool {
	if file.Doc == nil {
		return false
	}

	for _, comment := range file.Doc.List {
		if strings.Contains(comment.Text, "Code generated") ||
			strings.Contains(comment.Text, "DO NOT EDIT") {
			return true
		}
	}
	return false
}

// =============================================================================
// Additional Analyzers (can be used independently)
// =============================================================================

// OrderedBlocksAnalyzer is a focused analyzer for just OrderedBlocks violations.
var OrderedBlocksAnalyzer = &analysis.Analyzer{
	Name:     "orderedblocks",
	Doc:      "check for direct OrderedBlocks access",
	Requires: []*analysis.Analyzer{},
	Run: func(pass *analysis.Pass) (any, error) {
		for _, file := range pass.Files {
			if isGeneratedFile(file) {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				detectOrderedBlocksAccess(pass, n)
				return true
			})
		}
		return nil, nil
	},
}

// MessageConstructionAnalyzer is a focused analyzer for Message construction violations.
var MessageConstructionAnalyzer = &analysis.Analyzer{
	Name:     "messageconstruction",
	Doc:      "check for direct Message{} construction",
	Requires: []*analysis.Analyzer{},
	Run: func(pass *analysis.Pass) (any, error) {
		for _, file := range pass.Files {
			if isGeneratedFile(file) {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				detectDirectMessageConstruction(pass, n)
				return true
			})
		}
		return nil, nil
	},
}

// =============================================================================
// Suggested Fixes (for golang.org/x/tools/go/analysis/edit)
// =============================================================================

// exprToString converts an AST expression to a string.
func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprToString(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.ParenExpr:
		return "(" + exprToString(e.X) + ")"
	default:
		return ""
	}
}
