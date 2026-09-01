package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/workspacefiles"
)

func (s *Server) uploadDocument(w http.ResponseWriter, r *http.Request) {
	const maxUploadBytes = 25 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("multipart field \"file\" is required"))
		return
	}
	defer file.Close()
	if r.MultipartForm != nil && len(r.MultipartForm.File["file"]) > 1 {
		writeError(w, http.StatusBadRequest, errors.New("only one file may be uploaded per request"))
		return
	}
	name := cleanUploadFilename(header.Filename)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("uploaded filename is required"))
		return
	}
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	var sniff bytes.Buffer
	if _, err := io.Copy(&sniff, io.LimitReader(file, 512)); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	detectedContentType := http.DetectContentType(sniff.Bytes())
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = detectedContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	name = ensureUploadFilenameExtension(name, contentType, detectedContentType)
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	workspaceRoot, err := s.workspaceRootForSession(r.Context(), sessionID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	now := time.Now().UTC()
	isImage := isImageContentType(contentType)
	rootName := "uploads"
	artifactKind := "document_upload"
	if isImage {
		rootName = "media"
		artifactKind = "media_image_upload"
	}
	uploadRoot := filepath.Join(workspaceRoot, rootName)
	if err := os.MkdirAll(uploadRoot, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out, storedName, err := workspacefiles.OpenVersionedFile(uploadRoot, name, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	relPath := filepath.Join(rootName, storedName)
	path := out.Name()
	bytesWritten, copyErr := io.Copy(out, io.MultiReader(bytes.NewReader(sniff.Bytes()), file))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		writeError(w, http.StatusInternalServerError, copyErr)
		return
	}
	if closeErr != nil {
		_ = os.Remove(path)
		writeError(w, http.StatusInternalServerError, closeErr)
		return
	}
	object := app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        artifactKind,
		SessionID:   sessionID,
		Backend:     "workspace",
		Key:         relPath,
		URI:         "workspace://" + filepath.ToSlash(relPath),
		Path:        path,
		ContentType: contentType,
		Bytes:       int(bytesWritten),
		CreatedAt:   now,
	}
	stored, err := s.store.SaveArtifactObject(r.Context(), object)
	if err != nil {
		_ = os.Remove(path)
		writeArtifactMetadataStoreError(w, err)
		return
	}
	object = stored
	s.addAudit(r.Context(), app.AuditEvent{
		SessionID: sessionID,
		Actor:     "owner",
		Type:      uploadAuditType(isImage),
		Summary:   relPath,
		Fields: map[string]any{
			"artifact_id":  object.ID,
			"path":         path,
			"rel_path":     relPath,
			"content_type": contentType,
			"bytes":        bytesWritten,
		},
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"artifact": object,
		"path":     path,
		"rel_path": relPath,
		"bytes":    bytesWritten,
		"media":    uploadedMediaMetadata(path, relPath, contentType, isImage),
	})
}

