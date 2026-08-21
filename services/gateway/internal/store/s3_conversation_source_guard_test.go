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

var s3ConversationMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"AddMessage":         {parameters: 2, results: 2},
	"ListMessages":       {parameters: 2, results: 2},
	"MessageEventHead":   {parameters: 2, results: 2},
	"MessageEventsAfter": {parameters: 4, results: 2},
}

func TestS3ConversationMigrationSourceGuard(t *testing.T) {
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
			case "ConversationRepository":
				repositoryMethods = len(interfaceType.Methods.List)
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) != 1 {
						t.Errorf("ConversationRepository contains an embedded or unnamed method")
						continue
					}
					expected, exists := s3ConversationMethodSignatures[field.Names[0].Name]
					function, functionOK := field.Type.(*ast.FuncType)
					if !exists || !functionOK || countFieldList(function.Params) != expected.parameters || countFieldList(function.Results) != expected.results || !firstParameterIsContext(function.Params) || !lastResultIsError(function.Results) {
						t.Errorf("ConversationRepository method %s has an unexpected signature", field.Names[0].Name)
					}
				}
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "ConversationRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, conversation := s3ConversationMethodSignatures[field.Names[0].Name]; conversation {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(s3ConversationMethodSignatures) || embeddedRepositories != 1 || repeatedMethods != 0 {
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
			expected, conversation := s3ConversationMethodSignatures[function.Name.Name]
			if !conversation {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results || !firstParameterIsContext(function.Type.Params) || !lastResultIsError(function.Type.Results) {
				t.Errorf("%s has a legacy Conversation signature in %s", function.Name.Name, path)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.ExprStmt:
				if name := conversationCallName(current.X); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.AssignStmt:
				for _, expression := range current.Rhs {
					name := conversationCallName(expression)
					if name == "" || len(current.Lhs) == 0 {
						continue
					}
					if identifier, ok := current.Lhs[len(current.Lhs)-1].(*ast.Ident); ok && identifier.Name == "_" {
						ignoredCalls = append(ignoredCalls, path+":"+name)
					}
				}
			case *ast.CallExpr:
				name := conversationCallName(current)
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
	for method := range s3ConversationMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("Conversation repository errors are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("migrated Conversation calls use context.Background at %v", backgroundCalls)
	}
}

func lastResultIsError(results *ast.FieldList) bool {
	if results == nil || len(results.List) == 0 {
		return false
	}
	identifier, ok := results.List[len(results.List)-1].Type.(*ast.Ident)
	return ok && identifier.Name == "error"
}

func conversationCallName(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if _, conversation := s3ConversationMethodSignatures[selector.Sel.Name]; conversation {
		return selector.Sel.Name
	}
	return ""
}
