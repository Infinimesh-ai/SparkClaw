package emailautomation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type busyController struct {
	*fakePlaywrightController
	busy int
}

func (c *busyController) RunScript(ctx context.Context, request browsercontrol.RunScriptRequest) (browsercontrol.ScriptExecutionResult, error) {
	if c.busy > 0 {
		c.busy--
		c.requests = append(c.requests, request)
		return browsercontrol.ScriptExecutionResult{}, &browsercontrol.Error{Code: browsercontrol.CodeBusy, Retryable: true}
	}
	return c.fakePlaywrightController.RunScript(ctx, request)
}

func TestBrowserBusyYieldsAndRetriesOnlyBeforeScriptExecution(t *testing.T) {
	c := &busyController{fakePlaywrightController: &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}}, busy: 1}
	runner := NewPlaywrightRunner(c)
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	if _, err := runner.Probe(t.Context(), provider, "probe:busy", 0); err != nil {
		t.Fatal(err)
	}
	if len(c.requests) != 2 {
		t.Fatalf("expected one contention retry: %d", len(c.requests))
	}
	for _, r := range c.requests {
		if r.TaskID != "probe:busy" || r.CredentialGeneration != 7 || r.WaitTimeoutMS < 1 || r.WaitTimeoutMS > 2000 {
			t.Fatalf("lost binding or unbounded wait: %+v", r)
		}
	}
	c.requests = nil
	c.err = &browsercontrol.Error{Code: browsercontrol.CodeControllerUnavailable, Retryable: true}
	_, err := runner.Send(t.Context(), provider, SendRequest{Provider: provider.ID, Account: app.EmailAccountDefault, Recipient: "test@example.com", Body: "test", InvocationID: "send:no-replay", BrowserCredentialGeneration: 7, ProbeRevision: 1, ScriptRevision: 1})
	if ErrorCode(err) != app.ToolErrorEmailSendOutcomeUnknown || len(c.requests) != 1 {
		t.Fatalf("ambiguous send replayed: calls=%d code=%s", len(c.requests), ErrorCode(err))
	}
}

func TestBrowserBusyWaitIsBoundedAndCancelable(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(map[bool]string{false: "bounded", true: "canceled"}[cancelEarly], func(t *testing.T) {
			c := &busyController{fakePlaywrightController: &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}}, busy: 100}
			runner := NewPlaywrightRunner(c)
			provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
			ctx := t.Context()
			if cancelEarly {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
			}
			started := time.Now()
			_, err := runner.Probe(ctx, provider, "probe:bounded", 0)
			if cancelEarly {
				if !errors.Is(err, context.DeadlineExceeded) || len(c.requests) != 1 {
					t.Fatalf("cancellation became provider failure or retried: %v calls=%d", err, len(c.requests))
				}
			} else if ErrorCode(err) != app.ToolErrorEmailBrowserBusy || !LocalOperationalFailure(err) || !err.(*Error).Retryable() || time.Since(started) > 3*time.Second || len(c.requests) > 11 {
				t.Fatalf("busy classification/wait: %v calls=%d duration=%s", err, len(c.requests), time.Since(started))
			}
		})
	}
}

func TestBusyProbeDoesNotChangeProviderHealth(t *testing.T) {
	c := &Controller{store: store.NewMemoryStore()}
	original := app.EmailProviderSetting{Provider: app.EmailProviderGmail, State: app.EmailStateReady, Version: 7}
	saved, err := c.persistProbe(t.Context(), original, "test", ProbeResult{}, &Error{Code: app.ToolErrorEmailBrowserBusy})
	if err != nil || saved.State != original.State || saved.Version != original.Version || saved.ErrorCode != "" || saved.LastCheckedAt != nil {
		t.Fatalf("busy probe changed health: %+v %v", saved, err)
	}
}
