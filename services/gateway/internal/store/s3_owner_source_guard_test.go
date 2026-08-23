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

var s3OwnerMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"GetOwnerProfile":               {parameters: 1, results: 2},
	"UpdateOwnerProfile":            {parameters: 2, results: 2},
	"GetOwnerProfileByID":           {parameters: 2, results: 3},
	"SaveOwnerProfile":              {parameters: 2, results: 2},
	"ListOwnerProfiles":             {parameters: 1, results: 2},
	"FindOwnerProfileByExternalRef": {parameters: 3, results: 3},
}

func TestS3OwnerMigrationSourceGuard(t *testing.T) {
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
			case "OwnerRepository":
				repositoryMethods = len(interfaceType.Methods.List)
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "OwnerRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, owner := s3OwnerMethodSignatures[field.Names[0].Name]; owner {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(s3OwnerMethodSignatures) || embeddedRepositories != 0 || repeatedMethods != 0 {
		t.Fatalf("repository methods=%d embedded=%d repeated=%d", repositoryMethods, embeddedRepositories, repeatedMethods)
	}

	implementationCounts := map[string]int{}
	ignoredCalls := make([]string, 0)
	backgroundCalls := make([]string, 0)
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
			expected, owner := s3OwnerMethodSignatures[function.Name.Name]
			if !owner {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results || !firstParameterIsContext(function.Type.Params) {
				t.Errorf("%s has a legacy Owner signature in %s", function.Name.Name, path)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.ExprStmt:
				if ownerCallName(current.X) != "" {
					ignoredCalls = append(ignoredCalls, path+":"+ownerCallName(current.X))
				}
			case *ast.AssignStmt:
				for _, expression := range current.Rhs {
					name := ownerCallName(expression)
					if name == "" {
						continue
					}
					for _, target := range current.Lhs {
						if identifier, ok := target.(*ast.Ident); ok && identifier.Name == "_" {
							ignoredCalls = append(ignoredCalls, path+":"+name)
							break
						}
					}
				}
			case *ast.CallExpr:
				name := ownerCallName(current)
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
	for method := range s3OwnerMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("Owner repository results are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("migrated Owner calls use context.Background at %v", backgroundCalls)
	}
}

func ownerCallName(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if _, owner := s3OwnerMethodSignatures[selector.Sel.Name]; owner {
		return selector.Sel.Name
	}
	return ""
}

func isContextBackgroundCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Background" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "context"
}
