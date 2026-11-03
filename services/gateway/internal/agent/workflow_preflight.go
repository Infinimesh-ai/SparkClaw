package agent

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/workspacefiles"
)

type documentPreflight struct {
	InputRef  string
	OutputRef string
	Format    string
}

// documentPreflightError keeps the original diagnostic text while carrying the
// stable route reason the owner message is selected by.
type documentPreflightError struct {
	reason  string
	message string
}

func (e *documentPreflightError) Error() string { return e.message }

func documentPreflightFailure(reason, message string) error {
	return &documentPreflightError{reason: reason, message: message}
}

func documentPreflightReason(err error) string {
	var failure *documentPreflightError
	if errors.As(err, &failure) {
		return failure.reason + ": " + failure.message
	}
	return "document_preflight_failed: " + err.Error()
}

func preflightExternalMCPDocumentPath(requestedPath string, edit bool) (documentPreflight, error) {
	requestedPath = strings.TrimSpace(strings.ReplaceAll(requestedPath, "\\", "/"))
	if requestedPath == "" || strings.ContainsRune(requestedPath, 0) || strings.Contains(requestedPath, "://") {
		return documentPreflight{}, documentPreflightFailure("document_path_unavailable", "document path is invalid")
	}
	cleaned := path.Clean(requestedPath)
	if strings.HasPrefix(cleaned, "/") || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return documentPreflight{}, documentPreflightFailure("document_path_outside_workspace", "document path must be workspace-relative")
	}
	format := documentFormatFromMetadata(path.Base(cleaned), "")
	if format == "" {
		return documentPreflight{}, documentPreflightFailure("document_format_unrecognized", "document format cannot be determined from the requested path")
	}
	if edit && format != app.DocumentFormatText && format != app.DocumentFormatDOCX && format != app.DocumentFormatXLSX && format != app.DocumentFormatPPTX && format != app.DocumentFormatPDF {
		return documentPreflight{}, documentPreflightFailure("document_format_read_only", fmt.Sprintf("document format %q is read-only", format))
	}
	return documentPreflight{InputRef: cleaned, Format: format}, nil
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

var structuredDocumentLocator = regexp.MustCompile(`\bdocument\.[A-Za-z_][A-Za-z0-9_]*\[[0-9]+\]`)

func documentRoutePaths(content string) []string {
	if paths := attachmentPathsFromRoutingProjection(content); len(paths) > 0 {
		return paths
	}
	// Structured document block locations are operation qualifiers, not file
	// names. Keep the original request intact while excluding them from paths.
	content = structuredDocumentLocator.ReplaceAllString(content, " ")
	paths := extractPaths(content)
	for _, path := range unicodeDocumentPaths(content) {
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return paths
}

// unicodeDocumentName matches a file name whose stem has non-ASCII letters,
// such as 计算机网络.pdf, which the ASCII path patterns cannot see. Chinese
// text has no spaces, so the match may start with preceding words
// ("根据计算机网络.pdf"); workspaceDocumentPath trims them against the
// workspace.
var unicodeDocumentName = regexp.MustCompile(`[\p{L}\p{N}_\-]+(?:[/\\][\p{L}\p{N}_.\-]+)*\.[A-Za-z0-9]{1,8}\b`)

func unicodeDocumentPaths(content string) []string {
	out := []string{}
	for _, match := range unicodeDocumentName.FindAllString(content, -1) {
		if strings.IndexFunc(match, func(r rune) bool { return r > unicode.MaxASCII && unicode.IsLetter(r) }) < 0 {
			continue
		}
		clean := filepath.Clean(match)
		if !slices.Contains(out, clean) {
			out = append(out, clean)
		}
	}
	return out
}

// workspaceDocumentPath returns the longest suffix of a non-ASCII path
// candidate that names a regular file under root, dropping words written
// directly before the file name. Other candidates are returned unchanged.
func workspaceDocumentPath(root, path string) string {
	if strings.TrimSpace(root) == "" || strings.IndexFunc(path, func(r rune) bool { return r > unicode.MaxASCII }) < 0 {
		return path
	}
	for offset := range path {
		candidate := path[offset:]
		if info, err := os.Stat(filepath.Join(root, candidate)); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return path
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
		return documentPreflight{}, documentPreflightFailure("document_workspace_unavailable", "workspace root is unavailable")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return documentPreflight{}, documentPreflightFailure("document_workspace_unavailable", "workspace root is invalid")
	}
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" {
		return documentPreflight{}, documentPreflightFailure("document_path_unavailable", "document path is required")
	}
	candidate := filepath.Clean(filepath.FromSlash(requestedPath))
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return documentPreflight{}, documentPreflightFailure("document_path_outside_workspace", "document path escapes the workspace")
	}
	relativeCandidate, err := filepath.Rel(root, candidate)
	if err != nil || relativeCandidate == "." || strings.HasPrefix(relativeCandidate, ".."+string(os.PathSeparator)) {
		return documentPreflight{}, documentPreflightFailure("document_path_outside_workspace", "document path escapes the workspace")
	}
	current := root
	for _, component := range strings.Split(relativeCandidate, string(os.PathSeparator)) {
		current = filepath.Join(current, component)
		componentInfo, componentErr := os.Lstat(current)
		if componentErr != nil {
			return documentPreflight{}, documentPreflightFailure("document_path_unavailable", "document path is unavailable")
		}
		if componentInfo.Mode()&os.ModeSymlink != 0 {
			return documentPreflight{}, documentPreflightFailure("document_path_outside_workspace", "document path must not traverse symlinks")
		}
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return documentPreflight{}, documentPreflightFailure("document_path_unavailable", "document path is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return documentPreflight{}, documentPreflightFailure("document_path_outside_workspace", "document path must be a regular non-symlink file")
	}
	format, err := document.DetectFormat(candidate)
	if err != nil {
		return documentPreflight{}, documentPreflightFailure("document_format_unrecognized", err.Error())
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return documentPreflight{}, documentPreflightFailure("document_path_outside_workspace", "document path escapes the workspace")
	}
	result := documentPreflight{InputRef: filepath.ToSlash(relative), Format: format}
	if edit {
		if format != app.DocumentFormatText && format != app.DocumentFormatDOCX && format != app.DocumentFormatXLSX && format != app.DocumentFormatPPTX && format != app.DocumentFormatPDF {
			return documentPreflight{}, documentPreflightFailure("document_format_read_only", fmt.Sprintf("document format %q is read-only", format))
		}
		result.OutputRef, err = nextDocumentOutputRef(root, relative)
		if err != nil {
			return documentPreflight{}, documentPreflightFailure("document_workspace_unavailable", "document output path is unavailable")
		}
	}
	return result, nil
}

func nextDocumentOutputRef(root, inputRef string) (string, error) {
	directory := filepath.Dir(inputRef)
	if strings.HasPrefix(filepath.ToSlash(inputRef), "conversation-files/") {
		directory = "."
	}
	entries, err := os.ReadDir(filepath.Join(root, directory))
	if err != nil {
		return "", err
	}
	usedNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		usedNames[entry.Name()] = struct{}{}
	}

	baseFilename, firstSequence := workspacefiles.VersionFamily(filepath.Base(inputRef))
	for offset := 0; offset <= len(entries); offset++ {
		sequence := firstSequence + offset
		name := workspacefiles.VersionedName(baseFilename, sequence)
		if _, exists := usedNames[name]; !exists {
			return filepath.ToSlash(filepath.Join(directory, name)), nil
		}
	}
	return "", errors.New("no output copy name is available")
}
