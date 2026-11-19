package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// The business domains (mail, settings, medicine, speech, notifications,
// tools, presentation, browser receipts, transfer/object for large bodies,
// MCD selections and invalidation events) are reached through domain.call.
// The method is the domain operation's name and the arguments are its
// typed fields; the Gateway runs the domain's business adapter directly for
// the device the connector authenticated. No ISCP transport session, no
// HTTP routing and no transport negotiation is involved: feature
// availability follows the device conditions (design 3.9).

// linkBridgeExcluded lists operations replaced by core link operations or
// owned by the console.
var linkBridgeExcluded = map[string]string{
	"execution":      "conversation runs use message.send, run.* and approval.*",
	"approvals":      "use approval.list and approval.resolve",
	"clients":        "devices join and leave on the console",
	"authorizations": "device authorization is managed by the console",
	"installation":   "the link layer binds the device installation",
	"phone":          "use projection.snapshot and projection.activity",
	"capabilities":   "use device.describe",
	"workbench":      "use device.describe",
	"state":          "use sync.changes",
	"audio":          "use the speech domain",
}

// linkBridgeArgs are the typed fields of one domain operation.
type linkBridgeArgs struct {
	Params           map[string]string              `json:"params,omitempty"`
	Body             json.RawMessage                `json:"body,omitempty"`
	Object           *iscpworkbench.ObjectReference `json:"body_object,omitempty"`
	ExpectedRevision string                         `json:"expected_revision,omitempty"`
	InputDigest      string                         `json:"input_digest,omitempty"`
	RequestID        string                         `json:"request_id,omitempty"`
}

// linkBridgeResult is the domain's own outcome, unchanged.
type linkBridgeResult struct {
	Status    int                            `json:"status"`
	Body      json.RawMessage                `json:"body,omitempty"`
	Object    *iscpworkbench.ObjectReference `json:"body_object,omitempty"`
	ErrorCode string                         `json:"error_code,omitempty"`
	Error     string                         `json:"error,omitempty"`
	Retryable bool                           `json:"retryable,omitempty"`
}

type linkBridge struct {
	mu       sync.Mutex
	handlers map[string]iscpworkbench.Handler
}

var linkBridges sync.Map // *Server -> *linkBridge

func (s *Server) linkBridge() *linkBridge {
	v, _ := linkBridges.LoadOrStore(s, &linkBridge{handlers: map[string]iscpworkbench.Handler{}})
	return v.(*linkBridge)
}

// linkInstallationID is the device's stable installation UUID.
func linkInstallationID(deviceID string) string {
	sum := sha256.Sum256([]byte("sparkclaw-link-installation-v1\x00" + deviceID))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// linkForwardOperations lists the domain operations a device may call.
func linkForwardOperations(c contract.Conditions) []string {
	var out []string
	for _, op := range iscpworkbench.OperationsV2ForMode("node-managed") {
		spec, ok := iscpworkbench.LookupOperation(op)
		if !ok || spec.Direction != "forward" {
			continue
		}
		prefix := strings.SplitN(op, ".", 2)[0]
		if _, excluded := linkBridgeExcluded[prefix]; excluded {
			continue
		}
		if (prefix == "mcd" || prefix == "browser") && !c.BrowserSurface {
			continue
		}
		out = append(out, op)
	}
	return out
}

func (s *Server) linkBridgeHandler(p link.Principal) (iscpworkbench.Handler, iscpworkbench.Config, error) {
	b := s.linkBridge()
	b.mu.Lock()
	defer b.mu.Unlock()
	binding := iscpworkbench.Binding{DeploymentID: s.cfg.Gateway.DeploymentID, OwnerID: p.OwnerID, ClientID: p.ClientID}
	cfg := iscpworkbench.Config{Mode: "link", Role: iscpworkbench.RoleResponder, ApplicationProfiles: []string{iscpworkbench.ProfileV2}, Binding: &binding, ExpectedInstallationID: linkInstallationID(p.DeviceID), QualifiedCapabilities: linkForwardOperations(contract.Conditions{BrowserSurface: true})}
	if h := b.handlers[p.ClientID]; h != nil {
		return h, cfg, nil
	}
	domain, err := s.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		return nil, cfg, err
	}
	objects, err := s.NewWorkbenchISCPObjectHandler(cfg, domain)
	if err != nil {
		return nil, cfg, err
	}
	b.handlers[p.ClientID] = objects
	return objects, cfg, nil
}

// ensureLinkInstallation binds the device's installation for the domain
// adapters' execution-scoped state.
func (s *Server) ensureLinkInstallation(p link.Principal) error {
	service, err := s.executionService()
	if err != nil {
		return err
	}
	return service.Bind(p.OwnerID, p.ClientID, linkInstallationID(p.DeviceID))
}

