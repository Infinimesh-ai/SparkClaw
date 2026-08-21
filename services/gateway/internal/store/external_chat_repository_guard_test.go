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

var externalChatRepositoryMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"SaveExternalChatSession":                  {parameters: 2, results: 2},
	"GetExternalChatSession":                   {parameters: 2, results: 3},
	"ListExternalChatSessions":                 {parameters: 3, results: 2},
	"FindExternalChatSession":                  {parameters: 4, results: 3},
	"FindExternalChatSessionByLinkedSessionID": {parameters: 2, results: 3},
	"SaveExternalChatMessage":                  {parameters: 2, results: 2},
	"GetExternalChatMessage":                   {parameters: 2, results: 3},
	"FindExternalChatMessageByExternalID":      {parameters: 3, results: 3},
	"ListExternalChatMessages":                 {parameters: 3, results: 2},
}

func TestExternalChatRepositoryMigrationSourceGuard(t *testing.T) {
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
			case "ExternalChatRepository":
				repositoryMethods = len(interfaceType.Methods.List)
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) != 1 {
						t.Errorf("ExternalChatRepository contains an embedded or unnamed method")
						continue
					}
					expected, exists := externalChatRepositoryMethodSignatures[field.Names[0].Name]
					function, functionOK := field.Type.(*ast.FuncType)
					if !exists || !functionOK || countFieldList(function.Params) != expected.parameters ||
						countFieldList(function.Results) != expected.results || !firstParameterIsContext(function.Params) || !lastResultIsError(function.Results) {
						t.Errorf("ExternalChatRepository method %s has an unexpected signature", field.Names[0].Name)
					}
				}
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "ExternalChatRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, exists := externalChatRepositoryMethodSignatures[field.Names[0].Name]; exists {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(externalChatRepositoryMethodSignatures) || embeddedRepositories != 1 || repeatedMethods != 0 {
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
			expected, exists := externalChatRepositoryMethodSignatures[function.Name.Name]
			if !exists {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results ||
				!firstParameterIsContext(function.Type.Params) || !lastResultIsError(function.Type.Results) {
				t.Errorf("%s has a legacy ExternalChat signature in %s", function.Name.Name, path)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.ExprStmt:
				if name := externalChatRepositoryCallName(current.X); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.DeferStmt:
				if name := externalChatRepositoryCallName(current.Call); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.GoStmt:
				if name := externalChatRepositoryCallName(current.Call); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.AssignStmt:
				for _, expression := range current.Rhs {
					name := externalChatRepositoryCallName(expression)
					if name == "" || len(current.Lhs) == 0 {
						continue
					}
					if identifier, ok := current.Lhs[len(current.Lhs)-1].(*ast.Ident); ok && identifier.Name == "_" {
						ignoredCalls = append(ignoredCalls, path+":"+name)
					}
				}
			case *ast.CallExpr:
				name := externalChatRepositoryCallName(current)
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
	for method := range externalChatRepositoryMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("ExternalChat repository errors are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("migrated ExternalChat calls use context.Background at %v", backgroundCalls)
	}
}

func externalChatRepositoryCallName(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if _, exists := externalChatRepositoryMethodSignatures[selector.Sel.Name]; exists {
		return selector.Sel.Name
	}
	return ""
}
