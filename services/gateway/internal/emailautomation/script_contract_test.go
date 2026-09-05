package emailautomation

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// providerUnavailableScriptCodes are the codes the fixed scripts emit that
// intentionally classify as a provider outage: the browser runtime failed
// around the script rather than the page contract, the input, or the send
// verification. Every entry must still be emitted by some script.
var providerUnavailableScriptCodes = map[string]bool{
	"browser_runtime_unavailable":     true,
	"email_browser_failed":            true,
	"email_probe_configuration_error": true,
	"email_send_configuration_error":  true,
	"email_tab_cleanup_failed":        true,
	"login_probe_browser_failure":     true,
	// The Controller envelope substitutes this code when a script error
	// carries no well-formed code of its own.
	"provider_script_failed": true,
}

// providerUnavailableScriptCodeFamilies lists the template-built code
// families that cannot be enumerated statically; they classify as a provider
// outage by construction.
var providerUnavailableScriptCodeFamilies = map[string]bool{
	"${phase}_failed": true,
}

func TestScriptErrorCodesCoverEveryEmittedCode(t *testing.T) {
	root := repositoryRoot(t)
	scriptSources := readSources(t, filepath.Join(root, "scripts", "email"), ".mjs")
	controllerDir := filepath.Join(root, "tools", "browser-controller", "src")
	controllerSources := map[string]string{}
	for _, name := range []string{"cli-task.mjs", "cli-client.mjs", "provider-scripts.mjs"} {
		raw, err := os.ReadFile(filepath.Join(controllerDir, name))
		if err != nil {
			t.Fatal(err)
		}
		controllerSources[name] = string(raw)
	}

	emitted := map[string][]string{}
	families := map[string][]string{}
	for path, source := range scriptSources {
		for _, code := range scriptErrorCodeLiterals(source) {
			emitted[code] = append(emitted[code], path)
		}
		for _, family := range scriptErrorCodeTemplates(source) {
			families[family] = append(families[family], path)
		}
	}
	for path, source := range controllerSources {
		for _, code := range envelopeCodeLiterals(source) {
			emitted[code] = append(emitted[code], path)
		}
	}
	if !strings.Contains(controllerSources["provider-scripts.mjs"], `"provider_script_failed"`) {
		t.Fatal("provider-scripts.mjs no longer falls back to provider_script_failed; update the allowlist")
	}
	emitted["provider_script_failed"] = append(emitted["provider_script_failed"], "provider-scripts.mjs")
	if len(emitted) < 20 {
		t.Fatalf("extracted only %d script error codes; the extractor no longer matches the scripts", len(emitted))
	}

	for _, code := range sortedKeys(emitted) {
		mapped := normalizeScriptErrorCode(code)
		if mapped == app.ToolErrorEmailProviderUnavailable && !providerUnavailableScriptCodes[code] {
			t.Errorf("script code %q (%s) falls through to provider-unavailable; map it in scriptErrorCodes or allowlist it", code, strings.Join(emitted[code], ", "))
		}
	}
	for code := range providerUnavailableScriptCodes {
		if len(emitted[code]) == 0 {
			t.Errorf("allowlisted code %q is no longer emitted by any script", code)
		}
		if normalizeScriptErrorCode(code) != app.ToolErrorEmailProviderUnavailable {
			t.Errorf("allowlisted code %q is mapped explicitly; remove it from the allowlist", code)
		}
	}
	for family := range families {
		if !providerUnavailableScriptCodeFamilies[family] {
			t.Errorf("template code family %q (%s) is not allowlisted", family, strings.Join(families[family], ", "))
		}
	}
	for family := range providerUnavailableScriptCodeFamilies {
		if len(families[family]) == 0 {
			t.Errorf("allowlisted code family %q is no longer built by any script", family)
		}
	}
	for source := range scriptErrorCodes {
		if len(emitted[source]) == 0 {
			t.Errorf("scriptErrorCodes maps %q, which no script emits", source)
		}
		if normalizeScriptErrorCode(source) != scriptErrorCodes[source] {
			t.Errorf("scriptErrorCodes entry %q is shadowed by the canonical pass-through", source)
		}
	}
}

