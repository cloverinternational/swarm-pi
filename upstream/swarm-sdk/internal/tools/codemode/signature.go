// Package codemode provides code-mode execution for the swarm-sdk.
package codemode

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// SignatureRenderer converts JSON schemas to TypeScript/JSDoc-style function signatures.
// These signatures are embedded in the run_code tool's description so the model
// knows what functions are available inside the sandbox.
type SignatureRenderer struct {
	// indent is the indentation string for nested structures.
	indent string
}

// NewSignatureRenderer creates a new signature renderer.
func NewSignatureRenderer() *SignatureRenderer {
	return &SignatureRenderer{indent: "    "}
}

// Render generates the JSDoc-style description for all tool functions.
// The output is a markdown code block containing TypeScript-style signatures.
func (r *SignatureRenderer) Render(stubs []ToolStubInfo) string {
	var b strings.Builder

	b.WriteString("```typescript\n")

	// First, render any type definitions needed for complex types
	typeDefs := r.collectTypeDefs(stubs)
	for _, td := range typeDefs {
		b.WriteString(td)
		b.WriteString("\n\n")
	}

	// Render function signatures
	for _, stub := range stubs {
		sig := r.renderSignature(stub)
		b.WriteString(sig)
		b.WriteString("\n")
	}

	b.WriteString("```\n")

	return b.String()
}

// ToolStubInfo contains the information needed to render a tool signature.
type ToolStubInfo struct {
	// Name is the sanitized JavaScript-safe identifier.
	Name string

	// OriginalName is the actual tool name (may differ if sanitized).
	OriginalName string

	// Description is the tool's description for JSDoc.
	Description string

	// IsAsync indicates whether the tool should be called with await.
	IsAsync bool

	// Parameters is the JSON schema for the tool's parameters.
	Parameters map[string]any

	// Returns is the JSON schema for the tool's return value (may be nil).
	Returns map[string]any
}

// invalidIdentChar matches characters that aren't valid in JavaScript identifiers.
var invalidIdentChar = regexp.MustCompile(`[^a-zA-Z0-9_$]`)

// SanitizeName converts a tool name to a valid JavaScript identifier.
// Hyphens, dots, and other invalid characters are replaced with underscores.
// If the result starts with a digit, it's prefixed with underscore.
// If it's a JavaScript reserved word, it's suffixed with underscore.
func SanitizeName(name string) string {
	sanitized := invalidIdentChar.ReplaceAllString(name, "_")

	// Prefix with underscore if starts with digit
	if len(sanitized) > 0 && sanitized[0] >= '0' && sanitized[0] <= '9' {
		sanitized = "_" + sanitized
	}

	// Check for reserved words
	if isReservedWord(sanitized) {
		sanitized += "_"
	}

	return sanitized
}

// isReservedWord checks if a word is a JavaScript reserved keyword.
func isReservedWord(word string) bool {
	reserved := map[string]bool{
		"break": true, "case": true, "catch": true, "continue": true,
		"debugger": true, "default": true, "delete": true, "do": true,
		"else": true, "finally": true, "for": true, "function": true,
		"if": true, "in": true, "instanceof": true, "new": true,
		"return": true, "switch": true, "this": true, "throw": true,
		"try": true, "typeof": true, "var": true, "void": true,
		"while": true, "with": true, "class": true, "const": true,
		"enum": true, "export": true, "extends": true, "import": true,
		"super": true, "implements": true, "interface": true, "let": true,
		"package": true, "private": true, "protected": true, "public": true,
		"static": true, "yield": true, "await": true, "null": true,
		"true": true, "false": true, "undefined": true, "NaN": true,
		"Infinity": true, "eval": true, "arguments": true,
		// Our reserved name
		"run_code": true,
	}
	return reserved[word]
}

