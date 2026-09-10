package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

// AdmitIntake reuses a recent intake-only login proof while its setting and
// credential binding are unchanged. Discovery still checks the actual mailbox
// account on every call. This never rewrites human send-approval settings.
func (c *Controller) AdmitIntake(ctx context.Context, ownerID, providerID string) (AdmissionResult, error) {
	provider, ok := c.registry.Get(providerID)
	if !ok || strings.TrimSpace(ownerID) == "" {
		return AdmissionResult{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid email intake provider")
	}
	release, lockErr := c.lockProvider(ctx, provider.ID)
	if lockErr != nil {
		return AdmissionResult{}, lockErr
	}
	defer release()
	key := intakeProbeKey{ownerID: ownerID, providerID: provider.ID}
	setting, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		c.forgetIntakeProbe(key)
		return AdmissionResult{}, err
	}
	// A scheduled intake tick can detect reauthentication without requiring
	// another settings toggle. Never reuse the expired admission proof.
	if exists && setting.Enabled && setting.State == app.EmailStateLoginRequired {
		c.forgetIntakeProbe(key)
		result, probeErr := c.probe(ctx, provider, app.NewID("email_intake_recovery"))
		if probeErr != nil {
			// Keep the confirmed login alert through transient recovery failures.
			return AdmissionResult{}, codedError(app.ToolErrorEmailLoginRequired, "Email login has not been verified again")
		}
		setting, err = c.persistProbe(ctx, setting, "email_intake_recovery", result, nil)
		if err != nil {
			return AdmissionResult{}, err
		}
		c.rememberIntakeProbe(key, setting, result)
	}
	if !exists || !setting.Enabled || setting.State != app.EmailStateReady {
		c.forgetIntakeProbe(key)
		return AdmissionResult{}, codedError(app.ToolErrorEmailNotConfigured, "Email provider is not ready for intake")
	}
	proof, cached, err := c.intakeProbe(ctx, key, provider, setting)
	if err != nil {
		return AdmissionResult{}, err
	}
	current, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		c.forgetIntakeProbe(key)
		return AdmissionResult{}, err
	}
	if !exists || !current.Enabled || current.State != app.EmailStateReady || current.Version != setting.Version || current.Account != setting.Account {
		c.forgetIntakeProbe(key)
		return AdmissionResult{}, codedError(app.ToolErrorEmailAdmissionStale, "Email provider configuration changed during intake admission")
	}
	if !cached {
		c.rememberIntakeProbe(key, setting, proof)
	}
	return AdmissionResult{Provider: provider.ID, Account: setting.Account, AccountHint: proof.AccountHint, SettingVersion: setting.Version, BrowserCredentialGeneration: proof.Generation,
		ProbeRevision: provider.Probe.Revision, SendScriptRevision: provider.Send.Revision, ReadScriptRevision: provider.Read.Revision, ValidatedAt: proof.CheckedAt}, nil
}

func (c *Controller) intakeBinding(ctx context.Context, ownerID string, binding *ReadRequest) (Provider, error) {
	provider, ok := c.registry.Get(binding.Provider)
	if !ok || strings.TrimSpace(ownerID) == "" || c.runner == nil {
		return Provider{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid email intake binding")
	}
	setting, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		return Provider{}, err
	}
	if !exists || !setting.Enabled || setting.State != app.EmailStateReady || setting.Version != binding.SettingVersion || setting.Account != binding.Account {
		return Provider{}, codedError(app.ToolErrorEmailAdmissionStale, "Email provider configuration changed")
	}
	digest := sha256.Sum256([]byte(ownerID))
	binding.OwnerScope = hex.EncodeToString(digest[:])
	return provider, nil
}

func validThreadTarget(target app.EmailThreadTarget) bool {
	return validMailTarget(app.EmailCaptureTarget{AccountAddress: target.AccountAddress, ProviderMessageID: target.ProviderThreadID, ProviderSelectionID: target.ProviderSelectionID, Folder: target.Folder}) && target.Folder != ""
}

