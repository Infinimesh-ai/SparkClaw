package toolhub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/remindertarget"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/sandbox"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

type Repository interface {
	store.SessionRepository
	store.RunRepository
	store.ApprovalRepository
	store.AuditRepository
	store.ArtifactMetadataRepository
	store.MemoryRepository
	store.ScheduleRepository
	store.ConnectorRepository
	store.ExternalChatRepository
	store.MCPRepository
}

type ToolHub struct {
	cfg                   config.Config
	store                 Repository
	registry              *runtimeToolRegistry
	models                modelrouter.Router
	runner                sandbox.Runner
	artifacts             artifact.Store
	reminders             *remindertarget.Resolver
	info                  *infoRuntime
	browser               browserautomation.Adapter
	managedBrowserWindows *managedBrowserWindowRegistry
	ocr                   documentocr.Adapter
	ocrRuntime            *documentOCRRuntime
	pptxVisualQA          pptxVisualQARunner
	documents             *document.Pipeline
	lifecycle             *toolHubLifecycle
	connectorGate         func(ownerID, channel string) bool
}

// WithConnectorGate wires the owner connector opt-in check (usually
// connector.Registry.Enabled) so schedule admission through reminder tools
// enforces it. Without the gate, third-party return routes fail closed.
func (h *ToolHub) WithConnectorGate(gate func(ownerID, channel string) bool) *ToolHub {
	h.connectorGate = gate
	return h
}

type toolHubLifecycle struct {
	closeOnce sync.Once
	closeErr  error
}

type runtimeToolRegistry struct {
	mu          sync.RWMutex
	defs        map[string]app.ToolDefinition
	executors   map[string]toolExecutor
	origins     map[string]DynamicToolOrigin
	sourceNames map[string]map[string]struct{}
}

type DynamicToolExecutor func(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error)

type DynamicToolRegistration struct {
	Definition app.ToolDefinition
	RemoteName string
	Execute    DynamicToolExecutor
}

func (h *ToolHub) addAudit(ctx context.Context, event app.AuditEvent) {
	if h == nil || h.store == nil {
		return
	}
	if err := h.store.AddAudit(context.WithoutCancel(ctx), event); err != nil {
		slog.Warn("toolhub audit unavailable", "type", event.Type, "run_id", event.RunID, "code", store.StoreErrorCodeOf(err))
	}
}

type DynamicToolOrigin struct {
	Source     string `json:"source"`
	RemoteName string `json:"remote_name"`
}

type Result struct {
	Output        any
	ArchiveOutput any
}

type WeatherInfoAdapter interface {
	Weather(context.Context, infinimeshinfo.WeatherRequest) (infinimeshinfo.WeatherResponse, error)
}

func New(cfg config.Config, st Repository) *ToolHub {
	infoCfg := cfg.Plugins.Entries.InfinimeshInfo.Config
	// A failed constructor must leave the interface field nil, not hold a
	// typed-nil *Client that defeats the availability guard in lookupWeather.
	var searchInfo websearch.Adapter
	var weatherInfo WeatherInfoAdapter
	provider := strings.ToLower(strings.TrimSpace(cfg.Tools.Web.Search.Provider))
	if provider != "" && provider != websearch.InfoProviderName {
		searchInfo = websearch.NewAdapter(cfg)
	}
	if client, err := infinimeshinfo.NewClient(infinimeshinfo.Config{
		BaseURL:              infoCfg.BaseURL,
		LicenseID:            infoCfg.LicenseID,
		LicenseKey:           infoCfg.LicenseKey,
		TokenBatchSize:       infoCfg.TokenBatchSize,
		MaxAttempts:          infoCfg.MaxAttempts,
		RetryBaseDelay:       time.Duration(infoCfg.RetryBaseDelayMS) * time.Millisecond,
		RequestTimeout:       time.Duration(infoCfg.RequestTimeoutSeconds) * time.Second,
		ResponseBodyMaxBytes: infoCfg.ResponseBodyMaxBytes,
	}, nil); err == nil {
		weatherInfo = client
		if adapter, adapterErr := websearch.NewInfinimeshInfoAdapter(infoCfg, nil); adapterErr == nil {
			searchInfo = adapter
		}
	}
	ocrAdapter, err := documentocr.New(cfg.Adapters.DocumentOCR)
	ocrConstructorErr := err
	if err != nil {
		slog.Warn("document OCR adapter unavailable; continuing with OCR disabled", "error", err)
		ocrAdapter = documentocr.Disabled()
	}
	h := &ToolHub{
		cfg:   cfg,
		store: st,
		registry: &runtimeToolRegistry{
			defs:        map[string]app.ToolDefinition{},
			executors:   map[string]toolExecutor{},
			origins:     map[string]DynamicToolOrigin{},
			sourceNames: map[string]map[string]struct{}{},
		},
		models:                modelrouter.New(cfg),
		runner:                sandbox.NewRunner(cfg),
		artifacts:             artifact.NewStore(cfg.Storage),
		reminders:             remindertarget.NewResolver(st),
		info:                  newInfoRuntime(searchInfo, weatherInfo),
		browser:               browserautomation.NewAdapter(cfg),
		managedBrowserWindows: newManagedBrowserWindowRegistry(),
		ocr:                   ocrAdapter,
		ocrRuntime:            newDocumentOCRRuntime(cfg.Adapters.DocumentOCR, ocrAdapter, ocrConstructorErr),
		lifecycle:             &toolHubLifecycle{},
	}
	h.pptxVisualQA = newPPTXVisualQAService(cfg.Adapters.PPTXVisualQA, h.models)
	h.documents = newDocumentPipeline(h)
	for _, def := range defaultDefinitions() {
		reg, ok := toolRegistry[def.Name]
		if !ok {
			// A definition without an executor is a programming error; fail
			// loudly at startup instead of at first invocation.
			panic(fmt.Sprintf("toolhub: definition %q has no entry in toolRegistry", def.Name))
		}
		if reg.enabled != nil && !reg.enabled(cfg) {
			continue
		}
		def.Capabilities = append([]app.CapabilityDescriptor(nil), reg.capabilities...)
		def.OutcomeAdapter = reg.outcomeAdapter
		def.Directory = reg.directory
		h.registry.defs[def.Name] = def
		h.registry.executors[def.Name] = reg.run
	}
	return h
}