func (s *Server) linkBridgeCall(ctx context.Context, req *link.Request, p contract.DomainCallParams) (any, error) {
	spec, known := iscpworkbench.LookupOperation(p.Method)
	prefix := strings.SplitN(p.Method, ".", 2)[0]
	if !known || spec.Direction != "forward" || spec.Version != 2 || prefix != p.Domain {
		return nil, link.Errorf(contract.CodeUnsupportedCapability, "domain method %s.%s is not available", p.Domain, p.Method)
	}
	if why, excluded := linkBridgeExcluded[prefix]; excluded {
		return nil, link.Errorf(contract.CodeUnsupportedCapability, "%s: %s", p.Method, why)
	}
	allowed := false
	for _, op := range linkForwardOperations(req.Principal.Conditions) {
		allowed = allowed || op == p.Method
	}
	if !allowed {
		return nil, link.Errorf(contract.CodeUnsupportedCapability, "this device cannot use %s", p.Method)
	}
	var args linkBridgeArgs
	if len(p.Args) > 0 {
		if err := json.Unmarshal(p.Args, &args); err != nil {
			return nil, link.Errorf(contract.CodeInvalidRequest, "args: %v", err)
		}
	}
	if spec.Mutation && p.IdempotencyKey == "" {
		return nil, link.Errorf(contract.CodeInvalidRequest, "%s needs an idempotency_key (a UUID)", p.Method)
	}
	if err := s.ensureLinkInstallation(req.Principal); err != nil {
		return nil, linkConversationError(err)
	}
	handler, cfg, err := s.linkBridgeHandler(req.Principal)
	if err != nil {
		return nil, &contract.Error{Code: contract.CodeBackendUnavailable, Message: "business domains are unavailable", Retryable: true}
	}
	request := iscpworkbench.Request{
		Type: iscpworkbench.RequestType, Profile: iscpworkbench.ProfileV2, ID: newLinkUUID(), Operation: p.Method,
		InstallationID: linkInstallationID(req.Principal.DeviceID), OperationID: p.IdempotencyKey, ExpectedRevision: args.ExpectedRevision,
		Params: args.Params, Object: args.Object, Body: args.Body, InputDigest: args.InputDigest, RequestID: args.RequestID,
	}
	if err := request.Validate(); err != nil {
		return nil, link.Errorf(contract.CodeInvalidRequest, "%v", err)
	}
	connected, release, err := s.clientConnectionContext(ctx, req.Principal.ClientID)
	if err != nil {
		return nil, link.Errorf(contract.CodeRevoked, "this device was revoked")
	}
	defer release()
	principal := linkRequestPrincipal(req)
	connected = context.WithValue(connected, requestPrincipalContextKey{}, principal)
	operations := linkForwardOperations(req.Principal.Conditions)
	session := iscpworkbench.SessionInfo{SessionID: "link:" + req.Principal.DeviceID, Profile: iscpworkbench.ProfileV2, GrantRevision: 1, Binding: cfg.Binding, QualifiedOperations: operations, NegotiatedOperations: operations}
	connected = iscpworkbench.ContextWithSession(connected, session)
	connected = withExecutionAuthorization(connected, s.workbenchExecutionAuthorization(session))
	response := workbenchV2Result(handler(connected, request))
	return contract.DomainCallResult{Result: mustRaw(linkBridgeResult{Status: response.Status, Body: response.Body, Object: response.Object, ErrorCode: string(response.Code), Error: response.Error, Retryable: response.Retryable})}, nil
}

func newLinkUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// workbenchV2Result gives domain failures their typed error code, which
// the desktop's recovery relies on (cursor gap, unknown outcome).
func workbenchV2Result(response iscpworkbench.Response) iscpworkbench.Response {
	if response.Status >= 400 && response.Code == "" {
		response.Code = iscpworkbench.CodeForStatus(response.Status)
		var body struct {
			Code      string `json:"code"`
			Retryable *bool  `json:"retryable"`
		}
		_ = json.Unmarshal(response.Body, &body)
		if body.Code == "operation_outcome_unknown" {
			response.Code = iscpworkbench.ErrorOutcomeUnknown
		} else if response.Status == http.StatusConflict && domainDecode(response.Body, &body) == nil && body.Code == string(iscpworkbench.ErrorCursorGap) && body.Retryable != nil && !*body.Retryable {
			// Only this closed domain error permits an event cursor reset.
			response.Code = iscpworkbench.ErrorCursorGap
		}
		response.Retryable = response.Status == 429 || response.Status == 503
	}
	return response
}
