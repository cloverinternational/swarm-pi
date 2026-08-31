// Package lint implements the Settings API detector for swarmlint.
package lint

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// ==============================================================================
// Detector: Settings API Enforcement
// ==============================================================================

// detectDirectSwarmOSConfigAccess detects direct field access on SwarmOSConfig.
// Contract: Downstream code MUST use CoreConfig with ToCoreConfig()/ApplyCoreConfig().
func detectDirectSwarmOSConfigAccess(pass *analysis.Pass, n ast.Node) {
	// Check for direct SwarmOSConfig field access
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return
	}

	// Check if this is accessing a SwarmOSConfig field directly
	if !isSwarmOSConfigType(pass, sel.X) {
		return
	}

	// Skip if this is accessing CoreConfig (the embedded field)
	if sel.Sel.Name == "CoreConfig" {
		return
	}

	// Allow GetXxx/SetXxx method calls
	if isGetterOrSetter(sel.Sel.Name) {
		return
	}

	// Skip ToCoreConfig/ApplyCoreConfig bridge methods
	if sel.Sel.Name == "ToCoreConfig" || sel.Sel.Name == "ApplyCoreConfig" {
		return
	}

	// Report violation
	pass.Reportf(sel.Sel.Pos(), "%s", ErrDirectSwarmOSConfigAccess)
}

// isSwarmOSConfigType checks if an expression is of type SwarmOSConfig or *SwarmOSConfig.
func isSwarmOSConfigType(pass *analysis.Pass, expr ast.Expr) bool {
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok {
		return false
	}

	typeStr := tv.Type.String()
	return strings.Contains(typeStr, "SwarmOSConfig")
}

// isGetterOrSetter checks if a method name is a getter or setter.
func isGetterOrSetter(name string) bool {
	return strings.HasPrefix(name, "Get") || strings.HasPrefix(name, "Set")
}
