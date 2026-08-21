package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestS5RuntimeRemainsAssemblyOnlyWithoutRepositoryForwarders(t *testing.T) {
	gatewayRoot := filepath.Clean(filepath.Join("..", ".."))
	err := filepath.WalkDir(gatewayRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files := token.NewFileSet()
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		assemblyFile := strings.Contains(filepath.ToSlash(path), "/cmd/sparkclaw/")
		storeFile := parsed.Name.Name == "store"
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.SelectorExpr:
				packageName, ok := current.X.(*ast.Ident)
				if ok && packageName.Name == "store" && current.Sel.Name == "Runtime" && !assemblyFile {
					t.Errorf("store.Runtime escaped assembly in %s:%d", path, files.Position(current.Pos()).Line)
				}
			case *ast.FuncDecl:
				if !storeFile || current.Recv == nil || !runtimeReceiver(current.Recv.List[0].Type) {
					break
				}
				for _, methods := range s0RepositoryMethods {
					for _, method := range methods {
						if current.Name.Name == method {
							t.Errorf("store.Runtime forwards repository method %s in %s:%d", method, path, files.Position(current.Pos()).Line)
						}
					}
				}
			case *ast.UnaryExpr:
				if current.Op == token.AND {
					if literal, ok := current.X.(*ast.CompositeLit); ok {
						if selector, ok := literal.Type.(*ast.Ident); ok && selector.Name == "StoreError" && filepath.Base(path) != "operation.go" {
							t.Errorf("StoreError bypasses supervised constructor in %s:%d", path, files.Position(current.Pos()).Line)
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runtimeReceiver(expression ast.Expr) bool {
	switch current := expression.(type) {
	case *ast.Ident:
		return current.Name == "Runtime"
	case *ast.StarExpr:
		return runtimeReceiver(current.X)
	default:
		return false
	}
}
