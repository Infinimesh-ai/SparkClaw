package browserhost

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

// ReverseSession binds Controller operations to one authenticated installation.
type ReverseSession interface {
	SessionID() string
	AuthorizationRevision() uint64
	Done() <-chan struct{}
	browsercontrol.ISCPControllerTransport
}

// ReverseRouter finds the live session for an installation using local state.
type ReverseRouter interface {
	ReverseSession(Identity) (ReverseSession, error)
}

func (b *Broker) SetReverseRouter(router ReverseRouter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reverse = router
}

// The desktop derives the same values from its own session, so it can verify
// every binding without trusting anything the Gateway invented.
func rawDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func reverseHostID(identity Identity, sessionID string) string {
	return "iscp_" + rawDigest("sparkclaw.browser.iscp.host.v1\x00" + identity.OwnerID + "\x00" + identity.ClientID + "\x00" + identity.InstallationID + "\x00" + sessionID)[:32]
}
func reverseAuthorizationDigest(sessionID string, revision uint64) string {
	return rawDigest("sparkclaw.browser.iscp.authorization.v1\x00" + sessionID + "\x00" + strconv.FormatUint(revision, 10))
}

func sessionDone(session ReverseSession) bool {
	select {
	case <-session.Done():
		return true
	default:
		return false
	}
}
