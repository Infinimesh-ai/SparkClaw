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

func TestLocateRowAndSlideReturnOneStableEntity(t *testing.T) {
	row := map[string]any{"index": 2, "cells": []any{
		map[string]any{"address": "A2", "value": "alpha"},
		map[string]any{"address": "B2", "value": "beta"},
	}}
	sheet := map[string]any{"name": "Data", "index": 1, "rows": []any{row}}
	xlsx, err := Normalize(Metadata{Path: "/workspace/book.xlsx", Relative: "book.xlsx", Format: "xlsx"}, "small_file_v1", "alpha beta", map[string]any{"sheets": []any{sheet}})
	if err != nil {
		t.Fatal(err)
	}
	rowMatches, err := Locate(xlsx, LocatorRequest{Kind: LocatorRow, Sheet: "Data", Row: 2, AllowMultiple: true})
	rows := mapSlice(xlsx.Sheets[0]["rows"])
	if err != nil || len(rowMatches) != 1 || rowMatches[0].Kind != LocatorRow || rowMatches[0].BlockID != stringValue(rows[0]["id"]) {
		t.Fatalf("row locator did not return its stable row entity: matches=%#v err=%v", rowMatches, err)
	}

	slide := map[string]any{"index": 1, "items": []any{
		map[string]any{"shape_index": 1, "type": "text", "text": "title"},
		map[string]any{"shape_index": 2, "type": "text", "text": "body"},
	}}
	pptx, err := Normalize(Metadata{Path: "/workspace/deck.pptx", Relative: "deck.pptx", Format: "pptx"}, "small_file_v1", "title body", map[string]any{"slides": []any{slide}})
	if err != nil {
		t.Fatal(err)
	}
	slideMatches, err := Locate(pptx, LocatorRequest{Kind: LocatorSlide, SlideIndex: 1, AllowMultiple: true})
	if err != nil || len(slideMatches) != 1 || slideMatches[0].Kind != LocatorSlide || slideMatches[0].BlockID != stringValue(pptx.Slides[0]["id"]) {
		t.Fatalf("slide locator did not return its stable slide entity: matches=%#v err=%v", slideMatches, err)
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
	pipeline := NewPipeline(InspectorFunc(func(_ context.Context, _ string, path string) (Metadata, error) {
		if path == outputPath {
			return Metadata{Path: outputPath, Relative: "note-edited.docx", Format: "docx", Size: 7}, nil
		}
		return metadata, nil
	}), strategy)
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

func TestPipelineFailsClosedForInvalidApplyResults(t *testing.T) {
	newPipeline := func(t *testing.T, editor EditorFunc) (*Pipeline, string, string) {
		t.Helper()
		root := t.TempDir()
		inputPath := filepath.Join(root, "note.txt")
		outputPath := filepath.Join(root, "note-edited.txt")
		if err := os.WriteFile(inputPath, []byte("target"), 0o644); err != nil {
			t.Fatal(err)
		}
		strategy := NewSmallFileStrategy(map[string]Parser{
			"text": ParserFunc(func(_ context.Context, metadata Metadata, _ int) (AdapterReadResult, error) {
				content, err := os.ReadFile(metadata.Path)
				return AdapterReadResult{Content: string(content), Document: map[string]any{"blocks": []any{
					map[string]any{"text": "target", "location": map[string]any{"path": "document.p[1]"}},
				}}}, err
			}),
		}, map[string]Editor{EditorKey("text", "replace_text"): editor})
		return NewPipeline(InspectorFunc(InspectFile), strategy), inputPath, outputPath
	}

	t.Run("zero change removes output", func(t *testing.T) {
		pipeline, inputPath, outputPath := newPipeline(t, func(_ context.Context, request ApplyRequest) (ApplyResult, error) {
			if err := os.WriteFile(request.Edit.OutputPath, []byte("updated"), 0o644); err != nil {
				return ApplyResult{}, err
			}
			return ApplyResult{OutputPath: request.Edit.OutputPath}, nil
		})
		_, err := pipeline.Edit(context.Background(), EditRequest{Root: filepath.Dir(inputPath), Path: inputPath, OutputPath: outputPath, Operation: "replace_text", Target: LocatorRequest{Kind: LocatorExactText, Text: "target"}})
		if !IsErrorCode(err, CodeParseFailed) {
			t.Fatalf("zero-change editor did not fail closed: %v", err)
		}
		if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("zero-change output was not removed: %v", statErr)
		}
	})

	t.Run("missing output is rejected", func(t *testing.T) {
		pipeline, inputPath, outputPath := newPipeline(t, func(_ context.Context, request ApplyRequest) (ApplyResult, error) {
			return ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1}, nil
		})
		_, err := pipeline.Edit(context.Background(), EditRequest{Root: filepath.Dir(inputPath), Path: inputPath, OutputPath: outputPath, Operation: "replace_text", Target: LocatorRequest{Kind: LocatorExactText, Text: "target"}})
		if !IsErrorCode(err, CodeResourceInvalid) {
			t.Fatalf("missing editor output did not fail closed: %v", err)
		}
	})

	t.Run("input mutation invalidates and cleans output", func(t *testing.T) {
		pipeline, inputPath, outputPath := newPipeline(t, func(_ context.Context, request ApplyRequest) (ApplyResult, error) {
			if err := os.WriteFile(request.Edit.OutputPath, []byte("updated"), 0o644); err != nil {
				return ApplyResult{}, err
			}
			if err := os.WriteFile(request.Metadata.Path, []byte("mutated"), 0o644); err != nil {
				return ApplyResult{}, err
			}
			return ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1}, nil
		})
		_, err := pipeline.Edit(context.Background(), EditRequest{Root: filepath.Dir(inputPath), Path: inputPath, OutputPath: outputPath, Operation: "replace_text", Target: LocatorRequest{Kind: LocatorExactText, Text: "target"}})
		if !IsErrorCode(err, CodeResourceInvalid) {
			t.Fatalf("input mutation was not detected after apply: %v", err)
		}
		if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("invalid edit output was not removed: %v", statErr)
		}
	})
}