// renderSignature generates a JSDoc-style function signature.
func (r *SignatureRenderer) renderSignature(stub ToolStubInfo) string {
	var b strings.Builder

	// JSDoc comment
	b.WriteString("/**\n")
	if stub.Description != "" {
		lines := strings.SplitSeq(stub.Description, "\n")
		for line := range lines {
			b.WriteString(" * ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString(" */\n")

	// Function signature
	if stub.IsAsync {
		b.WriteString("async ")
	}
	b.WriteString("function ")
	b.WriteString(stub.Name)
	b.WriteString("(")

	// Render parameters
	params := r.renderParams(stub.Parameters)
	b.WriteString(params)

	b.WriteString("): ")
	b.WriteString(r.renderReturnType(stub.IsAsync, stub.Returns))

	return b.String()
}

// renderParams generates the parameter list for a function signature.
func (r *SignatureRenderer) renderParams(schema map[string]any) string {
	if schema == nil {
		return ""
	}

	props, hasProps := schema["properties"].(map[string]any)
	if !hasProps || len(props) == 0 {
		return ""
	}

	// Get required fields
	required := make(map[string]bool)
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	// Sort parameter names for consistent output
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	var parts []string
	for _, name := range names {
		prop := props[name]
		typ := r.renderType(prop)

		// Add optional marker if not required
		if !required[name] {
			parts = append(parts, fmt.Sprintf("%s?: %s", name, typ))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", name, typ))
		}
	}

	return strings.Join(parts, ", ")
}

// renderType converts a JSON schema type to a TypeScript type.
func (r *SignatureRenderer) renderType(schema any) string {
	if schema == nil {
		return "any"
	}

	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return "any"
	}

	// Handle $ref
	if ref, ok := schemaMap["$ref"].(string); ok {
		// Extract type name from ref (e.g., "#/definitions/Foo" -> "Foo")
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}

	// Handle oneOf / anyOf
	if oneOf, ok := schemaMap["oneOf"].([]any); ok {
		return r.renderUnionType(oneOf)
	}
	if anyOf, ok := schemaMap["anyOf"].([]any); ok {
		return r.renderUnionType(anyOf)
	}

	// Handle enum
	if enum, ok := schemaMap["enum"].([]any); ok {
		return r.renderEnumType(enum)
	}

	// Get type
	typ, hasType := schemaMap["type"].(string)
	if !hasType {
		// Try type as array
		if typeArr, ok := schemaMap["type"].([]any); ok && len(typeArr) > 0 {
			if first, ok := typeArr[0].(string); ok {
				typ = first
			}
		}
	}

	switch typ {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		items := schemaMap["items"]
		itemType := r.renderType(items)
		return fmt.Sprintf("%s[]", itemType)
	case "object":
		// Check for additionalProperties (record type)
		if addProps, ok := schemaMap["additionalProperties"].(map[string]any); ok {
			valueType := r.renderType(addProps)
			return fmt.Sprintf("Record<string, %s>", valueType)
		}
		// Check for inline properties
		if props, ok := schemaMap["properties"].(map[string]any); ok && len(props) > 0 {
			return r.renderInlineObject(props)
		}
		return "object"
	case "null":
		return "null"
	default:
		return "any"
	}
}

// renderUnionType creates a union type from oneOf/anyOf schemas.
func (r *SignatureRenderer) renderUnionType(schemas []any) string {
	var types []string
	for _, s := range schemas {
		types = append(types, r.renderType(s))
	}
	return strings.Join(types, " | ")
}

// renderEnumType creates a literal union type from enum values.
func (r *SignatureRenderer) renderEnumType(values []any) string {
	var literals []string
	for _, v := range values {
		switch val := v.(type) {
		case string:
			literals = append(literals, fmt.Sprintf(`"%s"`, val))
		case float64, int:
			literals = append(literals, fmt.Sprintf("%v", val))
		case bool:
			literals = append(literals, fmt.Sprintf("%v", val))
		}
	}
	return strings.Join(literals, " | ")
}

// renderInlineObject creates an inline object type.
func (r *SignatureRenderer) renderInlineObject(props map[string]any) string {
	var b strings.Builder
	b.WriteString("{ ")

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(r.renderType(props[name]))
	}

	b.WriteString(" }")
	return b.String()
}

// renderReturnType generates the return type for a function signature.
func (r *SignatureRenderer) renderReturnType(isAsync bool, schema map[string]any) string {
	typ := r.renderType(schema)
	if typ == "" || typ == "any" {
		if isAsync {
			return "Promise<any>"
		}
		return "any"
	}

	if isAsync {
		return fmt.Sprintf("Promise<%s>", typ)
	}
	return typ
}

// collectTypeDefs extracts type definitions needed for complex schemas.
func (r *SignatureRenderer) collectTypeDefs(stubs []ToolStubInfo) []string {
	var defs []string
	seen := make(map[string]bool)

	for _, stub := range stubs {
		// Check parameters for complex types
		r.extractTypeDefs(stub.Parameters, &defs, seen)
		// Check return type for complex types
		r.extractTypeDefs(stub.Returns, &defs, seen)
	}

	return defs
}

// extractTypeDefs recursively extracts type definitions from a schema.
func (r *SignatureRenderer) extractTypeDefs(schema any, defs *[]string, seen map[string]bool) {
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return
	}

	// Check for definitions
	if definitions, ok := schemaMap["definitions"].(map[string]any); ok {
		for name, def := range definitions {
			if seen[name] {
				continue
			}
			seen[name] = true
			typeDef := r.renderTypeDef(name, def)
			*defs = append(*defs, typeDef)
		}
	}

	// Recurse into properties
	if props, ok := schemaMap["properties"].(map[string]any); ok {
		for _, prop := range props {
			r.extractTypeDefs(prop, defs, seen)
		}
	}

	// Recurse into items
	if items, ok := schemaMap["items"]; ok {
		r.extractTypeDefs(items, defs, seen)
	}

	// Recurse into additionalProperties
	if addProps, ok := schemaMap["additionalProperties"]; ok {
		r.extractTypeDefs(addProps, defs, seen)
	}
}

