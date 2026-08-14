package workspacefiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSearchUsesFilenamesOnlyAndStablePathTieBreak(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"b/annual-report.pdf", "a/annual-report.pdf", "notes.txt"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		content := []byte("ordinary content")
		if rel == "notes.txt" {
			content = []byte("annual report appears only in content")
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Search(t.Context(), root, SearchRequest{Mode: MatchFuzzy, Term: "annual report", MaxResults: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Truncated || len(result.Matches) != 2 ||
		result.Matches[0].RelPath != "a/annual-report.pdf" || result.Matches[1].RelPath != "b/annual-report.pdf" {
		t.Fatalf("filename-only search result = %#v", result)
	}
}

func TestExactSearchIsCaseSensitiveAndDoesNotBroaden(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Report.pdf", "report-final.pdf"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Search(t.Context(), root, SearchRequest{Mode: MatchExact, Term: "report.pdf", MaxResults: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.Matches) != 0 {
		t.Fatalf("case-sensitive exact search unexpectedly broadened: %#v", result)
	}
}
