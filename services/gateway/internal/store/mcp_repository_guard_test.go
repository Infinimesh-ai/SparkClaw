package store

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMCPRepositoryInterfaceAndBackendsStayComplete(t *testing.T) {
	repositoryType := reflect.TypeOf((*MCPRepository)(nil)).Elem()
	if repositoryType.NumMethod() != 19 {
		t.Fatalf("MCPRepository methods=%d, want 19", repositoryType.NumMethod())
	}
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	methods := make(map[string]int, repositoryType.NumMethod())
	for index := range repositoryType.NumMethod() {
		method := repositoryType.Method(index)
		if method.Type.NumIn() == 0 || method.Type.In(0) != contextType {
			t.Errorf("%s does not accept context.Context first", method.Name)
		}
		if method.Type.NumOut() == 0 || method.Type.Out(method.Type.NumOut()-1) != errorType {
			t.Errorf("%s does not return error last", method.Name)
		}
		methods[method.Name] = method.Type.NumIn()
	}

	files := token.NewFileSet()
	implementationCounts := map[string]map[string]int{
		"MemoryStore": {}, "FileStore": {}, "PostgresStore": {},
	}
	for _, path := range []string{"memory.go", "mcp_access.go", "file.go", "mcp_access_postgres.go"} {
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			star, ok := function.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			receiver, ok := star.X.(*ast.Ident)
			if !ok {
				continue
			}
			if _, tracked := implementationCounts[receiver.Name]; !tracked {
				continue
			}
			if _, tracked := methods[function.Name.Name]; tracked {
				implementationCounts[receiver.Name][function.Name.Name]++
			}
		}
	}
	for receiver, counts := range implementationCounts {
		for method := range methods {
			if counts[method] != 1 {
				t.Errorf("%s.%s implementations=%d, want 1", receiver, method, counts[method])
			}
		}
	}
}

func TestMCPRepositoryProductionCallsUseCallerContextAndObserveWrites(t *testing.T) {
	repositoryType := reflect.TypeOf((*MCPRepository)(nil)).Elem()
	methods := make(map[string]int, repositoryType.NumMethod())
	for index := range repositoryType.NumMethod() {
		method := repositoryType.Method(index)
		methods[method.Name] = method.Type.NumIn()
	}
	files := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/store/") {
			return nil
		}
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		stack := []ast.Node{}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			var parent ast.Node
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, node)
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			wantArguments, tracked := methods[selector.Sel.Name]
			if !tracked {
				return true
			}
			position := files.Position(call.Pos())
			if len(call.Args) != wantArguments {
				t.Errorf("%s:%d %s arguments=%d, want %d", position.Filename, position.Line, selector.Sel.Name, len(call.Args), wantArguments)
				return true
			}
			if isRunRepositoryBackgroundCall(call.Args[0]) {
				t.Errorf("%s:%d %s replaces caller context with context.Background", position.Filename, position.Line, selector.Sel.Name)
			}
			if _, ignored := parent.(*ast.ExprStmt); ignored {
				t.Errorf("%s:%d ignores %s result", position.Filename, position.Line, selector.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
