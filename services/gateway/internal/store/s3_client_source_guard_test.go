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

var s3ClientMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"GetClient":             {parameters: 2, results: 3},
	"ListClients":           {parameters: 1, results: 2},
	"RevokeClient":          {parameters: 2, results: 2},
	"FindClientByTokenHash": {parameters: 2, results: 3},
	"TouchClient":           {parameters: 2, results: 3},
	"SavePairingCode":       {parameters: 2, results: 2},
	"GetPairingCode":        {parameters: 2, results: 3},
	"ClaimPairingCode":      {parameters: 3, results: 3},
}

func TestS3ClientMigrationSourceGuard(t *testing.T) {
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
			case "ClientRepository":
				repositoryMethods = len(interfaceType.Methods.List)
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "ClientRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, client := s3ClientMethodSignatures[field.Names[0].Name]; client {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(s3ClientMethodSignatures) || embeddedRepositories != 1 || repeatedMethods != 0 {
		t.Fatalf("repository methods=%d embedded=%d repeated=%d", repositoryMethods, embeddedRepositories, repeatedMethods)
	}

	implementationCounts := map[string]int{}
	ignoredCalls := make([]string, 0)
	backgroundCalls := make([]string, 0)
	legacySaveClient := make([]string, 0)
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
			if !ok {
				continue
			}
			if function.Name.Name == "SaveClient" {
				legacySaveClient = append(legacySaveClient, path+":declaration")
			}
			if function.Recv == nil {
				continue
			}
			expected, client := s3ClientMethodSignatures[function.Name.Name]
			if !client {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results || !firstParameterIsContext(function.Type.Params) {
				t.Errorf("%s has a legacy Client signature in %s", function.Name.Name, path)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.ExprStmt:
				if name := clientCallName(current.X); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.AssignStmt:
				for _, expression := range current.Rhs {
					name := clientCallName(expression)
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
				if selector, ok := current.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "SaveClient" {
					legacySaveClient = append(legacySaveClient, path+":call")
				}
				name := clientCallName(current)
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
	for method := range s3ClientMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("Client repository results are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("migrated Client calls use context.Background at %v", backgroundCalls)
	}
	if len(legacySaveClient) != 0 {
		t.Errorf("legacy SaveClient remains at %v", legacySaveClient)
	}
}

func clientCallName(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if _, client := s3ClientMethodSignatures[selector.Sel.Name]; client {
		return selector.Sel.Name
	}
	return ""
}
