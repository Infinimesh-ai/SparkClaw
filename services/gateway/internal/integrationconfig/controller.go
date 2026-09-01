package integrationconfig

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/localmind"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

const (
	InfoID      = "infinimesh-info"
	LocalMindID = "localmind"

	infoBinding      = "integration:infinimesh-info"
	localMindBinding = "integration:localmind"
	infoBundleKind   = "infinimesh-info-credential-bundle-v1"
	localBundleKind  = "localmind-credential-bundle-v1"

	StateNotConfigured          = "not_configured"
	StateConfigured             = "configured"
	StateChecking               = "checking"
	StateReady                  = "ready"
	StateNeedsAttention         = "needs_attention"
	StateTemporarilyUnavailable = "temporarily_unavailable"
	StateVaultUnavailable       = "vault_unavailable"

	SourceHousehold = "household"
	SourceOperator  = "operator"
	SourceNone      = "none"
)

type BindingVault interface {
	Ready() error
	OpenBinding(context.Context, string, string) ([]byte, bool, error)
	ReplaceBinding(context.Context, string, string, []byte) error
	DeleteBinding(context.Context, string, string) error
}

type AuditRepository interface {
	AddAudit(context.Context, app.AuditEvent) error
}

type localMindRuntime interface {
	CheckCredentials(context.Context, localmind.Credentials) (localmind.Snapshot, error)
	ActivateCredentials(context.Context, localmind.Credentials) (localmind.Snapshot, error)
	ActivateOperator(context.Context) (localmind.Snapshot, error)
	ClearRuntime() error
	OperatorConfigured() bool
	Run(context.Context)
}

type CredentialSummary struct {
	ID            string    `json:"id"`
	Label         string    `json:"label"`
	ValidatedAt   time.Time `json:"validated_at"`
	LastCheckedAt time.Time `json:"last_checked_at,omitempty"`
	State         string    `json:"state"`
	ErrorCode     string    `json:"error_code,omitempty"`
	Active        bool      `json:"active"`
}

type Status struct {
	ID                 string              `json:"id"`
	Category           string              `json:"category"`
	Configured         bool                `json:"configured"`
	Source             string              `json:"source"`
	State              string              `json:"state"`
	Editable           bool                `json:"editable"`
	Checkable          bool                `json:"checkable"`
	OperatorAvailable  bool                `json:"operator_available"`
	ActiveCredentialID string              `json:"active_credential_id,omitempty"`
	Credentials        []CredentialSummary `json:"credentials"`
	LastCheckedAt      time.Time           `json:"last_checked_at,omitempty"`
	ErrorCode          string              `json:"error_code,omitempty"`
}

type AddInfoCredentialInput struct {
	Label      string `json:"label"`
	LicenseID  string `json:"license_id"`
	LicenseKey string `json:"license_key"`
}

type AddLocalMindCredentialInput struct {
	Label       string `json:"label"`
	Endpoint    string `json:"endpoint"`
	BearerToken string `json:"bearer_token"`
}

type Error struct {
	Code      string
	Retryable bool
	cause     error
}

func (e *Error) Error() string {
	switch e.Code {
	case "integration_not_found":
		return "integration was not found"
	case "credential_not_found":
		return "credential was not found"
	case "credential_invalid":
		return "credential input is invalid"
	case "credential_auth_failed":
		return "credentials were rejected"
	case "credential_check_unavailable":
		return "credential check is temporarily unavailable"
	case "credential_contract_invalid":
		return "integration contract validation failed"
	case "active_credential_replacement_required":
		return "select another credential or operator configuration before deleting the active credential"
	case "operator_not_configured":
		return "operator configuration is not available"
	case "vault_unavailable":
		return "encrypted credential storage is unavailable"
	default:
		return "integration configuration failed"
	}
}

func (e *Error) Unwrap() error { return e.cause }

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func ErrorRetryable(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Retryable
}

type infoPayload struct {
	LicenseID  string `json:"license_id"`
	LicenseKey string `json:"license_key"`
}

type localMindPayload struct {
	Endpoint    string `json:"endpoint"`
	BearerToken string `json:"bearer_token"`
}

