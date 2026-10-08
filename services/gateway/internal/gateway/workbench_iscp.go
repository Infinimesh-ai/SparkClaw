package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// This trusted admission flag is never accepted in an execution envelope.
// Detached execution keeps it while retaining the normal durable control path.
type textOnlyExecutionContextKey struct{}

type workbenchISCPOperation struct {
	method    string
	path      string
	execution bool
	handler   http.HandlerFunc
}

// NewWorkbenchISCPHandler resolves one cryptographically pinned peer's local
// binding. Only Endpoint may expose it to a peer, after its grant, Hello, Ready,
// manifest and encrypted message checks. No bearer or HTTP URL is tunneled.
func (s *Server) NewWorkbenchISCPHandler(cfg iscpworkbench.Config) (iscpworkbench.Handler, error) {
	if cfg.SchemaVersion != 1 || cfg.Role != iscpworkbench.RoleResponder || cfg.Mode != "local-test" || cfg.Binding == nil || cfg.Binding.DeploymentID == "" || cfg.Binding.OwnerID == "" || cfg.Binding.ClientID == "" {
		return nil, errors.New("ISCP workbench requires a fixed responder and complete local Client binding")
	}
	binding := *cfg.Binding
	if _, err := s.workbenchISCPPrincipal(s.executionContext(), binding); err != nil {
		return nil, err
	}
	// This is the only business operation dispatch table. A request contains an
	// operation ID, never a method, route, owner override or arbitrary URL.
	operations := map[string]workbenchISCPOperation{
		iscpworkbench.OperationIdentity: {http.MethodGet, "/api/workbench/identity", false, s.getWorkbenchIdentity},
		iscpworkbench.OperationBind:     {http.MethodPost, "/api/v1/installations", false, s.installExecutionClient},
		iscpworkbench.OperationConfig:   {http.MethodGet, "/api/config", false, s.getConfig},
		iscpworkbench.OperationOwner:    {http.MethodGet, "/api/owner", false, s.getOwnerProfile},
		iscpworkbench.OperationReady:    {http.MethodGet, "/readyz", false, s.readyz},
		iscpworkbench.OperationSubmit:   {http.MethodPost, "/api/v1/executions", true, s.submitExecution},
		iscpworkbench.OperationLookup:   {http.MethodGet, "/api/v1/executions/{request}", true, s.lookupExecution},
		iscpworkbench.OperationCancel:   {http.MethodPost, "/api/v1/executions/{request}/cancel", true, s.cancelExecution},
		iscpworkbench.OperationAck:      {http.MethodPost, "/api/v1/executions/{request}/ack", true, s.ackExecution},
	}
	return func(ctx context.Context, request iscpworkbench.Request) iscpworkbench.Response {
		result := func(status int, message string) iscpworkbench.Response {
			return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: iscpworkbench.Profile, ID: request.ID, Status: status, Error: message}
		}
		if ctx.Err() != nil {
			return result(http.StatusUnauthorized, "ISCP authorization is closed")
		}
		if request.Type != iscpworkbench.RequestType || request.Profile != iscpworkbench.Profile || !execution.UUID(request.ID) {
			return result(http.StatusBadRequest, "invalid ISCP workbench request")
		}
		if len(request.Body) > iscpworkbench.MaxBodyBytes {
			return result(http.StatusRequestEntityTooLarge, "ISCP input exceeds the text profile limit")
		}
		op, found := operations[request.Operation]
		if !found {
			return result(http.StatusNotImplemented, "ISCP workbench operation is unavailable")
		}
		connected, release, err := s.clientConnectionContext(ctx, binding.ClientID)
		if err != nil {
			return result(http.StatusUnauthorized, "ISCP Client binding is unavailable")
		}
		defer release()
		principal, err := s.workbenchISCPPrincipal(connected, binding)
		if err != nil {
			return result(http.StatusUnauthorized, "ISCP Client binding is unavailable")
		}
		if op.execution && (!execution.UUID(request.RequestID) || !execution.UUID(request.InstallationID)) {
			return result(http.StatusBadRequest, "execution request and installation IDs must be UUIDs")
		}
		if op.method == http.MethodGet && len(bytes.TrimSpace(request.Body)) != 0 && !bytes.Equal(bytes.TrimSpace(request.Body), []byte("{}")) {
			return result(http.StatusBadRequest, "read operations do not accept parameters")
		}
		if request.Operation == iscpworkbench.OperationSubmit {
			envelope, err := execution.Decode(request.Body, request.InputDigest)
			if err != nil {
				return result(http.StatusBadRequest, "invalid workbench context digest or fields")
			}
			if envelope.RequestID != request.RequestID {
				return result(http.StatusConflict, "execution request ID conflicts with its envelope")
			}
			if len(envelope.InputFiles) != 0 {
				return result(http.StatusNotImplemented, "ISCP text profile does not support input files or attachments")
			}
		}
		if s.limiter != nil {
			if allowed, _ := s.limiter.allow("iscp-client:" + binding.ClientID); !allowed {
				return result(http.StatusTooManyRequests, "rate limit exceeded")
			}
		}
		connected, cancel := context.WithTimeout(connected, 30*time.Second)
		defer cancel()
		connected = context.WithValue(connected, requestPrincipalContextKey{}, principal)
		connected = context.WithValue(connected, textOnlyExecutionContextKey{}, true)
		r := &http.Request{Method: op.method, URL: &url.URL{Path: op.path}, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(request.Body))}
		r = r.WithContext(connected)
		r.SetPathValue("request", request.RequestID)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-SparkClaw-Installation", request.InstallationID)
		r.Header.Set("X-SparkClaw-Digest", request.InputDigest)
		writer := &workbenchISCPWriter{header: make(http.Header)}
		op.handler(writer, r)
		if connected.Err() != nil {
			return result(http.StatusUnauthorized, "ISCP authorization is closed")
		}
		if writer.oversized {
			return result(http.StatusRequestEntityTooLarge, "ISCP response exceeds the text profile limit")
		}
		response := result(writer.status, "")
		response.Body = json.RawMessage(writer.body.Bytes())
		raw, err := json.Marshal(response)
		if err != nil || len(raw) > iscpworkbench.MaxMessageBytes {
			return result(http.StatusRequestEntityTooLarge, "ISCP response exceeds the text profile limit")
		}
		return response
	}, nil
}

