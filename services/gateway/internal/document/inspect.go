package document

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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

func readPrefix(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit))
}
