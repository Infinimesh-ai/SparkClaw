package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Only the existing policy-authorized manual tool path admits this optional
// predicate. Caller-supplied admission fields never authorize a Sent read.
func (r Runtime) admitManualSentRead(ctx context.Context, sessionID, runID string, args map[string]any) (map[string]any, error) {
	fail := func(code app.ToolErrorCode) (map[string]any, error) {
		return nil, &app.CodedToolError{Code: code, Err: errors.New("Sent original reading requires fresh verified admission")}
	}
	raw, err := json.Marshal(args["sent_match"])
	if err != nil {
		return fail(app.ToolErrorEmailInvalidInput)
	}
	match, err := app.DecodeEmailSentMatch(raw)
	if err != nil {
		return fail(app.ToolErrorEmailInvalidInput)
	}
	if args["provider"] != app.EmailProviderGmail || r.emailAdmission == nil || r.store == nil {
		return fail(app.ToolErrorEmailProviderUnavailable)
	}
	session, found, err := r.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !found || strings.TrimSpace(session.OwnerID) == "" {
		return fail(app.ToolErrorEmailInvalidInput)
	}
	// A constant provider request cannot be influenced by subject/recipient.
	binding, err := r.emailAdmission.Admit(ctx, session.OwnerID, "read Gmail")
	if err != nil {
		return nil, err
	}
	if binding.Provider != app.EmailProviderGmail || binding.Account != app.EmailAccountDefault || binding.SettingVersion <= 0 || binding.BrowserCredentialGeneration == 0 || binding.ProbeRevision <= 0 || binding.ReadScriptRevision <= 0 || binding.ValidatedAt.IsZero() {
		return fail(app.ToolErrorEmailAdmissionStale)
	}
	// Reconstruct only the registered arguments, preserving all five predicate
	// literals exactly. There is no second model step or capture task.
	matchRaw, _ := json.Marshal(match)
	var predicate map[string]any
	if err := json.Unmarshal(matchRaw, &predicate); err != nil {
		return nil, err
	}
	bound := map[string]any{
		"provider": binding.Provider, "account": binding.Account, "account_hint": binding.AccountHint,
		"setting_version": strconv.FormatInt(binding.SettingVersion, 10), "browser_credential_generation": strconv.FormatUint(binding.BrowserCredentialGeneration, 10),
		"probe_revision": strconv.Itoa(binding.ProbeRevision), "read_script_revision": strconv.Itoa(binding.ReadScriptRevision),
		"validated_at": binding.ValidatedAt.UTC().Format(time.RFC3339Nano), "invocation_id": app.NewID("email_read"), "sent_match": predicate,
	}
	r.addAudit(ctx, app.AuditEvent{SessionID: sessionID, RunID: runID, Actor: "email_admission", Type: "email.admission.completed", Summary: "Validated the Runtime-selected Gmail account for one bounded Sent original read", Fields: map[string]any{"provider": binding.Provider, "account": binding.Account, "setting_version": binding.SettingVersion, "browser_credential_generation": binding.BrowserCredentialGeneration, "script_revision": binding.ReadScriptRevision, "operation": "read"}})
	return bound, nil
}
