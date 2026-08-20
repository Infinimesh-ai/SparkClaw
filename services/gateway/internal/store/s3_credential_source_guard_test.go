package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var s3CredentialMethodSignatures = map[string]struct {
	parameters int
	results    int
}{
	"SaveCredentialSecret":   {parameters: 2, results: 2},
	"GetCredentialSecret":    {parameters: 2, results: 3},
	"DeleteCredentialSecret": {parameters: 2, results: 2},
}

func TestS3CredentialMigrationSourceGuard(t *testing.T) {
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
			case "CredentialRepository":
				repositoryMethods = len(interfaceType.Methods.List)
			case "Store":
				for _, field := range interfaceType.Methods.List {
					if len(field.Names) == 0 {
						if identifier, ok := field.Type.(*ast.Ident); ok && identifier.Name == "CredentialRepository" {
							embeddedRepositories++
						}
						continue
					}
					if _, credential := s3CredentialMethodSignatures[field.Names[0].Name]; credential {
						repeatedMethods++
					}
				}
			}
		}
	}
	if repositoryMethods != len(s3CredentialMethodSignatures) || embeddedRepositories != 1 || repeatedMethods != 0 {
		t.Fatalf("repository methods=%d embedded=%d repeated=%d", repositoryMethods, embeddedRepositories, repeatedMethods)
	}

	implementationCounts := map[string]int{}
	ignoredCalls := make([]string, 0)
	backgroundCalls := make([]string, 0)
	unexpectedCallers := make([]string, 0)
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
			expected, credential := s3CredentialMethodSignatures[function.Name.Name]
			if !credential {
				continue
			}
			implementationCounts[function.Name.Name]++
			if countFieldList(function.Type.Params) != expected.parameters || countFieldList(function.Type.Results) != expected.results || !firstParameterIsContext(function.Type.Params) {
				t.Errorf("%s has a legacy Credential signature in %s", function.Name.Name, path)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.ExprStmt:
				if name := credentialCallName(current.X); name != "" {
					ignoredCalls = append(ignoredCalls, path+":"+name)
				}
			case *ast.AssignStmt:
				for _, expression := range current.Rhs {
					name := credentialCallName(expression)
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
				name := credentialCallName(current)
				if name != "" && len(current.Args) > 0 && isContextBackgroundCall(current.Args[0]) {
					backgroundCalls = append(backgroundCalls, path+":"+name)
				}
				if name != "" {
					normalizedPath := filepath.ToSlash(path)
					if !strings.Contains(normalizedPath, "/internal/store/") && !strings.HasSuffix(normalizedPath, "/internal/credential/vault.go") {
						unexpectedCallers = append(unexpectedCallers, path+":"+name)
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
	for method := range s3CredentialMethodSignatures {
		if implementationCounts[method] != 3 {
			t.Errorf("%s implementation count = %d, want 3", method, implementationCounts[method])
		}
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("Credential repository results are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("migrated Credential calls use context.Background at %v", backgroundCalls)
	}
	if len(unexpectedCallers) != 0 {
		t.Errorf("production consumers bypass the CredentialVault at %v", unexpectedCallers)
	}

	vaultSource, err := parser.ParseFile(files, filepath.Join("..", "credential", "vault.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(vaultSource, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Store" {
			return true
		}
		if identifier, ok := selector.X.(*ast.Ident); ok && identifier.Name == "store" {
			t.Error("CredentialVault depends on broad store.Store")
		}
		return true
	})
}

func TestS3CredentialCommandsRemainOpaqueAndValuesRemainRedacted(t *testing.T) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, "credential_contract.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := map[string]bool{"CredentialSaveCommand": false, "CredentialDeleteCondition": false}
	constructors := map[string]bool{
		"NewCredentialCreate": false, "NewCredentialReplace": false, "NewCredentialDeleteCondition": false,
	}
	for _, declaration := range parsed.Decls {
		switch current := declaration.(type) {
		case *ast.GenDecl:
			for _, rawSpec := range current.Specs {
				typeSpec, ok := rawSpec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, guarded := wantTypes[typeSpec.Name.Name]; !guarded {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					t.Fatalf("%s is not a struct", typeSpec.Name.Name)
				}
				for _, field := range structure.Fields.List {
					for _, name := range field.Names {
						if ast.IsExported(name.Name) {
							t.Errorf("%s exposes field %s", typeSpec.Name.Name, name.Name)
						}
					}
				}
				wantTypes[typeSpec.Name.Name] = true
			}
		case *ast.FuncDecl:
			if _, guarded := constructors[current.Name.Name]; guarded {
				constructors[current.Name.Name] = true
			}
		}
	}
	for name, found := range wantTypes {
		if !found {
			t.Errorf("opaque command type %s is missing", name)
		}
	}
	for name, found := range constructors {
		if !found {
			t.Errorf("credential command factory %s is missing", name)
		}
	}
	valueField, ok := reflect.TypeOf(app.CredentialSecret{}).FieldByName("Value")
	if !ok || valueField.Tag.Get("json") != "-" {
		t.Fatalf("CredentialSecret.Value json tag = %q, want -", valueField.Tag.Get("json"))
	}
}

func credentialCallName(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if _, credential := s3CredentialMethodSignatures[selector.Sel.Name]; credential {
		return selector.Sel.Name
	}
	return ""
}