// Close releases resources held by tool adapters. Safe to call multiple times.
func (h *ToolHub) Close() error {
	if h == nil {
		return nil
	}
	h.lifecycle.closeOnce.Do(func() {
		var errs []error
		errs = append(errs, h.closeManagedBrowserWindows())
		if h.browser != nil {
			errs = append(errs, h.browser.Close())
		}
		if h.ocr != nil {
			errs = append(errs, h.ocr.Close())
		}
		h.lifecycle.closeErr = errors.Join(errs...)
	})
	return h.lifecycle.closeErr
}

func (h *ToolHub) ArtifactStore() artifact.Store {
	return h.artifacts
}

func (h *ToolHub) WithArtifactStore(artifacts artifact.Store) *ToolHub {
	h.artifacts = artifacts
	return h
}

func (h *ToolHub) WithBrowserAutomationAdapter(adapter browserautomation.Adapter) *ToolHub {
	h.browser = adapter
	return h
}

func (h *ToolHub) WithWeatherInfoAdapter(adapter WeatherInfoAdapter) *ToolHub {
	if h.info != nil {
		h.info.mu.Lock()
		h.info.weather = adapter
		h.info.mu.Unlock()
	}
	return h
}

func (h *ToolHub) WithDocumentOCRAdapter(adapter documentocr.Adapter) *ToolHub {
	h.ocr = adapter
	if h.ocrRuntime == nil {
		h.ocrRuntime = newDocumentOCRRuntime(h.cfg.Adapters.DocumentOCR, adapter, nil)
	} else {
		h.ocrRuntime.setAdapter(adapter)
	}
	return h
}

func (h *ToolHub) DocumentOCRReadiness() documentocr.RuntimeReadiness {
	if h == nil || h.ocrRuntime == nil {
		return documentocr.RuntimeReadiness{RuntimeStatus: "disabled", ReasonCode: "runtime_unavailable"}
	}
	return h.ocrRuntime.readinessSnapshot()
}

func (h *ToolHub) DocumentMetrics() []string {
	if h == nil || h.ocrRuntime == nil {
		return nil
	}
	return h.ocrRuntime.prometheusLines()
}

func (h *ToolHub) Definitions() []app.ToolDefinition {
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()
	defs := make([]app.ToolDefinition, 0, len(h.registry.defs))
	for _, def := range h.registry.defs {
		defs = append(defs, def)
	}
	slices.SortFunc(defs, func(a, b app.ToolDefinition) int {
		return strings.Compare(a.Name, b.Name)
	})
	return defs
}

func (h *ToolHub) Definition(name string) (app.ToolDefinition, bool) {
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()
	def, ok := h.registry.defs[name]
	return def, ok
}

