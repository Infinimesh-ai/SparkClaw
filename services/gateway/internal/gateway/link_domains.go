package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// linkDomainMethod is one business-domain method reachable through
// domain.call. Methods take typed arguments, call business functions
// directly and fail with link error codes; none builds an http.Request
// (design 3.1). They are registered once, at package initialization, and
// receive the Server that serves the request.
type linkDomainMethod struct {
	// Mutation methods require an idempotency key and are replayed from
	// the receipt journal on retry.
	Mutation bool
	Call     func(ctx context.Context, s *Server, req *link.Request, args json.RawMessage) (any, error)
}

// linkArgs decodes a domain method's typed arguments. Unknown fields are
// ignored, as in every link operation.
func linkArgs[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, link.Errorf(contract.CodeInvalidRequest, "args: %v", err)
	}
	return v, nil
}

// linkDomainResult is the domain result {status, body}: the answer the
// host WebChat gives for the same business call, read unchanged by the
// desktop's original screens. A body too large for one frame is delivered
// as a domain object (body_object).
type linkDomainResult struct {
	Status int              `json:"status"`
	Body   json.RawMessage  `json:"body,omitempty"`
	Object *domainObjectRef `json:"body_object,omitempty"`
}

// linkReply is a domain method answered by a business function's reply.
// The function runs for the device's owner, under a context that ends when
// the device is revoked. A change cut off before its answer, or one whose
// answer cannot prove its effect, stays fenced as outcome unknown.
func linkReply[T any](mutation bool, call func(ctx context.Context, s *Server, p requestPrincipal, args T) domainReply) linkDomainMethod {
	return linkDomainMethod{Mutation: mutation, Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
		args, err := linkArgs[T](raw)
		if err != nil {
			return nil, err
		}
		connected, release, err := s.clientConnectionContext(ctx, req.Principal.ClientID)
		if err != nil {
			return nil, link.Errorf(contract.CodeRevoked, "this device was revoked")
		}
		defer release()
		principal := linkRequestPrincipal(req)
		connected = context.WithValue(connected, requestPrincipalContextKey{}, principal)
		connected = context.WithValue(connected, linkDomainObjectsKey{}, linkObjectAccess{objects: s.link.domainObj, deviceID: req.Principal.DeviceID, ownerID: req.Principal.OwnerID})
		answer := call(connected, s, principal, args)
		if mutation && (answer.Unknown || connected.Err() != nil) {
			return nil, errLinkOutcomeUnknown()
		}
		return s.linkDomainResult(req.Principal.DeviceID, answer)
	}}
}

// linkDomainResult renders a reply for domain.call; files and large bodies
// become the device's domain objects.
func (s *Server) linkDomainResult(deviceID string, answer domainReply) (linkDomainResult, error) {
	unavailable := &contract.Error{Code: contract.CodeBackendUnavailable, Message: "the result could not be stored for download", Retryable: true}
	if answer.Object != nil {
		meta, err := s.link.domainObj.put(deviceID, answer.Object.Purpose, answer.Object.Name, answer.Object.MediaType, answer.Object.Data)
		if err != nil {
			return linkDomainResult{}, unavailable
		}
		ref := meta.reference()
		return linkDomainResult{Status: answer.Status, Object: &ref}, nil
	}
	body := mustRaw(answer.Body)
	if len(body) <= linkDomainInlineBytes {
		return linkDomainResult{Status: answer.Status, Body: body}, nil
	}
	meta, err := s.link.domainObj.put(deviceID, "domain_result", "response.json", "application/json", body)
	if err != nil {
		return linkDomainResult{}, unavailable
	}
	ref := meta.reference()
	return linkDomainResult{Status: answer.Status, Object: &ref}, nil
}

// linkDomainError is a domain failure with its own reason.
func linkDomainError(code, reason, message string, retryable bool) error {
	return &contract.Error{Code: code, Reason: reason, Message: message, Retryable: retryable}
}

var (
	linkDomainsMu sync.RWMutex
	linkDomains   = map[string]map[string]linkDomainMethod{}
)

// registerLinkDomain adds a business domain's methods.
func registerLinkDomain(domain string, methods map[string]linkDomainMethod) {
	linkDomainsMu.Lock()
	defer linkDomainsMu.Unlock()
	if linkDomains[domain] == nil {
		linkDomains[domain] = map[string]linkDomainMethod{}
	}
	for name, m := range methods {
		linkDomains[domain][name] = m
	}
}

