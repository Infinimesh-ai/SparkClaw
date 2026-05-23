package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type patchFile struct {
	oldPath string
	newPath string
	hunks   []patchHunk
}

type patchHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []string
}

type patchManifest struct {
	PatchID       string              `json:"patch_id"`
	PatchPath     string              `json:"patch_path"`
	RollbackPatch string              `json:"rollback_patch"`
	BackupDir     string              `json:"backup_dir"`
	ChangedFiles  []patchManifestFile `json:"changed_files"`
	AppliedAt     time.Time           `json:"applied_at"`
}

type patchManifestFile struct {
	Path       string `json:"path"`
	BackupPath string `json:"backup_path,omitempty"`
	Created    bool   `json:"created"`
}

var hunkHeaderRE = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

func (h *ToolHub) codeApplyPatch(ctx context.Context, args map[string]any) (Result, error) {
	patch := stringArg(args, "patch", "")
	if strings.TrimSpace(patch) == "" {
		return Result{}, errors.New("patch cannot be empty")
	}
	files, err := parseUnifiedPatch(patch)
	if err != nil {
		return Result{}, err
	}
	patchID := app.NewID("patch")
	patchPath, err := h.resolvePath(filepath.Join(".sparkclaw", "patches", patchID+".patch"))
	if err != nil {
		return Result{}, err
	}
	backupRoot, err := h.resolvePath(filepath.Join(".sparkclaw", "patch-backups", patchID))
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(patchPath), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(patchPath, []byte(patch), 0o644); err != nil {
		return Result{}, err
	}
	changed := []string{}
	manifestFiles := []patchManifestFile{}
	for _, file := range files {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		default:
		}
		if file.newPath == "/dev/null" {
			return Result{}, fmt.Errorf("deleting files is not supported by code.apply_patch MVP: %s", file.oldPath)
		}
		target, err := h.resolvePath(file.newPath)
		if err != nil {
			return Result{}, err
		}
		raw, err := os.ReadFile(target)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		manifestFile := patchManifestFile{Path: file.newPath, Created: errors.Is(err, os.ErrNotExist)}
		if err == nil {
			backupPath := filepath.Join(backupRoot, file.newPath)
			if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
				return Result{}, err
			}
			if err := os.WriteFile(backupPath, raw, 0o644); err != nil {
				return Result{}, err
			}
			manifestFile.BackupPath = backupPath
		}
		next, err := applyPatchFile(string(raw), file)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", file.newPath, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(target, []byte(next), 0o644); err != nil {
			return Result{}, err
		}
		changed = append(changed, target)
		manifestFiles = append(manifestFiles, manifestFile)
	}
	rollbackPatchPath := filepath.Join(backupRoot, "rollback.patch")
	rollbackPatch := buildRollbackPatch(files)
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(rollbackPatchPath, []byte(rollbackPatch), 0o644); err != nil {
		return Result{}, err
	}
	appliedAt := time.Now().UTC()
	manifest := patchManifest{
		PatchID:       patchID,
		PatchPath:     patchPath,
		RollbackPatch: rollbackPatchPath,
		BackupDir:     backupRoot,
		ChangedFiles:  manifestFiles,
		AppliedAt:     appliedAt,
	}
	manifestPath := filepath.Join(backupRoot, "manifest.json")
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(manifestPath, manifestRaw, 0o644); err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"status":              "patch_applied",
		"patch_id":            patchID,
		"patch_path":          patchPath,
		"backup_dir":          backupRoot,
		"manifest_path":       manifestPath,
		"rollback_patch_path": rollbackPatchPath,
		"changed_files":       changed,
		"applied_at":          appliedAt.Format(time.RFC3339),
	}}, nil
}

