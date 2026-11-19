package gateway

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// Device capabilities over the link layer (design 3.12): a desktop with a
// built-in browser declares the controller and McDonald's operations at
// hello; the Gateway's browser broker and McDonald's host reach it through
// the same reverse routes they used before, now carried by link reverse
// calls. Bodies and behavior are unchanged.

// linkPeerRoute finds an open link connection of the device that serves
// reverse operations for this installation.
func (s *Server) linkPeerRoute(ownerID, clientID, installationID string) (iscpworkbench.PeerSession, error) {
	rt := s.link
	if rt == nil {
		return iscpworkbench.PeerSession{}, iscpworkbench.ErrPeerUnavailable
	}
	for _, conn := range rt.service.Connections("") {
		p := conn.Principal
		if p.OwnerID != ownerID || p.ClientID != clientID || linkInstallationID(p.DeviceID) != installationID || len(conn.Reverse) == 0 {
			continue
		}
		c := conn
		return iscpworkbench.PeerSession{
			ID:                    "link:" + p.DeviceID,
			InstallationID:        installationID,
			AuthorizationRevision: 1,
			Done:                  c.Context().Done(),
			Caller: func(ctx context.Context, operation string, body json.RawMessage) (iscpworkbench.Response, error) {
				raw, err := c.Call(ctx, operation, json.RawMessage(body))
				if errors.Is(err, link.ErrReverseUnavailable) {
					return iscpworkbench.Response{}, iscpworkbench.ErrPeerUnavailable
				}
				if err != nil {
					var cerr *contract.Error
					if errors.As(err, &cerr) {
						status := 500
						switch cerr.Code {
						case contract.CodeNotFound:
							status = 404
						case contract.CodeConflict, contract.CodeBusy:
							status = 409
						case contract.CodeInvalidRequest:
							status = 400
						}
						return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: iscpworkbench.ProfileV2, Status: status, Error: cerr.Message, Code: iscpworkbench.ErrorCode(cerr.Code)}, nil
					}
					return iscpworkbench.Response{}, err
				}
				return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: iscpworkbench.ProfileV2, Status: 200, Body: raw}, nil
			},
		}, nil
	}
	return iscpworkbench.PeerSession{}, iscpworkbench.ErrPeerUnavailable
}

// linkBrowserAdapter binds a run's browser tools to the device's built-in
// browser.
func (s *Server) linkBrowserAdapter(p link.Principal, sessionID, runID string) (browserautomation.Adapter, error) {
	broker, err := s.browserHostBroker()
	if err != nil {
		return nil, err
	}
	return broker.ForScope(browserhost.Scope{Identity: browserhost.Identity{OwnerID: p.OwnerID, ClientID: p.ClientID, InstallationID: linkInstallationID(p.DeviceID)}, ConversationID: sessionID, TaskID: runID}), nil
}
