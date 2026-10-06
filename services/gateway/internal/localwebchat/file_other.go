//go:build !unix

package localwebchat

import "os"

// Platforms without Unix ownership and private-domain sockets fail closed.
func ownedByCurrentUser(os.FileInfo) bool { return false }