type storedCredential struct {
	ID            string          `json:"id"`
	Label         string          `json:"label"`
	ValidatedAt   time.Time       `json:"validated_at"`
	LastCheckedAt time.Time       `json:"last_checked_at,omitempty"`
	State         string          `json:"state"`
	ErrorCode     string          `json:"error_code,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

type credentialBundle struct {
	Version            int                `json:"version"`
	ActiveCredentialID string             `json:"active_credential_id"`
	Credentials        []storedCredential `json:"credentials"`
}

type runtimeState struct {
	state         string
	errorCode     string
	lastCheckedAt time.Time
}

type Controller struct {
	mu        sync.Mutex
	cfg       config.Config
	vault     BindingVault
	audits    AuditRepository
	tools     *toolhub.ToolHub
	localMind localMindRuntime
	bundles   map[string]credentialBundle
	runtime   map[string]runtimeState
}

func New(cfg config.Config, vault BindingVault, audits AuditRepository, tools *toolhub.ToolHub, localMind *localmind.Manager) *Controller {
	var runtime localMindRuntime
	if localMind != nil {
		runtime = localMind
	}
	return newController(cfg, vault, audits, tools, runtime)
}

func newController(cfg config.Config, vault BindingVault, audits AuditRepository, tools *toolhub.ToolHub, localMind localMindRuntime) *Controller {
	return &Controller{
		cfg: cfg, vault: vault, audits: audits, tools: tools, localMind: localMind,
		bundles: map[string]credentialBundle{InfoID: {Version: 1}, LocalMindID: {Version: 1}},
		runtime: map[string]runtimeState{
			InfoID: {state: StateNotConfigured}, LocalMindID: {state: StateNotConfigured},
		},
	}
}

func (c *Controller) Initialize(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range []string{InfoID, LocalMindID} {
		bundle, err := c.readBundle(ctx, id)
		if err != nil {
			bundle = credentialBundle{Version: 1}
			c.bundles[id] = bundle
			c.clearRuntime(id)
			c.runtime[id] = runtimeState{state: StateVaultUnavailable, errorCode: "vault_unavailable"}
			continue
		}
		c.bundles[id] = bundle
		c.activateLoaded(ctx, id, bundle)
	}
}

func (c *Controller) clearRuntime(id string) {
	if id == InfoID {
		if c.tools != nil {
			c.tools.ReplaceInfoAdapters(nil, nil)
		}
		return
	}
	if c.localMind != nil {
		if err := c.localMind.ClearRuntime(); err != nil {
			slog.Warn("LocalMind runtime could not be cleared", "error", err)
		}
	}
}

func (c *Controller) Run(ctx context.Context) {
	if c != nil && c.localMind != nil {
		c.localMind.Run(ctx)
	}
}

func (c *Controller) List(_ context.Context) []Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []Status{c.statusLocked(InfoID), c.statusLocked(LocalMindID)}
}

func (c *Controller) Get(_ context.Context, id string) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !knownIntegration(id) {
		return Status{}, newError("integration_not_found", false, nil)
	}
	return c.statusLocked(id), nil
}

func (c *Controller) AddInfoCredential(ctx context.Context, input AddInfoCredentialInput) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	label, err := validateLabel(input.Label)
	if err != nil {
		return Status{}, err
	}
	payload := infoPayload{LicenseID: strings.TrimSpace(input.LicenseID), LicenseKey: strings.TrimSpace(input.LicenseKey)}
	if payload.LicenseID == "" || len(payload.LicenseID) > 256 || payload.LicenseKey == "" || len(payload.LicenseKey) > 2048 {
		return Status{}, newError("credential_invalid", false, nil)
	}
	keyLicenseID, valid := infinimeshinfo.ParseLicenseKeyLicenseID(payload.LicenseKey)
	if !valid || keyLicenseID != payload.LicenseID {
		return Status{}, newError("credential_invalid", false, nil)
	}
	if err := c.checkInfo(ctx, payload); err != nil {
		return Status{}, err
	}
	raw, _ := json.Marshal(payload)
	now := time.Now().UTC()
	bundle := c.bundles[InfoID]
	bundle.Credentials = append(bundle.Credentials, storedCredential{
		ID: app.NewID("info_cred"), Label: label, ValidatedAt: now, LastCheckedAt: now,
		State: StateReady, Payload: raw,
	})
	if err := c.writeBundle(ctx, InfoID, bundle); err != nil {
		return Status{}, err
	}
	c.bundles[InfoID] = bundle
	c.audit(ctx, InfoID, "credential_saved", "", StateReady, "")
	return c.statusLocked(InfoID), nil
}

func (c *Controller) AddLocalMindCredential(ctx context.Context, input AddLocalMindCredentialInput) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	label, err := validateLabel(input.Label)
	if err != nil {
		return Status{}, err
	}
	payload := localMindPayload{Endpoint: strings.TrimSpace(input.Endpoint), BearerToken: strings.TrimSpace(input.BearerToken)}
	if len(payload.Endpoint) > 2048 || payload.BearerToken == "" || len(payload.BearerToken) > 4096 || c.localMind == nil {
		return Status{}, newError("credential_invalid", false, nil)
	}
	if _, err := c.localMind.CheckCredentials(ctx, localmind.Credentials{Endpoint: payload.Endpoint, BearerToken: payload.BearerToken}); err != nil {
		return Status{}, classifyLocalMindCheck(err)
	}
	raw, _ := json.Marshal(payload)
	now := time.Now().UTC()
	bundle := c.bundles[LocalMindID]
	bundle.Credentials = append(bundle.Credentials, storedCredential{
		ID: app.NewID("localmind_cred"), Label: label, ValidatedAt: now, LastCheckedAt: now,
		State: StateReady, Payload: raw,
	})
	if err := c.writeBundle(ctx, LocalMindID, bundle); err != nil {
		return Status{}, err
	}
	c.bundles[LocalMindID] = bundle
	c.audit(ctx, LocalMindID, "credential_saved", "", StateReady, "")
	return c.statusLocked(LocalMindID), nil
}

func (c *Controller) Activate(ctx context.Context, id, credentialID string, useOperator bool) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !knownIntegration(id) {
		return Status{}, newError("integration_not_found", false, nil)
	}
	bundle := c.bundles[id]
	if useOperator {
		if !c.operatorAvailable(id) {
			return Status{}, newError("operator_not_configured", false, nil)
		}
		credentialID = ""
	} else if strings.TrimSpace(credentialID) == "" {
		return Status{}, newError("credential_invalid", false, nil)
	} else if _, ok := findCredential(bundle, credentialID); !ok {
		return Status{}, newError("credential_not_found", false, nil)
	}
	if bundle.ActiveCredentialID == credentialID && ((credentialID != "") || useOperator) {
		return c.statusLocked(id), nil
	}
	bundle.ActiveCredentialID = credentialID
	if err := c.writeBundle(ctx, id, bundle); err != nil {
		return Status{}, err
	}
	c.bundles[id] = bundle
	c.activateLoaded(ctx, id, bundle)
	status := c.statusLocked(id)
	c.audit(ctx, id, "active_credential_changed", status.Source, status.State, status.ErrorCode)
	return status, nil
}

func (c *Controller) Check(ctx context.Context, id, credentialID string) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !knownIntegration(id) {
		return Status{}, newError("integration_not_found", false, nil)
	}
	bundle := c.bundles[id]
	index, found := findCredential(bundle, credentialID)
	if !found {
		return Status{}, newError("credential_not_found", false, nil)
	}
	bundle.Credentials[index].LastCheckedAt = time.Now().UTC()
	bundle.Credentials[index].State = StateChecking
	bundle.Credentials[index].ErrorCode = ""
	var checkErr error
	if id == InfoID {
		var payload infoPayload
		if json.Unmarshal(bundle.Credentials[index].Payload, &payload) != nil {
			checkErr = newError("credential_invalid", false, nil)
		} else {
			checkErr = c.checkInfo(ctx, payload)
		}
	} else {
		var payload localMindPayload
		if json.Unmarshal(bundle.Credentials[index].Payload, &payload) != nil || c.localMind == nil {
			checkErr = newError("credential_invalid", false, nil)
		} else if _, err := c.localMind.CheckCredentials(ctx, localmind.Credentials{Endpoint: payload.Endpoint, BearerToken: payload.BearerToken}); err != nil {
			checkErr = classifyLocalMindCheck(err)
		}
	}
	if checkErr == nil {
		bundle.Credentials[index].ValidatedAt = bundle.Credentials[index].LastCheckedAt
		bundle.Credentials[index].State = StateReady
	} else {
		bundle.Credentials[index].State = stateForError(checkErr)
		bundle.Credentials[index].ErrorCode = ErrorCode(checkErr)
	}
	if err := c.writeBundle(ctx, id, bundle); err != nil {
		return Status{}, err
	}
	c.bundles[id] = bundle
	if bundle.ActiveCredentialID == credentialID {
		c.runtime[id] = runtimeState{
			state: bundle.Credentials[index].State, errorCode: bundle.Credentials[index].ErrorCode,
			lastCheckedAt: bundle.Credentials[index].LastCheckedAt,
		}
	}
	c.audit(ctx, id, "credential_checked", c.statusLocked(id).Source, bundle.Credentials[index].State, bundle.Credentials[index].ErrorCode)
	if checkErr != nil {
		return c.statusLocked(id), checkErr
	}
	return c.statusLocked(id), nil
}

func (c *Controller) Delete(ctx context.Context, id, credentialID string) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !knownIntegration(id) {
		return Status{}, newError("integration_not_found", false, nil)
	}
	bundle := c.bundles[id]
	index, found := findCredential(bundle, credentialID)
	if !found {
		return Status{}, newError("credential_not_found", false, nil)
	}
	if bundle.ActiveCredentialID == credentialID {
		return Status{}, newError("active_credential_replacement_required", false, nil)
	}
	bundle.Credentials = append(bundle.Credentials[:index], bundle.Credentials[index+1:]...)
	if len(bundle.Credentials) == 0 {
		if err := c.deleteBundle(ctx, id); err != nil {
			return Status{}, err
		}
		bundle = credentialBundle{Version: 1}
	} else if err := c.writeBundle(ctx, id, bundle); err != nil {
		return Status{}, err
	}
	c.bundles[id] = bundle
	status := c.statusLocked(id)
	c.audit(ctx, id, "credential_deleted", status.Source, status.State, status.ErrorCode)
	return status, nil
}

func (c *Controller) activateLoaded(ctx context.Context, id string, bundle credentialBundle) {
	if id == InfoID {
		c.activateInfo(ctx, bundle)
		return
	}
	c.activateLocalMind(ctx, bundle)
}

func (c *Controller) activateInfo(_ context.Context, bundle credentialBundle) {
	if c.tools == nil {
		c.runtime[InfoID] = runtimeState{state: StateNeedsAttention, errorCode: "runtime_unavailable"}
		return
	}
	if bundle.ActiveCredentialID == "" {
		if c.cfg.Plugins.Entries.InfinimeshInfo.Config.Configured() {
			search, weather, err := c.buildInfoAdapters(infoPayload{
				LicenseID:  c.cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseID,
				LicenseKey: c.cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseKey,
			})
			if err == nil {
				c.tools.ReplaceInfoAdapters(search, weather)
				c.runtime[InfoID] = runtimeState{state: StateConfigured}
				return
			}
		}
		c.tools.ReplaceInfoAdapters(nil, nil)
		c.runtime[InfoID] = runtimeState{state: StateNotConfigured}
		return
	}
	index, found := findCredential(bundle, bundle.ActiveCredentialID)
	if !found {
		c.tools.ReplaceInfoAdapters(nil, nil)
		c.runtime[InfoID] = runtimeState{state: StateNeedsAttention, errorCode: "credential_not_found"}
		return
	}
	var payload infoPayload
	if json.Unmarshal(bundle.Credentials[index].Payload, &payload) != nil {
		c.tools.ReplaceInfoAdapters(nil, nil)
		c.runtime[InfoID] = runtimeState{state: StateNeedsAttention, errorCode: "credential_invalid"}
		return
	}
	search, weather, err := c.buildInfoAdapters(payload)
	if err != nil {
		c.tools.ReplaceInfoAdapters(nil, nil)
		c.runtime[InfoID] = runtimeState{state: StateNeedsAttention, errorCode: "credential_invalid"}
		return
	}
	c.tools.ReplaceInfoAdapters(search, weather)
	c.runtime[InfoID] = runtimeState{
		state: bundle.Credentials[index].State, errorCode: bundle.Credentials[index].ErrorCode,
		lastCheckedAt: bundle.Credentials[index].LastCheckedAt,
	}
}

func (c *Controller) activateLocalMind(ctx context.Context, bundle credentialBundle) {
	if c.localMind == nil {
		c.runtime[LocalMindID] = runtimeState{state: StateNotConfigured}
		return
	}
	if bundle.ActiveCredentialID == "" {
		if !c.localMind.OperatorConfigured() {
			_ = c.localMind.ClearRuntime()
			c.runtime[LocalMindID] = runtimeState{state: StateNotConfigured}
			return
		}
		snapshot, err := c.localMind.ActivateOperator(ctx)
		c.setLocalMindRuntime(snapshot, err)
		return
	}
	index, found := findCredential(bundle, bundle.ActiveCredentialID)
	if !found {
		_ = c.localMind.ClearRuntime()
		c.runtime[LocalMindID] = runtimeState{state: StateNeedsAttention, errorCode: "credential_not_found"}
		return
	}
	var payload localMindPayload
	if json.Unmarshal(bundle.Credentials[index].Payload, &payload) != nil {
		_ = c.localMind.ClearRuntime()
		c.runtime[LocalMindID] = runtimeState{state: StateNeedsAttention, errorCode: "credential_invalid"}
		return
	}
	snapshot, err := c.localMind.ActivateCredentials(ctx, localmind.Credentials{Endpoint: payload.Endpoint, BearerToken: payload.BearerToken})
	c.setLocalMindRuntime(snapshot, err)
}

func (c *Controller) setLocalMindRuntime(snapshot localmind.Snapshot, err error) {
	if err == nil {
		c.runtime[LocalMindID] = runtimeState{state: StateReady, lastCheckedAt: snapshot.RefreshedAt}
		return
	}
	mapped := classifyLocalMindCheck(err)
	c.runtime[LocalMindID] = runtimeState{state: stateForError(mapped), errorCode: ErrorCode(mapped), lastCheckedAt: time.Now().UTC()}
}

func (c *Controller) checkInfo(ctx context.Context, payload infoPayload) error {
	client, err := c.newInfoClient(payload, true)
	if err != nil {
		return newError("credential_invalid", false, err)
	}
	_, err = client.Query(ctx, infinimeshinfo.QueryRequest{
		Query: "SparkClaw connection check", TaskType: "general_research", Freshness: "medium", MaxSources: 1, Language: "en",
	})
	if err == nil {
		return nil
	}
	var apiErr *infinimeshinfo.APIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
		return newError("credential_auth_failed", false, err)
	}
	return newError("credential_check_unavailable", true, err)
}

func (c *Controller) buildInfoAdapters(payload infoPayload) (websearch.Adapter, toolhub.WeatherInfoAdapter, error) {
	infoCfg := c.cfg.Plugins.Entries.InfinimeshInfo.Config
	infoCfg.LicenseID = payload.LicenseID
	infoCfg.LicenseKey = payload.LicenseKey
	search, err := websearch.NewInfinimeshInfoAdapter(infoCfg, nil)
	if err != nil {
		return nil, nil, err
	}
	weather, err := c.newInfoClient(payload, false)
	if err != nil {
		return nil, nil, err
	}
	return search, weather, nil
}

func (c *Controller) newInfoClient(payload infoPayload, validation bool) (*infinimeshinfo.Client, error) {
	cfg := c.cfg.Plugins.Entries.InfinimeshInfo.Config
	tokenBatchSize := cfg.TokenBatchSize
	maxAttempts := cfg.MaxAttempts
	if validation {
		tokenBatchSize = 1
		maxAttempts = 1
	}
	return infinimeshinfo.NewClient(infinimeshinfo.Config{
		BaseURL: cfg.BaseURL, LicenseID: payload.LicenseID, LicenseKey: payload.LicenseKey,
		TokenBatchSize: tokenBatchSize, MaxAttempts: maxAttempts,
		RetryBaseDelay:       time.Duration(cfg.RetryBaseDelayMS) * time.Millisecond,
		RequestTimeout:       time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
		ResponseBodyMaxBytes: cfg.ResponseBodyMaxBytes,
	}, nil)
}

func (c *Controller) statusLocked(id string) Status {
	bundle := c.bundles[id]
	runtime := c.runtime[id]
	status := Status{
		ID: id, Editable: true, Checkable: true, OperatorAvailable: c.operatorAvailable(id),
		State: runtime.state, ErrorCode: runtime.errorCode, LastCheckedAt: runtime.lastCheckedAt,
		ActiveCredentialID: bundle.ActiveCredentialID, Credentials: []CredentialSummary{},
	}
	if id == InfoID {
		status.Category = "data_provider"
	} else {
		status.Category = "outbound_mcp"
	}
	if runtime.state == StateVaultUnavailable {
		status.Source = SourceNone
		status.Configured = false
	} else if bundle.ActiveCredentialID != "" {
		status.Source = SourceHousehold
		status.Configured = true
	} else if status.OperatorAvailable {
		status.Source = SourceOperator
		status.Configured = true
	} else {
		status.Source = SourceNone
	}
	for _, item := range bundle.Credentials {
		status.Credentials = append(status.Credentials, CredentialSummary{
			ID: item.ID, Label: item.Label, ValidatedAt: item.ValidatedAt, LastCheckedAt: item.LastCheckedAt,
			State: item.State, ErrorCode: item.ErrorCode, Active: item.ID == bundle.ActiveCredentialID,
		})
	}
	return status
}

func (c *Controller) operatorAvailable(id string) bool {
	if id == InfoID {
		return c.cfg.Plugins.Entries.InfinimeshInfo.Config.Configured()
	}
	return c.localMind != nil && c.localMind.OperatorConfigured()
}

func (c *Controller) readBundle(ctx context.Context, id string) (credentialBundle, error) {
	binding, kind := bundleBinding(id)
	if c.vault == nil || c.vault.Ready() != nil {
		return credentialBundle{}, newError("vault_unavailable", false, nil)
	}
	raw, found, err := c.vault.OpenBinding(ctx, binding, kind)
	if err != nil {
		return credentialBundle{}, newError("vault_unavailable", false, err)
	}
	if !found {
		return credentialBundle{Version: 1}, nil
	}
	defer zero(raw)
	var bundle credentialBundle
	if json.Unmarshal(raw, &bundle) != nil || bundle.Version != 1 {
		return credentialBundle{}, newError("vault_unavailable", false, nil)
	}
	if bundle.Credentials == nil {
		bundle.Credentials = []storedCredential{}
	}
	return bundle, nil
}

func (c *Controller) writeBundle(ctx context.Context, id string, bundle credentialBundle) error {
	bundle.Version = 1
	raw, err := json.Marshal(bundle)
	if err != nil {
		return newError("vault_unavailable", false, err)
	}
	defer zero(raw)
	binding, kind := bundleBinding(id)
	if c.vault == nil || c.vault.ReplaceBinding(ctx, binding, kind, raw) != nil {
		return newError("vault_unavailable", false, nil)
	}
	return nil
}

func (c *Controller) deleteBundle(ctx context.Context, id string) error {
	binding, kind := bundleBinding(id)
	if c.vault == nil {
		return newError("vault_unavailable", false, nil)
	}
	if err := c.vault.DeleteBinding(ctx, binding, kind); err != nil {
		return newError("vault_unavailable", false, err)
	}
	return nil
}

func (c *Controller) audit(ctx context.Context, id, operation, source, state, errorCode string) {
	if c.audits == nil {
		return
	}
	if err := c.audits.AddAudit(context.WithoutCancel(ctx), app.AuditEvent{
		Actor: "household_control", Type: "integration." + operation,
		Summary: "Household integration configuration changed",
		Fields: map[string]any{
			"integration_id": id, "operation": operation, "source": source, "state": state, "error_code": errorCode,
		},
	}); err != nil {
		slog.Warn("integration audit unavailable", "integration_id", id, "operation", operation, "code", store.StoreErrorCodeOf(err))
	}
}

func validateLabel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 80 {
		return "", newError("credential_invalid", false, nil)
	}
	return value, nil
}

func findCredential(bundle credentialBundle, id string) (int, bool) {
	for index := range bundle.Credentials {
		if bundle.Credentials[index].ID == id {
			return index, true
		}
	}
	return 0, false
}

func knownIntegration(id string) bool { return id == InfoID || id == LocalMindID }

func bundleBinding(id string) (string, string) {
	if id == InfoID {
		return infoBinding, infoBundleKind
	}
	return localMindBinding, localBundleKind
}

func classifyLocalMindCheck(err error) error {
	if app.ToolErrorCodeFrom(err) == app.ToolErrorMCPAuthorization {
		return newError("credential_auth_failed", false, err)
	}
	if errors.Is(err, localmind.ErrInvalidCredentials) {
		return newError("credential_invalid", false, err)
	}
	if errors.Is(err, localmind.ErrContractInvalid) {
		return newError("credential_contract_invalid", false, err)
	}
	return newError("credential_check_unavailable", true, err)
}

func stateForError(err error) string {
	if ErrorRetryable(err) {
		return StateTemporarilyUnavailable
	}
	return StateNeedsAttention
}

func newError(code string, retryable bool, cause error) error {
	return &Error{Code: code, Retryable: retryable, cause: cause}
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ BindingVault = (*credential.Vault)(nil)
var _ AuditRepository = (store.AuditRepository)(nil)
