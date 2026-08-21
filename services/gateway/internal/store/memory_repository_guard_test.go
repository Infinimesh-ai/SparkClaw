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

var memoryRepositoryMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"AddMemoryCandidate":     {parameters: 2, results: 2},
	"ResolveMemoryCandidate": {parameters: 3, results: 3},
	"ListMemoryCandidates":   {parameters: 2, results: 2},
	"SearchMemories":         {parameters: 2, results: 2},
	"UpdateMemory":           {parameters: 4, results: 2},
	"DeleteMemory":           {parameters: 2, results: 2},
	"PruneMemories":          {parameters: 2, results: 2},
}

func TestMemoryRepositoryMigrationSourceGuard(t *testing.T) {
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
			case "MemoryRepository":
				repositoryMethods = len(interfaceType.Methods.List)
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) != 1 {
						t.Errorf("MemoryRepository contains an embedded or unnamed method")
						continue
					}
					expected, exists := memoryRepositoryMethodSignatures[field.Names[0].Name]
					function, functionOK := field.Type.(*ast.FuncType)
					if !exists || !functionOK || countFieldList(function.Params) != expected.parameters ||
						countFieldList(function.Results) != expected.results || !firstParameterIsContext(function.Params) || !lastResultIsError(function.Results) {
						t.Errorf("MemoryRepository method %s has an unexpected signature", field.Names[0].Name)
					}
				}
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "MemoryRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, memory := memoryRepositoryMethodSignatures[field.Names[0].Name]; memory {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(memoryRepositoryMethodSignatures) || embeddedRepositories != 0 || repeatedMethods != 0 {
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
			expected, memory := memoryRepositoryMethodSignatures[function.Name.Name]
			if !memory {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results ||
				!firstParameterIsContext(function.Type.Params) || !lastResultIsError(function.Type.Results) {
				t.Errorf("%s has a legacy Memory signature in %s", function.Name.Name, path)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.ExprStmt:
				if name := memoryRepositoryCallName(current.X); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.DeferStmt:
				if name := memoryRepositoryCallName(current.Call); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.GoStmt:
				if name := memoryRepositoryCallName(current.Call); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.AssignStmt:
				for _, expression := range current.Rhs {
					name := memoryRepositoryCallName(expression)
					if name == "" || len(current.Lhs) == 0 {
						continue
					}
					if identifier, ok := current.Lhs[len(current.Lhs)-1].(*ast.Ident); ok && identifier.Name == "_" {
						ignoredCalls = append(ignoredCalls, path+":"+name)
					}
				}
			case *ast.CallExpr:
				name := memoryRepositoryCallName(current)
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
	for method := range memoryRepositoryMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("Memory repository errors are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("migrated Memory calls use context.Background at %v", backgroundCalls)
	}
}

func memoryRepositoryCallName(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if _, memory := memoryRepositoryMethodSignatures[selector.Sel.Name]; memory {
		return selector.Sel.Name
	}
	return ""
}
