package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiscope"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type jingSiAgentExecutor struct {
	runtime    agent.Runtime
	repository interface {
		store.SessionRepository
		store.RunRepository
	}
}

func NewJingSiRuntimeProvider(cfg config.Config, runtime agent.Runtime, repository interface {
	store.SessionRepository
	store.RunRepository
}) (*jingsiruntime.Provider, error) {
	if !cfg.JingSiRuntime.Enabled {
		return nil, nil
	}
	if repository == nil {
		return nil, fmt.Errorf("JingSi Runtime repository is required")
	}
	return jingsiruntime.New(jingsiruntime.Config{
		StateDir: cfg.JingSiRuntime.StateDir, BearerToken: cfg.JingSiRuntime.BearerToken,
		CallerID: "jingsi-service-v1", MaxConcurrent: cfg.JingSiRuntime.MaxConcurrent,
	}, jingSiAgentExecutor{runtime: runtime, repository: repository})
}

func (e jingSiAgentExecutor) Execute(ctx context.Context, input jingsiruntime.ExecutionInput) (jingsiruntime.ExecutionOutput, error) {
	authorizedContext := ""
	if input.Memory != nil {
		authorizedContext = input.Memory.Summary
	}
	scopes := jingSiGrant(input).Scopes()
	sessionID := ""
	if run, found, err := e.repository.GetRun(ctx, input.ExecutionID); err != nil {
		return jingsiruntime.ExecutionOutput{}, err
	} else if found {
		sessionID = run.SessionID
	}
	if sessionID == "" {
		session, err := e.repository.CreateSessionWithScope(
			ctx, "JingSi task "+input.Authorization.TaskID, app.DefaultOwnerID, "", jingsiscope.AdapterID, true,
		)
		if err != nil {
			return jingsiruntime.ExecutionOutput{}, err
		}
		sessionID = session.ID
	}
	result, err := e.runtime.HandleMessageWithIngressAndContext(
		ctx,
		sessionID,
		input.ExecutionID+":message",
		input.ExecutionID,
		input.Goal,
		nil,
		app.MessageIngressContext{
			Source: app.MessageSourceContext{
				Kind: app.MessageSourceThirdPartyDevice, Adapter: jingsiscope.AdapterID,
				NativeMessageID: input.ExecutionID,
			},
			OwnerID: app.DefaultOwnerID,
			Authorization: app.MessageAuthorization{
				PrincipalID: "jingsi:" + input.Authorization.SpaceID + ":" + input.Authorization.TaskID,
				Scope:       scopes,
			},
			ReturnRoute: app.ReturnRoute{Mode: app.ReturnNowhere},
		},
		authorizedContext,
	)
	state := mapAgentState(result.Run.State)
	summary := strings.TrimSpace(result.Message.Content)
	if summary == "" {
		summary = strings.TrimSpace(result.Run.Summary)
	}
	return jingsiruntime.ExecutionOutput{
		State: state, Summary: summary,
		TraceRef: jingsiruntime.OpaqueRef{ID: "trace:" + input.ExecutionID, Version: "v1"},
	}, err
}

// jingSiGrant projects the verified authorization envelope and submit budget
// into the typed grant the Agent Runtime consumes. The provider validated the
// envelope before Execute is called; nothing here may widen it.
func jingSiGrant(input jingsiruntime.ExecutionInput) jingsiscope.Grant {
	return jingsiscope.Grant{
		Tools:          append([]string(nil), input.Authorization.ToolScope...),
		ApprovalPolicy: input.Authorization.ApprovalPolicy,
		MaxToolCalls:   input.Budget.MaxToolCalls,
		MaxOutputBytes: input.Budget.MaxOutputBytes,
		DataScope:      append([]string(nil), input.Authorization.DataScope...),
		NetworkScope:   append([]string(nil), input.Authorization.NetworkScope...),
		Purpose:        input.Authorization.Purpose.Name,
		GrantID:        input.Authorization.Grant.ID,
		GrantVersion:   input.Authorization.Grant.Version,
	}
}

func mapAgentState(value string) string {
	switch value {
	case "completed":
		return "succeeded"
	case "approval_pending", "browser_login_blocked":
		return "approval_required"
	case "cancelled":
		return "canceled"
	case "failed", "blocked", "clarification_required":
		return "failed"
	default:
		return "failed"
	}
}