func (s *Server) workbenchISCPPrincipal(ctx context.Context, binding iscpworkbench.Binding) (requestPrincipal, error) {
	if binding.DeploymentID != s.cfg.Gateway.DeploymentID {
		return requestPrincipal{}, errors.New("ISCP binding deployment does not match this Gateway")
	}
	client, found, err := s.store.GetClient(ctx, binding.ClientID)
	if err != nil || !found || client.RevokedAt != nil || strings.TrimSpace(client.TokenHash) == "" {
		return requestPrincipal{}, errors.New("ISCP binding requires an active issued Client")
	}
	owner, actor := strings.TrimSpace(client.OwnerID), strings.TrimSpace(client.ActorID)
	if owner == "" {
		owner = app.DefaultOwnerID
	}
	if actor == "" {
		actor = owner
	}
	if owner != binding.OwnerID || client.ID != binding.ClientID {
		return requestPrincipal{}, errors.New("ISCP binding does not match the issued Client scope")
	}
	return requestPrincipal{OwnerID: owner, ActorID: actor, ClientID: client.ID, Authenticated: true}, nil
}

// Keep a whole response or reject it. Partial JSON must never be returned as a
// completed execution result, and no oversized response is durably ACKed here.
type workbenchISCPWriter struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	oversized bool
}

func (w *workbenchISCPWriter) Header() http.Header { return w.header }
func (w *workbenchISCPWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *workbenchISCPWriter) Write(raw []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.oversized || w.body.Len()+len(raw) > iscpworkbench.MaxMessageBytes {
		w.oversized = true
		w.body.Reset()
		return 0, errors.New("ISCP response exceeds the text profile limit")
	}
	return w.body.Write(raw)
}
