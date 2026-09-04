package emailautomation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Repository interface {
	store.ConnectorRepository
}

type ProviderStatus struct {
	Provider      string     `json:"provider"`
	DisplayName   string     `json:"display_name"`
	Enabled       bool       `json:"enabled"`
	Default       bool       `json:"default"`
	Account       string     `json:"account"`
	AccountHint   string     `json:"account_hint,omitempty"`
	State         string     `json:"state"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	ErrorCode     string     `json:"error_code,omitempty"`
	Version       int64      `json:"version"`
	UpdatedAt     time.Time  `json:"updated_at,omitempty"`
}

type UpdateProviderInput struct {
	Enabled         *bool
	Default         *bool
	ExpectedVersion int64
}

type AdmissionResult = app.EmailAdmissionBinding

type Controller struct {
	store    Repository
	registry Registry
	browser  BrowserController
	runner   ScriptRunner
	now      func() time.Time
	profile  sync.Mutex
}

func NewController(st Repository, registry Registry, browser BrowserController, runner ScriptRunner) *Controller {
	return &Controller{
		store: st, registry: registry, browser: browser, runner: runner,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (c *Controller) List(ctx context.Context, ownerID string) ([]ProviderStatus, error) {
	settings, err := c.store.ListEmailProviderSettings(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	byProvider := make(map[string]app.EmailProviderSetting, len(settings))
	for _, setting := range settings {
		byProvider[setting.Provider] = setting
	}
	statuses := make([]ProviderStatus, 0, len(c.registry.List()))
	for _, provider := range c.registry.List() {
		setting, ok := byProvider[provider.ID]
		if !ok {
			setting = emptySetting(ownerID, provider.ID)
		}
		statuses = append(statuses, providerStatus(provider, setting))
	}
	return statuses, nil
}

func (c *Controller) Update(ctx context.Context, ownerID, actorID, providerID string, input UpdateProviderInput) (ProviderStatus, error) {
	provider, ok := c.registry.Get(providerID)
	if !ok {
		return ProviderStatus{}, codedError(CodeInvalidInput, "Email provider is not registered")
	}
	setting, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		return ProviderStatus{}, err
	}
	if !exists {
		setting = emptySetting(ownerID, provider.ID)
	}
	if input.ExpectedVersion < 0 || input.ExpectedVersion != setting.Version {
		return ProviderStatus{}, store.ErrEmailProviderSettingConflict
	}
	if input.Enabled != nil {
		setting.Enabled = *input.Enabled
	}
	if input.Default != nil {
		setting.Default = *input.Default
	}
	if !setting.Enabled {
		setting.Default = false
	}
	setting.UpdatedBy = strings.TrimSpace(actorID)
	updated, err := c.store.UpdateEmailProviderSetting(ctx, setting, input.ExpectedVersion)
	if err != nil {
		return ProviderStatus{}, err
	}
	return providerStatus(provider, updated), nil
}

func (c *Controller) OpenLoginBrowser(ctx context.Context, ownerID, actorID, providerID string) (ProviderStatus, error) {
	provider, ok := c.registry.Get(providerID)
	if !ok {
		return ProviderStatus{}, codedError(CodeInvalidInput, "Email provider is not registered")
	}
	c.profile.Lock()
	defer c.profile.Unlock()
	if c.browser == nil {
		return ProviderStatus{}, codedError(CodeProviderUnavailable, "SparkClaw browser is unavailable")
	}
	if err := c.browser.OpenLogin(ctx, provider.LoginURL); err != nil {
		return ProviderStatus{}, err
	}
	setting, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		return ProviderStatus{}, err
	}
	if !exists {
		setting = emptySetting(ownerID, provider.ID)
	}
	setting.Enabled = true
	setting.State = app.EmailStateLoginRequired
	setting.AccountHint = ""
	setting.LastCheckedAt = nil
	setting.ErrorCode = ""
	setting.UpdatedBy = strings.TrimSpace(actorID)
	updated, err := c.store.UpdateEmailProviderSetting(ctx, setting, setting.Version)
	if err != nil {
		return ProviderStatus{}, err
	}
	return providerStatus(provider, updated), nil
}

func (c *Controller) Check(ctx context.Context, ownerID, actorID, providerID string) (ProviderStatus, error) {
	provider, ok := c.registry.Get(providerID)
	if !ok {
		return ProviderStatus{}, codedError(CodeInvalidInput, "Email provider is not registered")
	}
	setting, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		return ProviderStatus{}, err
	}
	if !exists || !setting.Enabled {
		return providerStatus(provider, settingOrEmpty(setting, exists, ownerID, provider.ID)), codedError(CodeNotConfigured, "Email provider is not enabled")
	}
	c.profile.Lock()
	defer c.profile.Unlock()
	result, probeErr := c.probe(ctx, provider, app.NewID("email_probe"))
	updated, persistErr := c.persistProbe(ctx, setting, actorID, result, probeErr)
	if persistErr != nil {
		return ProviderStatus{}, persistErr
	}
	return providerStatus(provider, updated), probeErr
}

func (c *Controller) Admit(ctx context.Context, ownerID, request string) (AdmissionResult, error) {
	provider, setting, err := c.resolveProvider(ctx, ownerID, request)
	if err != nil {
		return AdmissionResult{}, err
	}
	c.profile.Lock()
	defer c.profile.Unlock()
	result, probeErr := c.probe(ctx, provider, app.NewID("email_admit"))
	updated, persistErr := c.persistProbe(ctx, setting, "email_admission", result, probeErr)
	if persistErr != nil {
		return AdmissionResult{}, persistErr
	}
	if probeErr != nil {
		return AdmissionResult{}, probeErr
	}
	return AdmissionResult{
		Provider: provider.ID, Account: app.EmailAccountDefault, AccountHint: updated.AccountHint,
		SettingVersion: updated.Version, BrowserGeneration: result.Generation,
		ProbeRevision: provider.Probe.Revision, SendScriptRevision: provider.Send.Revision, ValidatedAt: result.CheckedAt,
	}, nil
}

func (c *Controller) SendForOwner(ctx context.Context, ownerID string, request SendRequest) (SendResult, error) {
	provider, ok := c.registry.Get(request.Provider)
	if !ok {
		return SendResult{}, codedError(CodeInvalidInput, "Email provider is not registered")
	}
	setting, exists, err := c.store.GetEmailProviderSetting(ctx, ownerID, provider.ID)
	if err != nil {
		return SendResult{}, err
	}
	if !exists || !setting.Enabled || setting.State != app.EmailStateReady || setting.Version != request.SettingVersion || setting.Account != request.Account {
		return SendResult{}, codedError(CodeAdmissionStale, "Email provider configuration changed after approval was prepared")
	}
	c.profile.Lock()
	defer c.profile.Unlock()
	if c.runner == nil {
		return SendResult{}, codedError(CodeProviderUnavailable, "Email provider scripts are unavailable")
	}
	return c.runner.Send(ctx, provider, request)
}

func (c *Controller) probe(ctx context.Context, provider Provider, invocationID string) (ProbeResult, error) {
	if c.runner == nil {
		return ProbeResult{}, codedError(CodeProviderUnavailable, "Email provider scripts are unavailable")
	}
	result, err := c.runner.Probe(ctx, provider, invocationID, 0)
	if err != nil {
		return ProbeResult{}, err
	}
	if result.Provider != provider.ID || result.Generation == 0 || result.Revision != provider.Probe.Revision ||
		result.CheckedAt.IsZero() || !validAccountHint(result.AccountHint) {
		return ProbeResult{}, codedError(CodeScriptInvalidOutput, "Email login probe returned an invalid result")
	}
	return result, nil
}

func (c *Controller) resolveProvider(ctx context.Context, ownerID, request string) (Provider, app.EmailProviderSetting, error) {
	matches := c.registry.MatchRequest(request)
	if len(matches) > 1 {
		return Provider{}, app.EmailProviderSetting{}, codedError(CodeAccountAmbiguous, "More than one email provider was named")
	}
	if len(matches) == 1 {
		setting, ok, err := c.store.GetEmailProviderSetting(ctx, ownerID, matches[0].ID)
		if err != nil {
			return Provider{}, app.EmailProviderSetting{}, err
		}
		if !ok || !setting.Enabled {
			return Provider{}, app.EmailProviderSetting{}, codedError(CodeNotConfigured, "Requested email provider is not enabled")
		}
		return matches[0], setting, nil
	}
	settings, err := c.store.ListEmailProviderSettings(ctx, ownerID)
	if err != nil {
		return Provider{}, app.EmailProviderSetting{}, err
	}
	var selected *app.EmailProviderSetting
	for index := range settings {
		if !settings[index].Enabled || !settings[index].Default {
			continue
		}
		if selected != nil {
			return Provider{}, app.EmailProviderSetting{}, codedError(CodeAccountAmbiguous, "Default email provider is ambiguous")
		}
		copy := settings[index]
		selected = &copy
	}
	if selected == nil {
		return Provider{}, app.EmailProviderSetting{}, codedError(CodeNotConfigured, "No default email provider is configured")
	}
	provider, ok := c.registry.Get(selected.Provider)
	if !ok {
		return Provider{}, app.EmailProviderSetting{}, codedError(CodeNotConfigured, "Default email provider is not registered")
	}
	return provider, *selected, nil
}

func (c *Controller) persistProbe(ctx context.Context, setting app.EmailProviderSetting, actorID string, result ProbeResult, probeErr error) (app.EmailProviderSetting, error) {
	now := c.now()
	setting.UpdatedBy = strings.TrimSpace(actorID)
	setting.LastCheckedAt = &now
	setting.ErrorCode = ""
	if probeErr == nil {
		setting.State = app.EmailStateReady
		setting.AccountHint = result.AccountHint
	} else {
		setting.ErrorCode = ErrorCode(probeErr)
		switch setting.ErrorCode {
		case CodeLoginRequired, CodeNotConfigured:
			setting.State = app.EmailStateLoginRequired
			setting.AccountHint = ""
		case CodeProviderUnavailable, CodeScriptTimeout:
			setting.State = app.EmailStateTemporarilyUnavailable
		default:
			setting.State = app.EmailStateNeedsAttention
		}
	}
	updated, err := c.store.UpdateEmailProviderSetting(ctx, setting, setting.Version)
	if errors.Is(err, store.ErrEmailProviderSettingConflict) {
		return app.EmailProviderSetting{}, codedError(CodeAdmissionStale, "Email provider setting changed during login validation")
	}
	return updated, err
}

func emptySetting(ownerID, provider string) app.EmailProviderSetting {
	return app.EmailProviderSetting{
		OwnerID: strings.TrimSpace(ownerID), Provider: provider, Account: app.EmailAccountDefault,
		State: app.EmailStateNotConfigured,
	}
}

func settingOrEmpty(setting app.EmailProviderSetting, exists bool, ownerID, provider string) app.EmailProviderSetting {
	if exists {
		return setting
	}
	return emptySetting(ownerID, provider)
}

func providerStatus(provider Provider, setting app.EmailProviderSetting) ProviderStatus {
	return ProviderStatus{
		Provider: provider.ID, DisplayName: provider.DisplayName, Enabled: setting.Enabled, Default: setting.Default,
		Account: firstNonEmpty(setting.Account, app.EmailAccountDefault), AccountHint: setting.AccountHint,
		State: firstNonEmpty(setting.State, app.EmailStateNotConfigured), LastCheckedAt: setting.LastCheckedAt,
		ErrorCode: setting.ErrorCode, Version: setting.Version, UpdatedAt: setting.UpdatedAt,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
