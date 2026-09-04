package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func routeTargetsCapability(route app.RouteDecision, capability app.CapabilityID) bool {
	return len(route.CapabilityPath) > 0 && route.CapabilityPath[len(route.CapabilityPath)-1] == capability
}

func (r Runtime) admitEmailRoute(ctx context.Context, sessionID, runID, ownerID, request string, route app.RouteDecision) (app.RouteDecision, error) {
	if route.Slots.Operation != app.RouteOperationSend {
		return app.RouteDecision{}, errors.New("Email reading is not enabled")
	}
	if r.emailAdmission == nil {
		return app.RouteDecision{}, errors.New("Browser email is not configured")
	}
	binding, err := r.emailAdmission.Admit(ctx, strings.TrimSpace(ownerID), request)
	if err != nil {
		return app.RouteDecision{}, err
	}
	if strings.TrimSpace(binding.Provider) == "" || strings.TrimSpace(binding.Account) == "" ||
		binding.SettingVersion <= 0 || binding.BrowserGeneration == 0 || binding.ProbeRevision <= 0 ||
		binding.SendScriptRevision <= 0 || binding.ValidatedAt.IsZero() {
		return app.RouteDecision{}, errors.New("Email login admission returned an incomplete binding")
	}

	facts := make(map[string]string, len(route.Facts)+9)
	for key, value := range route.Facts {
		facts[key] = value
	}
	accountHint := strings.TrimSpace(binding.AccountHint)
	if accountHint == "" {
		accountHint = strings.TrimSpace(binding.Account)
	}
	facts[app.EmailRouteFactProvider] = strings.TrimSpace(binding.Provider)
	facts[app.EmailRouteFactAccount] = strings.TrimSpace(binding.Account)
	facts[app.EmailRouteFactAccountHint] = accountHint
	facts[app.EmailRouteFactSettingVersion] = strconv.FormatInt(binding.SettingVersion, 10)
	facts[app.EmailRouteFactBrowserGeneration] = strconv.FormatUint(binding.BrowserGeneration, 10)
	facts[app.EmailRouteFactProbeRevision] = strconv.Itoa(binding.ProbeRevision)
	facts[app.EmailRouteFactSendScriptRevision] = strconv.Itoa(binding.SendScriptRevision)
	facts[app.EmailRouteFactValidatedAt] = binding.ValidatedAt.UTC().Format(time.RFC3339Nano)
	facts[app.EmailRouteFactInvocationID] = app.NewID("email_send")
	route.Facts = facts
	if err := r.capabilities.ValidateDecision(route); err != nil {
		return app.RouteDecision{}, err
	}
	r.addAudit(ctx, app.AuditEvent{
		SessionID: sessionID, RunID: runID, Actor: "email_admission", Type: "email.admission.completed", Summary: "Validated the Runtime-selected browser email account before Workflow creation",
		Fields: map[string]any{
			"provider": binding.Provider, "account": binding.Account, "account_hint": accountHint,
			"setting_version": binding.SettingVersion, "browser_generation": binding.BrowserGeneration,
			"probe_revision": binding.ProbeRevision, "send_script_revision": binding.SendScriptRevision,
		},
	})
	return route, nil
}