// renderTypeDef generates a TypeScript type definition.
func (r *SignatureRenderer) renderTypeDef(name string, schema any) string {
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return fmt.Sprintf("type %s = any;", name)
	}

	typ := r.renderType(schemaMap)
	return fmt.Sprintf("type %s = %s;", name, typ)
}

// RenderDescription generates the complete description for the run_code tool.
func (r *SignatureRenderer) RenderDescription(stubs []ToolStubInfo, baseDescription string) string {
	var b strings.Builder

	b.WriteString(baseDescription)
	b.WriteString("\n\n")

	if len(stubs) == 0 {
		return b.String()
	}

	// Describe calling convention
	hasAsync := false
	hasSync := false
	for _, s := range stubs {
		if s.IsAsync {
			hasAsync = true
		} else {
			hasSync = true
		}
	}

	b.WriteString("The following functions are available inside the sandbox. ")
	b.WriteString("Call them directly (do **not** redefine or import them). ")
	b.WriteString("All parameters are keyword-only.\n\n")

	if hasAsync && !hasSync {
		b.WriteString("All tool functions are async: invoke them with `await`, ")
		b.WriteString("e.g. `const result = await tool_name({arg: value})`. ")
		b.WriteString("Calling without `await` returns an unresolved Promise, not the value.\n\n")
	} else if hasSync && !hasAsync {
		b.WriteString("All tool functions are synchronous: call them directly, ")
		b.WriteString("e.g. `const result = tool_name({arg: value})`.\n\n")
	} else {
		b.WriteString("Async functions (`async function`) must be invoked with `await`, ")
		b.WriteString("e.g. `const result = await tool_name({arg: value})`. ")
		b.WriteString("Sync functions (`function`) are called directly, ")
		b.WriteString("e.g. `const result = tool_name({arg: value})`.\n\n")
	}

	// Render signatures
	b.WriteString(r.Render(stubs))

	return b.String()
}
