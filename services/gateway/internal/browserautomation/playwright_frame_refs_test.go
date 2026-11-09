package browserautomation

import "testing"

func TestPlaywrightChildFrameReferencesKeepNamespace(t *testing.T) {
	tree := []any{map[string]any{"role": "iframe", "ref": "e8", "children": []any{
		map[string]any{"role": "button", "name": "Frame action", "ref": "f1e2"},
		map[string]any{"role": "button", "name": "Frame action", "ref": "f2e2"},
		map[string]any{"role": "button", "name": "Top action", "ref": "e2"},
		map[string]any{"ref": "css=button"}, map[string]any{"ref": "f0e1"}, map[string]any{"ref": "f1e0"},
	}}}
	refs := buildBrowserSnapshotRefs(playwrightSnapshotRefs(tree), "")
	expected := []string{"e2", "e8", "f1e2", "f2e2"}
	if len(refs) != len(expected) {
		t.Fatalf("refs: %#v", refs)
	}
	for i, ref := range refs {
		if ref.RawRef != expected[i] {
			t.Fatalf("ref %d = %s, want %s", i, ref.RawRef, expected[i])
		}
	}
	if refs[2].Fingerprint == refs[3].Fingerprint {
		t.Fatal("distinct frame controls share a fingerprint")
	}
}
