package store

// This file is the single source guard for every repository contract. It
// replaces the seventeen per-repository guard files that each hand-maintained
// a method table restating what the operationSpecs registry already holds
// (engineering baseline rule 3: define each fact in exactly one place).
//
// The registry in operation.go decides which repository owns which method;
// the repository interfaces in store.go decide the method signatures. This
// guard cross-checks both against every production source file in the
// gateway, so adding a repository method requires touching exactly two
// authoritative places - the interface and the operation registry - and
// nothing else.

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

type repositoryMethodShape struct {
	repository string
	parameters int
	results    int
}

// fullyObservedRepositories lists repositories whose call sites must observe
// every single result, not just the trailing error.
var fullyObservedRepositories = map[string]bool{
	"OwnerRepository": true, "ClientRepository": true, "CredentialRepository": true,
}

// specRepositoryMethods derives the repository -> method-set table from the
// operationSpecs registry and rejects method names claimed by two
// repositories, since every later check identifies calls by method name.
func specRepositoryMethods(t *testing.T) map[string]map[string]bool {
	t.Helper()
	methods := map[string]map[string]bool{}
	owners := map[string]string{}
	for _, spec := range operationSpecs {
		if owner, taken := owners[spec.Method]; taken && owner != spec.Repository {
			t.Fatalf("method %s is registered for both %s and %s", spec.Method, owner, spec.Repository)
		}
		owners[spec.Method] = spec.Repository
		if methods[spec.Repository] == nil {
			methods[spec.Repository] = map[string]bool{}
		}
		if methods[spec.Repository][spec.Method] {
			t.Fatalf("method %s.%s is registered by two operations", spec.Repository, spec.Method)
		}
		methods[spec.Repository][spec.Method] = true
	}
	return methods
}

// repositoryMethodShapes checks the repository interfaces in store.go against
// the registry and returns the authoritative signature shape per method.
func repositoryMethodShapes(t *testing.T, files *token.FileSet) map[string]repositoryMethodShape {
	t.Helper()
	specMethods := specRepositoryMethods(t)
	storeSource, err := parser.ParseFile(files, "store.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	shapes := map[string]repositoryMethodShape{}
	declared := map[string]bool{}
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
			name := typeSpec.Name.Name
			if name == "Store" {
				t.Errorf("the broad Store interface was deleted and must not return")
				continue
			}
			if !strings.HasSuffix(name, "Repository") {
				continue
			}
			declared[name] = true
			expected := specMethods[name]
			if expected == nil {
				t.Errorf("interface %s has no operations registered in operationSpecs", name)
				continue
			}
			seen := map[string]bool{}
			for _, field := range interfaceType.Methods.List {
				if len(field.Names) != 1 {
					t.Errorf("%s contains an embedded or unnamed method", name)
					continue
				}
				methodName := field.Names[0].Name
				function, functionOK := field.Type.(*ast.FuncType)
				if !functionOK || !firstParameterIsContext(function.Params) || !lastResultIsError(function.Results) {
					t.Errorf("%s.%s must accept context.Context first and return error last", name, methodName)
					continue
				}
				if !expected[methodName] {
					t.Errorf("%s.%s has no operation spec in operationSpecs", name, methodName)
					continue
				}
				seen[methodName] = true
				shapes[methodName] = repositoryMethodShape{
					repository: name,
					parameters: countFieldList(function.Params),
					results:    countFieldList(function.Results),
				}
			}
			for method := range expected {
				if !seen[method] {
					t.Errorf("operation spec method %s.%s is missing from the %s interface", name, method, name)
				}
			}
		}
	}
	for repository := range specMethods {
		if !declared[repository] {
			t.Errorf("operationSpecs repository %s is not declared as an interface in store.go", repository)
		}
	}
	if t.Failed() {
		t.Fatal("registry and interfaces disagree; skipping source walk")
	}
	return shapes
}

