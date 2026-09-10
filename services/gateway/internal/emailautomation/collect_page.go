package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const maxPageOutputBytes = 1 << 20

var pageIDPattern = regexp.MustCompile(`^page_[a-f0-9]{64}$`)

// PageCaptureInvocationID matches the Controller journal identity. Retries of
// an unacknowledged page and observations in other lanes recover the same source
// instead of exporting it again. Page checkpoints retain their full lane identity.
func PageCaptureInvocationID(batchInvocationID, provider string, target app.EmailCaptureTarget) string {
	for _, suffix := range []string{"_unread", "_recent_observation", "_recent_inbound"} {
		if strings.HasSuffix(batchInvocationID, suffix) {
			batchInvocationID = strings.TrimSuffix(batchInvocationID, suffix)
			break
		}
	}
	digest := sha256.Sum256([]byte(batchInvocationID + "\n" + provider + "\n" + strings.ToLower(target.AccountAddress) + "\n" + target.ProviderMessageID))
	return "email_capture_" + hex.EncodeToString(digest[:])
}

func validPageRequest(request ReadRequest) bool {
	d := request.Discovery
	return request.Target == nil && d != nil && (d.Lane == "unread" || d.Lane == "recent_inbound") &&
		validMailTarget(app.EmailCaptureTarget{AccountAddress: d.AccountAddress, ProviderMessageID: "check", ProviderSelectionID: "check"}) &&
		(d.Lane != "recent_inbound" || !d.IntervalStart.IsZero() && d.IntervalStart.Before(d.IntervalEnd)) &&
		d.Limit >= 1 && d.Limit <= 50 && validContinuation(d.Continuation) &&
		(request.AckPageID == "" || pageIDPattern.MatchString(request.AckPageID))
}

