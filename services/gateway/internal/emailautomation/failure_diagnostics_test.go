package emailautomation

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"strings"
	"testing"
)

type diagnosticCarrier interface {
	DiagnosticCause() (code, stage, source string)
}

func TestMappedSendErrorRetainsSafeCauseWithoutChangingPublicError(t *testing.T) {
	for _, tc := range []struct {
		err           error
		public        app.ToolErrorCode
		cause, source string
	}{
		{&browsercontrol.Error{Code: browsercontrol.CodeControllerUnavailable}, app.ToolErrorEmailSendOutcomeUnknown, browsercontrol.CodeControllerUnavailable, "browser_control"},
		{&browsercontrol.Error{Code: browsercontrol.CodeExtensionUnavailable}, app.ToolErrorEmailProviderUnavailable, browsercontrol.CodeExtensionUnavailable, "browser_control"},
		{context.DeadlineExceeded, app.ToolErrorEmailProviderUnavailable, "timeout", "context"},
		{&browsercontrol.Error{Code: "secret@example.test"}, app.ToolErrorEmailProviderUnavailable, "unclassified", "browser_control"},
	} {
		t.Run(tc.cause, func(t *testing.T) {
			err := mapPlaywrightError(tc.err, true)
			if ErrorCode(err) != tc.public {
				t.Fatalf("public behavior changed: %v", err)
			}
			d, ok := err.(diagnosticCarrier)
			if !ok {
				t.Fatal("mapping discarded internal cause")
			}
			code, stage, source := d.DiagnosticCause()
			if code != tc.cause || source != tc.source || stage != "controller_request" {
				t.Fatalf("cause=%s stage=%s source=%s", code, stage, source)
			}
			if strings.Contains(code+stage+source, "secret") {
				t.Fatal("unsafe diagnostic")
			}
		})
	}
}

func TestScriptFailurePreservesKnownCodeButNotArbitraryProviderText(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	for _, tc := range []struct{ raw, want string }{{"email_attachment_upload_failed", "email_attachment_upload_failed"}, {"private_subject_body", "unclassified"}} {
		raw := []byte(`{"schema_version":1,"status":"error","provider":"gmail","code":"` + tc.raw + `"}`)
		err := playwrightScriptFailure(provider, raw)
		d, ok := err.(diagnosticCarrier)
		if !ok {
			t.Fatal("script mapping discarded cause")
		}
		code, stage, source := d.DiagnosticCause()
		if code != tc.want || stage != "script_result" || source != "application" {
			t.Fatalf("%s %s %s", code, stage, source)
		}
	}
}
