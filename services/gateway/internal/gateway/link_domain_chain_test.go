package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// linkDomainTestChain is the business-domain chain the link bridge runs
// (domain adapters behind the object layer) for an already authenticated
// device; domain tests call it directly.
func linkDomainTestChain(t *testing.T, s *Server, cfg wb.Config) wb.Handler {
	t.Helper()
	if cfg.Binding == nil {
		t.Fatal("domain chain needs a binding")
	}
	cfg.ApplicationProfiles = []string{wb.ProfileV2}
	domain, err := s.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := s.NewWorkbenchISCPObjectHandler(cfg, domain)
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, request wb.Request) wb.Response {
		actor := cfg.Binding.OwnerID
		if client, found, err := s.store.GetClient(ctx, cfg.Binding.ClientID); err == nil && found {
			actor = client.ActorID
		}
		principal := requestPrincipal{OwnerID: cfg.Binding.OwnerID, ActorID: actor, ClientID: cfg.Binding.ClientID, Authenticated: true}
		ctx = context.WithValue(ctx, requestPrincipalContextKey{}, principal)
		ops := wb.OperationsV2ForMode(cfg.Mode)
		session := wb.SessionInfo{SessionID: "domain-test", Profile: wb.ProfileV2, GrantRevision: 1, Binding: cfg.Binding, QualifiedOperations: ops, NegotiatedOperations: ops}
		ctx = wb.ContextWithSession(ctx, session)
		ctx = withExecutionAuthorization(ctx, s.workbenchExecutionAuthorization(session))
		if request.Operation == wb.OperationBind {
			var in struct {
				InstallationID string `json:"installation_id"`
			}
			_ = json.Unmarshal(request.Body, &in)
			service, err := s.executionService()
			if err != nil || service.Bind(principal.OwnerID, principal.ClientID, in.InstallationID) != nil {
				return wb.Response{Type: wb.ResponseType, Profile: request.Profile, ID: request.ID, Status: http.StatusConflict}
			}
			return wb.Response{Type: wb.ResponseType, Profile: request.Profile, ID: request.ID, Status: http.StatusOK, Body: json.RawMessage(`{"bound":true}`)}
		}
		if request.Profile == "" {
			request.Profile = wb.ProfileV2
		}
		return workbenchV2Result(objects(ctx, request))
	}
}