func TestPipelineRejectsEvidenceCategoryChangesAndRemovesOutput(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "note.docx")
	outputPath := filepath.Join(root, "note-edited.docx")
	if err := os.WriteFile(inputPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	parser := ParserFunc(func(_ context.Context, metadata Metadata, _ int) (AdapterReadResult, error) {
		text := "target"
		imageHash := "stable-image"
		if metadata.Path == outputPath {
			text = "updated"
			imageHash = "changed-image"
		}
		return AdapterReadResult{Content: text, Document: map[string]any{
			"blocks": []any{map[string]any{"text": text, "location": map[string]any{"path": "document.p[1]"}}},
			"enrichment": map[string]any{
				"assets":      map[string]any{"images": []any{map[string]any{"kind": "image", "sha256": imageHash, "source": map[string]any{"relationship_id": "rId1", "part_name": "word/media/image1.png"}}}},
				"annotations": map[string]any{}, "layout": map[string]any{},
				"coverage": map[string]any{"content": "complete", "assets": "complete", "annotations": "complete", "layout": "complete", "extensions": "deferred"},
			},
		}}, nil
	})
	strategy := NewSmallFileStrategy(map[string]Parser{"docx": parser}, map[string]Editor{
		EditorKey("docx", "replace_text"): EditorFunc(func(_ context.Context, request ApplyRequest) (ApplyResult, error) {
			if err := os.WriteFile(request.Edit.OutputPath, []byte("updated"), 0o644); err != nil {
				return ApplyResult{}, err
			}
			return ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1}, nil
		}),
	})
	pipeline := NewPipeline(InspectorFunc(func(_ context.Context, _ string, path string) (Metadata, error) {
		return Metadata{Path: path, Relative: filepath.Base(path), Format: "docx", Size: 8, SHA256: "stable"}, nil
	}), strategy)
	_, err := pipeline.Edit(context.Background(), EditRequest{
		Root: root, Path: inputPath, OutputPath: outputPath, Operation: "replace_text",
		Target:    LocatorRequest{Kind: LocatorExactText, Text: "target"},
		Arguments: map[string]any{"replacements": []any{map[string]any{"find": "target", "replace": "updated"}}},
	})
	if !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("unexpected evidence mutation did not fail closed: %v", err)
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid output was not removed: %v", statErr)
	}
}

func TestMergedRangeBeforeCoordinatesMapsStructuralRowChanges(t *testing.T) {
	insert := EditRequest{Operation: "insert_row", Arguments: map[string]any{"row": 2, "position": "before"}}
	if got := mergedRangeBeforeCoordinates("A3:B4", insert); got != "A2:B3" {
		t.Fatalf("inserted-row range was not mapped to its original identity: %q", got)
	}
	deleteRequest := EditRequest{Operation: "delete_row", Arguments: map[string]any{"row": 2}}
	if got := mergedRangeBeforeCoordinates("A2:B3", deleteRequest); got != "A3:B4" {
		t.Fatalf("deleted-row range was not mapped to its original identity: %q", got)
	}
	scopedInsert := EditRequest{Operation: "insert_row", Arguments: map[string]any{"sheet": "Data", "row": 2, "position": "before"}}
	if got := xlsxMergedRangeBeforeCoordinates(map[string]any{"sheet": "Reference", "range": "A3:B4"}, scopedInsert); got != "A3:B4" {
		t.Fatalf("insert-row changed unrelated-sheet merged range coordinates: %q", got)
	}
	if got := xlsxMergedRangeBeforeCoordinates(map[string]any{"sheet": "data", "range": "A3:B4"}, scopedInsert); got != "A2:B3" {
		t.Fatalf("insert-row did not normalize target-sheet merged range coordinates: %q", got)
	}
}
