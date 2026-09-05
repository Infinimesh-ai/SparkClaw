package agent

import (
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type browserEmailProfile struct{}

func (browserEmailProfile) ID() app.WorkflowID           { return app.WorkflowBrowserEmail }
func (browserEmailProfile) Revision() int                { return 1 }
func (browserEmailProfile) Capability() app.CapabilityID { return app.CapabilityBrowserEmail }
func (browserEmailProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key:   "send",
		Route: workflowRouteTemplate{Operation: app.RouteOperationSend},
		EmbedTexts: []string{
			"用 Gmail 给 alice@example.com 发一封邮件", "通过 Outlook 发邮件给 Bob", "从 QQ 邮箱发送邮件", "Send an email with Gmail",
		},
		TreeDescription: "Send exactly one new plain-text email through a configured QQ Mail, Outlook, or Gmail browser account. The provider is resolved deterministically by Runtime; the model supplies only recipient, optional single-line subject, and body.",
		HardNegatives: []string{
			"查看 Gmail 收件箱", "读第一封未读邮件", "打开 QQ 邮箱网页", "给邮件添加附件", "回复这封邮件", "检查 Outlook 是否登录",
		},
	}}}
}
func (browserEmailProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}

func (p browserEmailProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	if route.Slots.Operation != app.RouteOperationSend {
		return app.IntentEnvelope{}, app.WorkflowPlan{}, errors.New("browser.email only supports send")
	}
	for _, key := range []string{
		app.EmailRouteFactProvider, app.EmailRouteFactAccount, app.EmailRouteFactAccountHint,
		app.EmailRouteFactSettingVersion, app.EmailRouteFactBrowserCredentialGeneration, app.EmailRouteFactProbeRevision,
		app.EmailRouteFactSendScriptRevision, app.EmailRouteFactValidatedAt, app.EmailRouteFactInvocationID,
	} {
		if strings.TrimSpace(route.Facts[key]) == "" {
			return app.IntentEnvelope{}, app.WorkflowPlan{}, errors.New("browser.email is missing fresh login admission")
		}
	}

	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationSend, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal)
	intent.Objectives[0].Output = app.OutputKindMessage
	nodeID := app.WorkflowNodeID("email_send")
	bindings := []app.ArgumentBinding{
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "provider", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactProvider},
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "account", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactAccount},
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "account_hint", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactAccountHint},
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "setting_version", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactSettingVersion},
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "browser_credential_generation", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactBrowserCredentialGeneration},
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "probe_revision", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactProbeRevision},
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "send_script_revision", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactSendScriptRevision},
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "validated_at", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactValidatedAt},
		{Capability: app.ToolCapabilityBrowserEmailSend, Argument: "invocation_id", ResourceKind: "email_admission", Source: app.ArgumentBindingRouteFact, SourceKey: app.EmailRouteFactInvocationID},
	}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(), InitialNodeIDs: []app.WorkflowNodeID{nodeID},
		Completion: app.CompletionEvidence, ResultProjection: app.WorkflowResultOutputsOnly,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "send_email",
			Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Send one exact approved plain-text email through the fresh Runtime-selected browser account", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
				Name: app.ToolCapabilityBrowserEmailSend, Qualifiers: map[string]string{app.CapabilityQualifierOperation: string(app.RouteOperationSend)},
			}}},
			ArgumentBindings: bindings, AllowedRisks: []app.RiskLevel{app.RiskDangerous}, MaxAttempts: 1,
		}},
	}, nil
}

func (browserEmailProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}

func (browserEmailProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalEmailSent) {
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "email_sent"
	} else {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "email_send_failed"
	}
	return assessment
}

func (browserEmailProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	stage := workflowStageContextForState(state, "email_send", "email_send_receipt", "local", "", "Dispatched by the send-only browser.email Workflow contract.")
	stage.EstimatedRisk = app.RiskDangerous
	return stage
}

func (browserEmailProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