func buildRollbackPatch(files []patchFile) string {
	var b strings.Builder
	for index, file := range files {
		if index > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("--- b/")
		b.WriteString(file.newPath)
		b.WriteByte('\n')
		b.WriteString("+++ a/")
		b.WriteString(file.oldPath)
		b.WriteByte('\n')
		for _, hunk := range file.hunks {
			b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", hunk.newStart, hunk.newCount, hunk.oldStart, hunk.oldCount))
			for _, line := range hunk.lines {
				switch line[0] {
				case '+':
					b.WriteByte('-')
					b.WriteString(line[1:])
				case '-':
					b.WriteByte('+')
					b.WriteString(line[1:])
				default:
					b.WriteString(line)
				}
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func parseUnifiedPatch(patch string) ([]patchFile, error) {
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	files := []patchFile{}
	for i := 0; i < len(lines); {
		if !strings.HasPrefix(lines[i], "--- ") {
			i++
			continue
		}
		oldPath := normalizeDiffPath(strings.TrimSpace(strings.TrimPrefix(lines[i], "--- ")))
		i++
		if i >= len(lines) || !strings.HasPrefix(lines[i], "+++ ") {
			return nil, errors.New("unified patch missing +++ header")
		}
		newPath := normalizeDiffPath(strings.TrimSpace(strings.TrimPrefix(lines[i], "+++ ")))
		i++
		file := patchFile{oldPath: oldPath, newPath: newPath}
		for i < len(lines) {
			if strings.HasPrefix(lines[i], "--- ") {
				break
			}
			if !strings.HasPrefix(lines[i], "@@ ") {
				i++
				continue
			}
			matches := hunkHeaderRE.FindStringSubmatch(lines[i])
			if len(matches) == 0 {
				return nil, fmt.Errorf("invalid hunk header %q", lines[i])
			}
			hunk := patchHunk{
				oldStart: mustAtoi(matches[1]),
				oldCount: countOrOne(matches[2]),
				newStart: mustAtoi(matches[3]),
				newCount: countOrOne(matches[4]),
			}
			i++
			for i < len(lines) {
				if strings.HasPrefix(lines[i], "@@ ") || strings.HasPrefix(lines[i], "--- ") {
					break
				}
				if lines[i] == `\ No newline at end of file` {
					i++
					continue
				}
				if lines[i] == "" {
					hunk.lines = append(hunk.lines, " ")
					i++
					continue
				}
				prefix := lines[i][0]
				if prefix != ' ' && prefix != '-' && prefix != '+' {
					return nil, fmt.Errorf("invalid patch line %q", lines[i])
				}
				hunk.lines = append(hunk.lines, lines[i])
				i++
			}
			file.hunks = append(file.hunks, hunk)
		}
		if len(file.hunks) == 0 {
			return nil, fmt.Errorf("patch for %s has no hunks", newPath)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return nil, errors.New("patch must contain unified diff headers")
	}
	return files, nil
}

func applyPatchFile(original string, file patchFile) (string, error) {
	originalLines := splitPatchText(original)
	out := []string{}
	cursor := 0
	for _, hunk := range file.hunks {
		target := hunk.oldStart - 1
		if target < cursor || target > len(originalLines) {
			return "", fmt.Errorf("hunk starts outside file at line %d", hunk.oldStart)
		}
		out = append(out, originalLines[cursor:target]...)
		cursor = target
		oldSeen := 0
		newSeen := 0
		for _, rawLine := range hunk.lines {
			prefix := rawLine[0]
			line := rawLine[1:]
			switch prefix {
			case ' ':
				if cursor >= len(originalLines) || originalLines[cursor] != line {
					return "", fmt.Errorf("context mismatch at line %d", cursor+1)
				}
				out = append(out, line)
				cursor++
				oldSeen++
				newSeen++
			case '-':
				if cursor >= len(originalLines) || originalLines[cursor] != line {
					return "", fmt.Errorf("remove mismatch at line %d", cursor+1)
				}
				cursor++
				oldSeen++
			case '+':
				out = append(out, line)
				newSeen++
			}
		}
		if oldSeen != hunk.oldCount {
			return "", fmt.Errorf("hunk old line count mismatch: expected %d saw %d", hunk.oldCount, oldSeen)
		}
		if newSeen != hunk.newCount {
			return "", fmt.Errorf("hunk new line count mismatch: expected %d saw %d", hunk.newCount, newSeen)
		}
	}
	out = append(out, originalLines[cursor:]...)
	return strings.Join(out, "\n"), nil
}

func splitPatchText(text string) []string {
	if text == "" {
		return []string{}
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

func normalizeDiffPath(path string) string {
	path = strings.Split(path, "\t")[0]
	path = strings.Fields(path)[0]
	if path == "/dev/null" {
		return path
	}
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return filepath.Clean(path)
}

func mustAtoi(value string) int {
	var out int
	for _, ch := range value {
		out = out*10 + int(ch-'0')
	}
	return out
}

func countOrOne(value string) int {
	if value == "" {
		return 1
	}
	return mustAtoi(value)
}
