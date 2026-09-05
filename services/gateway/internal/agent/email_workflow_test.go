package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type fakeEmailAdmission struct {
	binding app.EmailAdmissionBinding
	err     error
	owners  []string
	inputs  []string
}

func (f *fakeEmailAdmission) Admit(_ context.Context, ownerID, request string) (app.EmailAdmissionBinding, error) {
	f.owners = append(f.owners, ownerID)
	f.inputs = append(f.inputs, request)
	return f.binding, f.err
}

type recordingAgentEmailSender struct {
	calls int
}

func (r *recordingAgentEmailSender) SendForOwner(context.Context, string, app.EmailSendRequest) (app.EmailSendResult, error) {
	r.calls++
	return app.EmailSendResult{Provider: app.EmailProviderGmail, Status: "sent", RecipientDigest: "sha256:digest", BrowserCredentialGeneration: 7, ScriptRevision: 3}, nil
}

func TestEmailAdmissionFreezesRuntimeOwnedRouteFactsBeforeWorkflowCreation(t *testing.T) {
	validatedAt := time.Date(2026, 9, 3, 8, 30, 0, 0, time.UTC)
	admission := &fakeEmailAdmission{binding: app.EmailAdmissionBinding{
		Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, AccountHint: "a***@gmail.com",
		SettingVersion: 4, BrowserCredentialGeneration: 7, ProbeRevision: 2, SendScriptRevision: 3, ValidatedAt: validatedAt,
	}}
	runtime := Runtime{store: store.NewMemoryStore(), capabilities: capability.MustDefaultCatalog(), emailAdmission: admission}
	route := emailSendRoute(runtime.capabilities.Revision())
	admitted, err := runtime.admitEmailRoute(t.Context(), "session-email", "run-email", "owner-email", "send via Gmail", route)
	if err != nil {
		t.Fatal(err)
	}
	if len(admission.owners) != 1 || admission.owners[0] != "owner-email" || admission.inputs[0] != "send via Gmail" {
		t.Fatalf("admission calls owners=%v inputs=%v", admission.owners, admission.inputs)
	}
	for key, want := range map[string]string{
		app.EmailRouteFactProvider: app.EmailProviderGmail, app.EmailRouteFactAccount: app.EmailAccountDefault,
		app.EmailRouteFactAccountHint: "a***@gmail.com", app.EmailRouteFactSettingVersion: "4",
		app.EmailRouteFactBrowserCredentialGeneration: "7", app.EmailRouteFactProbeRevision: "2",
		app.EmailRouteFactSendScriptRevision: "3", app.EmailRouteFactValidatedAt: validatedAt.Format(time.RFC3339Nano),
	} {
		if admitted.Facts[key] != want {
			t.Fatalf("route fact %s=%q want %q", key, admitted.Facts[key], want)
		}
	}
	if !strings.HasPrefix(admitted.Facts[app.EmailRouteFactInvocationID], "email_send_") {
		t.Fatalf("invocation id = %q", admitted.Facts[app.EmailRouteFactInvocationID])
	}
}

