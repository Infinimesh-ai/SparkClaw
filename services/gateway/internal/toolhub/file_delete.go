package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type deleteManifest struct {
	DeleteID  string    `json:"delete_id"`
	Path      string    `json:"path"`
	TrashPath string    `json:"trash_path"`
	Bytes     int64     `json:"bytes"`
	Reason    string    `json:"reason,omitempty"`
	DeletedAt time.Time `json:"deleted_at"`
}

func (h *ToolHub) fileDelete(ctx context.Context, args map[string]any) (Result, error) {
	path, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	if h.isSparkClawControlPath(path) {
		return Result{}, errors.New("file.delete cannot remove SparkClaw control files")
	}
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	if info.IsDir() {
		return Result{}, errors.New("file.delete only supports single files")
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	rel, err := filepath.Rel(h.cfg.Workspaces.DefaultRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(path)
	}
	deleteID := app.NewID("del")
	trashRoot, err := h.resolvePath(filepath.Join(".sparkclaw", "trash", deleteID))
	if err != nil {
		return Result{}, err
	}
	trashPath := filepath.Join(trashRoot, rel)
	if err := os.MkdirAll(filepath.Dir(trashPath), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.Rename(path, trashPath); err != nil {
		return Result{}, fmt.Errorf("move file to trash: %w", err)
	}
	deletedAt := time.Now().UTC()
	manifest := deleteManifest{
		DeleteID:  deleteID,
		Path:      path,
		TrashPath: trashPath,
		Bytes:     info.Size(),
		Reason:    stringArg(args, "reason", ""),
		DeletedAt: deletedAt,
	}
	manifestPath := filepath.Join(trashRoot, "manifest.json")
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"status":        "moved_to_trash",
		"path":          path,
		"trash_path":    trashPath,
		"manifest_path": manifestPath,
		"bytes":         info.Size(),
		"deleted_at":    deletedAt.Format(time.RFC3339),
	}}, nil
}

func (h *ToolHub) isSparkClawControlPath(path string) bool {
	root, err := filepath.Abs(h.cfg.Workspaces.DefaultRoot)
	if err != nil {
		return false
	}
	controlRoot := filepath.Join(root, ".sparkclaw")
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	return abs == controlRoot || strings.HasPrefix(abs, controlRoot+string(os.PathSeparator))
}