// ReplaceDynamicTools atomically replaces every tool owned by source. A
// dynamic tool can replace an earlier definition from the same source, but it
// can never shadow a static tool or a tool owned by another source.
func (h *ToolHub) ReplaceDynamicTools(source string, registrations []DynamicToolRegistration) error {
	if h == nil || h.registry == nil {
		return errors.New("tool registry is unavailable")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("dynamic tool source is required")
	}
	prepared := make(map[string]DynamicToolRegistration, len(registrations))
	for _, registration := range registrations {
		name := strings.TrimSpace(registration.Definition.Name)
		remoteName := strings.TrimSpace(registration.RemoteName)
		if name == "" || remoteName == "" {
			return errors.New("dynamic tool local and remote names are required")
		}
		if registration.Execute == nil {
			return fmt.Errorf("dynamic tool %q has no executor", name)
		}
		if registration.Definition.InputSchema == nil {
			return fmt.Errorf("dynamic tool %q has no input schema", name)
		}
		if _, exists := prepared[name]; exists {
			return fmt.Errorf("dynamic tool %q is registered more than once by %q", name, source)
		}
		registration.Definition.Name = name
		registration.RemoteName = remoteName
		prepared[name] = registration
	}

	h.registry.mu.Lock()
	defer h.registry.mu.Unlock()
	owned := h.registry.sourceNames[source]
	for name := range prepared {
		if origin, dynamic := h.registry.origins[name]; dynamic {
			if origin.Source != source {
				return fmt.Errorf("dynamic tool %q is already owned by source %q", name, origin.Source)
			}
			continue
		}
		if _, exists := h.registry.defs[name]; exists {
			return fmt.Errorf("dynamic tool %q conflicts with a static tool", name)
		}
	}
	for name := range owned {
		delete(h.registry.defs, name)
		delete(h.registry.executors, name)
		delete(h.registry.origins, name)
	}
	names := make(map[string]struct{}, len(prepared))
	for name, registration := range prepared {
		registration := registration
		h.registry.defs[name] = registration.Definition
		h.registry.executors[name] = func(_ *ToolHub, ctx context.Context, _ string, args map[string]any, sessionID, runID string) (Result, error) {
			return registration.Execute(ctx, args, sessionID, runID)
		}
		h.registry.origins[name] = DynamicToolOrigin{Source: source, RemoteName: registration.RemoteName}
		names[name] = struct{}{}
	}
	if len(names) == 0 {
		delete(h.registry.sourceNames, source)
	} else {
		h.registry.sourceNames[source] = names
	}
	return nil
}

func (h *ToolHub) DynamicToolOrigin(name string) (DynamicToolOrigin, bool) {
	if h == nil || h.registry == nil {
		return DynamicToolOrigin{}, false
	}
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()
	origin, ok := h.registry.origins[name]
	return origin, ok
}

func (h *ToolHub) Config() config.Config {
	return h.cfg
}

func (h *ToolHub) forSession(ctx context.Context, sessionID string) (*ToolHub, error) {
	if strings.TrimSpace(sessionID) == "" || h.store == nil {
		return h, nil
	}
	session, ok, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("resolve tool session: %w", err)
	}
	if !ok || strings.TrimSpace(session.WorkspaceRoot) == "" {
		return h, nil
	}
	root, err := filepath.Abs(session.WorkspaceRoot)
	if err != nil {
		return h, nil
	}
	clone := *h
	clone.cfg = h.cfg
	clone.cfg.Workspaces.DefaultRoot = root
	if !containsPathRoot(clone.cfg.Workspaces.Allowlist, root) {
		clone.cfg.Workspaces.Allowlist = append(append([]string{}, clone.cfg.Workspaces.Allowlist...), root)
	}
	_ = os.MkdirAll(root, 0o755)
	return &clone, nil
}

func containsPathRoot(roots []string, root string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	for _, item := range roots {
		abs, err := filepath.Abs(item)
		if err == nil && abs == absRoot {
			return true
		}
	}
	return false
}

func (h *ToolHub) Validate(name string, args map[string]any) error {
	def, ok := h.Definition(name)
	if !ok {
		return fmt.Errorf("tool %q not found", name)
	}
	return validateInput(def, args)
}

func (h *ToolHub) Execute(ctx context.Context, name string, args map[string]any, sessionID, runID string) (Result, error) {
	if cause := integrationCredentialChangeCause(ctx); cause != nil {
		return Result{}, cause
	}
	def, ok := h.Definition(name)
	if !ok {
		return Result{}, fmt.Errorf("tool %q not found", name)
	}
	if err := validateInput(def, args); err != nil {
		return Result{}, err
	}
	// The declared TimeoutMS is authoritative when the caller carries no
	// deadline: direct-hub entry points previously fell through to the
	// unrelated 60s document adapter fallback, cutting tools that declare a
	// longer bound (e.g. 125s PPTX edits) or leaving undeclared ones to
	// adapter-level bounds only.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && def.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(def.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	var err error
	h, err = h.forSession(ctx, sessionID)
	if err != nil {
		if cause := integrationCredentialChangeCause(ctx); cause != nil {
			return Result{}, cause
		}
		if wrapper := toolhubDocumentProviderRegistry().errorWrapperForTool(name); wrapper != nil {
			return Result{}, wrapper(ctx, err)
		}
		return Result{}, err
	}
	h.registry.mu.RLock()
	executor, ok := h.registry.executors[name]
	h.registry.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("tool %q has no executor in MVP", name)
	}
	result, err := executor(h, ctx, name, args, sessionID, runID)
	if err != nil {
		if cause := integrationCredentialChangeCause(ctx); cause != nil {
			return result, cause
		}
		return result, err
	}
	if err := validateOutput(def, result.Output); err != nil {
		return Result{}, err
	}
	return result, nil
}

func integrationCredentialChangeCause(ctx context.Context) error {
	cause := context.Cause(ctx)
	switch app.ToolErrorCodeFrom(cause) {
	case app.ToolErrorInfoCredentialsChanged, app.ToolErrorLocalMindCredentialsChanged:
		return cause
	default:
		return nil
	}
}

func stringArg(args map[string]any, key, fallback string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return fallback
}

func intArg(args map[string]any, key string, fallback int) int {
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return fallback
	}
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(v)
		if err == nil {
			return parsed
		}
	}
	return fallback
}
