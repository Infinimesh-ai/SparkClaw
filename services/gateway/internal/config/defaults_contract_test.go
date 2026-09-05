package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// repositoryPath resolves a path relative to the repository root.
func repositoryPath(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "..", ".."}, parts...)...)
}

// diffConfig lists every leaf that differs between base and other as
// "path: base -> other". Unexported fields are included because they carry
// load-time state (for example runObservationCompactionExplicit).
func diffConfig(base, other Config) map[string]string {
	out := map[string]string{}
	diffValue("", reflect.ValueOf(base), reflect.ValueOf(other), out)
	return out
}

func diffValue(path string, a, b reflect.Value, out map[string]string) {
	switch a.Kind() {
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			name := a.Type().Field(i).Name
			diffValue(joinPath(path, name), a.Field(i), b.Field(i), out)
		}
	case reflect.Map:
		keys := map[string]reflect.Value{}
		for _, key := range a.MapKeys() {
			keys[fmt.Sprint(key)] = key
		}
		for _, key := range b.MapKeys() {
			keys[fmt.Sprint(key)] = key
		}
		names := make([]string, 0, len(keys))
		for name := range keys {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			av, bv := a.MapIndex(keys[name]), b.MapIndex(keys[name])
			switch {
			case !av.IsValid():
				out[joinPath(path, name)] = "<absent> -> " + formatLeaf(bv)
			case !bv.IsValid():
				out[joinPath(path, name)] = formatLeaf(av) + " -> <absent>"
			default:
				diffValue(joinPath(path, name), av, bv, out)
			}
		}
	default:
		if av, bv := formatLeaf(a), formatLeaf(b); av != bv {
			out[path] = av + " -> " + bv
		}
	}
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func formatLeaf(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Bool:
		return fmt.Sprint(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprint(v.Int())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprint(v.Float())
	case reflect.String:
		return v.String()
	case reflect.Slice:
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = formatLeaf(v.Index(i))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// readEnvFile parses KEY=VALUE lines, keeping declaration order.
func readEnvFile(t *testing.T, path string) (keys []string, values map[string]string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	values = map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("%s: malformed line %q", path, line)
		}
		keys = append(keys, key)
		values[key] = value
	}
	return keys, values
}

var composeEnvLine = regexp.MustCompile(`^\s{6}([A-Z0-9_]+): (.*)$`)
var composeFallback = regexp.MustCompile(`^\$\{([A-Z0-9_]+):-(.*)\}$`)
var composeRequired = regexp.MustCompile(`^\$\{([A-Z0-9_]+):\?.*\}$`)

// readComposeGatewayEnvironment returns the gateway service environment of
// docker/compose.yaml as declaration-ordered keys plus the value each key
// takes when nothing is exported: the ${NAME:-default} fallback, the
// literal, or "" for ${NAME:?required} entries.
func readComposeGatewayEnvironment(t *testing.T, path string) (keys []string, values map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values = map[string]string{}
	inGateway, inEnvironment := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case line == "  gateway:":
			inGateway = true
			continue
		case inGateway && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":"):
			inGateway = false
		}
		if !inGateway {
			continue
		}
		if line == "    environment:" {
			inEnvironment = true
			continue
		}
		if inEnvironment && strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") {
			inEnvironment = false
		}
		if !inEnvironment {
			continue
		}
		match := composeEnvLine.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("%s: unexpected gateway environment line %q", path, line)
		}
		key, value := match[1], strings.TrimSpace(match[2])
		switch {
		case composeRequired.MatchString(value):
			value = ""
		case composeFallback.MatchString(value):
			sub := composeFallback.FindStringSubmatch(value)
			if sub[1] != key {
				t.Fatalf("%s: %s falls back to a different variable %s", path, key, sub[1])
			}
			value = sub[2]
		}
		value = strings.Trim(value, `"`)
		keys = append(keys, key)
		values[key] = value
	}
	if len(keys) == 0 {
		t.Fatalf("%s: gateway environment block not found", path)
	}
	return keys, values
}

func envBindingNames() map[string]bool {
	names := map[string]bool{}
	for _, binding := range envBindings {
		names[binding.name] = true
		for _, alias := range binding.aliases {
			names[alias] = true
		}
	}
	return names
}

