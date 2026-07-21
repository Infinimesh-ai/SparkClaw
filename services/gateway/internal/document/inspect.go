package document

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var formatExtensions = map[string]string{
	app.DocumentFormatText: ".txt",
	app.DocumentFormatDOCX: ".docx",
	app.DocumentFormatXLSX: ".xlsx",
	app.DocumentFormatPPTX: ".pptx",
	app.DocumentFormatPDF:  ".pdf",
}

var formatContentTypes = map[string]string{
	app.DocumentFormatText:  "text/plain; charset=utf-8",
	app.DocumentFormatDOCX:  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	app.DocumentFormatXLSX:  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	app.DocumentFormatPPTX:  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	app.DocumentFormatPDF:   "application/pdf",
	app.DocumentFormatImage: "image/*",
}

// DetectFormat verifies signature-bearing formats before returning the
// canonical format used by document Workflow profiles and ToolHub.
func DetectFormat(path string) (string, error) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".pdf":
		raw, err := readPrefix(path, 8)
		if err != nil || len(raw) < 5 || string(raw[:5]) != "%PDF-" {
			return "", errors.New("PDF extension and signature do not match")
		}
		return app.DocumentFormatPDF, nil
	case ".docx", ".xlsx", ".pptx":
		archive, err := zip.OpenReader(path)
		if err != nil {
			return "", errors.New("Office extension and ZIP signature do not match")
		}
		defer archive.Close()
		required := map[string]string{".docx": "word/document.xml", ".xlsx": "xl/workbook.xml", ".pptx": "ppt/presentation.xml"}[extension]
		for _, file := range archive.File {
			if file.Name == required {
				return strings.TrimPrefix(extension, "."), nil
			}
		}
		return "", errors.New("Office extension and package type do not match")
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		if _, err := detectSupportedImageContentType(path, extension); err != nil {
			return "", err
		}
		return app.DocumentFormatImage, nil
	default:
		raw, err := readPrefix(path, 4096)
		if err != nil {
			return "", errors.New("document cannot be read for type inspection")
		}
		if strings.IndexByte(string(raw), 0) >= 0 {
			return "", fmt.Errorf("unsupported document extension %q", extension)
		}
		return app.DocumentFormatText, nil
	}
}

// InspectFile performs the resource and type stage shared by every document
// strategy. It deliberately returns metadata for oversized files so strategy
// selection can report an explicit deferred state.
func InspectFile(ctx context.Context, root, path string) (Metadata, error) {
	select {
	case <-ctx.Done():
		return Metadata{}, ctx.Err()
	default:
	}
	if strings.TrimSpace(root) == "" {
		return Metadata{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Detail: "workspace root is invalid"}
	}
	rootAbs, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return Metadata{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Detail: "workspace root is invalid"}
	}
	pathAbs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || pathAbs == rootAbs || !strings.HasPrefix(pathAbs, rootAbs+string(os.PathSeparator)) {
		return Metadata{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Detail: "document path escapes the workspace"}
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return Metadata{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Detail: "document path escapes the workspace"}
	}
	current := rootAbs
	for _, component := range strings.Split(relative, string(os.PathSeparator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return Metadata{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Detail: "document path is unavailable"}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return Metadata{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Detail: "document path must not traverse symlinks"}
		}
	}
	info, err := os.Lstat(pathAbs)
	if err != nil || !info.Mode().IsRegular() {
		return Metadata{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Detail: "document path must be a regular non-symlink file"}
	}
	format, err := DetectFormat(pathAbs)
	if err != nil {
		return Metadata{}, &PipelineError{Code: CodeFormatUnsupported, Stage: StageInspect, Detail: err.Error()}
	}
	contentType := ContentTypeForFormat(format)
	if format == app.DocumentFormatImage {
		contentType, err = detectSupportedImageContentType(pathAbs, strings.ToLower(filepath.Ext(pathAbs)))
		if err != nil {
			return Metadata{}, &PipelineError{Code: CodeFormatUnsupported, Stage: StageInspect, Format: format, Detail: err.Error()}
		}
	}
	metadata := Metadata{
		Path: pathAbs, Relative: filepath.ToSlash(relative), Format: format, ContentType: contentType,
		Size: info.Size(), ModifiedAt: info.ModTime().UTC(),
	}
	if metadata.Size <= SmallFileMaxBytes {
		metadata.SHA256, err = fileSHA256(ctx, pathAbs)
		if err != nil {
			return Metadata{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Format: format, Detail: "document hash could not be computed"}
		}
	}
	return metadata, nil
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ExtensionForFormat(format string) string {
	return formatExtensions[strings.ToLower(strings.TrimSpace(format))]
}

func ContentTypeForFormat(format string) string {
	return formatContentTypes[strings.ToLower(strings.TrimSpace(format))]
}

func IsSupportedImageContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func detectSupportedImageContentType(path, extension string) (string, error) {
	raw, err := readPrefix(path, 512)
	if err != nil {
		return "", errors.New("image cannot be read for type inspection")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(raw), ";")[0]))
	expected := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	}[strings.ToLower(strings.TrimSpace(extension))]
	if expected == "" || contentType != expected || !IsSupportedImageContentType(contentType) {
		return "", errors.New("image extension and signature do not match")
	}
	return contentType, nil
}

func readPrefix(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit))
}