func TestBrowserEmailWorkflowProjectsOnlyMessageFieldsAndRestoresFrozenBindings(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	sender := &recordingAgentEmailSender{}
	runtime.tools.WithEmailSender(sender)
	route := emailSendRoute(runtime.capabilities.Revision())
	dispatch, err := runtime.dispatchMatchedWorkflow(t.Context(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn-email")
	if err != nil {
		t.Fatal(err)
	}
	if got := visibleToolNames(dispatch.Tools); len(got) != 2 || got[0] != "email.send" || got[1] != "observation.read" {
		t.Fatalf("visible tools = %#v", got)
	}
	var emailDefinition app.ToolDefinition
	for _, definition := range dispatch.Tools {
		if definition.Name == "email.send" {
			emailDefinition = definition
			break
		}
	}
	properties, _ := anyMap(emailDefinition.InputSchema["properties"])
	if len(properties) != 3 || properties["recipient"] == nil || properties["subject"] == nil || properties["body"] == nil {
		t.Fatalf("model-visible email schema = %#v", emailDefinition.InputSchema)
	}

	stored, ok := testGetRun(st, dispatch.Run.ID)
	if !ok || stored.Workflow == nil {
		t.Fatal("email workflow was not persisted")
	}
	node := stored.Workflow.Nodes["email_send"]
	call, approval, _, err := runtime.runToolPlan(t.Context(), session.ID, stored.ID, toolPlan{
		Name: "email.send",
		Args: map[string]any{
			"provider": app.EmailProviderOutlook, "account": "invented", "account_hint": "invented@example.com",
			"recipient": "alice@example.com", "subject": "Exact subject", "body": "Exact body",
			"setting_version": "999", "browser_credential_generation": "999", "probe_revision": "999", "send_script_revision": "999",
			"validated_at": "2099-01-01T00:00:00Z", "invocation_id": "invented",
		},
		WorkflowID: app.WorkflowBrowserEmail, WorkflowNodeID: "email_send", ScopeRevision: node.ScopeRevision,
		Capability: app.ToolCapabilityBrowserEmailSend,
	})
	if err != nil || approval == nil || call.Status != app.ToolCallStatusApprovalPending {
		t.Fatalf("email send approval call=%#v approval=%#v err=%v", call, approval, err)
	}
	for key, want := range map[string]any{
		"provider": app.EmailProviderGmail, "account": app.EmailAccountDefault, "account_hint": "a***@gmail.com",
		"setting_version": "4", "browser_credential_generation": "7", "probe_revision": "2", "send_script_revision": "3",
		"validated_at": "2026-09-03T08:30:00Z", "invocation_id": "email_send_fixture",
		"recipient": "alice@example.com", "subject": "Exact subject", "body": "Exact body",
	} {
		if call.Arguments[key] != want || approval.Arguments[key] != want {
			t.Fatalf("binding %s call=%#v approval=%#v", key, call.Arguments, approval.Arguments)
		}
	}
	if !strings.Contains(approval.Summary, `Recipient: "alice@example.com"`) ||
		!strings.Contains(approval.Summary, `Subject: "Exact subject"`) || !strings.Contains(approval.Summary, `Full body: "Exact body"`) {
		t.Fatalf("approval summary is not exact-content: %q", approval.Summary)
	}
	if sender.calls != 0 {
		t.Fatalf("email was sent before approval: %d", sender.calls)
	}
}

func TestApprovedEmailRejectsAnyPersistedArgumentChangeBeforeExecution(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	sender := &recordingAgentEmailSender{}
	runtime.tools.WithEmailSender(sender)
	run := app.AgentRun{ID: "run_email_approval", SessionID: session.ID, State: "approval_pending", StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	callArgs := map[string]any{
		"provider": app.EmailProviderGmail, "account": app.EmailAccountDefault, "account_hint": "a***@gmail.com",
		"recipient": "alice@example.com", "subject": "subject", "body": "original body",
		"setting_version": "4", "browser_credential_generation": "7", "probe_revision": "2", "send_script_revision": "3",
		"validated_at": "2026-09-03T08:30:00Z", "invocation_id": "email:approval:1",
	}
	approvalArgs := clonePlanArgs(callArgs)
	approvalArgs["body"] = "modified body"
	call := app.ToolCall{
		ID: "tc_email_approval", SessionID: session.ID, RunID: run.ID, Tool: "email.send", Risk: app.RiskDangerous,
		Status: app.ToolCallStatusApprovalPending, Arguments: callArgs, ApprovalID: "approval_email_modified", StartedAt: time.Now().UTC(),
	}
	testSaveToolCall(st, call)
	approval, err := st.SaveApproval(t.Context(), app.Approval{
		ID: call.ApprovalID, ToolCallID: call.ID, SessionID: session.ID, RunID: run.ID, Tool: call.Tool, Risk: app.RiskDangerous,
		Status: app.ApprovalStatusPending, Summary: "modified approval fixture", Arguments: approvalArgs, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := st.ResolveApproval(t.Context(), approval.ID, app.ApprovalStatusApproved, "approved")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(t.Context(), approved)
	if err != nil || executed.Status != app.ToolCallStatusFailedAfterApproval || executed.ErrorCode != string(app.ToolErrorPolicyBlocked) ||
		!strings.Contains(executed.Error, "no longer matches") {
		t.Fatalf("modified email approval executed: call=%#v err=%v", executed, err)
	}
	if sender.calls != 0 {
		t.Fatalf("modified approval reached sender: %d", sender.calls)
	}
}

func TestBrowserEmailOutcomeIsTerminalAndNonRetryable(t *testing.T) {
	outcome := adaptBrowserEmailSendOutcome(app.ToolCall{
		ID: "tc_email", Tool: "email.send", Status: app.ToolCallStatusCompleted,
		Result: map[string]any{"provider": app.EmailProviderGmail, "status": "sent", "recipient_digest": "sha256:digest", "browser_credential_generation": 7, "script_revision": 3},
	}, "email_send")
	if outcome.Retryable || len(outcome.Signals) != 1 || outcome.Signals[0] != app.OutcomeSignalEmailSent || len(outcome.Refs) != 1 {
		t.Fatalf("email outcome = %#v", outcome)
	}
	assessment := (browserEmailProfile{}).Assess(nil, outcome)
	if assessment.Status != app.AssessmentComplete || assessment.ReasonCode != "email_sent" {
		t.Fatalf("email assessment = %#v", assessment)
	}
}

func emailSendRoute(catalogRevision string) app.RouteDecision {
	return app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalogRevision,
		CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserEmail},
		Slots:          app.RouteSlots{Operation: app.RouteOperationSend, Query: "send one email"},
		Facts: map[string]string{
			app.EmailRouteFactProvider: app.EmailProviderGmail, app.EmailRouteFactAccount: app.EmailAccountDefault,
			app.EmailRouteFactAccountHint: "a***@gmail.com", app.EmailRouteFactSettingVersion: "4",
			app.EmailRouteFactBrowserCredentialGeneration: "7", app.EmailRouteFactProbeRevision: "2",
			app.EmailRouteFactSendScriptRevision: "3", app.EmailRouteFactValidatedAt: "2026-09-03T08:30:00Z",
			app.EmailRouteFactInvocationID: "email_send_fixture",
		},
	}
}
