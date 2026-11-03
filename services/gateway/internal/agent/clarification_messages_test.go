package agent

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Each class must read as what it is: "not supported" never sounds temporary,
// "unavailable" never sounds permanent, and "needs input" asks for something.
func assertOwnerFailureWording(t *testing.T, name string, message ownerFailureMessage) {
	t.Helper()
	containsAny := func(text string, fragments ...string) bool {
		for _, fragment := range fragments {
			if strings.Contains(text, fragment) {
				return true
			}
		}
		return false
	}
	switch message.class {
	case failureUnsupported:
		if !strings.Contains(message.zh, "不支持") || containsAny(message.zh, "暂时", "稍后") {
			t.Fatalf("%s: unsupported zh must say 不支持 without sounding temporary: %q", name, message.zh)
		}
		if !containsAny(message.en, "not supported", "does not support", "do not support") || containsAny(message.en, "temporarily", "try again later") {
			t.Fatalf("%s: unsupported en must say not supported without sounding temporary: %q", name, message.en)
		}
	case failureUnavailable:
		if !containsAny(message.zh, "暂时", "稍后") || strings.Contains(message.zh, "不支持") {
			t.Fatalf("%s: unavailable zh must sound temporary and never say 不支持: %q", name, message.zh)
		}
		if !containsAny(message.en, "right now", "temporarily", "try again later", "for now") || strings.Contains(message.en, "not supported") {
			t.Fatalf("%s: unavailable en must sound temporary and never say not supported: %q", name, message.en)
		}
	case failureNeedsInput:
		if !strings.Contains(message.zh, "请") || containsAny(message.zh, "不支持", "暂时") {
			t.Fatalf("%s: needs-input zh must ask the owner without claiming unsupported or temporary: %q", name, message.zh)
		}
		if !containsAny(strings.ToLower(message.en), "please", "sign in", "check") {
			t.Fatalf("%s: needs-input en must ask the owner: %q", name, message.en)
		}
		if containsAny(message.en, "not supported", "temporarily") {
			t.Fatalf("%s: needs-input en must not claim unsupported or temporary: %q", name, message.en)
		}
	default:
		t.Fatalf("%s: unknown failure class %q", name, message.class)
	}
}

func TestOwnerFailureMessagesMatchTheirClass(t *testing.T) {
	for reason, message := range routingFailureMessages {
		assertOwnerFailureWording(t, reason, message)
	}
	assertOwnerFailureWording(t, "routing needs-input fallback", routingNeedsInputFallback)
	assertOwnerFailureWording(t, "routing unavailable fallback", routingUnavailableFallback)
	for code, message := range workflowFailureMessages {
		assertOwnerFailureWording(t, string(code), message)
	}
	assertOwnerFailureWording(t, "workflow fallback", workflowFailureFallback)
}

