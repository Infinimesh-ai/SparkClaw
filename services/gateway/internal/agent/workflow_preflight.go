package agent

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

type documentPreflight struct {
	InputRef  string
	OutputRef string
	Format    string
}

func attachedWorkspaceImageCanFinalize(resources []app.MessagePart, content string) bool {
	if !imageInspectCanFinalize(content) {
		return false
	}
	for _, resource := range resources {
		if resource.Kind == app.MessagePartImage && resource.Resource != nil && resource.Resource.Kind == "workspace_file" && strings.TrimSpace(resource.Resource.Ref) != "" {
			return true
		}
	}
	return false
}

func documentRoutePaths(content string) []string {
	if paths := attachmentPathsFromRoutingProjection(content); len(paths) > 0 {
		return paths
	}
	return extractPaths(content)
}

func attachmentPathsFromRoutingProjection(content string) []string {
	const header = "Attached files for this user turn:"
	index := strings.LastIndex(content, header)
	if index < 0 {
		return nil
	}
	seen := map[string]bool{}
	paths := []string{}
	for _, line := range strings.Split(content[index+len(header):], "\n") {
		pathIndex := strings.Index(line, " path=")
		if pathIndex < 0 {
			continue
		}
		value := line[pathIndex+len(" path="):]
		for _, suffix := range []string{" content_type=", " bytes=", " size=", " sha256=", " media_kind="} {
			if suffixIndex := strings.Index(value, suffix); suffixIndex >= 0 {
				value = value[:suffixIndex]
			}
		}
		value = filepath.Clean(strings.TrimSpace(value))
		if value == "" || value == "." || seen[value] {
			continue
		}
		seen[value] = true
		paths = append(paths, value)
	}
	return paths
}

func preflightDocumentPath(workspaceRoot, requestedPath string, edit bool) (documentPreflight, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return documentPreflight{}, errors.New("workspace root is unavailable")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return documentPreflight{}, errors.New("workspace root is invalid")
	}
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" {
		return documentPreflight{}, errors.New("document path is required")
	}
	candidate := filepath.Clean(filepath.FromSlash(requestedPath))
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return documentPreflight{}, errors.New("document path escapes the workspace")
	}
	relativeCandidate, err := filepath.Rel(root, candidate)
	if err != nil || relativeCandidate == "." || strings.HasPrefix(relativeCandidate, ".."+string(os.PathSeparator)) {
		return documentPreflight{}, errors.New("document path escapes the workspace")
	}
	current := root
	for _, component := range strings.Split(relativeCandidate, string(os.PathSeparator)) {
		current = filepath.Join(current, component)
		componentInfo, componentErr := os.Lstat(current)
		if componentErr != nil {
			return documentPreflight{}, errors.New("document path is unavailable")
		}
		if componentInfo.Mode()&os.ModeSymlink != 0 {
			return documentPreflight{}, errors.New("document path must not traverse symlinks")
		}
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return documentPreflight{}, errors.New("document path is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return documentPreflight{}, errors.New("document path must be a regular non-symlink file")
	}
	format, err := document.DetectFormat(candidate)
	if err != nil {
		return documentPreflight{}, err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return documentPreflight{}, errors.New("document path escapes the workspace")
	}
	result := documentPreflight{InputRef: filepath.ToSlash(relative), Format: format}
	if edit {
		if format != app.DocumentFormatText && format != app.DocumentFormatDOCX && format != app.DocumentFormatXLSX && format != app.DocumentFormatPPTX && format != app.DocumentFormatPDF {
			return documentPreflight{}, fmt.Errorf("document format %q is read-only", format)
		}
		extension := filepath.Ext(relative)
		base := strings.TrimSuffix(filepath.Base(relative), extension) + "-sparkclaw-edit" + extension
		result.OutputRef = filepath.ToSlash(filepath.Join(filepath.Dir(relative), base))
		outputPath := filepath.Join(root, filepath.FromSlash(result.OutputRef))
		if _, err := os.Lstat(outputPath); err == nil {
			return documentPreflight{}, errors.New("document output copy already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return documentPreflight{}, errors.New("document output path is unavailable")
		}
	}
	return result, nil
}

func normalizeBrowserURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimSpace(raw)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}
