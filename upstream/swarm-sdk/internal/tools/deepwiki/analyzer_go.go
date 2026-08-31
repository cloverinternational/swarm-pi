package deepwiki

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// GoParser uses the Go standard library's AST parser for real structural
// understanding of Go source files. This is NOT regex — it parses the actual
// syntax tree and extracts functions, methods, types, interfaces, imports,
// and their relationships.
type GoParser struct{}

func (p *GoParser) ParseFile(info FileInfo, source []byte) ([]*CodeEntity, []CodeEdge, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, info.Path, source, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse Go file: %w", err)
	}

	pkgName := file.Name.Name
	var entities []*CodeEntity
	var edges []CodeEdge

	// Extract imports
	var imports []string
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		imports = append(imports, path)
	}

	// Create file-level entity
	fileEntity := &CodeEntity{
		QualifiedName: pkgName + "/" + info.RelPath,
		Kind:          KindFile,
		Name:          info.RelPath,
		Package:       pkgName,
		FilePath:      info.RelPath,
		StartLine:     1,
		EndLine:       fset.Position(file.End()).Line,
		Imports:       imports,
	}
	entities = append(entities, fileEntity)

	// Walk the AST
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			ent, funcEdges := p.parseFunc(fset, d, pkgName, info.RelPath, source)
			if ent != nil {
				entities = append(entities, ent)
				edges = append(edges, funcEdges...)
				// file contains this entity
				edges = append(edges, CodeEdge{
					From: fileEntity.QualifiedName, To: ent.QualifiedName,
					Kind: EdgeContains, FilePath: info.RelPath,
				})
			}

		case *ast.GenDecl:
			ents, declEdges := p.parseGenDecl(fset, d, pkgName, info.RelPath, source)
			for _, ent := range ents {
				entities = append(entities, ent)
				edges = append(edges, declEdges...)
				edges = append(edges, CodeEdge{
					From: fileEntity.QualifiedName, To: ent.QualifiedName,
					Kind: EdgeContains, FilePath: info.RelPath,
				})
			}
		}
	}

	return entities, edges, nil
}

func (p *GoParser) parseFunc(fset *token.FileSet, fn *ast.FuncDecl, pkg, relPath string, source []byte) (*CodeEntity, []CodeEdge) {
	name := fn.Name.Name
	var kind EntityKind
	var receiver string
	var qname string

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		// Method
		kind = KindMethod
		receiver = exprString(fn.Recv.List[0].Type)
		qname = fmt.Sprintf("%s.%s.%s", pkg, receiver, name)
	} else {
		// Function
		kind = KindFunction
		qname = fmt.Sprintf("%s.%s", pkg, name)
	}

	startPos := fset.Position(fn.Pos())
	endPos := fset.Position(fn.End())

	// Extract signature (first line)
	sig := extractLines(source, startPos.Line, startPos.Line)

	// Extract doc comment
	doc := ""
	if fn.Doc != nil {
		doc = fn.Doc.Text()
	}

	// Extract body
	body := extractLines(source, startPos.Line, endPos.Line)

	ent := &CodeEntity{
		QualifiedName: qname,
		Kind:          kind,
		Name:          name,
		Package:       pkg,
		FilePath:      relPath,
		StartLine:     startPos.Line,
		EndLine:       endPos.Line,
		Signature:     strings.TrimSpace(sig),
		DocComment:    strings.TrimSpace(doc),
		Body:          body,
		Receiver:      receiver,
	}

	// Extract edges from function body
	var edges []CodeEdge

	// Method → receiver type
	if receiver != "" {
		edges = append(edges, CodeEdge{
			From: qname, To: pkg + "." + receiver,
			Kind: EdgeReceives, FilePath: relPath,
		})
	}

	// Extract function calls and type references from the body
	if fn.Body != nil {
		calls, refs := p.extractBodyRefs(fn.Body, pkg)
		ent.Calls = calls
		ent.References = refs

		for _, call := range calls {
			edges = append(edges, CodeEdge{
				From: qname, To: call, Kind: EdgeCalls, FilePath: relPath,
			})
		}
		for _, ref := range refs {
			edges = append(edges, CodeEdge{
				From: qname, To: ref, Kind: EdgeReferences, FilePath: relPath,
			})
		}
	}

	// Extract return types
	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			typeName := exprString(field.Type)
			if typeName != "" && !isBuiltinType(typeName) {
				edges = append(edges, CodeEdge{
					From: qname, To: pkg + "." + typeName,
					Kind: EdgeReturns, FilePath: relPath,
				})
			}
		}
	}

	return ent, edges
}

