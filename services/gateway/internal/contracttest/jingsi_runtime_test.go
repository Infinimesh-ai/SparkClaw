package contracttest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestJingSiRuntimeProviderConsumesCentralContract(t *testing.T) {
	t.Parallel()

	manifestPath := locateJingSiRuntimeManifest(t)
	manifest := readJSONObject(t, manifestPath)
	if valueString(t, manifest, "artifact") != "sparkclaw-jingsi-conformance" ||
		valueString(t, manifest, "wire_major") != "v1" {
		t.Fatalf("unexpected central manifest identity: %#v", manifest)
	}
	checkDigest(t, manifestPath, manifest, "protocol_schema", "protocol_schema_sha256")
	bindingPath := checkDigest(t, manifestPath, manifest, "http_binding", "http_binding_sha256")
	binding := readJSONObject(t, bindingPath)
	operations := valueArray(t, binding, "operations")
	if len(operations) != 5 {
		t.Fatalf("binding operation count = %d, want 5", len(operations))
	}

	requestKinds := make(map[string]struct{}, len(operations))
	successKinds := make(map[string]struct{}, len(operations))
	paths := make(map[string]struct{}, len(operations))
	for _, rawOperation := range operations {
		operation := valueObject(t, rawOperation)
		if method := valueString(t, operation, "method"); method != "POST" {
			t.Fatalf("binding method = %q, want POST", method)
		}
		path := valueString(t, operation, "path")
		if _, exists := paths[path]; exists {
			t.Fatalf("duplicate binding path %q", path)
		}
		paths[path] = struct{}{}
		requestKinds[valueString(t, operation, "request_kind")] = struct{}{}
		successKinds[valueString(t, operation, "success_kind")] = struct{}{}
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
		fixturePath := filepath.Clean(filepath.Join(filepath.Dir(manifestPath), valueString(t, entry, "file")))
		if !pathWithin(filepath.Dir(manifestPath), fixturePath) {
			t.Fatalf("fixture escapes central directory: %s", fixturePath)
		}
		fixture := readJSONObject(t, fixturePath)
		if valueString(t, fixture, "name") != name {
			t.Fatalf("fixture name does not match manifest case %q", name)
		}
		for _, rawMessage := range valueArray(t, fixture, "messages") {
			message := valueObject(t, rawMessage)
			body := valueObject(t, message["body"])
			kind := valueString(t, body, "kind")
			seenKinds[kind] = struct{}{}
			for _, rawAssertion := range valueArray(t, message, "assertions") {
				assertion, ok := rawAssertion.(string)
				if !ok || assertion == "" {
					t.Fatalf("invalid assertion in case %q", name)
				}
				seenAssertions[assertion] = struct{}{}
			}
			if kind == "problem" && valueBool(t, message, "expected_valid") {
				payload := valueObject(t, body["payload"])
				if valueString(t, payload, "side_effects") != "none" {
					t.Fatalf("application Problem lacks zero-new-side-effect guarantee")
				}
			}
			if kind == "execution.lookup.result" && valueBool(t, message, "expected_valid") {
				payload := valueObject(t, body["payload"])
				if valueString(t, payload, "outcome") == "not_started" {
					fence := valueObject(t, payload["negative_fence"])
					_ = valueString(t, fence, "fence_id")
				}
			}
		}
	}

	if len(seenCases) != 7 {
		t.Fatalf("central case count = %d, want 7", len(seenCases))
	}
	for kind := range requestKinds {
		if _, ok := seenKinds[kind]; !ok {
			t.Errorf("provider request kind %q is not exercised", kind)
		}
	}
	for kind := range successKinds {
		if _, ok := seenKinds[kind]; !ok {
			t.Errorf("provider success kind %q is not exercised", kind)
		}
	}

	requiredAssertions := []string{
		"authorization.denial_no_side_effects",
		"cancel.idempotent",
		"events.monotonic_cursor",
		"idempotency.exact_replay_same_handle",
		"idempotency.semantic_drift_conflict",
		"lookup.lost_response_recovers_handle",
		"lookup.not_started_negative_fence",
		"lookup.unresolved_blocks_replay",
		"no_handle.zero_side_effects_requires_negative_fence",
		"problem.no_new_side_effects",
		"request.unknown_field_rejected",
		"response.optional_field_ignored",
		"transport.failure_not_negative_proof",
	}
	missing := make([]string, 0)
	for _, assertion := range requiredAssertions {
		if _, ok := seenAssertions[assertion]; !ok {
			missing = append(missing, assertion)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("central provider assertions missing: %v", missing)
	}
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
