package toolhub

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type emailToolError interface {
	ToolErrorCode() app.ToolErrorCode
}

func (h *ToolHub) emailSend(ctx context.Context, args map[string]any, sessionID string) (Result, error) {
	if h.emailSender == nil {
		return Result{}, codedEmailToolError(app.ToolErrorEmailProviderUnavailable, errors.New("Email sending is unavailable"))
	}
	if h.store == nil || strings.TrimSpace(sessionID) == "" {
		return Result{}, codedEmailToolError(app.ToolErrorEmailInvalidInput, errors.New("Email sending requires an authenticated session"))
	}
	session, found, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	if !found || strings.TrimSpace(session.OwnerID) == "" {
		return Result{}, codedEmailToolError(app.ToolErrorEmailInvalidInput, errors.New("Email session owner is unavailable"))
	}

	settingVersion, err := positiveInt64StringArg(args, "setting_version")
	if err != nil {
		return Result{}, err
	}
	browserCredentialGeneration, err := positiveUint64StringArg(args, "browser_credential_generation")
	if err != nil {
		return Result{}, err
	}
	probeRevision, err := positiveIntStringArg(args, "probe_revision")
	if err != nil {
		return Result{}, err
	}
	scriptRevision, err := positiveIntStringArg(args, "send_script_revision")
	if err != nil {
		return Result{}, err
	}

	result, err := h.emailSender.SendForOwner(ctx, session.OwnerID, app.EmailSendRequest{
		Provider:                    stringArg(args, "provider", ""),
		Account:                     stringArg(args, "account", ""),
		Recipient:                   stringArg(args, "recipient", ""),
		Subject:                     stringArg(args, "subject", ""),
		Body:                        stringArg(args, "body", ""),
		InvocationID:                stringArg(args, "invocation_id", ""),
		BrowserCredentialGeneration: browserCredentialGeneration,
		ProbeRevision:               probeRevision,
		ScriptRevision:              scriptRevision,
		SettingVersion:              settingVersion,
	})
	if err != nil {
		var classified emailToolError
		if errors.As(err, &classified) && classified.ToolErrorCode() != "" {
			return Result{}, codedEmailToolError(classified.ToolErrorCode(), err)
		}
		return Result{}, codedEmailToolError(app.ToolErrorEmailProviderUnavailable, err)
	}
	return Result{Output: result}, nil
}

func positiveInt64StringArg(args map[string]any, key string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(stringArg(args, key, "")), 10, 64)
	if err != nil || value <= 0 {
		return 0, codedEmailToolError(app.ToolErrorEmailInvalidInput, errors.New("Email Runtime binding is invalid"))
	}
	return value, nil
}

func positiveUint64StringArg(args map[string]any, key string) (uint64, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(stringArg(args, key, "")), 10, 64)
	if err != nil || value == 0 {
		return 0, codedEmailToolError(app.ToolErrorEmailInvalidInput, errors.New("Email Runtime binding is invalid"))
	}
	return value, nil
}

func positiveIntStringArg(args map[string]any, key string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(stringArg(args, key, "")))
	if err != nil || value <= 0 {
		return 0, codedEmailToolError(app.ToolErrorEmailInvalidInput, errors.New("Email Runtime binding is invalid"))
	}
	return value, nil
}

func codedEmailToolError(code app.ToolErrorCode, err error) error {
	return &app.CodedToolError{Code: code, Err: err}
}
