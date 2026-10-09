//go:build !unix

package workspacefiles

import (
	"context"
	"errors"
)

// The Gateway currently runs on Linux and macOS. Other consumers may link the
// portable workspace helpers, but attachment reads must not weaken symlink
// confinement on an unqualified platform.
func ReadRegular(context.Context, string, string, int64) ([]byte, error) {
	return nil, errors.New("secure workspace attachment reads are unsupported on this platform")
}
