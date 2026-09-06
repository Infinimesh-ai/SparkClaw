package toolhub

import (
	"io/fs"
	"path"
	"regexp"
	"slices"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// The Python adapters are the last places that spell out the repair
// operation names: visual_repair.py dispatches on them and visual_qa.py
// advertises them as per-shape edit capabilities. Neither can import the Go
// table, so this test reads the embedded sources and asserts that both name
// sets are exactly the app vocabulary.
func TestPPTXVisualRepairOperationVocabularyMatchesPythonAdapters(t *testing.T) {
	want := app.PPTXVisualRepairOperationNames()
	slices.Sort(want)

	repair := readEmbeddedPPTXSlideModule(t, "visual_repair.py")
	dispatch := regexp.MustCompile(`\bop == "([a-z_]+)"|\bop in \(([^)]+)\)`)
	quoted := regexp.MustCompile(`"([a-z_]+)"`)
	dispatched := []string{}
	for _, match := range dispatch.FindAllStringSubmatch(repair, -1) {
		if match[1] != "" {
			dispatched = appendUniqueString(dispatched, match[1])
			continue
		}
		for _, name := range quoted.FindAllStringSubmatch(match[2], -1) {
			dispatched = appendUniqueString(dispatched, name[1])
		}
	}
	slices.Sort(dispatched)
	if !slices.Equal(dispatched, want) {
		t.Fatalf("visual_repair.py dispatches %#v, app table has %#v", dispatched, want)
	}

	qa := readEmbeddedPPTXSlideModule(t, "visual_qa.py")
	block := regexp.MustCompile(`(?s)"edit_capabilities": \[(.*?)\]`).FindStringSubmatch(qa)
	if block == nil {
		t.Fatal("visual_qa.py no longer declares edit_capabilities")
	}
	advertised := []string{}
	for _, match := range regexp.MustCompile(`\("([a-z_]+)",`).FindAllStringSubmatch(block[1], -1) {
		advertised = appendUniqueString(advertised, match[1])
	}
	slices.Sort(advertised)
	if !slices.Equal(advertised, want) {
		t.Fatalf("visual_qa.py advertises %#v, app table has %#v", advertised, want)
	}
}

func readEmbeddedPPTXSlideModule(t *testing.T, name string) string {
	t.Helper()
	raw, err := fs.ReadFile(pptxSlideAdapterPackage, path.Join(pptxSlideAdapterPackageRoot, name))
	if err != nil {
		t.Fatalf("read embedded %s: %v", name, err)
	}
	return string(raw)
}

func appendUniqueString(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