func (s *Server) linkDomainNames() []string {
	linkDomainsMu.RLock()
	seen := map[string]bool{}
	for d := range linkDomains {
		seen[d] = true
	}
	linkDomainsMu.RUnlock()
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// The presentation reads the desktop's original screens use, answered in
// the domain result shape {status, body}.
func init() {
	result := func(status int, body any) linkDomainResult {
		return linkDomainResult{Status: status, Body: mustRaw(body)}
	}
	registerLinkDomain("presentation", map[string]linkDomainMethod{
		"presentation.config": {Call: func(ctx context.Context, s *Server, req *link.Request, _ json.RawMessage) (any, error) {
			view, err := s.configView(ctx, req.Principal.OwnerID)
			if err != nil {
				return result(503, map[string]string{"error": "configuration unavailable"}), nil
			}
			return result(200, view), nil
		}},
		"presentation.ready": {Call: func(ctx context.Context, s *Server, _ *link.Request, _ json.RawMessage) (any, error) {
			status, payload := s.readinessView(ctx)
			return result(status, payload), nil
		}},
		"presentation.owner": {Call: func(ctx context.Context, s *Server, req *link.Request, _ json.RawMessage) (any, error) {
			profile, _, err := s.ownerProfileFor(ctx, linkRequestPrincipal(req))
			if err != nil {
				return result(503, map[string]string{"error": "owner unavailable"}), nil
			}
			return result(200, profile), nil
		}},
	})
}

func (s *Server) linkDomainCall(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.DomainCallParams](req)
	if err != nil {
		return nil, err
	}
	linkDomainsMu.RLock()
	method, ok := linkDomains[p.Domain][p.Method]
	linkDomainsMu.RUnlock()
	if !ok {
		return nil, link.Errorf(contract.CodeUnsupportedCapability, "domain method %s.%s is not available", p.Domain, p.Method)
	}
	if p.ArgsObject != "" {
		// Arguments too large for one frame were uploaded as a domain object
		// first; the call and its receipt are those of the arguments.
		if len(p.Args) > 0 && string(p.Args) != "null" {
			return nil, link.Errorf(contract.CodeInvalidRequest, "args and args_object are exclusive")
		}
		raw, err := s.link.domainObj.readAll(req.Principal.DeviceID, domainObjectRef{ObjectID: p.ArgsObject, Version: 1}, "domain_args", linkDomainArgsMaxBytes)
		if err != nil || !json.Valid(raw) {
			return nil, link.Errorf(contract.CodeNotFound, "the arguments object is unavailable")
		}
		p.Args, p.ArgsObject = raw, ""
	}
	if !method.Mutation {
		if p.ReplayOnly {
			return nil, link.Errorf(contract.CodeInvalidRequest, "replay_only applies to mutations")
		}
		result, err := method.Call(ctx, s, req, p.Args)
		if err != nil {
			return nil, err
		}
		return contract.DomainCallResult{Result: mustRaw(result)}, nil
	}
	if p.IdempotencyKey == "" {
		return nil, link.Errorf(contract.CodeInvalidRequest, "%s.%s needs an idempotency_key", p.Domain, p.Method)
	}
	key, digest := linkDomainReceipt(req.Principal.DeviceID, p)
	ctx = context.WithValue(ctx, linkDomainAttemptKey{}, linkDomainAttempt{Key: key, Digest: digest})
	// Domain mutations keep the receipt fence the previous transport's
	// journal gave them: an attempt cut off midway answers outcome_unknown.
	raw, err := s.link.receipts.Do(key, digest, linkReceiptOptions{Fence: true, ReplayOnly: p.ReplayOnly}, func() (any, error) {
		result, err := method.Call(ctx, s, req, p.Args)
		if err != nil {
			return nil, err
		}
		return contract.DomainCallResult{Result: mustRaw(result)}, nil
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// linkDomainAttempt names the receipt of the domain mutation a method
// runs for: a business function that records its own intent (mail intake)
// keys it by the attempt, and a later reconciliation finds it again.
type linkDomainAttempt struct {
	Key    string
	Digest string
}

type linkDomainAttemptKey struct{}

func linkDomainAttemptFrom(ctx context.Context) (linkDomainAttempt, bool) {
	attempt, ok := ctx.Value(linkDomainAttemptKey{}).(linkDomainAttempt)
	return attempt, ok
}

// linkDomainReceipt is the receipt key and content digest of a domain
// mutation: the device, the method and the idempotency key; the content is
// the call without replay_only.
func linkDomainReceipt(deviceID string, p contract.DomainCallParams) (string, string) {
	content := contract.DomainCallParams{Domain: p.Domain, Method: p.Method, Args: p.Args, IdempotencyKey: p.IdempotencyKey}
	return deviceID + "\x00domain\x00" + p.Domain + "." + p.Method + "\x00" + p.IdempotencyKey, linkDigest(content)
}

func mustRaw(v any) json.RawMessage {
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

func (s *Server) linkProjection(ctx context.Context, req *link.Request, requestType string) (any, error) {
	if s.bridge == nil {
		return nil, link.Errorf(contract.CodeUnsupportedCapability, "projections are unavailable")
	}
	payload := req.Params
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage(`{}`)
	}
	response := s.bridge.PhoneProjection(ctx, requestType, app.NewID("proj"), req.Principal.ClientID, req.Principal.OwnerID, req.Principal.OwnerID, payload, time.Now().UTC())
	if response.Error != nil {
		return nil, &contract.Error{Code: contract.CodeInvalidRequest, Message: response.Error.Message, Retryable: response.Error.Retryable}
	}
	return response, nil
}

func (s *Server) linkProjectionSnapshot(ctx context.Context, req *link.Request) (any, error) {
	return s.linkProjection(ctx, req, iscpbridge.TypeSnapshotGet)
}

func (s *Server) linkProjectionActivity(ctx context.Context, req *link.Request) (any, error) {
	return s.linkProjection(ctx, req, iscpbridge.TypeActivityList)
}

func asHappy(err error, target **happyPlanApprovalPending) bool { return errors.As(err, target) }

func removeFileQuiet(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// linkSessionUploads lists the uploaded attachment files of a conversation
// so they are removed with it.
func (s *Server) linkSessionUploads(ctx context.Context, sessionID string) []string {
	objects, err := s.store.ListArtifactObjects(ctx, 10000)
	if err != nil {
		return nil
	}
	var out []string
	for _, o := range objects {
		if o.SessionID == sessionID && o.Backend == "workspace" && (o.Kind == "document_upload" || o.Kind == "media_image_upload") && o.Path != "" {
			out = append(out, o.Path)
		}
	}
	return out
}
