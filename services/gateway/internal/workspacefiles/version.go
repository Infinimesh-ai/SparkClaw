package workspacefiles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxVersionedFileAttempts = 10000

// VersionFamily returns the unnumbered filename and the next sequence after
// filename. Canonical suffixes start at 2, for example report-2.pdf.
func VersionFamily(filename string) (baseFilename string, nextSequence int) {
	stem, extension := splitExtension(filename)
	separator := strings.LastIndexByte(stem, '-')
	if separator < 0 || separator == len(stem)-1 {
		return filename, 2
	}
	sequenceText := stem[separator+1:]
	sequence, err := strconv.Atoi(sequenceText)
	maxInt := int(^uint(0) >> 1)
	if err != nil || sequence < 2 || sequence == maxInt || strconv.Itoa(sequence) != sequenceText {
		return filename, 2
	}
	return stem[:separator] + extension, sequence + 1
}

// VersionedName inserts a canonical sequence immediately before the extension.
func VersionedName(baseFilename string, sequence int) string {
	if sequence <= 1 {
		return baseFilename
	}
	stem, extension := splitExtension(baseFilename)
	return fmt.Sprintf("%s-%d%s", stem, sequence, extension)
}

// OpenVersionedFile atomically creates filename or the next available member
// of its numeric version family. Existing files are never overwritten.
func OpenVersionedFile(directory, filename string, perm fs.FileMode) (*os.File, string, error) {
	if filename == "" || filename == "." || filename == ".." || filepath.Base(filename) != filename {
		return nil, "", errors.New("versioned filename must be a plain filename")
	}
	baseFilename, sequence := VersionFamily(filename)
	maxInt := int(^uint(0) >> 1)
	candidate := filename
	for attempt := 0; attempt < maxVersionedFileAttempts; attempt++ {
		if attempt > 0 {
			candidate = VersionedName(baseFilename, sequence)
		}
		file, err := os.OpenFile(filepath.Join(directory, candidate), os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err == nil {
			return file, candidate, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
		if attempt > 0 {
			if sequence == maxInt {
				break
			}
			sequence++
		}
	}
	return nil, "", fmt.Errorf("no versioned filename available after %d attempts", maxVersionedFileAttempts)
}

func splitExtension(filename string) (string, string) {
	extension := filepath.Ext(filename)
	if extension == filepath.Base(filename) {
		extension = ""
	}
	return strings.TrimSuffix(filename, extension), extension
}
