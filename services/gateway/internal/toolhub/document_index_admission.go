package toolhub

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const indexMaxPackageBytes = 128 << 20
const indexMaxPackageEntryBytes = 64 << 20
const indexMaxPackageEntries = 20000

// Check Office expansion before a package parser allocates its object graph.
// This is bounded package I/O, independent of model context or source ZIP size.
func inspectIndexPackage(ctx context.Context, path, format string) error {
	if format != app.DocumentFormatDOCX && format != app.DocumentFormatXLSX && format != app.DocumentFormatPPTX {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return err
	}
	denied := func(reason string) error {
		return &document.PipelineError{Code: document.CodeStrategyDeferred, Stage: document.StageRead, Format: format, Limit: indexMaxPackageBytes, Detail: "structure resource admission: " + reason}
	}
	if len(reader.File) > indexMaxPackageEntries {
		return denied("too many package entries")
	}
	total := uint64(0)
	seen := map[string]bool{}
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if seen[entry.Name] || strings.Contains(entry.Name, "\\") || strings.HasPrefix(entry.Name, "/") {
			return denied("ambiguous package entry")
		}
		seen[entry.Name] = true
		if entry.UncompressedSize64 > indexMaxPackageEntryBytes || total+entry.UncompressedSize64 > indexMaxPackageBytes {
			return denied("expanded package exceeds byte budget")
		}
		total += entry.UncompressedSize64
	}
	// Verify declared lengths and CRCs without retaining expanded bytes. A forged
	// central-directory length is not an admission bypass.
	buffer := make([]byte, 32<<10)
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		length := int64(0)
		for {
			if err = ctx.Err(); err != nil {
				break
			}
			var n int
			n, err = input.Read(buffer)
			length += int64(n)
			if length > int64(entry.UncompressedSize64) || length > indexMaxPackageEntryBytes {
				err = denied("expanded entry exceeds declared budget")
				break
			}
			if err != nil {
				break
			}
		}
		closeErr := input.Close()
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("validate package entry: %w", err)
		}
		if closeErr != nil {
			return closeErr
		}
		if length != int64(entry.UncompressedSize64) {
			return denied("expanded entry length changed")
		}
	}
	return nil
}
