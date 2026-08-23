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

var s2PilotMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"SaveISCPOnboarding":  {parameters: 2, results: 2},
	"GetISCPOnboarding":   {parameters: 2, results: 3},
	"ListISCPOnboardings": {parameters: 2, results: 2},
}

func TestS2OnboardingMigrationSourceGuard(t *testing.T) {
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
			case "ISCPOnboardingRepository":
				repositoryMethods = len(interfaceType.Methods.List)
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "ISCPOnboardingRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, pilot := s2PilotMethodSignatures[field.Names[0].Name]; pilot {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(s2PilotMethodSignatures) || embeddedRepositories != 0 || repeatedMethods != 0 {
		t.Fatalf("repository methods=%d embedded=%d repeated=%d", repositoryMethods, embeddedRepositories, repeatedMethods)
	}

	implementationCounts := map[string]int{}
	operationReferences := map[string]int{
		"OperationISCPOnboardingSave": 0, "OperationISCPOnboardingGet": 0, "OperationISCPOnboardingList": 0,
	}
	ignoredCalls := make([]string, 0)
	rowsErrChecks := 0
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
			expected, pilot := s2PilotMethodSignatures[function.Name.Name]
			if !pilot {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results || !firstParameterIsContext(function.Type.Params) {
				t.Errorf("%s has old pilot signature in %s", function.Name.Name, path)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.Ident:
				if _, tracked := operationReferences[current.Name]; tracked && !strings.HasSuffix(path, "operation.go") {
					operationReferences[current.Name]++
				}
			case *ast.ExprStmt:
				call, ok := current.X.(*ast.CallExpr)
				if !ok {
					break
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if ok {
					if _, pilot := s2PilotMethodSignatures[selector.Sel.Name]; pilot {
						ignoredCalls = append(ignoredCalls, path+":"+selector.Sel.Name)
					}
				}
			case *ast.CallExpr:
				selector, ok := current.Fun.(*ast.SelectorExpr)
				if !ok {
					break
				}
				identifier, identifierOK := selector.X.(*ast.Ident)
				if identifierOK && identifier.Name == "rows" && selector.Sel.Name == "Err" && strings.HasSuffix(path, "iscp_onboarding_postgres.go") {
					rowsErrChecks++
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for method := range s2PilotMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	for operation, references := range operationReferences {
		if references == 0 {
			t.Errorf("operation spec %s is not consumed", operation)
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("pilot persistence errors are ignored at %v", ignoredCalls)
	}
	if rowsErrChecks != 1 {
		t.Errorf("PostgreSQL onboarding list rows.Err checks = %d, want 1", rowsErrChecks)
	}

	for _, path := range []string{
		"iscp_onboarding.go", "file_durability.go", "iscp_onboarding_postgres.go",
		"../iscppairing/service.go", "../gateway/iscp_pairing.go",
	} {
		assertNoContextBackground(t, files, path)
	}
	assertISCPPairingConsumerHasNoBroadStore(t, files)
}

func countFieldList(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

func firstParameterIsContext(fields *ast.FieldList) bool {
	if fields == nil || len(fields.List) == 0 {
		return false
	}
	selector, ok := fields.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	identifier, identifierOK := selector.X.(*ast.Ident)
	return identifierOK && identifier.Name == "context" && selector.Sel.Name == "Context"
}

func assertNoContextBackground(t *testing.T, files *token.FileSet, path string) {
	t.Helper()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, identifierOK := selector.X.(*ast.Ident)
		if identifierOK && identifier.Name == "context" && selector.Sel.Name == "Background" {
			t.Errorf("pilot source %s contains context.Background()", path)
		}
		return true
	})
}

func assertISCPPairingConsumerHasNoBroadStore(t *testing.T, files *token.FileSet) {
	t.Helper()
	entries, err := filepath.Glob("../iscppairing/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, identifierOK := selector.X.(*ast.Ident)
			if identifierOK && identifier.Name == "store" && selector.Sel.Name == "Store" {
				t.Errorf("ISCP pairing consumer depends on broad store.Store in %s", path)
			}
			return true
		})
	}
}
