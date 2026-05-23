package app

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func NewID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_fallback"
	}
	return strings.TrimSuffix(prefix, "_") + "_" + hex.EncodeToString(b[:])
}