var (
	scriptErrorConstructorPattern = regexp.MustCompile(`new (?:QQMailScriptError|OutlookCliError|GmailCliError)\(\s*"([a-z0-9_]+)"`)
	scriptErrorTemplatePattern    = regexp.MustCompile("new QQMailScriptError\\(\\s*`([^`]*)`")
	envelopeCodePattern           = regexp.MustCompile(`\bcode:\s*"([a-z0-9_]+)"`)
	classifierReturnPattern       = regexp.MustCompile(`\breturn "([a-z0-9]+(?:_[a-z0-9]+)+)"`)
	codeParameterHelperPattern    = regexp.MustCompile(`function (\w+)\(([^)]*)\)`)
	codeParameterPattern          = regexp.MustCompile(`^(?:errorCode|invalidCode)(?:\s*=\s*"([a-z0-9_]+)")?$`)
	literalArgumentPattern        = regexp.MustCompile(`^"([a-z0-9_]+)"$`)
)

// scriptErrorCodeLiterals extracts every failure code a provider script can
// place in the Controller failure envelope: literal error-class constructor
// codes, `code:` properties, classifier return values, and literal codes
// passed to helpers whose trailing parameter names the code to throw.
func scriptErrorCodeLiterals(source string) []string {
	codes := []string{}
	for _, match := range scriptErrorConstructorPattern.FindAllStringSubmatch(source, -1) {
		codes = append(codes, match[1])
	}
	codes = append(codes, envelopeCodeLiterals(source)...)
	for _, match := range classifierReturnPattern.FindAllStringSubmatch(source, -1) {
		codes = append(codes, match[1])
	}
	for _, match := range codeParameterHelperPattern.FindAllStringSubmatch(source, -1) {
		parameters := strings.Split(match[2], ",")
		last := strings.TrimSpace(parameters[len(parameters)-1])
		parameter := codeParameterPattern.FindStringSubmatch(last)
		if parameter == nil {
			continue
		}
		if parameter[1] != "" {
			codes = append(codes, parameter[1])
		}
		codes = append(codes, trailingLiteralArguments(source, match[1])...)
	}
	return codes
}

func envelopeCodeLiterals(source string) []string {
	codes := []string{}
	for _, match := range envelopeCodePattern.FindAllStringSubmatch(source, -1) {
		codes = append(codes, match[1])
	}
	return codes
}

func scriptErrorCodeTemplates(source string) []string {
	families := []string{}
	for _, match := range scriptErrorTemplatePattern.FindAllStringSubmatch(source, -1) {
		families = append(families, match[1])
	}
	return families
}

// trailingLiteralArguments returns the literal final argument of every call
// to helper within source, skipping the helper's own declaration.
func trailingLiteralArguments(source, helper string) []string {
	codes := []string{}
	needle := helper + "("
	for offset := 0; ; {
		index := strings.Index(source[offset:], needle)
		if index < 0 {
			return codes
		}
		index += offset
		offset = index + len(needle)
		if index > 0 && (isIdentifierByte(source[index-1]) || strings.HasSuffix(source[:index], "function ")) {
			continue
		}
		arguments, ok := balancedArguments(source[offset:])
		if !ok {
			continue
		}
		last := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(arguments[len(arguments)-1]), ","))
		if match := literalArgumentPattern.FindStringSubmatch(last); match != nil {
			codes = append(codes, match[1])
		}
	}
}

// balancedArguments splits the text after an opening parenthesis into its
// top-level comma-separated arguments, stopping at the matching close.
func balancedArguments(text string) ([]string, bool) {
	depth := 0
	start := 0
	arguments := []string{}
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				arguments = append(arguments, text[start:index])
				return arguments, true
			}
			depth--
		case ',':
			if depth == 0 {
				arguments = append(arguments, text[start:index])
				start = index + 1
			}
		}
	}
	return nil, false
}

func isIdentifierByte(value byte) bool {
	return value == '_' || value == '.' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func readSources(t *testing.T, dir, extension string) map[string]string {
	t.Helper()
	sources := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != extension {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(dir, path)
		sources[relative] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) == 0 {
		t.Fatalf("no %s sources under %s", extension, dir)
	}
	return sources
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "email")); err != nil {
		t.Fatalf("repository root %s has no scripts/email: %v", root, err)
	}
	return root
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
