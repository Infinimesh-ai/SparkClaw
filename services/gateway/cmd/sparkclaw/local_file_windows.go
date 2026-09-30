//go:build windows

package main

import "os"

func localFileOwnedByCurrentUser(os.FileInfo) bool { return false }
