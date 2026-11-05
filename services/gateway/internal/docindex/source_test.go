package docindex

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type abaSourceExtractor struct {
	original, path string
	readonly       bool
}

func (e *abaSourceExtractor) Extract(_ context.Context, path, format string, emit func(Unit) error) (SourceInfo, error) {
	original, err := os.ReadFile(e.original)
	if err != nil {
		return SourceInfo{}, err
	}
	if err := os.WriteFile(e.original, []byte("intermediate replacement bytes"), 0600); err != nil {
		return SourceInfo{}, err
	}
	defer os.WriteFile(e.original, original, 0600)
	e.path = path
	info, err := os.Stat(path)
	if err != nil {
		return SourceInfo{}, err
	}
	e.readonly = info.Mode().Perm()&0222 == 0
	content, err := os.ReadFile(path)
	if err != nil {
		return SourceInfo{}, err
	}
	if path == e.original || filepath.Ext(path) != filepath.Ext(e.original) {
		return SourceInfo{}, errors.New("parser was not given a format-preserving snapshot")
	}
	return SourceInfo{}, emit(Unit{Text: string(content), Block: "fixed-source"})
}
func TestParserSnapshotIsolatesOriginalABAAndCleansTemporaryBytes(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "source.md")
	initial := "  original governed bytes\r\n"
	if err := os.WriteFile(original, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	extractor := &abaSourceExtractor{original: original}
	service := NewService(extractor)
	defer service.Close()
	index, err := service.Ensure(t.Context(), Target{WorkspaceRoot: root, RelPath: "source.md", Format: "text"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if index.Nodes[0].Text != initial || !extractor.readonly {
		t.Fatalf("mutable original entered parser: %+v", index.Nodes)
	}
	relative, err := filepath.Rel(root, extractor.path)
	if err != nil || strings.HasPrefix(relative, "..") {
		t.Fatal("snapshot escaped workspace")
	}
	if _, err := os.Stat(extractor.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("parser snapshot was retained", err)
	}
	actual, _ := os.ReadFile(original)
	if string(actual) != initial {
		t.Fatal("ABA original was not restored")
	}
}

func TestTargetedSnapshotAlsoIsolatesABAAndCleansOnConsumerFailure(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "scan.pdf")
	initial := []byte("original PDF bytes")
	if err := os.WriteFile(original, initial, 0600); err != nil {
		t.Fatal(err)
	}
	sum, _, err := fileSHA256(original)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{WorkspaceRoot: root, RelPath: "scan.pdf", Format: "pdf", SourceSHA256: sum}
	snapshotPath := ""
	err = WithSourceSnapshot(t.Context(), target, func(_ context.Context, path string) error {
		snapshotPath = path
		if err := os.WriteFile(original, []byte("transient PDF"), 0600); err != nil {
			return err
		}
		defer os.WriteFile(original, initial, 0600)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(content, initial) {
			return errors.New("renderer read mutable source")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("render snapshot retained")
	}
	failed := errors.New("renderer failed")
	err = WithSourceSnapshot(t.Context(), target, func(_ context.Context, path string) error { snapshotPath = path; return failed })
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed render snapshot retained")
	}
}

type cancelSourceReader struct {
	cancel context.CancelFunc
	calls  int
}

func (r *cancelSourceReader) Read(data []byte) (int, error) {
	r.calls++
	copy(data, "data")
	r.cancel()
	return 4, nil
}
func TestSourceAdmissionPrecedesHashAndCopyHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "oversized.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, _, err := fileSHA256Context(t.Context(), path, MaxSourceBytes); err == nil || !strings.Contains(err.Error(), "admission") {
		t.Fatal("oversized sparse source was admitted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := fileSHA256Context(ctx, filepath.Join(root, "does-not-exist"), MaxSourceBytes); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hash performed filesystem work: %v", err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	reader := &cancelSourceReader{cancel: cancel}
	var output bytes.Buffer
	if _, err := copySource(ctx, &output, reader, 64); !errors.Is(err, context.Canceled) || reader.calls != 1 || output.Len() != 0 {
		t.Fatalf("copy did not stop on cancellation: %v", err)
	}
}

func TestAcquisitionMetadataDoesNotChangeCanonicalIdentity(t *testing.T) {
	service, target, index := canonicalFixture(t, []Unit{{Text: "source", Block: "p1"}}, SourceInfo{})
	if index.Acquisition != AcquisitionBuilt {
		t.Fatalf("cold acquisition %s", index.Acquisition)
	}
	memory, err := service.Ensure(t.Context(), target, nil)
	if err != nil || memory.Acquisition != AcquisitionMemoryCache {
		t.Fatalf("warm acquisition %s %v", memory.Acquisition, err)
	}
	other := NewService(&fakeExtractor{err: errors.New("must load disk")})
	defer other.Close()
	disk, err := other.Ensure(t.Context(), target, nil)
	if err != nil || disk.Acquisition != AcquisitionDiskCache {
		t.Fatalf("disk acquisition %+v %v", disk, err)
	}
	rebuilt, err := service.RecognizedPages(t.Context(), target, nil)
	if err != nil || rebuilt.Acquisition != AcquisitionRebuilt {
		t.Fatalf("rebuild acquisition %+v %v", rebuilt, err)
	}
	if index.Manifest.StructureGeneration != memory.Manifest.StructureGeneration || index.Manifest.StructureGeneration != disk.Manifest.StructureGeneration || index.Manifest.StructureGeneration != rebuilt.Manifest.StructureGeneration {
		t.Fatal("acquisition changed canonical generation")
	}
	raw, err := os.ReadFile(filepath.Join(Dir(target.WorkspaceRoot, index.Manifest.SourceSHA256), manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("acquisition")) {
		t.Fatal("call-local acquisition was persisted")
	}
}
