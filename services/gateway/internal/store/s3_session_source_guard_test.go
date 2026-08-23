package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

var s3SessionMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"CreateSession":          {parameters: 2, results: 2},
	"CreateSessionWithScope": {parameters: 6, results: 2},
	"ListSessions":           {parameters: 1, results: 2},
	"GetSession":             {parameters: 2, results: 3},
	"UpdateSessionTitle":     {parameters: 3, results: 2},
	"DeleteSession":          {parameters: 2, results: 2},
}

func TestS3SessionMigrationSourceGuard(t *testing.T) {
	files := token.NewFileSet()
	storeSource, err := parser.ParseFile(files, "store.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var repositoryMethods, embeddedRepositories, repeatedMethods int
	for _, declaration := range storeSource.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			switch typeSpec.Name.Name {
			case "SessionRepository":
				repositoryMethods = len(interfaceType.Methods.List)
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "SessionRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, session := s3SessionMethodSignatures[field.Names[0].Name]; session {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(s3SessionMethodSignatures) || embeddedRepositories != 0 || repeatedMethods != 0 {
		t.Fatalf("repository methods=%d embedded=%d repeated=%d", repositoryMethods, embeddedRepositories, repeatedMethods)
	}

	implementationCounts := map[string]int{}
	ignoredCalls := []string{}
	backgroundCalls := []string{}
	err = filepath.WalkDir("../..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(files, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil {
				continue
			}
			expected, session := s3SessionMethodSignatures[function.Name.Name]
			if !session {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results || !firstParameterIsContext(function.Type.Params) {
				t.Errorf("%s has a legacy Session signature in %s", function.Name.Name, path)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.ExprStmt:
				if name := sessionCallName(current.X); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.AssignStmt:
				for _, expression := range current.Rhs {
					name := sessionCallName(expression)
					if name == "" || len(current.Lhs) == 0 {
						continue
					}
					if identifier, ok := current.Lhs[len(current.Lhs)-1].(*ast.Ident); ok && identifier.Name == "_" {
						ignoredCalls = append(ignoredCalls, path+":"+name)
					}
				}
			case *ast.CallExpr:
				name := sessionCallName(current)
				if name != "" && len(current.Args) > 0 && isContextBackgroundCall(current.Args[0]) {
					backgroundCalls = append(backgroundCalls, path+":"+name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for method := range s3SessionMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("Session repository results are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("migrated Session calls use context.Background at %v", backgroundCalls)
	}
}

func sessionCallName(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if _, session := s3SessionMethodSignatures[selector.Sel.Name]; session {
		return selector.Sel.Name
	}
	return ""
}
