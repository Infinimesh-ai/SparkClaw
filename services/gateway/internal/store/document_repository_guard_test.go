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

var documentRepositoryMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"SaveDocumentRecord":  {parameters: 2, results: 2},
	"GetDocumentRecord":   {parameters: 2, results: 3},
	"ListDocumentRecords": {parameters: 4, results: 2},
}

func TestDocumentRepositoryMigrationSourceGuard(t *testing.T) {
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
			case "DocumentRepository":
				repositoryMethods = len(interfaceType.Methods.List)
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) != 1 {
						t.Errorf("DocumentRepository contains an embedded or unnamed method")
						continue
					}
					expected, exists := documentRepositoryMethodSignatures[field.Names[0].Name]
					function, functionOK := field.Type.(*ast.FuncType)
					if !exists || !functionOK || countFieldList(function.Params) != expected.parameters ||
						countFieldList(function.Results) != expected.results || !firstParameterIsContext(function.Params) || !lastResultIsError(function.Results) {
						t.Errorf("DocumentRepository method %s has an unexpected signature", field.Names[0].Name)
					}
				}
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "DocumentRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, document := documentRepositoryMethodSignatures[field.Names[0].Name]; document {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(documentRepositoryMethodSignatures) || embeddedRepositories != 0 || repeatedMethods != 0 {
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
			expected, document := documentRepositoryMethodSignatures[function.Name.Name]
			if !document {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results ||
				!firstParameterIsContext(function.Type.Params) || !lastResultIsError(function.Type.Results) {
				t.Errorf("%s has a legacy Document signature in %s", function.Name.Name, path)
			}
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
			name := documentRepositoryCallName(call)
			if name == "" {
				return true
			}
			position := files.Position(call.Pos())
			if len(call.Args) > 0 && isContextBackgroundCall(call.Args[0]) {
				backgroundCalls = append(backgroundCalls, position.String()+":"+name)
			}
			switch statement := parent.(type) {
			case *ast.ExprStmt:
				ignoredCalls = append(ignoredCalls, position.String()+":"+name)
			case *ast.AssignStmt:
				if len(statement.Lhs) > 0 {
					if identifier, ok := statement.Lhs[len(statement.Lhs)-1].(*ast.Ident); ok && identifier.Name == "_" {
						ignoredCalls = append(ignoredCalls, position.String()+":"+name)
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
	for method := range documentRepositoryMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("Document repository errors are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("migrated Document calls use context.Background at %v", backgroundCalls)
	}
}

func documentRepositoryCallName(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if _, document := documentRepositoryMethodSignatures[selector.Sel.Name]; document {
		return selector.Sel.Name
	}
	return ""
}
