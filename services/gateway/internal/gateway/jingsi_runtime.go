package gateway

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiruntime"
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
	scopes := make([]string, 0, len(input.Authorization.ToolScope)+len(input.Authorization.DataScope)+len(input.Authorization.NetworkScope)+3)
	for _, value := range input.Authorization.ToolScope {
		scopes = append(scopes, "sparkclaw.tool:"+value)
	}
	for _, value := range input.Authorization.DataScope {
		scopes = append(scopes, "sparkclaw.data:"+value)
	}
	for _, value := range input.Authorization.NetworkScope {
		scopes = append(scopes, "sparkclaw.network:"+value)
	}
	scopes = append(scopes,
		"sparkclaw.approval:"+input.Authorization.ApprovalPolicy,
		fmt.Sprintf("sparkclaw.budget.max_tool_calls:%d", input.Budget.MaxToolCalls),
		fmt.Sprintf("sparkclaw.budget.max_output_bytes:%d", input.Budget.MaxOutputBytes),
		"sparkclaw.purpose:"+input.Authorization.Purpose.Name,
		"sparkclaw.grant:"+input.Authorization.Grant.ID+"@"+input.Authorization.Grant.Version,
	)
	slices.Sort(scopes)
	sessionID := ""
	if run, found, err := e.repository.GetRun(ctx, input.ExecutionID); err != nil {
		return jingsiruntime.ExecutionOutput{}, err
	} else if found {
		sessionID = run.SessionID
	}
	if sessionID == "" {
		session, err := e.repository.CreateSessionWithScope(
			ctx, "JingSi task "+input.Authorization.TaskID, app.DefaultOwnerID, "", "jingsi-runtime-v1", true,
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
				Kind: app.MessageSourceThirdPartyDevice, Adapter: "jingsi-runtime-v1",
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
