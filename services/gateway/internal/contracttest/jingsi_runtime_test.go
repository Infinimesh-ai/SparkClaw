package contracttest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// centralContract is the SparkClaw--JingSi Runtime v1 contract as read from
// the InfiniCenter hub: the authoritative schema, the HTTP binding and the
// conformance fixtures. Nothing in it is copied into this repository; the
// gate resolves the central manifest and reads the referenced files directly.
type centralContract struct {
	manifestDir      string
	protocol         string
	mediaType        string
	requestKeyHeader string
	token            *regexp.Regexp
	schema           map[string]any
	byRequestKind    map[string]bindingOperation
	bySuccessKind    map[string]bindingOperation
	byOperation      map[string]bindingOperation
	cases            []centralCase
}

type bindingOperation struct {
	Operation     string
	Method        string
	Path          string
	RequestKind   string
	SuccessKind   string
	SuccessStatus int
}

type centralCase struct {
	Name     string
	Category string
	Messages []fixtureMessage
}

type fixtureMessage struct {
	Label         string
	ExpectedValid bool
	Headers       map[string]string
	Body          map[string]any
	Assertions    []string
}

// TestJingSiRuntimeProviderConsumesCentralContract is the precondition gate:
// the central manifest must be self-consistent (digests, binding shape, case
// identity) and its fixtures must exercise exactly the assertion vocabulary
// this provider knows how to prove. The fixture-driven provider test below
// builds on the same loader, so a drifted hub fails here before any request
// is sent.
func TestJingSiRuntimeProviderConsumesCentralContract(t *testing.T) {
	t.Parallel()
	loadCentralContract(t)
}

