package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

var mailLocatorPattern = regexp.MustCompile(`^[A-Za-z0-9_+=:./~-]+$`)
var mailAddressPattern = regexp.MustCompile(`^[^\x00-\x20\x7f@<>]+@[^\x00-\x20\x7f@<>]+\.[^\x00-\x20\x7f@<>]+$`)
var qqMailFolderPattern = regexp.MustCompile(`^qq:[1-9][0-9]{3,9}$`)
var intakeContinuationPattern = regexp.MustCompile(`^(?:[a-f0-9]{64}:[1-9][0-9]{0,3}|(?:q1|n1):[A-Za-z0-9_-]{1,1000})$`)

func validMailTarget(target app.EmailCaptureTarget) bool {
	return validRecoveryCapture(target) && len(target.AccountAddress) <= 320 && utf8.ValidString(target.AccountAddress) && mailAddressPattern.MatchString(target.AccountAddress) &&
		len(target.ProviderMessageID) <= 1024 && len(target.ProviderSelectionID) <= 1024 && mailLocatorPattern.MatchString(target.ProviderMessageID) && mailLocatorPattern.MatchString(target.ProviderSelectionID) &&
		(target.ProviderThreadID == "" || len(target.ProviderThreadID) <= 1024 && mailLocatorPattern.MatchString(target.ProviderThreadID)) &&
		(target.ProviderNativeID == "" || len(target.ProviderNativeID) <= 1024 && mailLocatorPattern.MatchString(target.ProviderNativeID)) &&
		(target.Folder == "" || target.Folder == "inbox" || target.Folder == "sent" || target.Folder == "all" || qqMailFolderPattern.MatchString(target.Folder) || strings.HasPrefix(target.Folder, "outlook:") && len(target.Folder) <= 1032 && mailLocatorPattern.MatchString(strings.TrimPrefix(target.Folder, "outlook:")))
}