func (c *Controller) EnumerateThreadForOwner(ctx context.Context, ownerID string, request app.EmailThreadRequest) (app.EmailThreadResult, error) {
	release, lockErr := c.lockProvider(ctx, request.Binding.Provider)
	if lockErr != nil {
		return app.EmailThreadResult{}, lockErr
	}
	defer release()
	provider, err := c.intakeBinding(ctx, ownerID, &request.Binding)
	if err != nil {
		return app.EmailThreadResult{}, err
	}
	return c.runner.EnumerateThread(ctx, provider, request)
}

func (c *Controller) MarkReadForOwner(ctx context.Context, ownerID string, request app.EmailMarkReadRequest) (app.EmailMarkReadResult, error) {
	release, lockErr := c.lockProvider(ctx, request.Binding.Provider)
	if lockErr != nil {
		return app.EmailMarkReadResult{}, lockErr
	}
	defer release()
	provider, err := c.intakeBinding(ctx, ownerID, &request.Binding)
	if err != nil {
		return app.EmailMarkReadResult{}, err
	}
	if request.Binding.Target == nil || !validMailTarget(*request.Binding.Target) || !invocationIDPattern.MatchString(request.CaptureInvocationID) {
		return app.EmailMarkReadResult{}, codedError(app.ToolErrorEmailInvalidInput, "Published capture and target required")
	}
	captureRequest := request.Binding
	captureRequest.InvocationID = request.CaptureInvocationID
	if err := verifyCapture(ctx, c.captureWorkspaceRoot, captureRequest, ReadResult{Provider: provider.ID, Status: "collected", Capture: &request.CommittedCapture}); err != nil {
		return app.EmailMarkReadResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Published email capture cannot be verified")
	}
	return c.runner.MarkRead(ctx, provider, request)
}

func (r *PlaywrightRunner) runIntakeScript(ctx context.Context, provider Provider, binding ReadRequest, script Script, operation string, input map[string]any) (json.RawMessage, error) {
	if binding.Provider != provider.ID || binding.Account != app.EmailAccountDefault || binding.BrowserCredentialGeneration == 0 || binding.ProbeRevision != provider.Probe.Revision || binding.ScriptRevision != script.Revision ||
		!scopeDigestPattern.MatchString(binding.OwnerScope) || !invocationIDPattern.MatchString(binding.InvocationID) {
		return nil, codedError(app.ToolErrorEmailInvalidInput, "Invalid email intake binding")
	}
	generation, err := r.credentialGeneration(ctx, binding.BrowserCredentialGeneration)
	if err != nil {
		return nil, err
	}
	input["schema_version"] = 1
	input["operation"] = operation
	input["invocation_id"] = binding.InvocationID
	input["provider"] = provider.ID
	input["account"] = binding.Account
	input["owner_scope"] = binding.OwnerScope
	ctx, cancel := scriptContext(ctx, script)
	defer cancel()
	result, err := r.controller.RunScript(ctx, browsercontrol.RunScriptRequest{TaskID: binding.InvocationID, CredentialGeneration: generation, Provider: provider.ID, Operation: operation, ScriptID: script.ID, Revision: script.Revision, Input: input})
	if err != nil {
		return nil, mapPlaywrightError(err, false)
	}
	if result.State == "failed" {
		return nil, playwrightScriptFailure(provider, result.Result)
	}
	if result.State != "completed" || result.CredentialGeneration != generation || !utf8.Valid(result.Result) {
		return nil, codedError(app.ToolErrorEmailScriptInvalidOutput, "Invalid email intake result")
	}
	return result.Result, nil
}

