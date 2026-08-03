package iscpbridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequirePrivateFileRejectsGroupOrOtherAccess(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if err := requirePrivateFile(path); err == nil || !strings.Contains(err.Error(), "group or other access") {
				t.Fatalf("requirePrivateFile(%#o) error = %v", mode.Perm(), err)
			}
		})
	}
}

func TestRequirePrivateFileAcceptsOwnerOnlyAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requirePrivateFile(path); err != nil {
		t.Fatalf("requirePrivateFile(0600): %v", err)
	}
}
