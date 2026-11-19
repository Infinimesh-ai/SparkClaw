package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// linkDomainMethod is one business-domain method reachable through
// domain.call. Methods call business functions directly; none builds an
// http.Request (design 3.1).
type linkDomainMethod struct {
	// Mutation methods require an idempotency key and are replayed from
	// the receipt journal on retry.
	Mutation bool
	Call     func(ctx context.Context, req *link.Request, args json.RawMessage) (any, error)
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
	for _, op := range linkForwardOperations(contract.Conditions{BrowserSurface: true}) {
		seen[strings.SplitN(op, ".", 2)[0]] = true
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// The presentation reads the desktop's original screens use, answered in
// the domain result shape {status, body}.
func (s *Server) registerLinkPresentation() {
	result := func(status int, body any) linkBridgeResult {
		return linkBridgeResult{Status: status, Body: mustRaw(body)}
	}
	registerLinkDomain("presentation", map[string]linkDomainMethod{
		"presentation.config": {Call: func(ctx context.Context, req *link.Request, _ json.RawMessage) (any, error) {
			view, err := s.configView(ctx, req.Principal.OwnerID)
			if err != nil {
				return result(503, map[string]string{"error": "configuration unavailable"}), nil
			}
			return result(200, view), nil
		}},
		"presentation.ready": {Call: func(ctx context.Context, _ *link.Request, _ json.RawMessage) (any, error) {
			status, payload := s.readinessView(ctx)
			return result(status, payload), nil
		}},
		"presentation.owner": {Call: func(ctx context.Context, req *link.Request, _ json.RawMessage) (any, error) {
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
		return s.linkBridgeCall(ctx, req, p)
	}
	if !method.Mutation {
		result, err := method.Call(ctx, req, p.Args)
		if err != nil {
			return nil, err
		}
		return contract.DomainCallResult{Result: mustRaw(result)}, nil
	}
	if p.IdempotencyKey == "" {
		return nil, link.Errorf(contract.CodeInvalidRequest, "%s.%s needs an idempotency_key", p.Domain, p.Method)
	}
	key := req.Principal.DeviceID + "\x00domain\x00" + p.Domain + "." + p.Method + "\x00" + p.IdempotencyKey
	raw, err := s.link.receipts.Do(key, linkDigest(p), func() (any, error) {
		result, err := method.Call(ctx, req, p.Args)
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
