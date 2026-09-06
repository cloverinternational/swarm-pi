package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestLiteralMessageIDsAreRegistered(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source audit test")
	}
	tuiRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))

	used := make(map[string][]token.Position)
	files := token.NewFileSet()
	err := filepath.WalkDir(tuiRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".swarm", "testdata":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		parsed, parseErr := parser.ParseFile(files, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall || len(call.Args) == 0 || !isTranslationCall(call.Fun) {
				return true
			}
			literal, isLiteral := call.Args[0].(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				return true
			}
			messageID, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				t.Errorf("%s: invalid translation message ID: %v", files.Position(literal.Pos()), unquoteErr)
				return true
			}
			used[messageID] = append(used[messageID], files.Position(literal.Pos()))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk TUI source: %v", err)
	}

	catalog.RLock()
	defer catalog.RUnlock()
	for messageID, positions := range used {
		_, hasEnglish := catalog.english[messageID]
		_, hasSpanish := catalog.spanish[messageID]
		if hasEnglish && hasSpanish {
			continue
		}
		for _, position := range positions {
			t.Errorf("%s: message ID %q is not registered in both English and Spanish catalogs", position, messageID)
		}
	}
}

func isTranslationCall(function ast.Expr) bool {
	switch fn := function.(type) {
	case *ast.Ident:
		return fn.Name == "tr"
	case *ast.SelectorExpr:
		packageName, ok := fn.X.(*ast.Ident)
		return ok && packageName.Name == "i18n" && fn.Sel.Name == "T"
	default:
		return false
	}
}
