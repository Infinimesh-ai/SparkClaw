package workspacefiles

import (
	"errors"
	"path"
	"strings"
	"unicode"
)

var ErrUnsafePath = errors.New("workspace file path is unsafe")

// ValidateSharePath rejects private runtime state as well as noncanonical paths.
// Mail sources have their own owner-scoped download API and cannot be attached
// through the generic workspace surface.
func ValidateSharePath(relative string) error {
	if relative == "" || len(relative) > 1024 || path.IsAbs(relative) || path.Clean(relative) != relative || relative == "." || (strings.ContainsAny(relative, "\\:") || strings.IndexFunc(relative, unicode.IsControl) >= 0) {
		return ErrUnsafePath
	}
	for _, component := range strings.Split(relative, "/") {
		if component == "" || strings.HasPrefix(component, ".") {
			return ErrUnsafePath
		}
		switch strings.ToLower(component) {
		case "email", "email-send", "email-send-attachments", "node_modules", "vendor":
			return ErrUnsafePath
		}
	}
	return nil
}
