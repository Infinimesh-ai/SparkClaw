package weixin

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func singleMediaMarkdownPath(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if !strings.HasPrefix(answer, "![") {
		return "", false
	}
	closeAlt := strings.Index(answer, "](")
	if closeAlt < 2 || !strings.HasSuffix(answer, ")") {
		return "", false
	}
	path := strings.TrimSpace(answer[closeAlt+2 : len(answer)-1])
	cleaned, ok := cleanMediaMarkdownTarget(path)
	if !ok {
		return "", false
	}
	switch strings.ToLower(filepath.Ext(cleaned)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return cleaned, true
	default:
		return "", false
	}
}

func cleanMediaMarkdownTarget(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if strings.HasPrefix(path, "workspace://") {
		path = strings.TrimPrefix(path, "workspace://")
		path = strings.TrimLeft(path, "/")
		cleaned := filepath.ToSlash(filepath.Clean(path))
		if cleaned == "." || strings.HasPrefix(cleaned, "../") || !strings.HasPrefix(cleaned, "media/") {
			return "", false
		}
		return cleaned, true
	}
	if filepath.IsAbs(path) {
		cleaned := filepath.Clean(path)
		slash := filepath.ToSlash(cleaned)
		if !strings.Contains(slash, "/media/") {
			return "", false
		}
		return cleaned, true
	}
	path = strings.TrimLeft(path, "/")
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || !strings.HasPrefix(cleaned, "media/") {
		return "", false
	}
	return cleaned, true
}

func (d *Dispatcher) workspaceMediaPath(ctx context.Context, mediaPath string, inbound InboundMessage) (string, bool, error) {
	mediaPath = strings.TrimSpace(mediaPath)
	if mediaPath == "" {
		return "", false, nil
	}
	relPath := ""
	if !filepath.IsAbs(mediaPath) {
		relPath = filepath.ToSlash(strings.TrimSpace(mediaPath))
	}
	absPath := ""
	if filepath.IsAbs(mediaPath) {
		cleaned, err := filepath.Abs(mediaPath)
		if err != nil {
			return "", false, nil
		}
		absPath = filepath.Clean(cleaned)
	}
	objects, err := d.store.ListArtifactObjects(ctx, 200)
	if err != nil {
		return "", false, err
	}
	for _, object := range objects {
		key := filepath.ToSlash(object.Key)
		objectPath := strings.TrimSpace(object.Path)
		if !strings.HasPrefix(key, "media/") || objectPath == "" {
			continue
		}
		if relPath != "" && key == relPath {
			return object.Path, true, nil
		}
		if absPath != "" {
			if objectAbs, err := filepath.Abs(objectPath); err == nil && filepath.Clean(objectAbs) == absPath {
				return object.Path, true, nil
			}
		}
	}
	if relPath != "" {
		path, ok, err := d.workspaceSessionPath(ctx, relPath, inbound)
		if err != nil {
			return "", false, err
		}
		if ok {
			return path, true, nil
		}
	}
	return "", false, nil
}

var workspaceFilePathPattern = regexp.MustCompile(`(?:workspace://)?((?:outputs|uploads)/[A-Za-z0-9._~!$&'()*+,;=:@%/\-]+\.(?:docx|xlsx|pptx|pdf|txt|md|csv|tsv))`)

func (d *Dispatcher) workspaceFilePath(ctx context.Context, answer string, inbound InboundMessage) (string, string, bool, error) {
	matches := workspaceFilePathPattern.FindAllStringSubmatch(answer, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		relPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(match[1])))
		if relPath == "." || strings.HasPrefix(relPath, "../") {
			continue
		}
		if strings.HasPrefix(relPath, "uploads/") && !isLikelyOutputFileAnswer(answer) {
			continue
		}
		absPath, ok, err := d.workspaceObjectPath(ctx, relPath)
		if err != nil {
			return "", "", false, err
		}
		if ok {
			return absPath, filepath.Base(relPath), true, nil
		}
		absPath, ok, err = d.workspaceSessionPath(ctx, relPath, inbound)
		if err != nil {
			return "", "", false, err
		}
		if ok {
			return absPath, filepath.Base(relPath), true, nil
		}
	}
	return "", "", false, nil
}

func isLikelyOutputFileAnswer(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(lower, "output_path") ||
		strings.Contains(lower, "output file") ||
		strings.Contains(answer, "输出文件") ||
		strings.Contains(answer, "修改好的文件") ||
		strings.Contains(answer, "已完成") ||
		strings.Contains(answer, "修改后")
}

func (d *Dispatcher) workspaceObjectPath(ctx context.Context, relPath string) (string, bool, error) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	objects, err := d.store.ListArtifactObjects(ctx, 200)
	if err != nil {
		return "", false, err
	}
	for _, object := range objects {
		if filepath.ToSlash(object.Key) == relPath && strings.TrimSpace(object.Path) != "" {
			return object.Path, true, nil
		}
	}
	return "", false, nil
}

func (d *Dispatcher) workspaceSessionPath(ctx context.Context, relPath string, inbound InboundMessage) (string, bool, error) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" || strings.HasPrefix(relPath, "../") {
		return "", false, nil
	}
	externalUserID := strings.TrimSpace(inbound.FromUserID)
	if externalUserID == "" {
		externalUserID = strings.TrimSpace(inbound.Binding.ExternalUserID)
	}
	root := ""
	if chatSession, ok := d.store.FindExternalChatSession(inbound.Binding.ID, externalUserID, ""); ok {
		root = strings.TrimSpace(chatSession.WorkspaceRoot)
		if root == "" {
			session, ok, err := d.store.GetSession(ctx, chatSession.LinkedSessionID)
			if err != nil {
				return "", false, err
			}
			if ok {
				root = strings.TrimSpace(session.WorkspaceRoot)
			}
		}
	}
	if root == "" {
		return "", false, nil
	}
	absPath := filepath.Join(root, relPath)
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false, nil
	}
	cleanPath, err := filepath.Abs(absPath)
	if err != nil || !strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator)) {
		return "", false, nil
	}
	if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
		return cleanPath, true, nil
	}
	return "", false, nil
}
