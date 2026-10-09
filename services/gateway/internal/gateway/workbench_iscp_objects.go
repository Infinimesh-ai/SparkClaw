package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

type iscpObjectContextKey struct{}
type iscpObjectAccess struct {
	store   *iscpobjects.Store
	binding iscpobjects.Binding
}

func (s *Server) NewWorkbenchISCPObjectHandler(cfg iscpworkbench.Config, next iscpworkbench.Handler) (iscpworkbench.Handler, error) {
	root := s.executionRoot
	if root == "" {
		root = s.cfg.State.Path + ".execution"
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	objects, err := iscpobjects.NewStore(filepath.Join(absolute, "iscp-objects"), iscpobjects.Limits{})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, request iscpworkbench.Request) iscpworkbench.Response {
		failed := func(status int, code string) iscpworkbench.Response {
			result := domainError(status, code)
			return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: request.Profile, ID: request.ID, Status: result.status, Body: result.body}
		}
		session, ok := iscpworkbench.SessionFromContext(ctx)
		if !ok || cfg.Binding == nil {
			return failed(401, "authenticated_session_required")
		}
		principal := principalForRequest((&http.Request{}).WithContext(ctx))
		binding := iscpobjects.Binding{DeploymentID: cfg.Binding.DeploymentID, OwnerID: principal.OwnerID, ClientID: principal.ClientID, InstallationID: request.InstallationID, AuthorizationRevision: session.GrantRevision}
		// Transfers require the active installation, not merely a syntactically valid
		// caller field; a replacement installation cannot resume old objects.

		switch request.Operation {
		case iscpworkbench.OperationIdentity, iscpworkbench.OperationBind, iscpworkbench.OperationConfig, iscpworkbench.OperationOwner, iscpworkbench.OperationReady, iscpworkbench.OperationCapabilitiesGet:
			if request.Object != nil {
				return failed(400, "body_object_unsupported")
			}
			return next(ctx, request)
		}
		service, err := s.executionService()
		if err != nil {
			return failed(503, "execution_unavailable")
		}
		if err = service.Installation(binding.OwnerID, binding.ClientID, binding.InstallationID); err != nil {
			return failed(403, "installation_required")
		}
		ctx = context.WithValue(ctx, iscpObjectContextKey{}, iscpObjectAccess{objects, binding})
		if response, handled := objects.Handle(ctx, binding, request); handled {
			return response
		}
		if request.Operation == iscpworkbench.OperationExecutionInputPut {
			if request.Object == nil || request.Object.Purpose != "execution_input" || request.InputDigest != request.Object.SHA256 {
				return failed(400, "invalid_input_object")
			}
			raw, err := objects.ReadAll(ctx, binding, *request.Object, execution.ResultBytes)
			if err != nil {
				return failed(409, "input_object_unavailable")
			}
			if err = service.Upload(binding.OwnerID, binding.ClientID, binding.InstallationID, request.Params["request_id"], request.Params["file_id"], request.InputDigest, raw); err != nil {
				result := domainExecutionError(err)
				return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: request.Profile, ID: request.ID, Status: result.status, Body: result.body}
			}
			body, _ := json.Marshal(map[string]bool{"stored": true})
			return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: request.Profile, ID: request.ID, Status: 200, Body: body}
		}
		if request.Operation == iscpworkbench.OperationExecutionFileGet {
			raw, err := service.File(binding.OwnerID, binding.ClientID, request.Params["request_id"], request.Params["file_id"])
			if err != nil {
				return failed(404, "execution_file_unavailable")
			}
			ref, err := objects.Put(ctx, binding, "execution_result", "result.bin", "application/octet-stream", raw)
			if err != nil {
				return failed(503, "result_object_unavailable")
			}
			return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: request.Profile, ID: request.ID, Status: 200, Object: &ref}
		}
		if request.Object != nil {
			purpose := "context"
			limit := int64(1 << 20)
			if request.Operation == iscpworkbench.OperationSubmit {
				purpose = "execution_request"
				limit = execution.ContextBytes
			}
			if request.Object.Purpose != purpose {
				return failed(400, "invalid_body_object_purpose")
			}
			raw, err := objects.ReadAll(ctx, binding, *request.Object, limit)
			if err != nil || !json.Valid(raw) {
				return failed(409, "body_object_unavailable")
			}
			request.Body, request.Object = raw, nil
		}
		response := next(ctx, request)
		if len(response.Body) > iscpworkbench.MaxBodyBytes {
			if ctx.Err() != nil {
				return failed(401, "authorization_closed")
			}
			ref, err := objects.Put(ctx, binding, "execution_result", "response.json", "application/json", response.Body)
			if err != nil {
				return failed(503, "response_object_unavailable")
			}
			response.Body = nil
			response.Object = &ref
		}
		return response
	}, nil
}
func domainReadObject(ctx context.Context, ref iscpworkbench.ObjectReference, purpose string, limit int64) ([]byte, error) {
	access, ok := ctx.Value(iscpObjectContextKey{}).(iscpObjectAccess)
	if !ok || ref.Purpose != purpose {
		return nil, errors.New("object purpose unavailable")
	}
	return access.store.ReadAll(ctx, access.binding, ref, limit)
}
func domainPutObject(ctx context.Context, purpose, name, mediaType string, raw []byte) (iscpworkbench.ObjectReference, error) {
	access, ok := ctx.Value(iscpObjectContextKey{}).(iscpObjectAccess)
	if !ok {
		return iscpworkbench.ObjectReference{}, errors.New("object service unavailable")
	}
	return access.store.Put(ctx, access.binding, purpose, name, mediaType, raw)
}
