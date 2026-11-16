package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// phone serves the read-only phone projections over the node-issuer Workbench
// path. The request body is the Bridge payload (snapshot: {}, activity:
// {"date","limit"}); the response body is the unchanged Bridge
// agent.capability.v1 response so existing phone screens keep their parser.
func (a *iscpDomainAdapter) phone(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if a.server.bridge == nil {
		return domainError(503, "capability_unavailable")
	}
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	requestType := iscpbridge.TypeSnapshotGet
	if request.Operation == iscpworkbench.OperationPhoneActivityList {
		requestType = iscpbridge.TypeActivityList
	}
	payload := request.Body
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage(`{}`)
	}
	response := a.server.bridge.PhoneProjection(ctx, requestType, request.ID, principal.ClientID, principal.OwnerID, principal.ActorID, payload, time.Now().UTC())
	status := 200
	if response.Error != nil {
		status = 400
		if response.Error.Retryable {
			status = 503
		}
	}
	return domainJSON(status, response)
}
