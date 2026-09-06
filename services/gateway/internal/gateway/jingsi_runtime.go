package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		ArtifactRefs: jingSiArtifactRefs(input.ExecutionID, result.Message.Attachments),
		TraceRef:     jingsiruntime.OpaqueRef{ID: "trace:" + input.ExecutionID, Version: "v1"},
	}, err
}

// jingSiArtifactRefs projects the attachments the run delivered with its
// assistant message into opaque versioned references. The id is a digest of
// the execution and the artifact object identity, so a replay or a restart
// re-entry yields the same reference while no store id, path or URI crosses
// the surface. Attachments without a registered artifact object have no
// stable identity and are not projected.
func jingSiArtifactRefs(executionID string, attachments []app.MessageAttachment) []jingsiruntime.ArtifactRef {
	refs := make([]jingsiruntime.ArtifactRef, 0, len(attachments))
	seen := map[string]bool{}
	for _, attachment := range attachments {
		objectID := strings.TrimSpace(attachment.ArtifactID)
		if objectID == "" || seen[objectID] {
			continue
		}
		seen[objectID] = true
		sum := sha256.Sum256([]byte(executionID + "\x00" + objectID))
		refs = append(refs, jingsiruntime.ArtifactRef{
			ID: "artifact:" + hex.EncodeToString(sum[:16]), Version: "v1",
			Kind: jingSiArtifactKind(attachment.ContentType), MediaType: strings.TrimSpace(attachment.ContentType),
		})
	}
	return refs
}

func jingSiArtifactKind(contentType string) string {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "image"
	case strings.HasPrefix(contentType, "audio/"):
		return "audio"
	default:
		return "file"
	}
}

// jingSiGrant projects the verified authorization envelope and the tool-call
// budget into the typed grant the Agent Runtime consumes. The provider
// validated the envelope before Execute is called; nothing here may widen it.
// max_output_bytes stays with the provider, which bounds the result summary.
func jingSiGrant(input jingsiruntime.ExecutionInput) jingsiscope.Grant {
	return jingsiscope.Grant{
		Tools:          append([]string(nil), input.Authorization.ToolScope...),
		ApprovalPolicy: input.Authorization.ApprovalPolicy,
		MaxToolCalls:   input.Budget.MaxToolCalls,
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
