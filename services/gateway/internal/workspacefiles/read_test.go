//go:build unix

package workspacefiles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadRegularRejectsHardLinksAcrossWorkspaceBoundary(t *testing.T) {
	for _, sourceInside := range []bool{false, true} {
		name := "outside source linked into workspace"
		if sourceInside {
			name = "workspace source linked outside"
		}
		t.Run(name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			insidePath, outsidePath := filepath.Join(root, "file.txt"), filepath.Join(outside, "private.txt")
			source, link := outsidePath, insidePath
			if sourceInside {
				source, link = insidePath, outsidePath
			}
			if err := os.WriteFile(source, []byte("linked bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(source, link); err != nil {
				t.Fatal(err)
			}
			data, err := ReadRegular(t.Context(), root, "file.txt", 100)
			if !errors.Is(err, ErrUnsafePath) || len(data) != 0 {
				t.Fatalf("hard link exposed bytes %q: %v", data, err)
			}
			if err := os.Remove(outsidePath); err != nil {
				t.Fatal(err)
			}
			data, err = ReadRegular(t.Context(), root, "file.txt", 100)
			if err != nil || string(data) != "linked bytes" {
				t.Fatalf("single workspace link %q: %v", data, err)
			}
		})
	}
}

func TestReadRegularRejectsPathsSymlinksAndSpecialFiles(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("reviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"escape": outside, "alias.txt": filepath.Join(root, "ok.txt")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"../secret.txt", "/etc/passwd", "https://example.test/file", "file:ok.txt", "a/../ok.txt", "a\\ok.txt", ".env", "public/.ssh/key", "email/other/source.txt", ".sparkclaw/state.json", "escape/secret.txt", "alias.txt", "pipe", "."} {
		t.Run(relative, func(t *testing.T) {
			if _, err := ReadRegular(t.Context(), root, relative, 100); err == nil {
				t.Fatal("unsafe file accepted")
			}
		})
	}
	if _, err := ReadRegular(t.Context(), root, "ok.txt", 3); err == nil {
		t.Fatal("oversized file accepted")
	}
	data, err := ReadRegular(t.Context(), root, "ok.txt", 8)
	if err != nil || string(data) != "reviewed" {
		t.Fatalf("regular file %q: %v", data, err)
	}
}
