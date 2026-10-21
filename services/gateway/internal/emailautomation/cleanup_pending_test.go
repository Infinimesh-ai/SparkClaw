package emailautomation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// A fenced browser host rejects admission before any page or script runs.
// The mail client reports the lower-cased task reason in place of a script
// error code; it must stay distinct from a provider outage.
func TestFencedHostRejectionIsReadableAndLocal(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	c := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7},
		result: browsercontrol.ScriptExecutionResult{State: "failed", CredentialGeneration: 7,
			Result: json.RawMessage(`{"schema_version":1,"status":"error","provider":"gmail","code":"browser_cleanup_pending"}`)}}
	_, err := NewPlaywrightRunner(c).Probe(t.Context(), provider, "probe:fenced", 0)
	if ErrorCode(err) != app.ToolErrorEmailBrowserCleanupPending || !LocalOperationalFailure(err) || !err.(*Error).Retryable() ||
		err.Error() != publicScriptErrorMessage(app.ToolErrorEmailBrowserCleanupPending) {
		t.Fatalf("fenced host rejection: %v code=%s", err, ErrorCode(err))
	}

	c.result = browsercontrol.ScriptExecutionResult{}
	c.err = &browsercontrol.Error{Code: browsercontrol.CodeCleanupPending, Retryable: true}
	_, err = NewPlaywrightRunner(c).Probe(t.Context(), provider, "probe:fenced-transport", 0)
	if ErrorCode(err) != app.ToolErrorEmailBrowserCleanupPending || !LocalOperationalFailure(err) {
		t.Fatalf("fenced Controller error: %v code=%s", err, ErrorCode(err))
	}
}

func TestFencedProbeIsTemporarilyUnavailable(t *testing.T) {
	c := &Controller{store: store.NewMemoryStore(), now: time.Now}
	saved, err := c.persistProbe(t.Context(), app.EmailProviderSetting{Provider: app.EmailProviderGmail, State: app.EmailStateReady},
		"test", ProbeResult{}, &Error{Code: app.ToolErrorEmailBrowserCleanupPending})
	if err != nil || saved.State != app.EmailStateTemporarilyUnavailable || saved.ErrorCode != string(app.ToolErrorEmailBrowserCleanupPending) {
		t.Fatalf("fenced probe state: %+v %v", saved, err)
	}
}
