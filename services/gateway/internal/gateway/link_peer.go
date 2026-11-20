package gateway

import (
	"context"
	"encoding/json"
	"errors"
)

// errLinkPeerUnavailable means no open link connection reaches the
// device's installation. Nothing was sent.
var errLinkPeerUnavailable = errors.New("no open link connection reaches the device")

// linkPeerResponse is a device's answer to a reverse call: the original
// reverse response envelope.
type linkPeerResponse struct {
	Status    int
	Body      json.RawMessage
	Code      string
	Error     string
	Retryable bool
}

// linkPeer is one open link connection of a device serving reverse
// operations. Done closes when the connection ends; a replacement
// connection never receives a call addressed to the old one.
type linkPeer struct {
	ID                    string
	InstallationID        string
	AuthorizationRevision uint64
	Done                  <-chan struct{}
	// Caller carries one reverse round trip on this connection.
	Caller func(ctx context.Context, operation string, body json.RawMessage) (linkPeerResponse, error)
}

// Command performs one reverse round trip on this exact connection. A
// refusal before sending is errLinkPeerUnavailable; any later failure
// leaves the outcome unknown to the caller.
func (p linkPeer) Command(ctx context.Context, operation string, body json.RawMessage) (linkPeerResponse, error) {
	if p.Caller == nil {
		return linkPeerResponse{}, errLinkPeerUnavailable
	}
	select {
	case <-p.Done:
		return linkPeerResponse{}, errLinkPeerUnavailable
	default:
	}
	return p.Caller(ctx, operation, body)
}