func (p *GoParser) parseGenDecl(fset *token.FileSet, decl *ast.GenDecl, pkg, relPath string, source []byte) ([]*CodeEntity, []CodeEdge) {
	var entities []*CodeEntity
	var edges []CodeEdge

	doc := ""
	if decl.Doc != nil {
		doc = decl.Doc.Text()
	}

	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			ent, typeEdges := p.parseTypeSpec(fset, s, doc, pkg, relPath, source)
			if ent != nil {
				entities = append(entities, ent)
				edges = append(edges, typeEdges...)
			}

		case *ast.ValueSpec:
			// Constants and variables
			for _, name := range s.Names {
				if name.Name == "_" {
					continue
				}
				kind := KindVar
				if decl.Tok == token.CONST {
					kind = KindConst
				}
				startPos := fset.Position(s.Pos())
				endPos := fset.Position(s.End())

				entities = append(entities, &CodeEntity{
					QualifiedName: pkg + "." + name.Name,
					Kind:          kind,
					Name:          name.Name,
					Package:       pkg,
					FilePath:      relPath,
					StartLine:     startPos.Line,
					EndLine:       endPos.Line,
					DocComment:    strings.TrimSpace(doc),
					Body:          extractLines(source, startPos.Line, endPos.Line),
				})
			}
		}
	}

	return entities, edges
}

func (p *GoParser) parseTypeSpec(fset *token.FileSet, ts *ast.TypeSpec, doc, pkg, relPath string, source []byte) (*CodeEntity, []CodeEdge) {
	name := ts.Name.Name
	qname := pkg + "." + name

	startPos := fset.Position(ts.Pos())
	endPos := fset.Position(ts.End())

	var kind EntityKind
	var edges []CodeEdge

	switch t := ts.Type.(type) {
	case *ast.StructType:
		kind = KindStruct
		// Extract embedded types and field type references
		if t.Fields != nil {
			for _, field := range t.Fields.List {
				typeName := exprString(field.Type)
				if len(field.Names) == 0 {
					// Embedded type
					edges = append(edges, CodeEdge{
						From: qname, To: pkg + "." + typeName,
						Kind: EdgeEmbeds, FilePath: relPath,
					})
				} else if !isBuiltinType(typeName) {
					edges = append(edges, CodeEdge{
						From: qname, To: pkg + "." + typeName,
						Kind: EdgeReferences, FilePath: relPath,
					})
				}
			}
		}

	case *ast.InterfaceType:
		kind = KindInterface
		// Extract embedded interfaces
		if t.Methods != nil {
			for _, method := range t.Methods.List {
				if len(method.Names) == 0 {
					// Embedded interface
					typeName := exprString(method.Type)
					edges = append(edges, CodeEdge{
						From: qname, To: pkg + "." + typeName,
						Kind: EdgeEmbeds, FilePath: relPath,
					})
				}
			}
		}

	default:
		kind = KindType
	}

	// Use spec-level doc if no decl-level doc
	if doc == "" && ts.Doc != nil {
		doc = ts.Doc.Text()
	}

	ent := &CodeEntity{
		QualifiedName: qname,
		Kind:          kind,
		Name:          name,
		Package:       pkg,
		FilePath:      relPath,
		StartLine:     startPos.Line,
		EndLine:       endPos.Line,
		Signature:     extractLines(source, startPos.Line, startPos.Line),
		DocComment:    strings.TrimSpace(doc),
		Body:          extractLines(source, startPos.Line, endPos.Line),
	}

	return ent, edges
}

// extractBodyRefs walks a function body and extracts call targets and type references.
func (p *GoParser) extractBodyRefs(body *ast.BlockStmt, pkg string) (calls, refs []string) {
	callSet := make(map[string]bool)
	refSet := make(map[string]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			switch fn := x.Fun.(type) {
			case *ast.Ident:
				// Local function call
				name := fn.Name
				if !isBuiltinFunc(name) {
					callSet[pkg+"."+name] = true
				}
			case *ast.SelectorExpr:
				// pkg.Func or receiver.Method
				if ident, ok := fn.X.(*ast.Ident); ok {
					callSet[ident.Name+"."+fn.Sel.Name] = true
				}
			}
		case *ast.Ident:
			// Type reference (heuristic: uppercase = exported type)
			if x.Name != "" && x.Name[0] >= 'A' && x.Name[0] <= 'Z' && !isBuiltinType(x.Name) {
				refSet[pkg+"."+x.Name] = true
			}
		}
		return true
	})

	for c := range callSet {
		calls = append(calls, c)
	}
	for r := range refSet {
		refs = append(refs, r)
	}
	return calls, refs
}

// --- helpers ---

func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return exprString(e.X)
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(e.Elt)
	case *ast.MapType:
		return "map[" + exprString(e.Key) + "]" + exprString(e.Value)
	case *ast.InterfaceType:
		return "any"
	case *ast.Ellipsis:
		if e.Elt != nil {
			return "..." + exprString(e.Elt)
		}
		return "..."
	case *ast.IndexExpr:
		return exprString(e.X)
	default:
		return ""
	}
}

func extractLines(source []byte, startLine, endLine int) string {
	lines := strings.Split(string(source), "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > len(lines) {
		return ""
	}
	selected := lines[startLine-1 : endLine]
	return strings.Join(selected, "\n")
}

var builtinTypes = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "any": true,
}

func isBuiltinType(name string) bool { return builtinTypes[name] }

var builtinFuncs = map[string]bool{
	"append": true, "cap": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true,
	"make": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true, "min": true, "max": true,
	"clear": true,
}

func isBuiltinFunc(name string) bool { return builtinFuncs[name] }
