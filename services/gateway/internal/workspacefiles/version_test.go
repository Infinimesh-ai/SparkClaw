package workspacefiles

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestVersionFamilyAndVersionedName(t *testing.T) {
	for _, test := range []struct {
		input string
		base  string
		next  int
	}{
		{input: "report.pdf", base: "report.pdf", next: 2},
		{input: "report-2.pdf", base: "report.pdf", next: 3},
		{input: "report-12.pdf", base: "report.pdf", next: 13},
		{input: "report-02.pdf", base: "report-02.pdf", next: 2},
		{input: "report-1.pdf", base: "report-1.pdf", next: 2},
		{input: ".env", base: ".env", next: 2},
	} {
		t.Run(test.input, func(t *testing.T) {
			base, next := VersionFamily(test.input)
			if base != test.base || next != test.next {
				t.Fatalf("VersionFamily(%q) = (%q, %d), want (%q, %d)", test.input, base, next, test.base, test.next)
			}
		})
	}
	if got := VersionedName("report.pdf", 2); got != "report-2.pdf" {
		t.Fatalf("VersionedName() = %q, want report-2.pdf", got)
	}
	if got := VersionedName(".env", 2); got != ".env-2" {
		t.Fatalf("hidden-file VersionedName() = %q, want .env-2", got)
	}
}

func TestOpenVersionedFileAllocatesConcurrentVersionsWithoutOverwrite(t *testing.T) {
	const uploads = 24
	directory := t.TempDir()
	type result struct {
		name string
		err  error
	}
	results := make(chan result, uploads)
	var workers sync.WaitGroup
	for index := 0; index < uploads; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			file, name, err := OpenVersionedFile(directory, "report.pdf", 0o644)
			if err == nil {
				_, err = file.WriteString(fmt.Sprintf("upload-%d", index))
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
			}
			results <- result{name: name, err: err}
		}(index)
	}
	workers.Wait()
	close(results)

	want := make(map[string]bool, uploads)
	want["report.pdf"] = true
	for sequence := 2; sequence <= uploads; sequence++ {
		want[fmt.Sprintf("report-%d.pdf", sequence)] = true
	}
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent version allocation failed: %v", result.err)
		}
		if !want[result.name] {
			t.Fatalf("unexpected or duplicate allocated name %q", result.name)
		}
		delete(want, result.name)
	}
	if len(want) != 0 {
		t.Fatalf("missing allocated versions: %#v", want)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != uploads {
		t.Fatalf("allocated files = %d, want %d: %v", len(entries), uploads, err)
	}
	for _, entry := range entries {
		if info, statErr := os.Stat(filepath.Join(directory, entry.Name())); statErr != nil || info.Size() == 0 {
			t.Fatalf("allocated file %q was not written: info=%#v err=%v", entry.Name(), info, statErr)
		}
	}
}