func loadCentralContract(t *testing.T) *centralContract {
	t.Helper()
	manifestPath := locateJingSiRuntimeManifest(t)
	manifest := readJSONObject(t, manifestPath)
	if valueString(t, manifest, "artifact") != "sparkclaw-jingsi-conformance" ||
		valueString(t, manifest, "wire_major") != "v1" {
		t.Fatalf("unexpected central manifest identity: %#v", manifest)
	}
	schemaPath := checkDigest(t, manifestPath, manifest, "protocol_schema", "protocol_schema_sha256")
	bindingPath := checkDigest(t, manifestPath, manifest, "http_binding", "http_binding_sha256")
	schema := readJSONObject(t, schemaPath)
	binding := readJSONObject(t, bindingPath)
	contract := &centralContract{
		manifestDir:      filepath.Dir(manifestPath),
		protocol:         schemaString(t, schema, "$defs", "protocol", "const"),
		mediaType:        valueString(t, binding, "media_type"),
		requestKeyHeader: valueString(t, binding, "request_key_header"),
		token:            regexp.MustCompile(schemaString(t, schema, "$defs", "token", "pattern")),
		schema:           schema,
		byRequestKind:    map[string]bindingOperation{},
		bySuccessKind:    map[string]bindingOperation{},
		byOperation:      map[string]bindingOperation{},
	}

	operations := valueArray(t, binding, "operations")
	if len(operations) != 5 {
		t.Fatalf("binding operation count = %d, want 5", len(operations))
	}
	paths := make(map[string]struct{}, len(operations))
	for _, rawOperation := range operations {
		operation := valueObject(t, rawOperation)
		entry := bindingOperation{
			Operation:     valueString(t, operation, "operation"),
			Method:        valueString(t, operation, "method"),
			Path:          valueString(t, operation, "path"),
			RequestKind:   valueString(t, operation, "request_kind"),
			SuccessKind:   valueString(t, operation, "success_kind"),
			SuccessStatus: valueInt(t, operation, "success_status"),
		}
		if entry.Method != "POST" {
			t.Fatalf("binding method = %q, want POST", entry.Method)
		}
		if _, exists := paths[entry.Path]; exists {
			t.Fatalf("duplicate binding path %q", entry.Path)
		}
		paths[entry.Path] = struct{}{}
		for name, index := range map[string]map[string]bindingOperation{
			entry.Operation: contract.byOperation, entry.RequestKind: contract.byRequestKind, entry.SuccessKind: contract.bySuccessKind,
		} {
			if _, exists := index[name]; exists {
				t.Fatalf("duplicate binding identity %q", name)
			}
			index[name] = entry
		}
	}

	seenKinds := make(map[string]struct{})
	seenAssertions := make(map[string]struct{})
	seenCases := make(map[string]struct{})
	for _, rawCase := range valueArray(t, manifest, "cases") {
		entry := valueObject(t, rawCase)
		name := valueString(t, entry, "name")
		if _, duplicate := seenCases[name]; duplicate {
			t.Fatalf("duplicate central case %q", name)
		}
		seenCases[name] = struct{}{}
		fixturePath := filepath.Clean(filepath.Join(contract.manifestDir, valueString(t, entry, "file")))
		if !pathWithin(contract.manifestDir, fixturePath) {
			t.Fatalf("fixture escapes central directory: %s", fixturePath)
		}
		fixture := readJSONObject(t, fixturePath)
		if valueString(t, fixture, "name") != name {
			t.Fatalf("fixture name does not match manifest case %q", name)
		}
		central := centralCase{Name: name, Category: valueString(t, entry, "category")}
		for _, rawMessage := range valueArray(t, fixture, "messages") {
			message := valueObject(t, rawMessage)
			body := valueObject(t, message["body"])
			kind := valueString(t, body, "kind")
			seenKinds[kind] = struct{}{}
			parsed := fixtureMessage{
				Label: valueString(t, message, "label"), ExpectedValid: valueBool(t, message, "expected_valid"),
				Headers: map[string]string{}, Body: body,
			}
			if rawHeaders, ok := message["headers"]; ok {
				for header, rawValue := range valueObject(t, rawHeaders) {
					value, ok := rawValue.(string)
					if !ok {
						t.Fatalf("header %q in case %q is %T, want string", header, name, rawValue)
					}
					parsed.Headers[header] = value
				}
			}
			for _, rawAssertion := range valueArray(t, message, "assertions") {
				assertion, ok := rawAssertion.(string)
				if !ok || assertion == "" {
					t.Fatalf("invalid assertion in case %q", name)
				}
				seenAssertions[assertion] = struct{}{}
				parsed.Assertions = append(parsed.Assertions, assertion)
			}
			if kind == "problem" && parsed.ExpectedValid {
				payload := valueObject(t, body["payload"])
				if valueString(t, payload, "side_effects") != "none" {
					t.Fatalf("application Problem lacks zero-new-side-effect guarantee")
				}
			}
			if kind == "execution.lookup.result" && parsed.ExpectedValid {
				payload := valueObject(t, body["payload"])
				if valueString(t, payload, "outcome") == "not_started" {
					fence := valueObject(t, payload["negative_fence"])
					_ = valueString(t, fence, "fence_id")
				}
			}
			central.Messages = append(central.Messages, parsed)
		}
		contract.cases = append(contract.cases, central)
	}

	if len(seenCases) != 7 {
		t.Fatalf("central case count = %d, want 7", len(seenCases))
	}
	for kind := range contract.byRequestKind {
		if _, ok := seenKinds[kind]; !ok {
			t.Errorf("provider request kind %q is not exercised", kind)
		}
	}
	for kind := range contract.bySuccessKind {
		if _, ok := seenKinds[kind]; !ok {
			t.Errorf("provider success kind %q is not exercised", kind)
		}
	}

	// The assertion vocabulary must match the provider checks exactly: a
	// central assertion this gate cannot prove is a drift the hub has to
	// resolve, and a provider check no fixture exercises is a dropped case.
	missing := make([]string, 0)
	for assertion := range assertionChecks {
		if _, ok := seenAssertions[assertion]; !ok {
			missing = append(missing, assertion)
		}
	}
	unmapped := make([]string, 0)
	for assertion := range seenAssertions {
		if _, ok := assertionChecks[assertion]; !ok {
			unmapped = append(unmapped, assertion)
		}
	}
	sort.Strings(missing)
	sort.Strings(unmapped)
	if len(missing) > 0 || len(unmapped) > 0 {
		t.Fatalf("central provider assertions missing from fixtures: %v; fixture assertions without a provider check: %v", missing, unmapped)
	}
	return contract
}

func (c *centralContract) operation(t *testing.T, name string) bindingOperation {
	t.Helper()
	entry, ok := c.byOperation[name]
	if !ok {
		t.Fatalf("HTTP binding has no operation %q", name)
	}
	return entry
}

func (c *centralContract) caseByCategory(t *testing.T, category string) centralCase {
	t.Helper()
	for _, entry := range c.cases {
		if entry.Category == category {
			return entry
		}
	}
	t.Fatalf("central manifest has no case in category %q", category)
	return centralCase{}
}

