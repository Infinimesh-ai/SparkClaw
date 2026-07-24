package weixin

import (
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

func (d *Dispatcher) workspaceMediaPath(mediaPath string, inbound InboundMessage) (string, bool) {
	mediaPath = strings.TrimSpace(mediaPath)
	if mediaPath == "" {
		return "", false
	}
	relPath := ""
	if !filepath.IsAbs(mediaPath) {
		relPath = filepath.ToSlash(strings.TrimSpace(mediaPath))
	}
	absPath := ""
	if filepath.IsAbs(mediaPath) {
		cleaned, err := filepath.Abs(mediaPath)
		if err != nil {
			return "", false
		}
		absPath = filepath.Clean(cleaned)
	}
	for _, object := range d.store.ListArtifactObjects(200) {
		key := filepath.ToSlash(object.Key)
		objectPath := strings.TrimSpace(object.Path)
		if !strings.HasPrefix(key, "media/") || objectPath == "" {
			continue
		}
		if relPath != "" && key == relPath {
			return object.Path, true
		}
		if absPath != "" {
			if objectAbs, err := filepath.Abs(objectPath); err == nil && filepath.Clean(objectAbs) == absPath {
				return object.Path, true
			}
		}
	}
	if relPath != "" {
		if path, ok := d.workspaceSessionPath(relPath, inbound); ok {
			return path, true
		}
	}
	return "", false
}

var workspaceFilePathPattern = regexp.MustCompile(`(?:workspace://)?((?:outputs|uploads)/[A-Za-z0-9._~!$&'()*+,;=:@%/\-]+\.(?:docx|xlsx|pptx|pdf|txt|md|csv|tsv))`)

func (d *Dispatcher) workspaceFilePath(answer string, inbound InboundMessage) (string, string, bool) {
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
		if absPath, ok := d.workspaceObjectPath(relPath); ok {
			return absPath, filepath.Base(relPath), true
		}
		if absPath, ok := d.workspaceSessionPath(relPath, inbound); ok {
			return absPath, filepath.Base(relPath), true
		}
	}
	return "", "", false
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

func (d *Dispatcher) workspaceObjectPath(relPath string) (string, bool) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	for _, object := range d.store.ListArtifactObjects(200) {
		if filepath.ToSlash(object.Key) == relPath && strings.TrimSpace(object.Path) != "" {
			return object.Path, true
		}
	}
	return "", false
}

func (d *Dispatcher) workspaceSessionPath(relPath string, inbound InboundMessage) (string, bool) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" || strings.HasPrefix(relPath, "../") {
		return "", false
	}
	externalUserID := strings.TrimSpace(inbound.FromUserID)
	if externalUserID == "" {
		externalUserID = strings.TrimSpace(inbound.Binding.ExternalUserID)
	}
	root := ""
	if chatSession, ok := d.store.FindExternalChatSession(inbound.Binding.ID, externalUserID, ""); ok {
		root = strings.TrimSpace(chatSession.WorkspaceRoot)
		if root == "" {
			if session, ok := d.store.GetSession(chatSession.LinkedSessionID); ok {
				root = strings.TrimSpace(session.WorkspaceRoot)
			}
		}
	}
	if root == "" {
		return "", false
	}
	absPath := filepath.Join(root, relPath)
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	cleanPath, err := filepath.Abs(absPath)
	if err != nil || !strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator)) {
		return "", false
	}
	if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
		return cleanPath, true
	}
	return "", false
}
