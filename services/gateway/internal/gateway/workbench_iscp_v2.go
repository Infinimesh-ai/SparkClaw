package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// NewWorkbenchISCPHandler retains the v1 text boundary and mounts v2 only
// when explicitly configured. A peer can never enable a server capability.
func (s *Server) NewWorkbenchISCPHandler(cfg iscpworkbench.Config) (iscpworkbench.Handler, error) {
	textHandler, err := s.newWorkbenchISCPTextHandler(cfg)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(cfg.ApplicationProfiles, iscpworkbench.ProfileV2) {
		return textHandler, nil
	}
	domain, err := s.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		return nil, err
	}
	objects, err := s.NewWorkbenchISCPObjectHandler(cfg, func(ctx context.Context, request iscpworkbench.Request) iscpworkbench.Response {
		spec, _ := iscpworkbench.LookupOperation(request.Operation)
		if spec.Version != 1 {
			return domain(ctx, request)
		}
		session, _ := iscpworkbench.SessionFromContext(ctx)
		ctx = withExecutionAuthorization(ctx, s.workbenchExecutionAuthorization(session))
		legacy := request
		legacy.Profile = iscpworkbench.Profile
		result := textHandler(ctx, legacy)
		result.Profile = iscpworkbench.ProfileV2
		return result
	})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, request iscpworkbench.Request) iscpworkbench.Response {
		if request.Profile == iscpworkbench.Profile {
			return textHandler(ctx, request)
		}
		if request.Profile != iscpworkbench.ProfileV2 || request.Validate() != nil {
			return workbenchV2Error(request, 400, "invalid_request")
		}
		session, authenticated := iscpworkbench.SessionFromContext(ctx)
		if !authenticated || session.Profile != iscpworkbench.ProfileV2 || session.Binding == nil || *session.Binding != *cfg.Binding || session.GrantRevision == 0 {
			return workbenchV2Error(request, 403, "authenticated_session_required")
		}
		spec, known := iscpworkbench.LookupOperation(request.Operation)
		if !known || spec.Direction != "forward" {
			return workbenchV2Error(request, 400, "invalid_operation_direction")
		}
		if !workbenchOperationPermitted(spec, session.Scopes) {
			return workbenchV2Error(request, 403, "permission_denied")
		}
		if spec.Version == 2 && request.Operation != iscpworkbench.OperationCapabilitiesGet && !slices.Contains(cfg.QualifiedCapabilities, request.Operation) {
			return workbenchV2Error(request, 501, "qualification_required")
		}
		connected, release, err := s.clientConnectionContext(ctx, cfg.Binding.ClientID)
		if err != nil {
			return workbenchV2Error(request, 403, "client_binding_unavailable")
		}
		defer release()
		principal, err := s.workbenchISCPPrincipal(connected, *cfg.Binding)
		if err != nil {
			return workbenchV2Error(request, 403, "client_binding_unavailable")
		}
		connected = context.WithValue(connected, requestPrincipalContextKey{}, principal)
		if spec.Version == 2 && request.Operation != iscpworkbench.OperationCapabilitiesGet {
			r := (&http.Request{Header: make(http.Header)}).WithContext(connected)
			r.Header.Set("X-SparkClaw-Installation", request.InstallationID)
			if _, _, err := s.executionReadPrincipal(r); err != nil {
				return workbenchV2Error(request, 403, "installation_binding_unavailable")
			}
		}
		if request.Operation == iscpworkbench.OperationAuthorizationsList {
			return workbenchV2Authorization(connected, request, session, principal)
		}
		if request.Operation == iscpworkbench.OperationAuthorizationsDelete {
			// Only the subject device can sign deletion. A Gateway must never
			// impersonate it or weaken the receipt-only post-revocation channel.
			return workbenchV2Error(request, 409, "device_authorization_control_required")
		}
		return workbenchV2Result(objects(connected, request))
	}, nil
}

func workbenchV2Error(request iscpworkbench.Request, status int, message string) iscpworkbench.Response {
	return workbenchV2Result(iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: iscpworkbench.ProfileV2, ID: request.ID, Status: status, Error: message})
}

func workbenchV2Result(response iscpworkbench.Response) iscpworkbench.Response {
	if response.Status >= 400 && response.Code == "" {
		response.Code = iscpworkbench.CodeForStatus(response.Status)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(response.Body, &body)
		if body.Code == "operation_outcome_unknown" {
			response.Code = iscpworkbench.ErrorOutcomeUnknown
		}
		response.Retryable = response.Status == 429 || response.Status == 503
	}
	return response
}
