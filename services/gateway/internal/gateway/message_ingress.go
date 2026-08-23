package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
)

type webMessageInput struct {
	Content          string                    `json:"content"`
	Attachments      []agent.MessageAttachment `json:"attachments"`
	TargetEndpointID app.EndpointID            `json:"target_endpoint_id,omitempty"`
	Schedule         *scheduleActionInput      `json:"schedule_action,omitempty"`
	ClientTimezone   string                    `json:"client_timezone,omitempty"`
}

func (s *Server) webMessageIngress(ctx context.Context, r *http.Request, session app.Session, target app.EndpointID, clientTimezone string) (app.MessageIngressContext, error) {
	principal := principalForRequest(r)
	ownerID := strings.TrimSpace(session.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	if ownerID != principal.OwnerID {
		return app.MessageIngressContext{}, delivery.NewError(delivery.CodeCrossUserDenied, "message session is outside the owner scope", "blocked")
	}

	sourceEndpointID := messagecontrol.WebEndpointID(session.ID)
	returnRoute := app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: sourceEndpointID}
	if target != "" {
		if s.endpoints == nil || s.delivery == nil {
			return app.MessageIngressContext{}, errors.New("message delivery is unavailable")
		}
		endpoint, err := s.endpoints.GetForMessageSend(ctx, target, principal.OwnerID, principal.ActorID)
		if err != nil {
			return app.MessageIngressContext{}, err
		}
		returnRoute = app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: endpoint.ID}
	}

	return app.MessageIngressContext{
		Source: app.MessageSourceContext{
			Kind: app.MessageSourceWeb, Adapter: "web", EndpointID: sourceEndpointID,
		},
		OwnerID:        principal.OwnerID,
		Authorization:  app.MessageAuthorization{PrincipalID: principal.ActorID},
		ReturnRoute:    returnRoute,
		ClientTimezone: normalizeClientTimezone(clientTimezone),
	}, nil
}

func normalizeClientTimezone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	if _, err := time.LoadLocation(value); err != nil {
		return ""
	}
	return value
}

func (s *Server) deliverAgentResult(ctx context.Context, result agent.Result) (*app.DeliveryReceipt, error) {
	if result.WorkflowResult == nil {
		return nil, nil
	}
	if result.WorkflowResult.ReturnRoute.Mode == app.ReturnNowhere {
		if s.mcpAccess != nil {
			if err := s.mcpAccess.RecordWorkflowResult(ctx, result); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if s.endpoints == nil || s.delivery == nil {
		if result.WorkflowResult.ReturnRoute.Mode == app.ReturnToEndpoint {
			return nil, errors.New("message delivery is unavailable")
		}
		return nil, nil
	}
	receipt, err := delivery.NewWorkflowResultDeliverer(
		messagecontrol.NewReturnRouteResolver(s.endpoints),
		s.delivery,
	).DeliverWorkflowResult(ctx, *result.WorkflowResult)
	if err != nil {
		return &receipt, err
	}
	if s.mcpAccess != nil {
		if err := s.mcpAccess.RecordWorkflowResult(ctx, result); err != nil {
			return &receipt, err
		}
	}
	return &receipt, nil
}
