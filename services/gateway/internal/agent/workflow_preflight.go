package agent

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
		result.OutputRef, err = nextDocumentOutputRef(root, relative)
		if err != nil {
			return documentPreflight{}, errors.New("document output path is unavailable")
		}
	}
	return result, nil
}

func nextDocumentOutputRef(root, inputRef string) (string, error) {
	directory := filepath.Dir(inputRef)
	entries, err := os.ReadDir(filepath.Join(root, directory))
	if err != nil {
		return "", err
	}
	usedNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		usedNames[entry.Name()] = struct{}{}
	}

	extension := filepath.Ext(inputRef)
	stem, firstSequence := documentOutputFamily(strings.TrimSuffix(filepath.Base(inputRef), extension))
	for offset := 0; offset <= len(entries); offset++ {
		sequence := firstSequence + offset
		name := stem + extension
		if sequence > 1 {
			name = fmt.Sprintf("%s-%d%s", stem, sequence, extension)
		}
		if _, exists := usedNames[name]; !exists {
			return filepath.ToSlash(filepath.Join(directory, name)), nil
		}
	}
	return "", errors.New("no output copy name is available")
}

func documentOutputFamily(inputStem string) (string, int) {
	const marker = "-sparkclaw-edit"
	if strings.HasSuffix(inputStem, marker) {
		return inputStem, 2
	}
	if markerIndex := strings.LastIndex(inputStem, marker+"-"); markerIndex >= 0 {
		suffix := inputStem[markerIndex+len(marker)+1:]
		if sequence, err := strconv.Atoi(suffix); err == nil && sequence >= 2 && strconv.Itoa(sequence) == suffix {
			return inputStem[:markerIndex] + marker, sequence + 1
		}
	}
	return inputStem + marker, 1
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