// TestRepositorySourceGuard walks every production gateway source file and
// enforces, for all repositories at once:
//   - each repository method is implemented exactly once per backend
//     (MemoryStore, FileStore, PostgresStore) and nowhere else, with the
//     interface signature (context first, error last, same arity);
//   - production call sites pass the full argument list, never replace the
//     caller context with context.Background(), and never discard results
//     via a bare statement, defer, go, or a blank assignment target;
//   - credential secrets are only touched by the store backends and the
//     CredentialVault;
//   - the legacy pre-repository SaveClient entry point stays deleted.
func TestRepositorySourceGuard(t *testing.T) {
	files := token.NewFileSet()
	shapes := repositoryMethodShapes(t, files)

	backendReceivers := []string{"MemoryStore", "FileStore", "PostgresStore"}
	implementations := map[string]map[string]int{}
	var ignoredCalls, backgroundCalls, arityMismatches []string
	var credentialBypasses, legacySaveClient []string

	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// internal/storetest holds shared test fixtures, not production
		// callers; its legacy run fixtures intentionally use
		// context.Background because their call sites have no testing.TB.
		if strings.Contains(filepath.ToSlash(path), "/internal/storetest/") {
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
			shape, guarded := shapes[function.Name.Name]
			if !guarded {
				continue
			}
			receiver := receiverTypeName(function.Recv)
			if implementations[function.Name.Name] == nil {
				implementations[function.Name.Name] = map[string]int{}
			}
			implementations[function.Name.Name][receiver]++
			if countFieldList(function.Type.Params) != shape.parameters || countFieldList(function.Type.Results) != shape.results ||
				!firstParameterIsContext(function.Type.Params) || !lastResultIsError(function.Type.Results) {
				t.Errorf("%s implementation in %s does not match the %s interface signature", function.Name.Name, path, shape.repository)
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
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := selector.Sel.Name
			shape, guarded := shapes[name]
			if name == "SaveClient" {
				legacySaveClient = append(legacySaveClient, path+":call")
			}
			if !guarded {
				return true
			}
			position := files.Position(call.Pos()).String()
			if shape.repository == "CredentialRepository" {
				normalizedPath := filepath.ToSlash(path)
				if !strings.Contains(normalizedPath, "/internal/store/") && !strings.HasSuffix(normalizedPath, "/internal/credential/vault.go") {
					credentialBypasses = append(credentialBypasses, position+":"+name)
				}
			}
			if len(call.Args) != shape.parameters {
				arityMismatches = append(arityMismatches, position+":"+name)
				return true
			}
			if isContextBackgroundCall(call.Args[0]) {
				backgroundCalls = append(backgroundCalls, position+":"+name)
			}
			switch statement := parent.(type) {
			case *ast.ExprStmt, *ast.DeferStmt, *ast.GoStmt:
				ignoredCalls = append(ignoredCalls, position+":"+name)
			case *ast.AssignStmt:
				// Every repository forbids discarding the trailing error.
				// Owner, client, and credential calls additionally forbid
				// discarding any result (their historical guards did).
				if len(statement.Lhs) > 0 {
					if identifier, ok := statement.Lhs[len(statement.Lhs)-1].(*ast.Ident); ok && identifier.Name == "_" {
						ignoredCalls = append(ignoredCalls, position+":"+name)
						break
					}
				}
				if fullyObservedRepositories[shape.repository] {
					for _, target := range statement.Lhs {
						if identifier, ok := target.(*ast.Ident); ok && identifier.Name == "_" {
							ignoredCalls = append(ignoredCalls, position+":"+name)
							break
						}
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

	for method, shape := range shapes {
		counts := implementations[method]
		for _, backend := range backendReceivers {
			if counts[backend] != 1 {
				t.Errorf("%s.%s implementations on %s = %d, want 1", shape.repository, method, backend, counts[backend])
			}
		}
		for receiver, count := range counts {
			if receiver != "MemoryStore" && receiver != "FileStore" && receiver != "PostgresStore" {
				t.Errorf("%s.%s is also implemented %d time(s) on unexpected receiver %q", shape.repository, method, count, receiver)
			}
		}
	}
	if len(arityMismatches) != 0 {
		t.Errorf("repository calls drop or add arguments at %v", arityMismatches)
	}
	if len(ignoredCalls) != 0 {
		t.Errorf("repository results or errors are ignored at %v", ignoredCalls)
	}
	if len(backgroundCalls) != 0 {
		t.Errorf("repository calls replace the caller context with context.Background at %v", backgroundCalls)
	}
	if len(credentialBypasses) != 0 {
		t.Errorf("production consumers bypass the CredentialVault at %v", credentialBypasses)
	}
	if len(legacySaveClient) != 0 {
		t.Errorf("legacy SaveClient remains at %v", legacySaveClient)
	}
}

// TestEveryStoreOperationIsConsumed keeps the registry honest in the other
// direction: an operation constant that no production file outside
// operation.go references is a dead registration.
func TestEveryStoreOperationIsConsumed(t *testing.T) {
	files := token.NewFileSet()
	operationSource, err := parser.ParseFile(files, "operation.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	references := map[string]int{}
	for _, declaration := range operationSource.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			valueSpec, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			identifier, ok := valueSpec.Type.(*ast.Ident)
			if !ok || identifier.Name != "StoreOperation" {
				continue
			}
			for _, name := range valueSpec.Names {
				references[name.Name] = 0
			}
		}
	}
	if len(references) != len(operationSpecs) {
		t.Fatalf("StoreOperation constants = %d, operationSpecs entries = %d", len(references), len(operationSpecs))
	}
	err = filepath.WalkDir("../..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(filepath.ToSlash(path), "/store/operation.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(files, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if identifier, ok := node.(*ast.Ident); ok {
				if _, tracked := references[identifier.Name]; tracked {
					references[identifier.Name]++
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for operation, count := range references {
		if count == 0 {
			t.Errorf("operation spec %s is not consumed outside operation.go", operation)
		}
	}
}

// TestISCPOnboardingDurabilityDetails preserves the file-specific checks from
// the S2 onboarding migration: the PostgreSQL list path must check rows.Err
// exactly once, and the onboarding/pairing sources must not manufacture
// context.Background() anywhere.
func TestISCPOnboardingDurabilityDetails(t *testing.T) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, "iscp_onboarding_postgres.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	rowsErrChecks := 0
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
		if identifierOK && identifier.Name == "rows" && selector.Sel.Name == "Err" {
			rowsErrChecks++
		}
		return true
	})
	if rowsErrChecks != 1 {
		t.Errorf("PostgreSQL onboarding list rows.Err checks = %d, want 1", rowsErrChecks)
	}
	for _, path := range []string{
		"iscp_onboarding.go", "file_durability.go", "iscp_onboarding_postgres.go",
		"../iscppairing/service.go", "../gateway/iscp_pairing.go",
	} {
		assertNoContextBackground(t, files, path)
	}
}

// TestS3CredentialCommandsRemainOpaqueAndValuesRemainRedacted is carried over
// verbatim from the S3 credential migration: write commands stay opaque so
// secrets cannot be constructed ad hoc, and the secret value never serializes.
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

func receiverTypeName(receiver *ast.FieldList) string {
	if receiver == nil || len(receiver.List) != 1 {
		return ""
	}
	expression := receiver.List[0].Type
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Name
	}
	return ""
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

func lastResultIsError(results *ast.FieldList) bool {
	if results == nil || len(results.List) == 0 {
		return false
	}
	identifier, ok := results.List[len(results.List)-1].Type.(*ast.Ident)
	return ok && identifier.Name == "error"
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

func assertNoContextBackground(t *testing.T, files *token.FileSet, path string) {
	t.Helper()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		if expression, ok := node.(ast.Expr); ok && isContextBackgroundCall(expression) {
			t.Errorf("source %s contains context.Background()", path)
		}
		return true
	})
}
