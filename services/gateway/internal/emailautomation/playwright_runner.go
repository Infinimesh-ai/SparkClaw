package emailautomation

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

type PlaywrightController interface {
	Status(context.Context) browsercontrol.Status
	RunScript(context.Context, browsercontrol.RunScriptRequest) (browsercontrol.ScriptExecutionResult, error)
	OpenProviderLogin(context.Context, browsercontrol.OpenProviderLoginRequest) (browsercontrol.OpenProviderLoginResult, error)
}

type PlaywrightRunner struct {
	controller PlaywrightController
	now        func() time.Time
}

func NewPlaywrightRunner(controller PlaywrightController) *PlaywrightRunner {
	return &PlaywrightRunner{
		controller: controller,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// scriptWaitGrace covers the controller's profile reservation wait and the
// response round trip on top of a script's own budget.
const scriptWaitGrace = 60 * time.Second

// scriptContext bounds a controller call by the script's declared budget so a
// wedged controller cannot pin the request and the per-process profile mutex.
func scriptContext(ctx context.Context, script Script) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, script.Timeout+scriptWaitGrace)
}

func (r *PlaywrightRunner) OpenLogin(ctx context.Context, provider Provider) error {
	if r == nil || r.controller == nil || strings.TrimSpace(provider.ID) == "" {
		return codedError(CodeProviderUnavailable, "Email browser automation is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, scriptWaitGrace)
	defer cancel()
	_, err := r.controller.OpenProviderLogin(ctx, browsercontrol.OpenProviderLoginRequest{
		TaskID:   app.NewID("email_login"),
		Provider: provider.ID,
	})
	return mapPlaywrightError(err, false)
}

func (r *PlaywrightRunner) Probe(
	ctx context.Context,
	provider Provider,
	invocationID string,
	expectedGeneration uint64,
) (ProbeResult, error) {
	if !invocationIDPattern.MatchString(invocationID) {
		return ProbeResult{}, codedError(CodeInvalidInput, "Email probe invocation is invalid")
	}
	generation, err := r.credentialGeneration(ctx, expectedGeneration)
	if err != nil {
		return ProbeResult{}, err
	}
	input := struct {
		SchemaVersion int    `json:"schema_version"`
		Operation     string `json:"operation"`
		InvocationID  string `json:"invocation_id"`
		Provider      string `json:"provider"`
		Account       string `json:"account"`
	}{1, "probe", invocationID, provider.ID, app.EmailAccountDefault}
	ctx, cancel := scriptContext(ctx, provider.Probe)
	defer cancel()
	result, err := r.controller.RunScript(ctx, browsercontrol.RunScriptRequest{
		TaskID: invocationID, CredentialGeneration: generation,
		Provider: provider.ID, Operation: "probe", ScriptID: provider.Probe.ID,
		Revision: provider.Probe.Revision, Input: input,
	})
	if err != nil {
		return ProbeResult{}, mapPlaywrightError(err, false)
	}
	if result.State == "failed" {
		return ProbeResult{}, playwrightScriptFailure(provider, result.Result)
	}
	var output struct {
		SchemaVersion int    `json:"schema_version"`
		Status        string `json:"status"`
		Provider      string `json:"provider"`
		AccountHint   string `json:"account_hint,omitempty"`
	}
	if err := decodeStrictJSON(result.Result, &output); err != nil ||
		output.SchemaVersion != 1 || output.Status != "ready" || output.Provider != provider.ID ||
		!validAccountHint(output.AccountHint) || result.CredentialGeneration != generation {
		return ProbeResult{}, codedError(CodeScriptInvalidOutput, "Email login probe returned an invalid result")
	}
	return ProbeResult{
		Provider: provider.ID, AccountHint: output.AccountHint,
		Generation: uint64(result.CredentialGeneration), Revision: provider.Probe.Revision,
		CheckedAt: r.now(),
	}, nil
}

func (r *PlaywrightRunner) Send(
	ctx context.Context,
	provider Provider,
	request SendRequest,
) (SendResult, error) {
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	request.Account = strings.ToLower(strings.TrimSpace(request.Account))
	request.Recipient = strings.TrimSpace(request.Recipient)
	if request.Provider != provider.ID || request.Account != app.EmailAccountDefault ||
		request.BrowserCredentialGeneration == 0 || request.ProbeRevision != provider.Probe.Revision ||
		request.ScriptRevision != provider.Send.Revision ||
		!invocationIDPattern.MatchString(request.InvocationID) {
		return SendResult{}, codedError(CodeInvalidInput, "Email send binding is invalid")
	}
	if err := validateMessage(request.Recipient, request.Subject, request.Body); err != nil {
		return SendResult{}, err
	}
	generation, err := r.credentialGeneration(ctx, request.BrowserCredentialGeneration)
	if err != nil {
		return SendResult{}, err
	}
	input := struct {
		SchemaVersion int    `json:"schema_version"`
		Operation     string `json:"operation"`
		InvocationID  string `json:"invocation_id"`
		Provider      string `json:"provider"`
		Account       string `json:"account"`
		Message       struct {
			Recipient string `json:"recipient"`
			Subject   string `json:"subject"`
			Body      struct {
				Format  string `json:"format"`
				Content string `json:"content"`
			} `json:"body"`
		} `json:"message"`
	}{SchemaVersion: 1, Operation: "send", InvocationID: request.InvocationID, Provider: provider.ID, Account: app.EmailAccountDefault}
	input.Message.Recipient = request.Recipient
	input.Message.Subject = request.Subject
	input.Message.Body.Format = "text"
	input.Message.Body.Content = request.Body
	ctx, cancel := scriptContext(ctx, provider.Send)
	defer cancel()
	result, err := r.controller.RunScript(ctx, browsercontrol.RunScriptRequest{
		TaskID: request.InvocationID, CredentialGeneration: generation,
		Provider: provider.ID, Operation: "send", ScriptID: provider.Send.ID,
		Revision: provider.Send.Revision, Input: input,
	})
	if err != nil {
		return SendResult{}, mapPlaywrightError(err, true)
	}
	if result.State == "failed" {
		return SendResult{}, playwrightScriptFailure(provider, result.Result)
	}
	var output struct {
		SchemaVersion     int    `json:"schema_version"`
		Status            string `json:"status"`
		Provider          string `json:"provider"`
		RecipientDigest   string `json:"recipient_digest"`
		ProviderMessageID string `json:"provider_message_id,omitempty"`
	}
	if err := decodeStrictJSON(result.Result, &output); err != nil ||
		output.SchemaVersion != 1 || output.Status != "sent" || output.Provider != provider.ID ||
		output.RecipientDigest != recipientDigest(request.Recipient) ||
		!validOpaqueProviderID(output.ProviderMessageID) ||
		result.CredentialGeneration != generation {
		return SendResult{}, codedError(CodeScriptInvalidOutput, "Email send script returned an invalid result")
	}
	return SendResult{
		Provider: provider.ID, Status: output.Status, RecipientDigest: output.RecipientDigest,
		ProviderMessageID:           output.ProviderMessageID,
		BrowserCredentialGeneration: uint64(result.CredentialGeneration), ScriptRevision: provider.Send.Revision,
	}, nil
}

func (r *PlaywrightRunner) credentialGeneration(ctx context.Context, expected uint64) (int64, error) {
	if r == nil || r.controller == nil {
		return 0, codedError(CodeProviderUnavailable, "Email browser automation is unavailable")
	}
	status := r.controller.Status(ctx)
	if !status.Configured || status.CredentialGeneration <= 0 {
		return 0, codedError(CodeNotConfigured, "Browser control is not configured")
	}
	if expected != 0 && uint64(status.CredentialGeneration) != expected {
		return 0, codedError(CodeAdmissionStale, "Email login admission is stale; check login status again")
	}
	return status.CredentialGeneration, nil
}

func playwrightScriptFailure(provider Provider, raw json.RawMessage) error {
	var output struct {
		SchemaVersion int    `json:"schema_version"`
		Status        string `json:"status"`
		Provider      string `json:"provider"`
		Code          string `json:"code"`
	}
	if err := decodeStrictJSON(raw, &output); err != nil || output.SchemaVersion != 1 ||
		output.Status != "error" || output.Provider != provider.ID ||
		!scriptCodePattern.MatchString(output.Code) {
		return codedError(CodeScriptInvalidOutput, "Email provider script returned an invalid error")
	}
	code := normalizeScriptErrorCode(output.Code)
	return codedError(code, publicScriptErrorMessage(code))
}

func mapPlaywrightError(err error, send bool) error {
	if err == nil {
		return nil
	}
	switch browsercontrol.ErrorCode(err) {
	case browsercontrol.CodeInvalidRequest:
		return codedError(CodeInvalidInput, "Email request is invalid")
	case browsercontrol.CodeNotConfigured:
		return codedError(CodeNotConfigured, "Browser control is not configured")
	case browsercontrol.CodeCredentialStale:
		return codedError(CodeAdmissionStale, "Email login admission is stale; check login status again")
	case browsercontrol.CodeScriptTimeout:
		return codedError(CodeScriptTimeout, "Email provider script timed out")
	case browsercontrol.CodePageStale:
		return codedError(CodePageContractChanged, "Email provider page contract changed")
	case browsercontrol.CodeControllerUnavailable:
		if send {
			return codedError(CodeSendOutcomeUnknown, "Email send outcome is unknown and must not be retried")
		}
		return codedError(CodeProviderUnavailable, "Email provider is unavailable")
	default:
		return codedError(CodeProviderUnavailable, "Email provider is unavailable")
	}
}
