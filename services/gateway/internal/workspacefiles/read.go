package workspacefiles

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"
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

// ReadRegular opens each component relative to an already opened directory,
// without following symlinks. It never blocks opening a FIFO/device, and holds
// the descriptor throughout the bounded read so path substitution cannot change
// which bytes will be sent.
func ReadRegular(ctx context.Context, root, relative string, maximum int64) ([]byte, error) {
	if err := ValidateSharePath(relative); err != nil {
		return nil, err
	}
	if maximum < 0 {
		return nil, ErrUnsafePath
	}
	directory, err := unix.Open(root, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { unix.Close(directory) }()
	components := strings.Split(relative, "/")
	for _, component := range components[:len(components)-1] {
		next, err := unix.Openat(directory, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			return nil, err
		}
		unix.Close(directory)
		directory = next
	}
	fd, err := unix.Openat(directory, components[len(components)-1], unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), relative)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return nil, ErrUnsafePath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if len(data) > int(maximum) {
		return nil, ErrUnsafePath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
