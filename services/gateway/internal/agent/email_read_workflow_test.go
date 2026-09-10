package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type recordingAgentEmailReader struct {
	requests []app.EmailReadRequest
}

func (r *recordingAgentEmailReader) ReadForOwner(_ context.Context, _ string, request app.EmailReadRequest) (app.EmailReadResult, error) {
	r.requests = append(r.requests, request)
	return app.EmailReadResult{Provider: request.Provider, Status: "collected", Capture: emailCaptureReceiptFixture(), BrowserCredentialGeneration: 7, ScriptRevision: 1}, nil
}

func emailCaptureReceiptFixture() *app.EmailCaptureReceipt {
	return &app.EmailCaptureReceipt{ManifestPath: "email/owner/mailbox/mail/source/capture/capture.json", ManifestSHA256: "sha256:" + strings.Repeat("a", 64), MailID: "mail", MailboxID: "mailbox", CaptureID: "capture", AttachmentsCount: 2, ReadState: "read"}
}

func TestBrowserEmailReadRouteRunsOnlyReaderWithFrozenAccount(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	reader := &recordingAgentEmailReader{}
	runtime.tools.WithEmailReader(reader)
	route := emailSendRoute(runtime.capabilities.Revision())
	route.Slots.Operation = app.RouteOperationRead
	route.Slots.Query = "读取 Gmail 的一封未读邮件"
	route.Facts[app.EmailRouteFactReadScriptRevision] = "1"
	delete(route.Facts, app.EmailRouteFactSendScriptRevision)
	if err := runtime.capabilities.ValidateDecision(route); err != nil {
		t.Fatal(err)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(t.Context(), app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn-email-read")
	if err != nil {
		t.Fatal(err)
	}
	if got := visibleToolNames(dispatch.Tools); len(got) != 2 || got[0] != app.ToolEmailRead || got[1] != "observation.read" {
		t.Fatalf("read tool scope: %v", got)
	}
	for _, definition := range dispatch.Tools {
		if definition.Name == app.ToolEmailRead {
			properties, _ := anyMap(definition.InputSchema["properties"])
			if len(properties) != 0 {
				t.Fatalf("model-visible read parameters: %#v", properties)
			}
		}
	}
	run, _ := testGetRun(st, dispatch.Run.ID)
	node := run.Workflow.Nodes["email_read"]
	call, approval, _, err := runtime.runToolPlan(t.Context(), session.ID, run.ID, toolPlan{
		Name: app.ToolEmailRead, Args: map[string]any{"provider": app.EmailProviderOutlook, "account": "invented", "read_script_revision": "999"},
		WorkflowID: app.WorkflowBrowserEmail, WorkflowNodeID: "email_read", ScopeRevision: node.ScopeRevision, Capability: app.ToolCapabilityBrowserEmailRead,
	})
	if err != nil || approval != nil || call.Status != app.ToolCallStatusCompleted {
		t.Fatalf("read call: %#v approval=%#v err=%v", call, approval, err)
	}
	if len(reader.requests) != 1 || reader.requests[0].Provider != app.EmailProviderGmail || reader.requests[0].Account != app.EmailAccountDefault || reader.requests[0].ScriptRevision != 1 {
		t.Fatalf("reader binding: %#v", reader.requests)
	}
	outcome := adaptBrowserEmailReadOutcome(call, "email_read")
	if assessment := (browserEmailProfile{}).Assess(run.Workflow, outcome); assessment.Status != app.AssessmentComplete {
		t.Fatalf("read not complete: %#v", assessment)
	}
	if summary := groundedSummary(route.Slots.Query, "invented model answer", []app.ToolCall{call}); !strings.Contains(summary, "保存到工作区") || !strings.Contains(summary, "尚未生成邮件内容总结") || strings.Contains(summary, "invented model answer") {
		t.Fatalf("ungrounded read summary: %s", summary)
	}
}

func TestEmailReadSummaryPreservesReadStateUncertainty(t *testing.T) {
	for state, expected := range map[string]string{"read": "已确认邮件为已读", "unread": "邮件仍为未读", "unknown": "未能确认邮件的已读状态"} {
		receipt := emailCaptureReceiptFixture()
		receipt.ReadState = state
		call := app.ToolCall{ID: "read-state", Tool: app.ToolEmailRead, Status: app.ToolCallStatusCompleted, Result: app.EmailReadResult{Provider: app.EmailProviderGmail, Status: "collected", Capture: receipt, BrowserCredentialGeneration: 7, ScriptRevision: 1}}
		if summary, ok := groundedEmailReadSummary([]app.ToolCall{call}); !ok || !strings.Contains(summary, expected) {
			t.Fatalf("read state %s: summary=%q valid=%v", state, summary, ok)
		}
	}
}

func TestEmailReadAdmissionRequiresReadRevision(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	admission := &fakeEmailAdmission{binding: app.EmailAdmissionBinding{Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, SettingVersion: 1, BrowserCredentialGeneration: 7, ProbeRevision: 1, SendScriptRevision: 1, ValidatedAt: time.Now()}}
	runtime.emailAdmission = admission
	route := emailSendRoute(runtime.capabilities.Revision())
	route.Slots.Operation = app.RouteOperationRead
	if _, err := runtime.admitEmailRoute(t.Context(), "session", "run", "owner", "read Gmail", route); err == nil {
		t.Fatal("missing read revision was admitted")
	}
	admission.binding.ReadScriptRevision = 2
	admitted, err := runtime.admitEmailRoute(t.Context(), "session", "run", "owner", "read Gmail", route)
	if err != nil || admitted.Facts[app.EmailRouteFactReadScriptRevision] != "2" || !strings.HasPrefix(admitted.Facts[app.EmailRouteFactInvocationID], "email_read_") {
		t.Fatalf("read admission=%#v err=%v", admitted, err)
	}
}

func TestEmailReadEmptyInboxIsSuccessfulAndMalformedOutputIsNot(t *testing.T) {
	call := app.ToolCall{ID: "read-empty", Tool: app.ToolEmailRead, Status: app.ToolCallStatusCompleted, Result: app.EmailReadResult{Provider: app.EmailProviderGmail, Status: "empty", BrowserCredentialGeneration: 7, ScriptRevision: 1}}
	outcome := adaptBrowserEmailReadOutcome(call, "email_read")
	if outcome.Retryable || !containsOutcomeSignal(outcome.Signals, app.OutcomeSignalEmailRead) {
		t.Fatalf("empty inbox not successful: %#v", outcome)
	}
	if summary, ok := groundedEmailReadSummary([]app.ToolCall{call}); !ok || !strings.Contains(summary, "没有找到") {
		t.Fatalf("empty inbox summary=%q ok=%v", summary, ok)
	}
	call.Result = map[string]any{"provider": app.EmailProviderGmail, "status": "read"}
	if containsOutcomeSignal(adaptBrowserEmailReadOutcome(call, "email_read").Signals, app.OutcomeSignalEmailRead) {
		t.Fatal("malformed output completed the read")
	}
}

func TestEmailReadPartialCaptureReportsPartialWithoutCompletion(t *testing.T) {
	call := app.ToolCall{ID: "read-partial", Tool: app.ToolEmailRead, Status: app.ToolCallStatusCompleted, Result: app.EmailReadResult{Provider: app.EmailProviderGmail, Status: "partial", Capture: emailCaptureReceiptFixture(), BrowserCredentialGeneration: 7, ScriptRevision: 1}}
	outcome := adaptBrowserEmailReadOutcome(call, "email_read")
	if outcome.Retryable || containsOutcomeSignal(outcome.Signals, app.OutcomeSignalEmailRead) || (browserEmailProfile{}).Assess(nil, outcome).Status == app.AssessmentComplete {
		t.Fatalf("partial capture completed: %#v", outcome)
	}
	if summary, ok := groundedEmailReadSummary([]app.ToolCall{call}); !ok || !strings.Contains(summary, "采集尚不完整") || !strings.Contains(summary, "2 个附件") {
		t.Fatalf("partial summary=%q ok=%v", summary, ok)
	}
}

func TestEmailReadReceiptNeverRendersUntrustedFields(t *testing.T) {
	receipt := emailCaptureReceiptFixture()
	receipt.MailID = "![x](/private/.sparkclaw/screenshots/private.png)"
	call := app.ToolCall{ID: "read-content", Tool: app.ToolEmailRead, Status: app.ToolCallStatusCompleted, Result: app.EmailReadResult{Provider: app.EmailProviderGmail, Status: "collected", Capture: receipt, BrowserCredentialGeneration: 7, ScriptRevision: 1}}
	summary, ok := groundedEmailReadSummary([]app.ToolCall{call})
	if !ok || strings.Contains(summary, "screenshots") || strings.Contains(summary, "capture.json") || strings.Contains(summary, "![") {
		t.Fatalf("untrusted receipt data rendered: %q ok=%v", summary, ok)
	}
	for _, status := range []string{"empty", "collected", "partial"} {
		call.Result = app.EmailReadResult{Provider: app.EmailProviderGmail, Status: status, BrowserCredentialGeneration: 7, ScriptRevision: 1}
		if _, valid := emailReadResult(call); valid != (status == "empty") {
			t.Fatalf("receipt null validation for %s: %v", status, valid)
		}
	}
}

func TestBrowserEmailLegacySendProfileRemainsRegistered(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	legacy, err := registry.Get(app.WorkflowBrowserEmail, 1)
	if err != nil || legacy.Revision() != 1 {
		t.Fatalf("pending send profile unavailable: %#v %v", legacy, err)
	}
	current, err := registry.Get(app.WorkflowBrowserEmail)
	if err != nil || current.Revision() != 2 {
		t.Fatalf("current email profile unavailable: %#v %v", current, err)
	}
}