func (r *PlaywrightRunner) EnumerateThread(ctx context.Context, provider Provider, request app.EmailThreadRequest) (app.EmailThreadResult, error) {
	invalid := func() (app.EmailThreadResult, error) {
		return app.EmailThreadResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Invalid email thread inventory")
	}
	if !validThreadTarget(request.Thread) || request.Binding.Target != nil || request.Binding.Discovery != nil || !validIntakeLimit(request.Limit) || !validContinuation(request.Continuation) {
		return app.EmailThreadResult{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid email thread request")
	}
	raw, err := r.runIntakeScript(ctx, provider, request.Binding, provider.EnumerateThread, "enumerate_thread", map[string]any{"thread": request.Thread, "continuation": request.Continuation, "limit": request.Limit})
	if err != nil {
		return app.EmailThreadResult{}, err
	}
	var output app.EmailThreadResult
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || !hasRequiredJSONFields(fields, "schema_version", "provider", "status", "thread", "members", "coverage", "observed_at") {
		return invalid()
	}
	var memberFields []map[string]json.RawMessage
	if json.Unmarshal(fields["members"], &memberFields) != nil {
		return invalid()
	}
	for _, member := range memberFields {
		if !hasRequiredJSONFields(member, "target", "direction", "draft", "read_state") {
			return invalid()
		}
	}
	if decodeStrictJSON(raw, &output) != nil || output.SchemaVersion != 1 || output.Provider != provider.ID || output.Thread != request.Thread || output.ObservedAt.IsZero() || output.Members == nil || len(output.Members) > request.Limit || !validCoverage(output.Coverage, len(output.Members)) || output.Coverage.Scope != "thread" {
		return invalid()
	}
	if output.Status != "partial" && output.Status != "complete_for_observation" || (output.Status == "complete_for_observation") != output.Coverage.ScanComplete {
		return invalid()
	}
	seen := map[string]bool{}
	for _, member := range output.Members {
		if !validMailTarget(member.Target) || !strings.EqualFold(member.Target.AccountAddress, request.Thread.AccountAddress) || member.Target.ProviderThreadID != request.Thread.ProviderThreadID || member.Target.ProviderSelectionID != request.Thread.ProviderSelectionID ||
			seen[member.Target.ProviderMessageID] || member.Direction != "inbound" && member.Direction != "outbound" && member.Direction != "unknown" || !validReadState(member.ReadState) {
			return invalid()
		}
		seen[member.Target.ProviderMessageID] = true
	}
	return output, nil
}

func hasRequiredJSONFields(fields map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			return false
		}
	}
	return true
}

func (r *PlaywrightRunner) MarkRead(ctx context.Context, provider Provider, request app.EmailMarkReadRequest) (app.EmailMarkReadResult, error) {
	if request.Binding.Target == nil || !validMailTarget(*request.Binding.Target) || request.Binding.Discovery != nil {
		return app.EmailMarkReadResult{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid email read target")
	}
	raw, err := r.runIntakeScript(ctx, provider, request.Binding, provider.MarkRead, "mark_read", map[string]any{"target": request.Binding.Target, "committed_capture": request.CommittedCapture})
	if err != nil {
		return app.EmailMarkReadResult{}, err
	}
	var output app.EmailMarkReadResult
	if decodeStrictJSON(raw, &output) != nil || output.SchemaVersion != 1 || output.Provider != provider.ID || output.Target != *request.Binding.Target || output.ObservedAt.IsZero() || !validReadState(output.ReadState) {
		return app.EmailMarkReadResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Invalid email read confirmation")
	}
	return output, nil
}

func validReadState(value string) bool {
	return value == "read" || value == "unread" || value == "unknown"
}
func validIntakeLimit(value int) bool { return value >= 1 && value <= 100 }
func validContinuation(value string) bool {
	return value == "" || intakeContinuationPattern.MatchString(value)
}
func validCoverage(coverage app.EmailDiscoveryCoverage, candidates int) bool {
	return coverage.ScannedRows >= 0 && coverage.UnsupportedRows >= 0 && coverage.UnsupportedRows <= coverage.ScannedRows && candidates <= coverage.ScannedRows-coverage.UnsupportedRows && coverage.ScanComplete != coverage.Limited && validContinuation(coverage.Continuation) &&
		(coverage.Reason == "" || scriptCodePattern.MatchString(coverage.Reason)) && (coverage.Ordering == "" || scriptCodePattern.MatchString(coverage.Ordering)) && (coverage.OldestObservedAt == nil || !coverage.OldestObservedAt.IsZero()) && (!coverage.ScanComplete || coverage.Continuation == "")
}