// CollectPageForOwner admits one provider operation for a whole page. Captured
// originals are reverified locally before the caller can publish or acknowledge.
func (c *Controller) CollectPageForOwner(ctx context.Context, ownerID string, request ReadRequest) (app.EmailPageResult, error) {
	if !validPageRequest(request) {
		return app.EmailPageResult{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid email page request")
	}
	release, err := c.lockProvider(ctx, request.Provider)
	if err != nil {
		return app.EmailPageResult{}, err
	}
	defer release()
	provider, err := c.intakeBinding(ctx, ownerID, &request)
	if err != nil {
		return app.EmailPageResult{}, err
	}
	output, err := c.runner.CollectPage(ctx, provider, request)
	if err != nil {
		return app.EmailPageResult{}, err
	}
	if !validPageResult(output, provider, request) {
		return app.EmailPageResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Invalid email page result")
	}
	for _, capture := range output.Captures {
		binding := request
		binding.Target = &capture.Target
		binding.Discovery = nil
		binding.AckPageID = ""
		binding.InvocationID = PageCaptureInvocationID(request.InvocationID, provider.ID, capture.Target)
		if err := verifyCapture(ctx, c.captureWorkspaceRoot, binding, capture.Result); err != nil {
			return app.EmailPageResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Email page capture files could not be verified")
		}
	}
	return output, nil
}

func (r *PlaywrightRunner) CollectPage(ctx context.Context, provider Provider, request ReadRequest) (app.EmailPageResult, error) {
	if !validPageRequest(request) {
		return app.EmailPageResult{}, codedError(app.ToolErrorEmailInvalidInput, "Invalid email page request")
	}
	input := map[string]any{"discovery": request.Discovery}
	if request.AckPageID != "" {
		input["ack_page_id"] = request.AckPageID
	}
	raw, err := r.runIntakeScript(ctx, provider, request, provider.CollectPage, "collect_page", input)
	if err != nil {
		return app.EmailPageResult{}, err
	}
	return decodePageResult(raw, provider, request)
}

func decodePageResult(raw []byte, provider Provider, request ReadRequest) (app.EmailPageResult, error) {
	invalid := func() (app.EmailPageResult, error) {
		return app.EmailPageResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Invalid email page result")
	}
	var wire struct {
		SchemaVersion    int                       `json:"schema_version"`
		Provider         string                    `json:"provider"`
		Status           string                    `json:"status"`
		AccountAddress   string                    `json:"account_address"`
		PageID           string                    `json:"page_id"`
		Discovery        json.RawMessage           `json:"discovery"`
		DiscoveryOptions app.EmailDiscoveryOptions `json:"discovery_options"`
		Captures         []struct {
			Target app.EmailCaptureTarget `json:"target"`
			Result json.RawMessage        `json:"result"`
		} `json:"captures"`
		Failures   []app.EmailPageFailure `json:"failures"`
		ObservedAt time.Time              `json:"observed_at"`
	}
	if decodeStrictJSONLimit(raw, &wire, maxPageOutputBytes) != nil || wire.Captures == nil || wire.Failures == nil || len(wire.Captures) > 50 || len(wire.Failures) > 50 {
		return invalid()
	}
	checkpointRequest := request
	checkpointRequest.Discovery = &wire.DiscoveryOptions
	if !validPageRequest(checkpointRequest) || wire.DiscoveryOptions.Lane != request.Discovery.Lane || !strings.EqualFold(wire.DiscoveryOptions.AccountAddress, request.Discovery.AccountAddress) {
		return invalid()
	}
	discovery, err := decodeDiscoveryResult(wire.Discovery, provider, checkpointRequest, maxPageOutputBytes)
	if err != nil {
		return invalid()
	}
	output := app.EmailPageResult{SchemaVersion: wire.SchemaVersion, Provider: wire.Provider, Status: wire.Status, AccountAddress: wire.AccountAddress, PageID: wire.PageID, Discovery: discovery, DiscoveryOptions: wire.DiscoveryOptions, Captures: make([]app.EmailPageCapture, 0, len(wire.Captures)), Failures: wire.Failures, ObservedAt: wire.ObservedAt}
	for _, capture := range wire.Captures {
		binding := request
		binding.Target = &capture.Target
		binding.InvocationID = PageCaptureInvocationID(request.InvocationID, provider.ID, capture.Target)
		result, err := decodeReadReceipt(capture.Result, provider, binding, int64(request.BrowserCredentialGeneration), provider.CollectPage)
		if err != nil {
			return invalid()
		}
		output.Captures = append(output.Captures, app.EmailPageCapture{Target: capture.Target, Result: result})
	}
	if !validPageResult(output, provider, request) {
		return invalid()
	}
	return output, nil
}

func validPageResult(output app.EmailPageResult, provider Provider, request ReadRequest) bool {
	if !validPageRequest(request) || output.SchemaVersion != 1 || output.Provider != provider.ID || !pageIDPattern.MatchString(output.PageID) || output.ObservedAt.IsZero() ||
		!strings.EqualFold(output.AccountAddress, request.Discovery.AccountAddress) || !strings.EqualFold(output.Discovery.AccountAddress, output.AccountAddress) ||
		output.Discovery.Provider != provider.ID || output.Discovery.Coverage.Lane != request.Discovery.Lane || output.Captures == nil || output.Failures == nil ||
		len(output.Discovery.Candidates) > output.DiscoveryOptions.Limit || len(output.Captures)+len(output.Failures) != len(output.Discovery.Candidates) {
		return false
	}
	checkpointRequest := request
	checkpointRequest.Discovery = &output.DiscoveryOptions
	if !validPageRequest(checkpointRequest) || output.DiscoveryOptions.Lane != request.Discovery.Lane || !strings.EqualFold(output.DiscoveryOptions.AccountAddress, request.Discovery.AccountAddress) {
		return false
	}
	discoveryRaw, err := json.Marshal(output.Discovery)
	if err != nil {
		return false
	}
	if _, err := decodeDiscoveryResult(discoveryRaw, provider, checkpointRequest, maxPageOutputBytes); err != nil {
		return false
	}
	switch output.Status {
	case "empty":
		if len(output.Discovery.Candidates) != 0 || output.Discovery.Coverage.UnsupportedRows != 0 || !emptyPageObservation(output.Discovery) {
			return false
		}
	case "collected":
		if len(output.Captures) == 0 || len(output.Failures) != 0 {
			return false
		}
	case "partial":
	default:
		return false
	}
	candidates := make(map[string]app.EmailCaptureTarget, len(output.Discovery.Candidates))
	for _, target := range output.Discovery.Candidates {
		if _, duplicate := candidates[target.ProviderMessageID]; duplicate || !validMailTarget(target) || !strings.EqualFold(target.AccountAddress, output.AccountAddress) {
			return false
		}
		candidates[target.ProviderMessageID] = target
	}
	claim := func(target app.EmailCaptureTarget) bool {
		candidate, found := candidates[target.ProviderMessageID]
		if !found || candidate != target {
			return false
		}
		delete(candidates, target.ProviderMessageID)
		return true
	}
	for _, capture := range output.Captures {
		result := capture.Result
		if !claim(capture.Target) || result.Provider != provider.ID || result.Status != "collected" && result.Status != "partial" || result.Capture == nil ||
			result.BrowserCredentialGeneration != request.BrowserCredentialGeneration || result.ScriptRevision != provider.CollectPage.Revision ||
			!validReadState(result.Capture.ReadState) || result.Capture.AttachmentsCount < 0 || result.Capture.AttachmentsCount > 20 {
			return false
		}
	}
	for _, failure := range output.Failures {
		if !claim(failure.Target) || !scriptCodePattern.MatchString(failure.ErrorCode) {
			return false
		}
	}
	return len(candidates) == 0
}

// The page may contain no eligible mail even while the provider cannot prove
// complete mailbox coverage. Keep the discovery contract and its gaps intact.
func emptyPageObservation(discovery app.EmailDiscoveryResult) bool {
	if discovery.Status == "empty" {
		return true
	}
	if discovery.Status != "partial" {
		return false
	}
	switch discovery.Coverage.Reason {
	case "loaded_rows_only", "native_folder_scan_partial", "folder_scope_and_pagination_unqualified", "network_page_continues", "network_page_changed":
		return true
	default:
		return false
	}
}