func validRecoveryCapture(target app.EmailCaptureTarget) bool {
	v := target.RecoveryCapture
	if v == nil {
		return true
	}
	if v.PurgedAt != nil || v.PurgeReason != "" || len(v.ManifestJSON) == 0 || len(v.ManifestJSON) > 64<<10 || !utf8.ValidString(v.ManifestJSON) {
		return false
	}
	digest := sha256.Sum256([]byte(v.ManifestJSON))
	if v.ManifestSHA256 != "sha256:"+hex.EncodeToString(digest[:]) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(v.OriginalSHA256) || !regexp.MustCompile(`^cap_[a-f0-9]{32}$`).MatchString(v.ID) {
		return false
	}
	var m struct {
		SchemaVersion     int    `json:"schema_version"`
		Stage             string `json:"stage"`
		AccountAddress    string `json:"account_address"`
		ProviderMessageID string `json:"provider_message_id"`
		CaptureID         string `json:"capture_id"`
		Files             []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if json.Unmarshal([]byte(v.ManifestJSON), &m) != nil || m.SchemaVersion != 1 || m.Stage != "script_capture" || !strings.EqualFold(m.AccountAddress, target.AccountAddress) || m.ProviderMessageID != target.ProviderMessageID || m.CaptureID != v.ID || len(m.Files) != 1 || m.Files[0].Path != v.OriginalPath || m.Files[0].SHA256 != v.OriginalSHA256 {
		return false
	}
	return strings.HasSuffix(v.ManifestPath, "/"+v.ID+"/capture.json") && v.OriginalPath == strings.TrimSuffix(v.ManifestPath, "capture.json")+"message.eml" && !strings.Contains(v.ManifestPath, "..") && !strings.HasPrefix(v.ManifestPath, "/")
}

// DiscoverForOwner returns bounded discovery evidence without opening mail.
// The caller must persist candidates before scheduling any capture.
func (c *Controller) DiscoverForOwner(ctx context.Context, ownerID string, request ReadRequest) (app.EmailDiscoveryResult, error) {
	provider, ok := c.registry.Get(request.Provider)
	if !ok || strings.TrimSpace(ownerID) == "" || request.Target != nil {
		return app.EmailDiscoveryResult{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid discovery binding")
	}
	release, lockErr := c.lockProvider(ctx, provider.ID)
	if lockErr != nil {
		return app.EmailDiscoveryResult{}, lockErr
	}
	defer release()
	setting, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		return app.EmailDiscoveryResult{}, err
	}
	if !exists || !setting.Enabled || setting.State != app.EmailStateReady || setting.Version != request.SettingVersion || setting.Account != request.Account {
		return app.EmailDiscoveryResult{}, codedError(app.ToolErrorEmailAdmissionStale, "Email provider configuration changed")
	}
	if c.runner == nil {
		return app.EmailDiscoveryResult{}, codedError(app.ToolErrorEmailProviderUnavailable, "Email scripts unavailable")
	}
	digest := sha256.Sum256([]byte(ownerID))
	request.OwnerScope = hex.EncodeToString(digest[:])
	return c.runner.Discover(ctx, provider, request)
}

func (r *PlaywrightRunner) Discover(ctx context.Context, provider Provider, request ReadRequest) (app.EmailDiscoveryResult, error) {
	invalid := func() (app.EmailDiscoveryResult, error) {
		return app.EmailDiscoveryResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Invalid email discovery result")
	}
	if request.Provider != provider.ID || request.Account != app.EmailAccountDefault || request.Target != nil || request.BrowserCredentialGeneration == 0 ||
		request.ProbeRevision != provider.Probe.Revision || request.ScriptRevision != provider.Discover.Revision || !scopeDigestPattern.MatchString(request.OwnerScope) || !invocationIDPattern.MatchString(request.InvocationID) {
		return app.EmailDiscoveryResult{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid discovery binding")
	}
	if d := request.Discovery; d != nil {
		if d.Lane != "recent_inbound" || !validMailTarget(app.EmailCaptureTarget{AccountAddress: d.AccountAddress, ProviderMessageID: "check", ProviderSelectionID: "check"}) ||
			d.IntervalStart.IsZero() || !d.IntervalStart.Before(d.IntervalEnd) || !validIntakeLimit(d.Limit) || !validContinuation(d.Continuation) {
			return app.EmailDiscoveryResult{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid email discovery interval")
		}
	}
	generation, err := r.credentialGeneration(ctx, request.BrowserCredentialGeneration)
	if err != nil {
		return app.EmailDiscoveryResult{}, err
	}
	ctx, cancel := scriptContext(ctx, provider.Discover)
	defer cancel()
	input := map[string]any{"schema_version": 1, "operation": "discover", "invocation_id": request.InvocationID, "provider": provider.ID, "account": request.Account, "owner_scope": request.OwnerScope}
	if request.Discovery != nil {
		input["discovery"] = request.Discovery
	}
	result, err := r.controller.RunScript(ctx, browsercontrol.RunScriptRequest{TaskID: request.InvocationID, CredentialGeneration: generation, Provider: provider.ID, Operation: "discover", ScriptID: provider.Discover.ID, Revision: provider.Discover.Revision, Input: input})
	if err != nil {
		return app.EmailDiscoveryResult{}, mapPlaywrightError(err, false)
	}
	if result.State == "failed" {
		return app.EmailDiscoveryResult{}, playwrightScriptFailure(provider, result.Result)
	}
	if result.State != "completed" || result.CredentialGeneration != generation {
		return invalid()
	}
	return decodeDiscoveryResult(result.Result, provider, request, maxScriptOutputBytes)
}

func decodeDiscoveryResult(raw []byte, provider Provider, request ReadRequest, maxBytes int) (app.EmailDiscoveryResult, error) {
	// "Invalid discovery result" with no reason is unactionable in production:
	// an operator sees a mailbox that never advances and has nothing to go on.
	// The reason names the failed check only — never any message content.
	invalid := func(reason string) (app.EmailDiscoveryResult, error) {
		slog.Warn("email discovery result rejected", "provider", provider.ID, "reason", reason, "bytes", len(raw))
		return app.EmailDiscoveryResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Invalid email discovery result: "+reason)
	}
	var output app.EmailDiscoveryResult
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 7 && (request.Discovery == nil || len(fields) != 8 || len(fields["threads"]) == 0) {
		return invalid("missing_top_field")
	}
	for _, key := range []string{"schema_version", "provider", "status", "account_address", "candidates", "coverage", "observed_at"} {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			return invalid("coverage_shape")
		}
	}
	var coverageFields map[string]json.RawMessage
	if json.Unmarshal(fields["coverage"], &coverageFields) != nil || len(coverageFields) < 5 {
		return invalid("missing_coverage_field")
	}
	for _, key := range []string{"scope", "scan_complete", "scanned_rows", "unsupported_rows", "limited"} {
		if len(coverageFields[key]) == 0 || string(coverageFields[key]) == "null" {
			return invalid("output_decode")
		}
	}
	if !utf8.Valid(raw) || decodeStrictJSONLimit(raw, &output, maxBytes) != nil ||
		output.SchemaVersion != 1 || output.Provider != provider.ID || output.ObservedAt.IsZero() || output.Candidates == nil || len(output.Candidates) > 100 ||
		!validMailTarget(app.EmailCaptureTarget{AccountAddress: output.AccountAddress, ProviderMessageID: "check", ProviderSelectionID: "check"}) {
		return invalid("coverage_invalid")
	}
	coverage := output.Coverage
	if !validCoverage(coverage, len(output.Candidates)) {
		return invalid("bootstrap_scope")
	}
	if request.Discovery == nil {
		if coverage.Scope != "account" || coverage.Lane != "" || len(coverageFields) != 5 {
			return invalid("interval_mismatch")
		}
	} else {
		d := request.Discovery
		if !strings.EqualFold(d.AccountAddress, output.AccountAddress) || coverage.Lane != d.Lane || len(output.Candidates) > d.Limit {
			return invalid("scope_not_inbound_received")
		}
		if coverage.Scope != "inbound_received" {
			return invalid("check_9")
		}
		if d.Lane == "recent_inbound" && coverage.ScanComplete && !coverage.BoundaryQualified {
			return invalid("check_10")
		}
	}
	switch output.Status {
	case "empty":
		if len(output.Candidates) != 0 || !coverage.ScanComplete || coverage.UnsupportedRows != 0 {
			return invalid("check_11")
		}
	case "listed":
		if !coverage.Limited && (request.Discovery == nil || request.Discovery.Lane != "recent_inbound" || !coverage.BoundaryQualified || coverage.UnsupportedRows != 0 || len(output.Candidates) == 0) {
			return invalid("check_12")
		}
	case "partial":
		if request.Discovery == nil {
			if coverage.Scope != "account" || !coverage.Limited {
				return invalid("check_13")
			}
		} else if !coverage.Limited || coverage.Reason == "" {
			return invalid("check_14")
		}
	default:
		return invalid("check_15")
	}
	seen := map[string]bool{}
	if request.Discovery == nil && len(output.Threads) > 0 || request.Discovery != nil && len(output.Threads) > request.Discovery.Limit {
		return invalid("check_16")
	}
	for _, thread := range output.Threads {
		if !validThreadTarget(thread) || !strings.EqualFold(thread.AccountAddress, output.AccountAddress) || seen[thread.ProviderThreadID] {
			return invalid("check_17")
		}
		seen[thread.ProviderThreadID] = true
	}
	seen = map[string]bool{}
	for _, target := range output.Candidates {
		if !validMailTarget(target) || !strings.EqualFold(target.AccountAddress, output.AccountAddress) || seen[target.ProviderMessageID] {
			return invalid("check_18")
		}
		seen[target.ProviderMessageID] = true
	}
	return output, nil
}
