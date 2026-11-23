package iscpbridge

import (
	"context"
	"encoding/json"
	"time"
)

// PhoneProjection runs one read-only phone projection (TypeSnapshotGet or
// TypeActivityList) for ownerID, limited to the conversations of deviceID and returns the exact Bridge response, so a
// phone that moved to the node-issuer Workbench path keeps parsing the same
// agent.capability.v1 shape. endpointID only labels the response.
func (a *GatewayAdapter) PhoneProjection(ctx context.Context, requestType, requestID, endpointID, ownerID, actorID, deviceID string, payload json.RawMessage, now time.Time) Response {
	req := Request{ProtocolVersion: ProtocolVersion, Type: requestType, RequestID: requestID, EndpointID: endpointID, IssuedAt: now, ExpiresAt: now.Add(2 * time.Minute), Payload: payload}
	principal := Principal{OwnerID: ownerID, ActorID: actorID, DeviceID: deviceID}
	switch requestType {
	case TypeSnapshotGet:
		return a.snapshot(ctx, req, principal, now)
	case TypeActivityList:
		return a.listActivities(ctx, req, principal, now)
	default:
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "unsupported phone projection", false), now)
	}
}
