package document

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeProducesStableLocatableIDs(t *testing.T) {
	metadata := Metadata{Path: "/workspace/note.docx", Relative: "note.docx", Format: "docx", Size: 128}
	raw := map[string]any{
		"source": "test",
		"paragraphs": []any{
			map[string]any{"index": 1, "text": "Heading", "style": "Heading 1", "location": map[string]any{"paragraph_index": 1, "path": "document.p[1]"}},
			map[string]any{"index": 2, "text": "Target sentence", "location": map[string]any{"paragraph_index": 2, "path": "document.p[2]"}},
		},
		"blocks": []any{
			map[string]any{"text": "Heading", "style": "Heading 1", "location": map[string]any{"block_type": "paragraph", "paragraph_index": 1, "path": "document.p[1]"}},
			map[string]any{"text": "Target sentence", "location": map[string]any{"block_type": "paragraph", "paragraph_index": 2, "path": "document.p[2]"}},
		},
	}
	first, err := Normalize(metadata, "small_file_v1", "Heading\nTarget sentence", raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Normalize(metadata, "small_file_v1", "Heading\nTarget sentence", raw)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.ID != second.ID || first.Blocks[1].ID == "" || first.Blocks[1].ID != second.Blocks[1].ID {
		t.Fatalf("structured IDs are not stable: first=%#v second=%#v", first, second)
	}
	if len(first.Paragraphs) != 2 || first.Paragraphs[1]["id"] == "" || len(first.Sections) != 1 || first.Sections[0]["id"] == "" {
		t.Fatalf("paragraph/section IDs are incomplete: %#v", first)
	}
	matches, err := Locate(first, LocatorRequest{Kind: LocatorExactText, Text: "Target sentence"})
	if err != nil || len(matches) != 1 || matches[0].BlockID != first.Blocks[1].ID {
		t.Fatalf("stable target was not located: matches=%#v err=%v", matches, err)
	}
}

func TestLocateDistinguishesMissingAmbiguousAndExplicitMultiple(t *testing.T) {
	document := Representation{Format: "docx", Blocks: []Block{
		{ID: "block_1", Kind: "paragraph", Text: "repeat", Location: map[string]any{"path": "document.p[1]"}},
		{ID: "block_2", Kind: "paragraph", Text: "repeat", Location: map[string]any{"path": "document.p[2]"}},
	}}
	if _, err := Locate(document, LocatorRequest{Kind: LocatorExactText, Text: "missing"}); !IsErrorCode(err, CodeTargetNotFound) {
		t.Fatalf("missing target did not return typed error: %v", err)
	}
	if _, err := Locate(document, LocatorRequest{Kind: LocatorExactText, Text: "repeat"}); !IsErrorCode(err, CodeTargetAmbiguous) {
		t.Fatalf("ambiguous target did not return typed error: %v", err)
	}
	matches, err := Locate(document, LocatorRequest{Kind: LocatorExactText, Text: "repeat", AllowMultiple: true, ExpectedMatches: 2})
	if err != nil || len(matches) != 2 {
		t.Fatalf("explicit multi-match target failed: matches=%#v err=%v", matches, err)
	}
	if _, err := Locate(document, LocatorRequest{Kind: LocatorExactText, Text: "repeat", AllowMultiple: true, ExpectedMatches: 3}); !IsErrorCode(err, CodeMatchCountMismatch) {
		t.Fatalf("match-count mismatch did not return typed error: %v", err)
	}
}

func TestPipelineDefersOversizedResourcesWithoutCallingParser(t *testing.T) {
	called := false
	strategy := NewSmallFileStrategy(map[string]Parser{
		"text": ParserFunc(func(context.Context, Metadata, int) (AdapterReadResult, error) {
			called = true
			return AdapterReadResult{}, nil
		}),
	}, nil)
	pipeline := NewPipeline(InspectorFunc(func(context.Context, string, string) (Metadata, error) {
		return Metadata{Path: "/workspace/large.txt", Relative: "large.txt", Format: "text", Size: SmallFileMaxBytes + 1}, nil
	}), strategy)
	_, err := pipeline.Read(context.Background(), ReadRequest{Root: "/workspace", Path: "/workspace/large.txt"})
	if !IsErrorCode(err, CodeStrategyDeferred) || called {
		t.Fatalf("oversized input was not deferred before parsing: called=%v err=%v", called, err)
	}
}

func TestPipelineRejectsAdapterTruncationAndPreservesOriginalOnEdit(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "note.docx")
	outputPath := filepath.Join(root, "note-edited.docx")
	if err := os.WriteFile(inputPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata := Metadata{Path: inputPath, Relative: "note.docx", Format: "docx", Size: 8}
	truncated := NewSmallFileStrategy(map[string]Parser{
		"docx": ParserFunc(func(context.Context, Metadata, int) (AdapterReadResult, error) {
			return AdapterReadResult{Content: "partial", Truncated: true}, nil
		}),
	}, nil)
	deferredPipeline := NewPipeline(InspectorFunc(func(context.Context, string, string) (Metadata, error) { return metadata, nil }), truncated)
	if _, err := deferredPipeline.Read(context.Background(), ReadRequest{Root: root, Path: inputPath}); !IsErrorCode(err, CodeStrategyDeferred) {
		t.Fatalf("adapter truncation was not deferred: %v", err)
	}

	strategy := NewSmallFileStrategy(map[string]Parser{
		"docx": ParserFunc(func(context.Context, Metadata, int) (AdapterReadResult, error) {
			return AdapterReadResult{
				Content: "target",
				Document: map[string]any{"blocks": []any{
					map[string]any{"text": "target", "location": map[string]any{"path": "document.p[1]", "paragraph_index": 1}},
				}},
			}, nil
		}),
	}, map[string]Editor{
		EditorKey("docx", "replace_text"): EditorFunc(func(_ context.Context, request ApplyRequest) (ApplyResult, error) {
			if len(request.Matches) != 1 {
				return ApplyResult{}, errors.New("expected one constrained match")
			}
			if err := os.WriteFile(request.Edit.OutputPath, []byte("updated"), 0o644); err != nil {
				return ApplyResult{}, err
			}
			return ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1}, nil
		}),
	})
	pipeline := NewPipeline(InspectorFunc(func(context.Context, string, string) (Metadata, error) { return metadata, nil }), strategy)
	result, err := pipeline.Edit(context.Background(), EditRequest{
		Root: root, Path: inputPath, OutputPath: outputPath, Operation: "replace_text",
		Target: LocatorRequest{Kind: LocatorExactText, Text: "target"},
	})
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(inputPath)
	if string(original) != "original" || result.ChangeSummary.OriginalUnchanged != true || result.ChangeSummary.Changed != 1 {
		t.Fatalf("edit did not preserve the original and audit summary: original=%q result=%#v", original, result)
	}
}
