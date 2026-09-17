package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

var scopeDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var captureDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var mailboxIDPattern = regexp.MustCompile(`^mb_[a-f0-9]{32}$`)
var mailIDPattern = regexp.MustCompile(`^mail_[a-f0-9]{32}$`)
var captureIDPattern = regexp.MustCompile(`^cap_[a-f0-9]{32}$`)

// CaptureForOwner is the background intake entry and never selects first unread.
func (c *Controller) CaptureForOwner(ctx context.Context, ownerID string, request ReadRequest) (ReadResult, error) {
	if request.Target == nil || !validMailTarget(*request.Target) {
		return ReadResult{}, codedError(app.ToolErrorEmailInvalidInput, "Specified email target required")
	}
	return c.ReadForOwner(ctx, ownerID, request)
}

func (c *Controller) ReadForOwner(ctx context.Context, ownerID string, request ReadRequest) (ReadResult, error) {
	provider, ok := c.registry.Get(request.Provider)
	if !ok || strings.TrimSpace(ownerID) == "" {
		return ReadResult{}, codedError(app.ToolErrorEmailInvalidInput, "Email provider or owner is invalid")
	}
	release, lockErr := c.lockProvider(ctx, provider.ID)
	if lockErr != nil {
		return ReadResult{}, lockErr
	}
	defer release()
	setting, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		return ReadResult{}, err
	}
	if !exists || !setting.Enabled || setting.State != app.EmailStateReady || setting.Version != request.SettingVersion || setting.Account != request.Account {
		return ReadResult{}, codedError(app.ToolErrorEmailAdmissionStale, "Email provider configuration changed after login admission")
	}
	if c.runner == nil {
		return ReadResult{}, codedError(app.ToolErrorEmailProviderUnavailable, "Email provider scripts are unavailable")
	}
	digest := sha256.Sum256([]byte(ownerID))
	request.OwnerScope = hex.EncodeToString(digest[:])
	result, err := c.runner.Read(ctx, provider, request)
	if err != nil {
		return ReadResult{}, err
	}
	if result.Status == "empty" && result.Capture == nil {
		if request.Target != nil {
			return ReadResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Specified email cannot return empty")
		}
		return result, nil
	}
	if err := verifyCapture(ctx, c.captureWorkspaceRoot, request, result); err != nil {
		return ReadResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Email capture files could not be verified")
	}
	return result, nil
}

func (r *PlaywrightRunner) Read(ctx context.Context, provider Provider, request ReadRequest) (ReadResult, error) {
	script, operation := provider.Read, "read"
	if request.Target != nil {
		script, operation = provider.Capture, "capture"
		if !validMailTarget(*request.Target) {
			return ReadResult{}, codedError(app.ToolErrorEmailInvalidInput, "Email target is invalid")
		}
	}
	if request.Provider != provider.ID || request.Account != app.EmailAccountDefault || request.BrowserCredentialGeneration == 0 ||
		request.ProbeRevision != provider.Probe.Revision || request.ScriptRevision != script.Revision ||
		!invocationIDPattern.MatchString(request.InvocationID) || !scopeDigestPattern.MatchString(request.OwnerScope) {
		return ReadResult{}, codedError(app.ToolErrorEmailInvalidInput, "Email capture binding is invalid")
	}
	generation, err := r.credentialGeneration(ctx, request.BrowserCredentialGeneration)
	if err != nil {
		return ReadResult{}, err
	}
	input := struct {
		SchemaVersion int                     `json:"schema_version"`
		Operation     string                  `json:"operation"`
		InvocationID  string                  `json:"invocation_id"`
		Provider      string                  `json:"provider"`
		Account       string                  `json:"account"`
		OwnerScope    string                  `json:"owner_scope"`
		Target        *app.EmailCaptureTarget `json:"target,omitempty"`
	}{1, operation, request.InvocationID, provider.ID, app.EmailAccountDefault, request.OwnerScope, request.Target}
	ctx, cancel := scriptContext(ctx, script)
	defer cancel()
	result, err := r.controller.RunScript(ctx, browsercontrol.RunScriptRequest{
		TaskID: request.InvocationID, CredentialGeneration: generation, Provider: provider.ID,
		Operation: operation, ScriptID: script.ID, Revision: script.Revision, Input: input,
	})
	if err != nil {
		return ReadResult{}, mapPlaywrightError(err, false)
	}
	if result.State == "failed" {
		return ReadResult{}, playwrightScriptFailure(provider, result.Result)
	}
	if result.State != "completed" || result.CredentialGeneration != generation {
		return ReadResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Email capture script returned an invalid receipt")
	}
	return decodeReadReceipt(result.Result, provider, request, generation, script)
}

