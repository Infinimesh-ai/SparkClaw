package mcdintegration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRuntimeManifestPinRejectsContentReplacementAndUnlistedCode(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(root, "bin/python3")
	content := []byte("synthetic runtime integrity fixture")
	if err = os.WriteFile(python, content, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	manifest := map[string]any{"schema_version": 1, "kind": "sparkclaw-mcd-runtime-v1", "platform": runtime.GOOS, "architecture": map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH], "contract": json.RawMessage(runtimeContractJSON), "files": map[string]string{"bin/python3": hex.EncodeToString(sum[:])}}
	raw, _ := json.Marshal(manifest)
	pin := sha256.Sum256(raw)
	expected := hex.EncodeToString(pin[:])
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = VerifyRuntime(python, expected); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "sitecustomize.py"), []byte("unlisted"), 0600); err != nil {
		t.Fatal(err)
	}
	if VerifyRuntime(python, expected) == nil {
		t.Fatal("unlisted code accepted")
	}
	if err = os.Remove(filepath.Join(root, "sitecustomize.py")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(python, []byte("replaced"), 0700); err != nil {
		t.Fatal(err)
	}
	if VerifyRuntime(python, expected) == nil {
		t.Fatal("changed interpreter accepted")
	}
	if err = os.WriteFile(python, content, 0700); err != nil {
		t.Fatal(err)
	}
	if VerifyRuntime(python, hex.EncodeToString(sum[:])) == nil {
		t.Fatal("unpinned manifest accepted")
	}
}
