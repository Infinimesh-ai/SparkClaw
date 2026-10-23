package agent

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func sentManualArgs() map[string]any {
	return map[string]any{"provider": "gmail", "account": "default", "account_hint": "caller hint", "setting_version": "999", "browser_credential_generation": "999", "probe_revision": "999", "read_script_revision": "999", "validated_at": "caller timestamp", "invocation_id": "caller-id", "sent_match": map[string]any{"folder": "sent", "subject": "验收进度汇报 [case-3]", "recipient": "sink@example.test", "interval_start": "2026-10-23T13:00:00Z", "interval_end": "2026-10-23T14:00:00Z"}}
}

func TestManualSentReadFreshAdmissionOverridesCallerBindingOnce(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	reader := &recordingAgentEmailReader{}
	runtime.tools.WithEmailReader(reader)
	admission := &fakeEmailAdmission{binding: app.EmailAdmissionBinding{Provider: "gmail", Account: "default", AccountHint: "o***@example.test", SettingVersion: 4, BrowserCredentialGeneration: 7, ProbeRevision: 1, ReadScriptRevision: 1, ValidatedAt: time.Now().UTC()}}
	runtime.emailAdmission = admission
	args := sentManualArgs()
	predicate := args["sent_match"]
	result, err := runtime.InvokeToolManually(t.Context(), app.ToolEmailRead, args, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(admission.owners) != 1 || admission.owners[0] != session.OwnerID || admission.inputs[0] != "read Gmail" || len(reader.requests) != 1 {
		t.Fatal("admission/read not one each")
	}
	request := reader.requests[0]
	if request.SettingVersion != 4 || request.BrowserCredentialGeneration != 7 || request.ScriptRevision != 1 || request.ProbeRevision != 1 || !strings.HasPrefix(request.InvocationID, "email_read_") || request.InvocationID == "caller-id" || request.AccountAddress != "" {
		t.Fatal("caller binding survived")
	}
	if result.Call.Status != app.ToolCallStatusCompleted || !reflect.DeepEqual(result.Call.Arguments["sent_match"], predicate) || args["invocation_id"] != "caller-id" {
		t.Fatal("selector or original arguments changed")
	}
	saved, found, err := st.GetToolCall(t.Context(), result.Call.ID)
	if err != nil || !found || !reflect.DeepEqual(saved.Arguments, result.Call.Arguments) {
		t.Fatal("bound receipt not durable")
	}
}

func TestManualSentReadPolicyAndAdmissionFailuresDoNotRead(t *testing.T) {
	for _, mode := range []string{"denied", "admission failed", "account changed", "provider changed", "missing revision", "wrong session", "invalid selector", "unsupported provider"} {
		t.Run(mode, func(t *testing.T) {
			runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
			defer closeRuntime()
			reader := &recordingAgentEmailReader{}
			runtime.tools.WithEmailReader(reader)
			admission := &fakeEmailAdmission{binding: app.EmailAdmissionBinding{Provider: "gmail", Account: "default", SettingVersion: 4, BrowserCredentialGeneration: 7, ProbeRevision: 1, ReadScriptRevision: 1, ValidatedAt: time.Now().UTC()}}
			runtime.emailAdmission = admission
			args := sentManualArgs()
			sessionID := session.ID
			switch mode {
			case "denied":
				cfg := runtime.tools.Config()
				cfg.Security.DeniedTools = []string{app.ToolEmailRead}
				runtime.policy.Update(cfg)
			case "admission failed":
				admission.err = errors.New("revoked login")
			case "account changed":
				admission.binding.Account = "other"
			case "provider changed":
				admission.binding.Provider = "outlook"
			case "missing revision":
				admission.binding.ReadScriptRevision = 0
			case "wrong session":
				sessionID = "unknown"
			case "invalid selector":
				args["sent_match"].(map[string]any)["folder"] = "all"
			case "unsupported provider":
				args["provider"] = "outlook"
			}
			result, err := runtime.InvokeToolManually(t.Context(), app.ToolEmailRead, args, sessionID)
			if err == nil || len(reader.requests) != 0 {
				t.Fatal("failed admission read mail")
			}
			if mode == "denied" || mode == "invalid selector" || mode == "wrong session" || mode == "unsupported provider" {
				if len(admission.owners) != 0 {
					t.Fatal("pre-admission failure called provider")
				}
			}
			if mode == "admission failed" || mode == "account changed" || mode == "provider changed" || mode == "missing revision" {
				if result.Call.Status != app.ToolCallStatusFailed {
					t.Fatal("failure was not recorded")
				}
				run, ok := testGetRun(st, result.Call.RunID)
				if !ok || run.State != "failed" {
					t.Fatal("failed run missing")
				}
			}
		})
	}
}

func TestSynchronizationReadWorkflowRejectsSentSelectorInjection(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	route := emailSendRoute(runtime.capabilities.Revision())
	route.Slots.Operation = app.RouteOperationRead
	route.Facts[app.EmailRouteFactReadScriptRevision] = "1"
	delete(route.Facts, app.EmailRouteFactSendScriptRevision)
	dispatch, err := runtime.dispatchMatchedWorkflow(t.Context(), app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn-read")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := testGetRun(st, dispatch.Run.ID)
	node := run.Workflow.Nodes["email_read"]
	for _, def := range dispatch.Tools {
		if def.Name == app.ToolEmailRead {
			props, _ := anyMap(def.InputSchema["properties"])
			if len(props) != 0 {
				t.Fatal("default workflow gained model parameters")
			}
		}
	}
	_, err = runtime.materializeWorkflowBoundArguments(t.Context(), run.ID, toolPlan{Name: app.ToolEmailRead, Args: sentManualArgs(), WorkflowID: app.WorkflowBrowserEmail, WorkflowNodeID: "email_read", ScopeRevision: node.ScopeRevision, Capability: app.ToolCapabilityBrowserEmailRead})
	if err == nil {
		t.Fatal("model selector entered synchronization workflow")
	}
}