func (s *Server) listAvailableDocuments(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	out := []app.ArtifactObject{}
	seen := map[string]bool{}
	workspaceRoot, err := s.workspaceRootForSession(r.Context(), strings.TrimSpace(r.URL.Query().Get("session_id")))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	for _, rootName := range []string{"uploads", "media"} {
		if len(out) >= limit {
			break
		}
		uploadRoot := filepath.Join(workspaceRoot, rootName)
		_ = filepath.WalkDir(uploadRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || len(out) >= limit {
				return nil
			}
			if path != uploadRoot && strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if !pathWithinRoot(uploadRoot, path) || !pathWithinRoot(workspaceRoot, path) {
				return nil
			}
			rel, err := filepath.Rel(workspaceRoot, path)
			if err != nil {
				return nil
			}
			key := filepath.ToSlash(rel)
			if seen[key] {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			contentType := "application/octet-stream"
			if file, err := os.Open(path); err == nil {
				var sniff [512]byte
				n, _ := file.Read(sniff[:])
				_ = file.Close()
				contentType = http.DetectContentType(sniff[:n])
			}
			out = append(out, app.ArtifactObject{
				ID:          "workspace:" + key,
				Kind:        availableObjectKind(key, contentType),
				Backend:     "workspace",
				Key:         key,
				URI:         "workspace://" + key,
				Path:        path,
				ContentType: contentType,
				Bytes:       int(info.Size()),
				CreatedAt:   info.ModTime().UTC(),
			})
			seen[key] = true
			return nil
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": out})
}

func isImageContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func uploadAuditType(isImage bool) string {
	if isImage {
		return "media.image.uploaded"
	}
	return "document.uploaded"
}

func availableObjectKind(key, contentType string) string {
	if strings.HasPrefix(filepath.ToSlash(key), "media/") || isImageContentType(contentType) {
		return "media_image_upload"
	}
	return "document_upload"
}

func uploadedMediaMetadata(path, relPath, contentType string, isImage bool) map[string]any {
	if !isImage {
		return nil
	}
	meta := map[string]any{
		"rel_path":     filepath.ToSlash(relPath),
		"content_type": contentType,
	}
	if raw, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(raw)
		meta["sha256"] = hex.EncodeToString(sum[:])
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
			meta["width"] = cfg.Width
			meta["height"] = cfg.Height
		}
	}
	return meta
}

func (s *Server) getUploadedDocument(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if relPath == "" {
		writeError(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	clean, ok := cleanWorkspaceRelativePath(relPath)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("invalid document path"))
		return
	}
	workspaceRoot, err := s.workspaceRootForSession(r.Context(), strings.TrimSpace(r.URL.Query().Get("session_id")))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	path := filepath.Join(workspaceRoot, clean)
	if !pathWithinRoot(workspaceRoot, path) {
		writeError(w, http.StatusBadRequest, errors.New("document path escapes workspace"))
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, path)
}

func (s *Server) workspaceRootForSession(ctx context.Context, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) != "" {
		session, ok, err := s.store.GetSession(ctx, sessionID)
		if err != nil {
			return "", err
		}
		if ok && strings.TrimSpace(session.WorkspaceRoot) != "" {
			return strings.TrimSpace(session.WorkspaceRoot), nil
		}
	}
	return strings.TrimSpace(s.cfg.Workspaces.DefaultRoot), nil
}

func (s *Server) getWorkspaceScreenshot(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(strings.TrimSpace(r.PathValue("name")))
	if name == "" || name == "." || name != strings.TrimSpace(r.PathValue("name")) {
		writeError(w, http.StatusBadRequest, errors.New("invalid screenshot name"))
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		writeError(w, http.StatusBadRequest, errors.New("unsupported screenshot type"))
		return
	}
	dir := filepath.Join(s.cfg.Workspaces.DefaultRoot, ".sparkclaw", "screenshots")
	path := filepath.Join(dir, name)
	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !strings.HasPrefix(cleanPath, cleanDir+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, errors.New("invalid screenshot path"))
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, cleanPath)
}

func cleanWorkspaceRelativePath(value string) (string, bool) {
	value = strings.TrimSpace(filepath.ToSlash(value))
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\x00") {
		return "", false
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

func pathWithinRoot(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func cleanUploadFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, string(os.PathSeparator), "_")
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	name = strings.Trim(name, "._-")
	if len(name) > 120 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if len(ext) > 20 {
			ext = ""
		}
		if len(base) > 100 {
			base = base[:100]
		}
		name = base + ext
	}
	return name
}

func ensureUploadFilenameExtension(name, contentType, detectedContentType string) string {
	if filepath.Ext(name) != "" {
		return name
	}
	ext := extensionForUploadContentType(contentType)
	if ext == "" {
		ext = extensionForUploadContentType(detectedContentType)
	}
	if ext == "" {
		return name
	}
	return name + ext
}

func extensionForUploadContentType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "application/pdf":
		return ".pdf"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "text/plain":
		return ".txt"
	case "text/markdown", "text/x-markdown":
		return ".md"
	case "text/csv":
		return ".csv"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}