// defaultsAfterEmptyEnvironment is Default() with the two paths that
// applyEnvBindings always resolves to absolute form, so applying a file
// through the bindings can be diffed against it without path noise.
func defaultsAfterEmptyEnvironment(t *testing.T) Config {
	t.Helper()
	cfg := Default()
	if err := applyEnvBindings(&cfg, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func applyValuesThroughBindings(t *testing.T, values map[string]string) Config {
	t.Helper()
	cfg := Default()
	if err := applyEnvBindings(&cfg, func(name string) string { return values[name] }); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// nonGatewayProductKeys are product.env entries the gateway binary never
// reads directly; each is consumed by the listed component instead.
var nonGatewayProductKeys = map[string]string{
	"SPARKCLAW_WEBCHAT_BIND":                             "compose webchat port mapping",
	"SPARKCLAW_WEBCHAT_PORT":                             "compose webchat port mapping",
	"SPARKCLAW_JINGSI_LAN_BIND":                          "compose.jingsi-lan.yaml listener",
	"SPARKCLAW_JINGSI_LAN_PORT":                          "compose.jingsi-lan.yaml and webchat nginx template",
	"SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT":              "sandbox-runner",
	"SPARKCLAW_SANDBOX_CONTAINER_WORKSPACE_ROOT":         "sandbox-runner",
	"SPARKCLAW_EMBEDDING_SERVED_NAME":                    "vLLM served-model-name in compose.models.local.yaml",
	"SPARKCLAW_GUARD_SERVED_NAME":                        "vLLM served-model-name in compose.models.local.yaml",
	"SPARKCLAW_ASR_SERVED_NAME":                          "vLLM served-model-name in compose.models.local.yaml",
	"SPARKCLAW_OCR_SERVED_NAME":                          "vLLM served-model-name in compose.models.local.yaml",
	"SPARKCLAW_ISCP_AUTHORITY_TOKEN":                     "read indirectly through SPARKCLAW_ISCP_AUTHORITY_TOKEN_ENV",
	"LOCALMIND_MCP_URL":                                  "read indirectly through mcp_servers.localmind.url_env",
	"LOCALMIND_MCP_TOKEN":                                "read indirectly through mcp_servers.localmind.bearer_token_env",
	"SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE":         "secretFromEnvOrFile in applyInfinimeshInfoCredentials",
	"SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST":       "compose volume mount and browser setup scripts",
	"SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST": "browser setup and doctor scripts",
	"SPARKCLAW_EVALUATOR_GATEWAY_URL":                    "cmd/evaluator",
	"SPARKCLAW_EVALUATOR_PROFILE":                        "cmd/evaluator",
	"SPARKCLAW_EVALUATOR_OUTPUT":                         "cmd/evaluator",
	"SPARKCLAW_EVALUATOR_CONFIG":                         "cmd/evaluator",
}

// nonGatewayComposeKeys are gateway-service compose entries that no env
// binding reads directly.
var nonGatewayComposeKeys = map[string]string{
	"OPENAI_API_KEY":                             "modelrouter reads it per request",
	"SPARKCLAW_DEPLOYMENT_PROFILE":               "deployment entrypoint marker",
	"SPARKCLAW_SPEECH_EXPECTED_RUNTIME_VERSION":  "scripts/doctor.sh",
	"SPARKCLAW_ISCP_AUTHORITY_TOKEN":             "read indirectly through SPARKCLAW_ISCP_AUTHORITY_TOKEN_ENV",
	"LOCALMIND_MCP_URL":                          "read indirectly through mcp_servers.localmind.url_env",
	"LOCALMIND_MCP_TOKEN":                        "read indirectly through mcp_servers.localmind.bearer_token_env",
	"SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE": "secretFromEnvOrFile in applyInfinimeshInfoCredentials",
}

func assertKnownKeys(t *testing.T, source string, keys []string, allowed map[string]string) {
	t.Helper()
	known := envBindingNames()
	for _, key := range keys {
		if known[key] {
			continue
		}
		if _, ok := allowed[key]; ok {
			continue
		}
		t.Errorf("%s sets %s, which no env binding reads and no allowlist explains; add a binding or document the consumer", source, key)
	}
	for key := range allowed {
		if known[key] {
			t.Errorf("%s allowlist entry %s is now a live env binding; drop it from the allowlist", source, key)
		}
	}
}

func assertDiff(t *testing.T, source string, got, want map[string]string) {
	t.Helper()
	for path, change := range got {
		if expected, ok := want[path]; !ok {
			t.Errorf("%s changes %s (%s) but the override is not documented in this test", source, path, change)
		} else if expected != change {
			t.Errorf("%s changes %s to %s, expected %s", source, path, change, expected)
		}
	}
	for path, expected := range want {
		if _, ok := got[path]; !ok {
			t.Errorf("%s no longer overrides %s (%s); update the test if that is intended", source, path, expected)
		}
	}
}

// TestGoDefaultsMatchRepositoryDefaultJSON pins the exact set of fields
// where configs/sparkclaw.default.json intentionally departs from
// Default(). Any other difference between the two is a silent drift.
func TestGoDefaultsMatchRepositoryDefaultJSON(t *testing.T) {
	raw, err := os.ReadFile(repositoryPath("configs", "sparkclaw.default.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	diff := diffConfig(Default(), cfg)
	if _, ok := diff["MCPServers.localmind"]; !ok {
		t.Fatal("the repository default should declare the localmind MCP server")
	}
	delete(diff, "MCPServers.localmind")
	assertDiff(t, "configs/sparkclaw.default.json", diff, map[string]string{
		// The shipped file resolves the catalog next to itself.
		"Model.CapacityCatalog": "configs/model.profiles.json -> model.profiles.json",
		// The repository default runs mock models; the Go default describes
		// the local DGX Spark layout that the deployment profiles override.
		"Model.CapacityProfile":   "dgx-spark-dual-light-v1 -> mock",
		"Model.Mock":              "false -> true",
		"Model.Fast.BaseURL":      "http://127.0.0.1:8001/v1 -> ",
		"Model.Deep.BaseURL":      "http://127.0.0.1:8002/v1 -> ",
		"Model.Embedding.BaseURL": "http://127.0.0.1:8003/v1 -> ",
		"Model.Fast.Model":        "nvidia/Qwen3.6-35B-A3B-NVFP4 -> sparkclaw-fast",
		"Model.Deep.Model":        "nvidia/Qwen3.6-35B-A3B-NVFP4 -> sparkclaw-deep",
		"Model.Embedding.Model":   "Qwen/Qwen3-Embedding-0.6B -> sparkclaw-embedding",
		// The file spells out the compaction budget, which marks it explicit.
		"Runtime.runObservationCompactionExplicit": "false -> true",
		"Tools.BrowserAutomation.Enabled":          "false -> true",
	})
}

// TestProductEnvOverridesAreExplicit feeds docker/env/sparkclaw.product.env
// through the env bindings and pins every field it moves away from
// Default(). The stage evidence budget is the motivating case: the Go and
// JSON default stay at 8000 bytes for the local 32K-context profile, while
// the hosted product profile deliberately raises it to the extracted-
// document contract so a whole document can be provisioned to one stage.
func TestProductEnvOverridesAreExplicit(t *testing.T) {
	path := repositoryPath("docker", "env", "sparkclaw.product.env")
	keys, values := readEnvFile(t, path)
	assertKnownKeys(t, "product.env", keys, nonGatewayProductKeys)

	composeKeys, _ := readComposeGatewayEnvironment(t, repositoryPath("docker", "compose.yaml"))
	passed := map[string]bool{}
	for _, key := range composeKeys {
		passed[key] = true
	}
	known := envBindingNames()
	for _, key := range keys {
		if known[key] && !passed[key] {
			t.Errorf("product.env sets gateway knob %s but docker/compose.yaml never passes it to the gateway container", key)
		}
	}

	base := defaultsAfterEmptyEnvironment(t)
	assertDiff(t, "product.env", diffConfig(base, applyValuesThroughBindings(t, values)), map[string]string{
		"Adapters.DocumentOCR.Enabled":             "false -> true",
		"Adapters.PPTXVisualQA.AllowedHosts":       "[] -> [gotenberg]",
		"Adapters.PPTXVisualQA.BaseURL":            " -> http://gotenberg:3000",
		"Adapters.PPTXVisualQA.Phase":              "disabled -> shadow",
		"Gateway.Bind":                             "127.0.0.1 -> 0.0.0.0",
		"Gateway.PairingRequired":                  "false -> true",
		"ISCPPairing.TokenEnv":                     " -> SPARKCLAW_ISCP_AUTHORITY_TOKEN",
		"Model.CapacityProfile":                    "dgx-spark-dual-light-v1 -> sparkclaw-product-v1",
		"Model.Fast.Model":                         "nvidia/Qwen3.6-35B-A3B-NVFP4 -> sparkclaw-fast",
		"Model.Deep.Model":                         "nvidia/Qwen3.6-35B-A3B-NVFP4 -> sparkclaw-fast",
		"Model.Embedding.Model":                    "Qwen/Qwen3-Embedding-0.6B -> sparkclaw-embedding",
		"Model.Guard.Model":                        "Qwen/Qwen3Guard-Gen-0.6B -> sparkclaw-guard",
		"Runtime.StageEvidenceMaxBytes":            fmt.Sprintf("8000 -> %d", document.SmallExtractedMaxBytes),
		"Runtime.RunObservationCompactionBytes":    "36000 -> 72000",
		"Runtime.RunMaxObservationBytes":           "48000 -> 96000",
		"Runtime.runObservationCompactionExplicit": "false -> true",
		"Sandbox.Backend":                          "local-docker -> http",
		"Sandbox.RunnerURL":                        " -> http://sandbox-runner:18889",
		"Security.ToolPolicyPath":                  "./configs/tools.policy.json -> /var/lib/sparkclaw/memory/tools.policy.json",
		"Speech.Enabled":                           "false -> true",
		"State.Backend":                            "file -> postgres",
		"State.CredentialKeyFile":                  base.State.CredentialKeyFile + " -> /var/lib/sparkclaw/memory/gateway-credentials.key",
		"State.DSN":                                " -> postgres://sparkclaw:sparkclaw@postgres:5432/sparkclaw?sslmode=disable",
		"State.Path":                               "./data/memory/gateway-state.json -> /var/lib/sparkclaw/memory/gateway-state.json",
		"Storage.ArtifactDir":                      base.Storage.ArtifactDir + " -> /var/lib/sparkclaw/artifacts",
		"Storage.S3AccessKey":                      " -> sparkclaw",
		"Storage.S3Endpoint":                       " -> http://minio:9000",
		"Storage.S3SecretKey":                      " -> sparkclaw-local",
		"Storage.TraceDir":                         "./data/traces -> /var/lib/sparkclaw/traces",
		"Workspaces.Allowlist":                     "[./data/workspaces] -> [/var/lib/sparkclaw/workspaces]",
		"Workspaces.DefaultRoot":                   "./data/workspaces -> /var/lib/sparkclaw/workspaces",
	})
}

// TestComposeGatewayFallbacksMatchGoDefaults checks the ${NAME:-default}
// fallbacks docker/compose.yaml gives the gateway when a deployment exports
// nothing. Apart from the container layout, model service names, and the
// product security posture, they must equal Default().
func TestComposeGatewayFallbacksMatchGoDefaults(t *testing.T) {
	keys, values := readComposeGatewayEnvironment(t, repositoryPath("docker", "compose.yaml"))
	assertKnownKeys(t, "docker/compose.yaml", keys, nonGatewayComposeKeys)

	base := defaultsAfterEmptyEnvironment(t)
	assertDiff(t, "docker/compose.yaml", diffConfig(base, applyValuesThroughBindings(t, values)), map[string]string{
		"Adapters.PPTXVisualQA.AllowedHosts": "[] -> [gotenberg]",
		"Adapters.PPTXVisualQA.BaseURL":      " -> http://gotenberg:3000",
		"Adapters.PPTXVisualQA.Phase":        "disabled -> shadow",
		"Gateway.Bind":                       "127.0.0.1 -> 0.0.0.0",
		"Gateway.PairingRequired":            "false -> true",
		"Model.Fast.BaseURL":                 "http://127.0.0.1:8001/v1 -> http://sparkclaw-fast:8001/v1",
		"Model.Deep.BaseURL":                 "http://127.0.0.1:8002/v1 -> http://sparkclaw-fast:8001/v1",
		"Model.Embedding.BaseURL":            "http://127.0.0.1:8003/v1 -> http://sparkclaw-embedding:8003/v1",
		"Model.Guard.BaseURL":                "http://127.0.0.1:8005/v1 -> http://sparkclaw-guard:8005/v1",
		"Model.Fast.Model":                   "nvidia/Qwen3.6-35B-A3B-NVFP4 -> sparkclaw-fast",
		"Model.Deep.Model":                   "nvidia/Qwen3.6-35B-A3B-NVFP4 -> sparkclaw-fast",
		"Model.Embedding.Model":              "Qwen/Qwen3-Embedding-0.6B -> sparkclaw-embedding",
		"Sandbox.Backend":                    "local-docker -> http",
		"Sandbox.RunnerURL":                  " -> http://sandbox-runner:18889",
		"Security.ToolPolicyPath":            "./configs/tools.policy.json -> /var/lib/sparkclaw/memory/tools.policy.json",
		"State.CredentialKeyFile":            base.State.CredentialKeyFile + " -> /var/lib/sparkclaw/memory/gateway-credentials.key",
		"State.DSN":                          " -> postgres://sparkclaw:sparkclaw@postgres:5432/sparkclaw?sslmode=disable",
		"State.Path":                         "./data/memory/gateway-state.json -> /var/lib/sparkclaw/memory/gateway-state.json",
		"Storage.ArtifactDir":                base.Storage.ArtifactDir + " -> /var/lib/sparkclaw/artifacts",
		"Storage.S3AccessKey":                " -> sparkclaw",
		"Storage.S3Endpoint":                 " -> http://minio:9000",
		"Storage.S3SecretKey":                " -> sparkclaw-local",
		"Storage.TraceDir":                   "./data/traces -> /var/lib/sparkclaw/traces",
		"Workspaces.Allowlist":               "[./data/workspaces] -> [/var/lib/sparkclaw/workspaces]",
		"Workspaces.DefaultRoot":             "./data/workspaces -> /var/lib/sparkclaw/workspaces",
	})
}