func decodeReadReceipt(raw []byte, provider Provider, request ReadRequest, generation int64, script Script) (ReadResult, error) {
	var output struct {
		SchemaVersion int             `json:"schema_version"`
		Status        string          `json:"status"`
		Provider      string          `json:"provider"`
		Capture       json.RawMessage `json:"capture"`
	}
	invalid := func() (ReadResult, error) {
		return ReadResult{}, codedError(app.ToolErrorEmailScriptInvalidOutput, "Email capture script returned an invalid receipt")
	}
	if err := decodeStrictJSON(raw, &output); err != nil || !utf8.Valid(raw) || output.SchemaVersion != 1 || output.Provider != provider.ID || len(output.Capture) == 0 {
		return invalid()
	}
	var capture *app.EmailCaptureReceipt
	switch output.Status {
	case "empty":
		if string(output.Capture) != "null" || request.Target != nil {
			return invalid()
		}
	case "collected", "partial":
		var raw struct {
			ManifestPath     *string `json:"manifest_path"`
			ManifestSHA256   *string `json:"manifest_sha256"`
			MailID           *string `json:"mail_id"`
			MailboxID        *string `json:"mailbox_id"`
			CaptureID        *string `json:"capture_id"`
			AttachmentsCount *int    `json:"attachments_count"`
			ReadState        *string `json:"read_state"`
		}
		if err := decodeStrictJSON(output.Capture, &raw); err != nil || raw.ManifestPath == nil || raw.ManifestSHA256 == nil || raw.MailID == nil || raw.MailboxID == nil || raw.CaptureID == nil || raw.AttachmentsCount == nil || raw.ReadState == nil {
			return invalid()
		}
		if !validManifestPath(*raw.ManifestPath) || !captureDigestPattern.MatchString(*raw.ManifestSHA256) || !mailIDPattern.MatchString(*raw.MailID) || !mailboxIDPattern.MatchString(*raw.MailboxID) || !captureIDPattern.MatchString(*raw.CaptureID) || *raw.AttachmentsCount < 0 || *raw.AttachmentsCount > 20 {
			return invalid()
		}
		// The date directory is derived from the captured bytes, so it is parsed
		// out rather than reconstructed here; every other segment still has to
		// equal a value this request already holds.
		if _, ok := captureManifestDate(*raw.ManifestPath, request.OwnerScope, *raw.MailboxID, *raw.MailID, *raw.CaptureID); !ok {
			return invalid()
		}
		if *raw.ReadState != "read" && *raw.ReadState != "unread" && *raw.ReadState != "unknown" {
			return invalid()
		}
		capture = &app.EmailCaptureReceipt{ManifestPath: *raw.ManifestPath, ManifestSHA256: *raw.ManifestSHA256, MailID: *raw.MailID, MailboxID: *raw.MailboxID, CaptureID: *raw.CaptureID, AttachmentsCount: *raw.AttachmentsCount, ReadState: *raw.ReadState}
	default:
		return invalid()
	}
	return ReadResult{Provider: provider.ID, Status: output.Status, Capture: capture, BrowserCredentialGeneration: uint64(generation), ScriptRevision: script.Revision}, nil
}

func validManifestPath(value string) bool {
	return value != "" && len(value) <= 1024 && utf8.ValidString(value) && !strings.ContainsAny(value, "\\\x00\r\n:") && !path.IsAbs(value) && path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../") && path.Base(value) == "capture.json"
}
