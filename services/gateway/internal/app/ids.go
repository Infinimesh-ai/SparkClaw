package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

func NewID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_fallback"
	}
	return strings.TrimSuffix(prefix, "_") + "_" + hex.EncodeToString(b[:])
}

var operationIDPattern = regexp.MustCompile(`^op3\.(0|[1-9][0-9]{0,14})\.(0|[1-9][0-9]{0,14})\.[0-9a-f]{32}$`)

// NewOperationID issues an App-CLI lifecycle 3.0 operation identity in the
// runtime's open request epoch. The 128-bit random nonce means restoring an
// old product database can never reissue an identity that was already used.
func NewOperationID(generation, epoch int64) (string, error) {
	if generation <= 0 || epoch <= 0 {
		return "", fmt.Errorf("operation epoch is invalid")
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("op3.%d.%d.%s", generation, epoch, hex.EncodeToString(b[:])), nil
}

// OperationEpoch returns the generation and request epoch of an op3 identity.
func OperationEpoch(id string) (generation, epoch int64, ok bool) {
	if !operationIDPattern.MatchString(id) {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(id, "op3.%d.%d.", &generation, &epoch); err != nil {
		return 0, 0, false
	}
	return generation, epoch, true
}