func TestRoutingFailureSeparatesUnsupportedUnavailableAndNeedsInput(t *testing.T) {
	for _, test := range []struct {
		status app.RouteStatus
		reason string
		class  ownerFailureClass
	}{
		{app.RouteClarify, "document_operation_variant_conflicts_with_format", failureUnsupported},
		{app.RouteBlocked, `document_format_read_only: document format "image" is read-only`, failureUnsupported},
		{app.RouteBlocked, "localmind_text_only_input_required", failureUnsupported},
		{app.RouteBlocked, "publication_unavailable", failureUnsupported},
		{app.RouteBlocked, "pptx_edit_target_unsupported", failureUnsupported},
		{app.RouteBlocked, "email_operation_unsupported: Unsupported email operation", failureUnsupported},
		{app.RouteBlocked, "semantic_channels_unavailable", failureUnavailable},
		{app.RouteBlocked, "mutation_requires_healthy_semantic_pipeline", failureUnavailable},
		{app.RouteBlocked, "routing_failed: semantic intent router is unavailable", failureUnavailable},
		{app.RouteBlocked, "delivery_endpoint_unavailable: the requested external delivery endpoint is unavailable", failureUnavailable},
		{app.RouteBlocked, "email_provider_unavailable: provider offline", failureUnavailable},
		{app.RouteBlocked, "an unmapped internal failure", failureUnavailable},
		{app.RouteClarify, "top_candidates_compete", failureNeedsInput},
		{app.RouteClarify, "required_grounded_target_missing", failureNeedsInput},
		{app.RouteClarify, "delivery_recipient_ambiguous: external delivery recipient matches more than one account or chat", failureNeedsInput},
		{app.RouteBlocked, "email_login_required: login page shown", failureNeedsInput},
		{app.RouteBlocked, "document_path_unavailable: document path is unavailable", failureNeedsInput},
		{app.RouteUnmatched, "semantic_coverage_low", failureNeedsInput},
		{app.RouteUnmatched, "explicit_localmind_assignment_required", failureNeedsInput},
		{app.RouteClarify, "an unmapped clarification", failureNeedsInput},
	} {
		message := routingFailure(app.RouteDecision{Status: test.status, Reason: test.reason})
		if message.class != test.class {
			t.Fatalf("%s/%q classified as %q, want %q", test.status, test.reason, message.class, test.class)
		}
		for _, text := range []string{message.text(true, false), message.text(false, false), message.text(true, true)} {
			if strings.Contains(text, routeReasonCode(test.reason)) || strings.Contains(text, "capability boundary") {
				t.Fatalf("owner message leaked a diagnostic for %q: %q", test.reason, text)
			}
		}
	}
}

func TestRoutingFailureTimerWording(t *testing.T) {
	low := routingFailure(app.RouteDecision{Status: app.RouteUnmatched, Reason: "semantic_coverage_low"})
	if got := low.text(true, true); got != "这条定时任务的内容无法确定如何执行，请修改任务后再试。" {
		t.Fatalf("timer semantic_coverage_low = %q", got)
	}
	outage := routingFailure(app.RouteDecision{Status: app.RouteBlocked, Reason: "semantic_channels_unavailable"})
	if got := outage.text(true, true); got != "这条定时任务未能执行：意图识别服务暂时不可用，请稍后重试。" {
		t.Fatalf("timer outage must keep its own cause, got %q", got)
	}
	if got := outage.text(false, true); !strings.HasPrefix(got, "The scheduled task did not run: ") || !strings.Contains(got, "temporarily unavailable") {
		t.Fatalf("english timer outage = %q", got)
	}
}

type testToolCodedError struct{ code app.ToolErrorCode }

func (e testToolCodedError) Error() string                    { return "coded failure" }
func (e testToolCodedError) ToolErrorCode() app.ToolErrorCode { return e.code }

