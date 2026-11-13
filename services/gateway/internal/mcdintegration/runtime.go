package mcdintegration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// VerifyRuntime validates the complete relocatable bundle before any private
// stdin is supplied. The deployment pins its manifest separately from content.
func VerifyRuntime(python, manifestSHA string) error {
	if !filepath.IsAbs(python) || filepath.Base(python) != "python3" || !hashPattern.MatchString(manifestSHA) {
		return ErrUnavailable
	}
	root := filepath.Dir(filepath.Dir(python))
	for p := root; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return ErrUnavailable
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil || len(raw) > 2<<20 {
		return ErrUnavailable
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != manifestSHA {
		return ErrUnavailable
	}
	var manifest struct {
		SchemaVersion int               `json:"schema_version"`
		Kind          string            `json:"kind"`
		Platform      string            `json:"platform"`
		Architecture  string            `json:"architecture"`
		Contract      json.RawMessage   `json:"contract"`
		Files         map[string]string `json:"files"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.SchemaVersion != 1 || manifest.Kind != "sparkclaw-mcd-runtime-v1" || manifest.Platform != runtime.GOOS || len(manifest.Files) < 1 || len(manifest.Files) > 6000 {
		return ErrUnavailable
	}
	arch := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH]
	if arch == "" || manifest.Architecture != arch {
		return ErrUnavailable
	}
	current, _ := Canonical(manifest.Contract)
	expected, _ := Canonical([]byte(runtimeContractJSON))
	if string(current) != string(expected) {
		return ErrUnavailable
	}
	var bytes int64
	seen := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrUnavailable
		}
		if entry.IsDir() {
			return nil
		}
		relative, e := filepath.Rel(root, path)
		if e != nil || strings.Contains(relative, "..") {
			return ErrUnavailable
		}
		relative = filepath.ToSlash(relative)
		if relative == "manifest.json" {
			return nil
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() {
			return ErrUnavailable
		}
		bytes += info.Size()
		if bytes > 160<<20 || info.Size() > 40<<20 {
			return ErrUnavailable
		}
		expected, ok := manifest.Files[relative]
		if !ok || !hashPattern.MatchString(expected) {
			return ErrUnavailable
		}
		content, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		sha := sha256.Sum256(content)
		if hex.EncodeToString(sha[:]) != expected {
			return ErrUnavailable
		}
		seen++
		return nil
	})
	if err != nil || seen != len(manifest.Files) || manifest.Files["bin/python3"] == "" {
		return ErrUnavailable
	}
	return nil
}
