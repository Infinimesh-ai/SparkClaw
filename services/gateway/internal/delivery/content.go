package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type governedArtifactStore interface {
	ListArtifactObjects(int) []app.ArtifactObject
	GetSession(string) (app.Session, bool)
}

func ResolveBrowserContent(st governedArtifactStore, ownerID string, content app.MessageContent) (app.MessageContent, error) {
	ownerID = strings.TrimSpace(ownerID)
	if st == nil || ownerID == "" {
		return app.MessageContent{}, NewError(CodeArtifactInvalid, "artifact authorization is unavailable", "blocked")
	}
	resolved := app.MessageContent{Parts: make([]app.MessagePart, 0, len(content.Parts))}
	for _, part := range content.Parts {
		if part.Kind == app.MessagePartText {
			part.ArtifactID, part.Resource, part.Name, part.ContentType, part.Bytes, part.SHA256 = "", nil, "", "", 0, ""
			resolved.Parts = append(resolved.Parts, part)
			continue
		}
		artifactID := strings.TrimSpace(part.ArtifactID)
		if artifactID == "" {
			return app.MessageContent{}, NewError(CodeArtifactInvalid, fmt.Sprintf("part %q requires an artifact", part.ID), "blocked")
		}
		object, ok := findArtifact(st, artifactID)
		if !ok || strings.TrimSpace(object.SessionID) == "" {
			return app.MessageContent{}, NewError(CodeArtifactInvalid, fmt.Sprintf("artifact for part %q is unavailable", part.ID), "blocked")
		}
		session, ok := st.GetSession(object.SessionID)
		if !ok || normalizedOwnerID(session.OwnerID) != ownerID {
			return app.MessageContent{}, NewError(CodeCrossUserDenied, "artifact is outside the actor owner scope", "blocked")
		}
		path, info, err := validateArtifactPath(session, object)
		if err != nil {
			return app.MessageContent{}, NewError(CodeArtifactInvalid, fmt.Sprintf("artifact for part %q is invalid", part.ID), "blocked")
		}
		if object.Bytes > 0 && int64(object.Bytes) != info.Size() {
			return app.MessageContent{}, NewError(CodeArtifactInvalid, fmt.Sprintf("artifact for part %q changed after upload", part.ID), "blocked")
		}
		digest, err := fileSHA256(path)
		if err != nil {
			return app.MessageContent{}, NewError(CodeArtifactInvalid, fmt.Sprintf("artifact for part %q cannot be verified", part.ID), "blocked")
		}
		name := safeDisplayName(firstNonEmptyValue(part.Name, filepath.Base(object.Key), filepath.Base(path)))
		contentType := strings.TrimSpace(object.ContentType)
		if contentType == "" {
			contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		part.ArtifactID = object.ID
		part.Resource = &app.ResourceRef{Kind: "artifact", Ref: object.ID, Provenance: "web_direct_upload"}
		part.Name = name
		part.ContentType = contentType
		part.Bytes = int(info.Size())
		part.SHA256 = digest
		resolved.Parts = append(resolved.Parts, part)
	}
	return resolved, nil
}

func ContentDigest(target app.EndpointID, content app.MessageContent) (string, error) {
	payload := struct {
		Target  app.EndpointID     `json:"target"`
		Content app.MessageContent `json:"content"`
	}{Target: target, Content: content}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func findArtifact(st governedArtifactStore, id string) (app.ArtifactObject, bool) {
	for _, object := range st.ListArtifactObjects(0) {
		if object.ID == id {
			return object, true
		}
	}
	return app.ArtifactObject{}, false
}

func validateArtifactPath(session app.Session, object app.ArtifactObject) (string, os.FileInfo, error) {
	path := strings.TrimSpace(object.Path)
	if path == "" {
		return "", nil, errors.New("artifact has no local path")
	}
	lstat, err := os.Lstat(path)
	if err != nil || lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return "", nil, errors.New("artifact is not a regular file")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", nil, err
	}
	root := strings.TrimSpace(session.WorkspaceRoot)
	if root == "" {
		return "", nil, errors.New("artifact session has no workspace")
	}
	root, err = filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil || (resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator))) {
		return "", nil, errors.New("artifact escapes the workspace")
	}
	info, err := os.Stat(resolved)
	return resolved, info, err
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizedOwnerID(ownerID string) string {
	if ownerID = strings.TrimSpace(ownerID); ownerID != "" {
		return ownerID
	}
	return app.DefaultOwnerID
}

func safeDisplayName(name string) string {
	name = filepath.Base(strings.TrimSpace(strings.ReplaceAll(name, "\\", "/")))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "attachment.bin"
	}
	return name
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