// enum reads an enum list from the central protocol schema by JSON path.
func (c *centralContract) enum(t *testing.T, path ...string) []string {
	t.Helper()
	raw, ok := jsonPath(c.schema, append(path, "enum")...)
	if !ok {
		t.Fatalf("central schema has no enum at %v", path)
	}
	values := make([]string, 0)
	for _, item := range raw.([]any) {
		value, ok := item.(string)
		if !ok {
			t.Fatalf("central schema enum at %v holds %T", path, item)
		}
		values = append(values, value)
	}
	return values
}

// propertyNames reads an object's declared property names from the schema.
func (c *centralContract) propertyNames(t *testing.T, path ...string) map[string]struct{} {
	t.Helper()
	raw, ok := jsonPath(c.schema, append(path, "properties")...)
	if !ok {
		t.Fatalf("central schema has no properties at %v", path)
	}
	names := make(map[string]struct{})
	for name := range raw.(map[string]any) {
		names[name] = struct{}{}
	}
	return names
}

func (c *centralContract) requiredNames(t *testing.T, path ...string) []string {
	t.Helper()
	raw, ok := jsonPath(c.schema, append(path, "required")...)
	if !ok {
		t.Fatalf("central schema has no required list at %v", path)
	}
	names := make([]string, 0)
	for _, item := range raw.([]any) {
		names = append(names, item.(string))
	}
	return names
}

func (c *centralContract) schemaNumber(t *testing.T, path ...string) float64 {
	t.Helper()
	raw, ok := jsonPath(c.schema, path...)
	value, isNumber := raw.(float64)
	if !ok || !isNumber {
		t.Fatalf("central schema has no number at %v", path)
	}
	return value
}

func (c *centralContract) isToken(value string) bool {
	return value != "" && len(value) <= 256 && c.token.MatchString(value)
}

// jingsiContractManifestEnv points the gate at an explicit central manifest.
// When it is unset the test looks for a sibling InfiniCenter checkout; a fresh
// clone (and CI) has neither, so the gate skips instead of failing the suite.
const jingsiContractManifestEnv = "SPARKCLAW_JINGSI_CONTRACT_MANIFEST"

func locateJingSiRuntimeManifest(t *testing.T) string {
	t.Helper()
	if explicit := strings.TrimSpace(os.Getenv(jingsiContractManifestEnv)); explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil || info.IsDir() {
			t.Fatalf("%s=%q does not name a central manifest file", jingsiContractManifestEnv, explicit)
		}
		return explicit
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	for directory := filepath.Dir(currentFile); ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, "InfiniCenter", "clusters", "ProjectGroup-2", "contracts", "SparkClaw--JingSi", "conformance", "v1", "manifest.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Skipf("sibling InfiniCenter checkout not found; set %s to gate against the central SparkClaw--JingSi contract", jingsiContractManifestEnv)
		}
	}
}

func checkDigest(t *testing.T, manifestPath string, manifest map[string]any, pathKey, digestKey string) string {
	t.Helper()
	path := filepath.Clean(filepath.Join(filepath.Dir(manifestPath), valueString(t, manifest, pathKey)))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", pathKey, err)
	}
	digest := sha256.Sum256(raw)
	if got, want := hex.EncodeToString(digest[:]), valueString(t, manifest, digestKey); got != want {
		t.Fatalf("%s digest = %s, want %s", pathKey, got, want)
	}
	return path
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func readJSONObject(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}

// jsonPath walks decoded JSON by object key or array index.
func jsonPath(value any, path ...string) (any, bool) {
	current := value
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func schemaString(t *testing.T, schema map[string]any, path ...string) string {
	t.Helper()
	raw, ok := jsonPath(schema, path...)
	value, isString := raw.(string)
	if !ok || !isString || value == "" {
		t.Fatalf("central schema has no string at %v", path)
	}
	return value
}

func valueObject(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value is %T, want object", value)
	}
	return result
}

func valueArray(t *testing.T, object map[string]any, key string) []any {
	t.Helper()
	result, ok := object[key].([]any)
	if !ok {
		t.Fatalf("%s is %T, want array", key, object[key])
	}
	return result
}

func valueString(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	result, ok := object[key].(string)
	if !ok || result == "" {
		t.Fatalf("%s is %T, want non-empty string", key, object[key])
	}
	return result
}

func valueBool(t *testing.T, object map[string]any, key string) bool {
	t.Helper()
	result, ok := object[key].(bool)
	if !ok {
		t.Fatalf("%s is %T, want bool", key, object[key])
	}
	return result
}

func valueInt(t *testing.T, object map[string]any, key string) int {
	t.Helper()
	result, ok := object[key].(float64)
	if !ok || result != float64(int(result)) {
		t.Fatalf("%s is %T, want integer", key, object[key])
	}
	return int(result)
}
