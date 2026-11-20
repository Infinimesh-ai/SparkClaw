package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// linkInstallationID is the device's stable installation UUID: the
// identity the execution service, the browser broker and McDonald's
// selections key a desktop's state by.
func linkInstallationID(deviceID string) string {
	sum := sha256.Sum256([]byte("sparkclaw-link-installation-v1\x00" + deviceID))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func newLinkUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// textOnlyExecutionContextKey marks an execution whose answer is text only.
type textOnlyExecutionContextKey struct{}

// linkDomainObjectsKey carries the calling device's domain objects to the
// business functions that read objects it uploaded (mail attachments).
type linkDomainObjectsKey struct{}

func linkDomainObjectsFrom(ctx context.Context) (linkObjectAccess, bool) {
	access, ok := ctx.Value(linkDomainObjectsKey{}).(linkObjectAccess)
	return access, ok && access.objects != nil
}

var errLinkDomainObjectUnavailable = errors.New("domain object unavailable")