func TestEmailAdmissionReasonKeepsStableCodeAndDiagnostic(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{errEmailOperationUnsupported, "email_operation_unsupported: Unsupported email operation"},
		{errEmailAdmissionUnavailable, "email_unsupported_here: Browser email is not configured"},
		{testToolCodedError{app.ToolErrorEmailLoginRequired}, "email_login_required: coded failure"},
		{testToolCodedError{app.ToolErrorEmailBrowserBusy}, "email_browser_busy: coded failure"},
		{errors.New("Email login admission returned an incomplete binding"), "email_admission_unavailable: Email login admission returned an incomplete binding"},
	} {
		if got := emailAdmissionReason(test.err); got != test.want {
			t.Fatalf("emailAdmissionReason(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestDocumentPreflightReasonClassifiesFailures(t *testing.T) {
	root := t.TempDir()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "photo.png"), encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	_, missing := preflightDocumentPath(root, "missing.txt", false)
	_, outside := preflightExternalMCPDocumentPath("../outside.txt", false)
	_, unrecognized := preflightExternalMCPDocumentPath("photo.png", true)
	_, readOnly := preflightDocumentPath(root, "photo.png", true)
	for _, test := range []struct {
		err  error
		code string
	}{
		{missing, "document_path_unavailable"},
		{outside, "document_path_outside_workspace"},
		{unrecognized, "document_format_unrecognized"},
		{readOnly, "document_format_read_only"},
		{errors.New("materialize failed"), "document_preflight_failed"},
	} {
		if test.err == nil {
			t.Fatalf("expected a preflight failure for %s", test.code)
		}
		reason := documentPreflightReason(test.err)
		if routeReasonCode(reason) != test.code || !strings.HasSuffix(reason, test.err.Error()) {
			t.Fatalf("documentPreflightReason(%v) = %q, want code %q with diagnostic", test.err, reason, test.code)
		}
	}
}

func TestMessageControlTerminalRouteReasonsSelectOwnerMessages(t *testing.T) {
	for status, class := range map[TargetResolutionStatus]ownerFailureClass{
		TargetNeedsChannel:   failureNeedsInput,
		TargetNeedsRecipient: failureNeedsInput,
		TargetAmbiguous:      failureNeedsInput,
		TargetUnavailable:    failureUnavailable,
	} {
		route, terminal := messageControlTerminalRoute(DeliveryTargetSelection{Status: status}, "rev")
		if !terminal {
			t.Fatalf("%s is not terminal", status)
		}
		if _, mapped := routingFailureMessages[routeReasonCode(route.Reason)]; !mapped {
			t.Fatalf("%s reason %q has no owner message", status, route.Reason)
		}
		if got := routingFailure(route).class; got != class {
			t.Fatalf("%s classified as %q, want %q", status, got, class)
		}
	}
}

func TestOwnerWorkflowFailureMessageIsLocalized(t *testing.T) {
	run := app.AgentRun{}
	if got := ownerWorkflowFailureMessage(workflowFailureModelUnavailable, run, "帮我总结一下"); got != "模型服务暂时不可用，请稍后重试。" {
		t.Fatalf("chinese model-unavailable message = %q", got)
	}
	if got := ownerWorkflowFailureMessage(workflowFailurePromptFixedOversized, run, "summarize this"); !strings.Contains(got, "not supported") {
		t.Fatalf("english oversized-prompt message = %q", got)
	}
	if got := ownerWorkflowFailureMessage(workflowFailureCode("unknown_code"), run, "你好"); got != workflowFailureFallback.zh {
		t.Fatalf("unknown workflow failure = %q", got)
	}
}

func TestDeliveryEndpointReportsWhichDestinationPartIsMissing(t *testing.T) {
	run := app.AgentRun{SessionID: "session", MessageContext: &app.MessageRunContext{OwnerID: app.DefaultOwnerID, Source: app.MessageSourceContext{Kind: app.MessageSourceWeb}}}
	_, err := Runtime{}.deliveryEndpoint(t.Context(), run, DeliveryDirective{ExplicitExternal: true, RequestedRecipientText: "张三"})
	status, reason := deliveryEndpointRouting(err)
	if status != app.RouteBlocked || routeReasonCode(reason) != "delivery_target_resolution_failed" {
		t.Fatalf("missing resolver = %s %q, want blocked delivery_target_resolution_failed", status, reason)
	}
	for selectionStatus, want := range map[TargetResolutionStatus]string{
		TargetNeedsChannel:   "delivery_channel_required",
		TargetNeedsRecipient: "delivery_recipient_required",
		TargetAmbiguous:      "delivery_recipient_ambiguous",
		TargetUnavailable:    "delivery_endpoint_unavailable",
	} {
		terminal, _ := messageControlTerminalRoute(DeliveryTargetSelection{Status: selectionStatus}, "")
		status, reason := deliveryEndpointRouting(deliveryEndpointError{status: terminal.Status, reason: terminal.Reason})
		if status != terminal.Status || routeReasonCode(reason) != want {
			t.Fatalf("%s = %s %q, want %q", selectionStatus, status, reason, want)
		}
	}
	if status, reason := deliveryEndpointRouting(errors.New("other")); status != app.RouteClarify || reason != "delivery_confirmation_required" {
		t.Fatalf("unclassified endpoint failure = %s %q", status, reason)
	}
}
