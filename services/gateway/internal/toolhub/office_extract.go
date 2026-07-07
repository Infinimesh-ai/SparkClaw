package toolhub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const maxOfficeEntryBytes = 5_000_000

var officeXMLSpace = regexp.MustCompile(`\s+`)

func extractOfficeText(path string, raw []byte) (string, bool, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".docx":
		text, err := extractOfficeZipText(raw, func(name string) bool {
			return name == "word/document.xml" || strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer")
		})
		return text, true, err
	case ".pptx":
		text, err := extractOfficeZipText(raw, func(name string) bool {
			return strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml")
		})
		return text, true, err
	case ".xlsx":
		text, err := extractXLSXText(raw)
		return text, true, err
	default:
		return "", false, nil
	}
}

func extractOfficeZipText(raw []byte, keep func(string) bool) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", fmt.Errorf("invalid office file: %w", err)
	}
	names := make([]string, 0, len(reader.File))
	entries := map[string]*zip.File{}
	for _, file := range reader.File {
		if keep(file.Name) {
			names = append(names, file.Name)
			entries[file.Name] = file
		}
	}
	slices.Sort(names)
	var parts []string
	for _, name := range names {
		text, err := extractXMLTextFromZipEntry(entries[name])
		if err != nil {
			return "", err
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func extractXLSXText(raw []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", fmt.Errorf("invalid office file: %w", err)
	}
	sharedStrings, err := extractSharedStrings(reader)
	if err != nil {
		return "", err
	}
	names := []string{}
	entries := map[string]*zip.File{}
	for _, file := range reader.File {
		if strings.HasPrefix(file.Name, "xl/worksheets/sheet") && strings.HasSuffix(file.Name, ".xml") {
			names = append(names, file.Name)
			entries[file.Name] = file
		}
	}
	slices.Sort(names)
	var sheets []string
	for _, name := range names {
		text, err := extractWorksheetText(entries[name], sharedStrings)
		if err != nil {
			return "", err
		}
		if text != "" {
			sheets = append(sheets, text)
		}
	}
	return strings.Join(sheets, "\n\n"), nil
}

func extractSharedStrings(reader *zip.Reader) ([]string, error) {
	for _, file := range reader.File {
		if file.Name != "xl/sharedStrings.xml" {
			continue
		}
		raw, err := readZipEntry(file)
		if err != nil {
			return nil, err
		}
		values := []string{}
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		var current strings.Builder
		inSI := false
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			switch tok := token.(type) {
			case xml.StartElement:
				if tok.Name.Local == "si" {
					current.Reset()
					inSI = true
				}
			case xml.EndElement:
				if tok.Name.Local == "si" && inSI {
					values = append(values, normalizeOfficeText(current.String()))
					inSI = false
				}
			case xml.CharData:
				if inSI {
					current.Write([]byte(tok))
				}
			}
		}
		return values, nil
	}
	return nil, nil
}

func extractWorksheetText(file *zip.File, sharedStrings []string) (string, error) {
	raw, err := readZipEntry(file)
	if err != nil {
		return "", err
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var rows []string
	var cells []string
	inV := false
	cellType := ""
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch tok := token.(type) {
		case xml.StartElement:
			switch tok.Name.Local {
			case "row":
				cells = nil
			case "c":
				cellType = attrValue(tok.Attr, "t")
			case "v", "t":
				inV = true
			}
		case xml.EndElement:
			switch tok.Name.Local {
			case "row":
				if len(cells) > 0 {
					rows = append(rows, strings.Join(cells, "\t"))
				}
			case "v", "t":
				inV = false
			}
		case xml.CharData:
			if !inV {
				continue
			}
			value := normalizeOfficeText(string(tok))
			if value == "" {
				continue
			}
			if cellType == "s" {
				if idx, ok := parseNonNegativeInt(value); ok && idx < len(sharedStrings) {
					value = sharedStrings[idx]
				}
			}
			cells = append(cells, value)
		}
	}
	return strings.Join(rows, "\n"), nil
}

func extractXMLTextFromZipEntry(file *zip.File) (string, error) {
	raw, err := readZipEntry(file)
	if err != nil {
		return "", err
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var parts []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if data, ok := token.(xml.CharData); ok {
			text := normalizeOfficeText(string(data))
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, " "), nil
}

func readZipEntry(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > maxOfficeEntryBytes {
		return nil, fmt.Errorf("office entry %s is too large", file.Name)
	}
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxOfficeEntryBytes+1))
}

func normalizeOfficeText(text string) string {
	return strings.TrimSpace(officeXMLSpace.ReplaceAllString(text, " "))
}

func attrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

func parseNonNegativeInt(value string) (int, bool) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
