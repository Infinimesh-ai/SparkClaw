package toolhub

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/workspacefiles"
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
	webSearch             websearch.Adapter
	weatherInfo           WeatherInfoAdapter
	browser               browserautomation.Adapter
	managedBrowserWindows *managedBrowserWindowRegistry
	ocr                   documentocr.Adapter
	ocrRuntime            *documentOCRRuntime
	documents             *document.Pipeline
	lifecycle             *toolHubLifecycle
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
	var weatherInfo WeatherInfoAdapter
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
		webSearch:             websearch.NewAdapter(cfg),
		weatherInfo:           weatherInfo,
		browser:               browserautomation.NewAdapter(cfg),
		managedBrowserWindows: newManagedBrowserWindowRegistry(),
		ocr:                   ocrAdapter,
		ocrRuntime:            newDocumentOCRRuntime(cfg.Adapters.DocumentOCR, ocrAdapter, ocrConstructorErr),
		lifecycle:             &toolHubLifecycle{},
	}
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
	h.weatherInfo = adapter
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
	def, ok := h.Definition(name)
	if !ok {
		return Result{}, fmt.Errorf("tool %q not found", name)
	}
	if err := validateInput(def, args); err != nil {
		return Result{}, err
	}
	var err error
	h, err = h.forSession(ctx, sessionID)
	if err != nil {
		if strings.HasPrefix(name, "pptx.") {
			return Result{}, wrapPPTXToolError(ctx, err)
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
		return result, err
	}
	if err := validateOutput(def, result.Output); err != nil {
		return Result{}, err
	}
	return result, nil
}

func defaultDefinitions() []app.ToolDefinition {
	definitions := defaultDefinitionsBeforeDocumentFormats()
	definitions = append(definitions, documentToolDefinitions()...)
	return append(definitions, defaultDefinitionsAfterDocumentFormats()...)
}

func defaultDefinitionsBeforeDocumentFormats() []app.ToolDefinition {
	return []app.ToolDefinition{
		{
			Name:        app.ToolWorkspaceDataAccess,
			Description: "Confirm one frozen Runtime-owned workspace data access contract before protected discovery or reading.",
			InputSchema: schema("object", []string{"contract_revision", "locators", "access_class", "output_class", "request_digest", "invocation", "workflow", "return_route"}, map[string]any{
				"contract_revision": stringSchema(),
				"locators":          arraySchema(objectValueSchema()),
				"access_class": map[string]any{"type": "string", "enum": []any{
					string(app.PolicyAccessWorkspaceSourceRead), string(app.PolicyAccessWorkspaceDerivativeDisclosure),
				}},
				"output_class":   stringSchema(),
				"request_digest": stringSchema(),
				"invocation":     objectValueSchema(),
				"workflow":       objectValueSchema(),
				"return_route":   objectValueSchema(),
			}),
			OutputSchema: objectSchema([]string{"status", "request_digest"}, map[string]any{
				"status": stringSchema(), "request_digest": stringSchema(),
			}),
			Risk: app.RiskRead, RequiresApproval: false, Idempotent: true,
			TimeoutMS: 1000, Sandbox: "forbidden", Audit: "always",
		},
		{
			Name:        "observation.read",
			Description: "Read one bounded byte window from a persisted artifact owned by the current session.",
			InputSchema: schema("object", []string{"artifact_uri"}, map[string]any{
				"artifact_uri": stringSchema(),
				"offset":       map[string]any{"type": "integer", "minimum": float64(0)},
				"max_bytes":    map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(maxObservationReadBytes)},
			}),
			OutputSchema: objectSchema([]string{"artifact_uri", "offset", "max_bytes", "bytes", "total_bytes", "content", "truncated", "next_offset", "untrusted"}, map[string]any{
				"artifact_uri": stringSchema(),
				"offset":       integerSchema(),
				"max_bytes":    integerSchema(),
				"bytes":        integerSchema(),
				"total_bytes":  integerSchema(),
				"content":      stringSchema(),
				"truncated":    booleanSchema(),
				"next_offset":  integerSchema(),
				"untrusted":    booleanSchema(),
			}),
			Risk: app.RiskRead, RequiresApproval: false, Idempotent: true,
			TimeoutMS: 5000, Sandbox: "forbidden", Audit: "always",
		},
		{
			Name:        "files.search",
			Description: "Search base file names inside an allowed workspace and return bounded workspace-relative candidates.",
			InputSchema: schema("object", []string{"query"}, map[string]any{
				"query":       map[string]any{"type": "string"},
				"root":        map[string]any{"type": "string"},
				"max_results": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"query", "results", "count", "complete", "truncated"}, map[string]any{
				"query":     stringSchema(),
				"results":   arraySchema(objectValueSchema()),
				"count":     integerSchema(),
				"complete":  booleanSchema(),
				"truncated": booleanSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        5000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "images.inspect",
			Description: "Inspect an uploaded workspace image with Fast visual semantics and, when OCR is enabled, extract verbatim in-image text as Markdown with explicit text/no-text classification. Text-bearing results retain OCR evidence, text-free results use visual understanding, and mixed images combine both.",
			InputSchema: schema("object", []string{"path"}, map[string]any{
				"path":         stringSchema(),
				"question":     stringSchema(),
				"content_type": stringSchema(),
			}),
			OutputSchema: objectSchema([]string{"status", "path", "content_type", "bytes", "summary", "untrusted"}, map[string]any{
				"status":             stringSchema(),
				"path":               stringSchema(),
				"content_type":       stringSchema(),
				"bytes":              integerSchema(),
				"width":              integerSchema(),
				"height":             integerSchema(),
				"model_content_type": stringSchema(),
				"model_bytes":        integerSchema(),
				"model_width":        integerSchema(),
				"model_height":       integerSchema(),
				"resized":            booleanSchema(),
				"resize_note":        stringSchema(),
				"fallback_policy":    stringSchema(),
				"question":           stringSchema(),
				"summary":            stringSchema(),
				"model":              stringSchema(),
				"profile":            stringSchema(),
				"lane":               stringSchema(),
				"mock":               booleanSchema(),
				"text_detected":      booleanSchema(),
				"ocr_status":         stringSchema(),
				"ocr_markdown":       stringSchema(),
				"ocr_model":          stringSchema(),
				"ocr_inference_ms":   integerSchema(),
				"ocr_warning":        stringSchema(),
				"untrusted":          booleanSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        120000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		weatherLookupDefinition(),
		weatherRenderDefinition(),
		{
			Name:        "files.write_draft",
			Description: "Write a draft file inside the configured workspace draft folder.",
			InputSchema: schema("object", []string{"content"}, map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"path", "bytes", "status"}, map[string]any{
				"path":   stringSchema(),
				"bytes":  integerSchema(),
				"status": stringSchema(),
			}),
			Risk:             app.RiskDraft,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        3000,
			Sandbox:          "optional",
			Audit:            "always",
		},
		{
			Name:        "file.delete",
			Description: "Move a single workspace file into SparkClaw trash after explicit owner approval.",
			InputSchema: schema("object", []string{"path"}, map[string]any{
				"path":   map[string]any{"type": "string"},
				"reason": map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"status", "path", "trash_path", "manifest_path", "deleted_at"}, map[string]any{
				"status":        stringSchema(),
				"path":          stringSchema(),
				"trash_path":    stringSchema(),
				"manifest_path": stringSchema(),
				"bytes":         integerSchema(),
				"deleted_at":    stringSchema(),
			}),
			Risk:             app.RiskDangerous,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        3000,
			Sandbox:          "required",
			Audit:            "always",
		},
	}
}

func (h *ToolHub) confirmWorkspaceDataAccess(_ context.Context, args map[string]any) (Result, error) {
	return Result{Output: map[string]any{
		"status": "approval_contract_confirmed", "request_digest": strings.TrimSpace(fmt.Sprint(args["request_digest"])),
	}}, nil
}

func defaultDefinitionsAfterDocumentFormats() []app.ToolDefinition {
	return []app.ToolDefinition{
		{
			Name:        "memory.search",
			Description: "Search accepted private memories.",
			InputSchema: schema("object", []string{"query"}, map[string]any{
				"query": map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"query", "results", "count"}, map[string]any{
				"query":   stringSchema(),
				"results": arraySchema(objectValueSchema()),
				"count":   integerSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "memory.write_candidate",
			Description: "Propose a memory candidate for owner review.",
			InputSchema: schema("object", []string{"content"}, map[string]any{
				"content":     map[string]any{"type": "string"},
				"kind":        map[string]any{"type": "string"},
				"sensitivity": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"id", "kind", "content", "sensitivity", "status", "reason", "created_at"}, map[string]any{
				"id":          stringSchema(),
				"session_id":  stringSchema(),
				"run_id":      stringSchema(),
				"kind":        stringSchema(),
				"content":     stringSchema(),
				"sensitivity": stringSchema(),
				"status":      stringSchema(),
				"reason":      stringSchema(),
				"created_at":  stringSchema(),
				"resolved_at": stringSchema(),
			}),
			Risk:             app.RiskDraft,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "memory.propose",
			Description: "Compatibility alias for proposing a memory candidate for owner review.",
			InputSchema: schema("object", []string{"content"}, map[string]any{
				"content":     map[string]any{"type": "string"},
				"kind":        map[string]any{"type": "string"},
				"sensitivity": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"id", "kind", "content", "sensitivity", "status", "reason", "created_at"}, map[string]any{
				"id":          stringSchema(),
				"session_id":  stringSchema(),
				"run_id":      stringSchema(),
				"kind":        stringSchema(),
				"content":     stringSchema(),
				"sensitivity": stringSchema(),
				"status":      stringSchema(),
				"reason":      stringSchema(),
				"created_at":  stringSchema(),
				"resolved_at": stringSchema(),
			}),
			Risk:             app.RiskDraft,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "memory.write_sensitive",
			Description: "Write sensitive private memory only after explicit owner approval.",
			InputSchema: schema("object", []string{"content"}, map[string]any{
				"content": map[string]any{"type": "string"},
				"kind":    map[string]any{"type": "string"},
				"reason":  map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"id", "kind", "content", "source_run_id", "created_at", "sensitivity"}, map[string]any{
				"id":            stringSchema(),
				"kind":          stringSchema(),
				"content":       stringSchema(),
				"source_run_id": stringSchema(),
				"created_at":    stringSchema(),
				"sensitivity":   stringSchema(),
			}),
			Risk:             app.RiskDangerous,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "browser.read",
			Description: "Read a public HTTP(S) page through the mode-safe access path. Browser sessions use agent-browser rendered active-tab text and typed page evidence; use browser.snapshot separately for executable controls.",
			InputSchema: schema("object", []string{"url"}, map[string]any{
				"url":                     map[string]any{"type": "string"},
				"max_bytes":               map[string]any{"type": "number"},
				"timeout_ms":              map[string]any{"type": "number"},
				"browser_mode":            stringSchema(),
				"presentation":            stringSchema(),
				"surface_visible":         booleanSchema(),
				"force_browser_session":   booleanSchema(),
				"browser_session":         booleanSchema(),
				"require_browser_session": booleanSchema(),
				"reuse_active_page":       booleanSchema(),
				"disable_hidden_browser":  booleanSchema(),
				"visible_browser":         booleanSchema(),
				"owner_id":                stringSchema(),
				"browser_profile_id":      stringSchema(),
				"site_realm":              stringSchema(),
				"account_hint":            stringSchema(),
				"login_handoff_completed": booleanSchema(),
			}),
			OutputSchema: objectSchema([]string{"url", "status_code", "content_type", "title", "text", "bytes", "truncated", "untrusted", "untrusted_external_content", "warning"}, map[string]any{
				"url":                          stringSchema(),
				"status_code":                  integerSchema(),
				"status_code_source":           stringSchema(),
				"content_type":                 stringSchema(),
				"title":                        stringSchema(),
				"text":                         stringSchema(),
				"bytes":                        integerSchema(),
				"truncated":                    booleanSchema(),
				"untrusted":                    booleanSchema(),
				"untrusted_external_content":   booleanSchema(),
				"warning":                      stringSchema(),
				"extractor":                    stringSchema(),
				"readability_status":           stringSchema(),
				"readability_error":            stringSchema(),
				"readability_length":           integerSchema(),
				"readability_readerable":       booleanSchema(),
				"needs_structure_snapshot":     booleanSchema(),
				"structure_snapshot_reasons":   stringArraySchema(),
				"excerpt":                      stringSchema(),
				"byline":                       stringSchema(),
				"site_name":                    stringSchema(),
				"lang":                         stringSchema(),
				"published_time":               stringSchema(),
				"snapshot_ref":                 stringSchema(),
				"snapshot_object_key":          stringSchema(),
				"snapshot_error":               stringSchema(),
				"read_mode":                    stringSchema(),
				"browser_mode":                 stringSchema(),
				"presentation":                 stringSchema(),
				"surface_visible":              booleanSchema(),
				"rendered":                     booleanSchema(),
				"browser_provider":             stringSchema(),
				"browser_duration_ms":          integerSchema(),
				"browser_actions":              stringArraySchema(),
				"browser_read_source":          stringSchema(),
				"browser_ready_state":          stringSchema(),
				"browser_lang":                 stringSchema(),
				"browser_html_length":          integerSchema(),
				"browser_html_truncated":       booleanSchema(),
				"browser_text_length":          integerSchema(),
				"browser_text_truncated":       booleanSchema(),
				"browser_scroll_height":        integerSchema(),
				"browser_snapshot_text":        stringSchema(),
				"browser_session_error":        stringSchema(),
				"browser_session_warnings":     objectValueSchema(),
				"browser_page_auth_state":      stringSchema(),
				"browser_page_auth_confidence": stringSchema(),
				"browser_page_auth_signals":    stringArraySchema(),
				"auth_challenge_detected":      booleanSchema(),
				"auth_challenge_kind":          stringSchema(),
				"auth_site_origin":             stringSchema(),
				"auth_site_realm":              stringSchema(),
				"browser_auth_status":          stringSchema(),
				"browser_auth_strategy":        stringSchema(),
				"browser_profile_id":           stringSchema(),
				"owner_id":                     stringSchema(),
				"account_hint":                 stringSchema(),
				"login_surface":                stringSchema(),
				"login_handoff_required":       booleanSchema(),
				"login_handoff_opened":         booleanSchema(),
				"login_handoff_url":            stringSchema(),
				"login_handoff_provider":       stringSchema(),
				"login_handoff_error":          stringSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        30000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "web.search",
			Description: "Search the public web as the discovery step for browser-backed research and return untrusted source evidence.",
			InputSchema: schema("object", []string{"query"}, map[string]any{
				"query":       map[string]any{"type": "string"},
				"max_results": map[string]any{"type": "number"},
				"freshness":   map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"schema_version", "request_id", "status", "query", "provider", "retrieved_at", "aggregate", "sources", "usage", "untrusted"}, map[string]any{
				"schema_version": integerSchema(),
				"request_id":     stringSchema(),
				"status":         stringSchema(),
				"query":          stringSchema(),
				"provider":       stringSchema(),
				"retrieved_at":   stringSchema(),
				"took_ms":        integerSchema(),
				"aggregate":      objectValueSchema(),
				"sources":        arraySchema(objectValueSchema()),
				"usage":          objectValueSchema(),
				"untrusted":      booleanSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        30000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "browser.identify_public_target",
			Description: "Select the first provider-ranked structured Info result URL that passes mandatory public HTTPS and redirect validation.",
			InputSchema: schema("object", nil, map[string]any{}),
			OutputSchema: objectSchema([]string{"status", "evidence_id", "resolution_source", "owner_target_phrase", "requested_surface_kind", "info_result_index", "source_result_ref", "canonical_entry_url", "normalized_final_url", "safety_gate_status", "created_at", "untrusted"}, map[string]any{
				"status":                  stringSchema(),
				"evidence_id":             stringSchema(),
				"resolution_source":       stringSchema(),
				"owner_target_phrase":     stringSchema(),
				"requested_surface_kind":  stringSchema(),
				"info_request_id":         stringSchema(),
				"info_result_index":       integerSchema(),
				"source_result_ref":       stringSchema(),
				"canonical_entry_url":     stringSchema(),
				"normalized_final_url":    stringSchema(),
				"observed_redirect_chain": stringArraySchema(),
				"safety_gate_status":      stringSchema(),
				"created_at":              stringSchema(),
				"untrusted":               booleanSchema(),
			}),
			Risk: app.RiskRead, RequiresApproval: false, Idempotent: true,
			TimeoutMS: 10000, Sandbox: "forbidden", Audit: "always",
		},
		{
			Name:        "browser.visual_inspect",
			Description: "Capture and inspect one fresh generation-bound browser screenshot, then revalidate structured page identity after Fast inference.",
			InputSchema: schema("object", []string{"page_id", "snapshot_id", "session_generation", "page_generation", "snapshot_digest", "reason"}, map[string]any{
				"page_id": map[string]any{"type": []any{"string", "number"}}, "snapshot_id": stringSchema(),
				"session_generation": map[string]any{"type": []any{"string", "number"}},
				"page_generation":    map[string]any{"type": []any{"string", "number"}},
				"snapshot_digest":    stringSchema(), "reason": stringSchema(), "question": stringSchema(),
				"browser_mode": stringSchema(), "presentation": stringSchema(), "surface_visible": booleanSchema(),
			}),
			OutputSchema: objectSchema([]string{"status", "evidence_id", "reason", "session_generation", "page_generation", "page_id", "snapshot_id", "snapshot_digest", "post_snapshot_id", "normalized_url", "screenshot_ref", "screenshot_digest", "summary", "model", "profile", "lane", "created_at", "untrusted"}, map[string]any{
				"status": stringSchema(), "evidence_id": stringSchema(), "reason": stringSchema(),
				"session_generation": integerSchema(), "page_generation": integerSchema(), "page_id": stringSchema(),
				"snapshot_id": stringSchema(), "snapshot_digest": stringSchema(), "post_snapshot_id": stringSchema(),
				"normalized_url": stringSchema(), "screenshot_ref": stringSchema(), "screenshot_digest": stringSchema(),
				"summary": stringSchema(), "model": stringSchema(), "profile": stringSchema(), "lane": stringSchema(),
				"created_at": stringSchema(), "untrusted": booleanSchema(),
			}),
			Risk: app.RiskRead, RequiresApproval: false, Idempotent: true,
			TimeoutMS: 125000, Sandbox: "forbidden", Audit: "always",
		},
		browserAutomationDefinition("browser.status", "Check whether the managed agent-browser automation adapter is available.", app.RiskRead, false, nil, nil, []string{"tool", "output", "untrusted", "provider"}),
		browserAutomationDefinition("browser.list_tabs", "List tabs/pages in the managed agent-browser Chromium session.", app.RiskRead, false, nil, nil, []string{"tool", "output", "pages", "untrusted", "provider"}),
		browserAutomationDefinition("browser.open", "Open a URL in a managed agent-browser Chromium page/tab.", app.RiskRead, false, []string{"url"}, nil, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		browserAutomationDefinition("browser.focus", "Focus/select a browser page/tab by stable page identifier.", app.RiskRead, false, []string{"page_id"}, nil, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		browserAutomationDefinition("browser.close", "Close a managed Chromium tab opened by the active Workflow, using its stable page identifier.", app.RiskDraft, false, []string{"page_id"}, nil, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		browserAutomationDefinition("browser.navigate", "Navigate the current or selected tab to a URL while preserving browser context.", app.RiskRead, false, []string{"url"}, []string{"page_id"}, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		browserAutomationDefinition("browser.snapshot", "Take a structured page snapshot for reading and stable element refs.", app.RiskRead, false, nil, []string{"page_id", "interaction_goal", "url", "browser_page_ref", "verbose", "filePath"}, []string{"tool", "raw_tool", "output", "text", "untrusted", "provider"}),
		browserAutomationDefinition("browser.screenshot", "Take a browser screenshot for visual confirmation.", app.RiskRead, false, nil, nil, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		browserAutomationDefinition("browser.wait", "Wait for visible text or observable page state before continuing.", app.RiskRead, false, nil, []string{"page_id", "text"}, []string{"tool", "raw_tool", "output", "text", "untrusted", "provider"}),
		browserAutomationDefinition("browser.click", "Click a clear element ref from the latest browser snapshot.", app.RiskDraft, false, []string{"uid"}, []string{"page_id", "snapshot_id", "expected_effect"}, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		{
			Name:        "browser.validate_transition",
			Description: "Deterministically validate one before/click/after browser transition without deciding whether the owner goal is satisfied.",
			InputSchema: schema("object", []string{"before_snapshot_id", "after_snapshot_id", "element_ref"}, map[string]any{
				"schema_version":     integerSchema(),
				"before_snapshot_id": stringSchema(),
				"after_snapshot_id":  stringSchema(),
				"element_ref":        stringSchema(),
			}),
			OutputSchema: objectSchema([]string{"schema_version", "status", "code", "before_snapshot_id", "after_snapshot_id", "state_changed", "session_generation"}, map[string]any{
				"schema_version":     integerSchema(),
				"status":             stringSchema(),
				"code":               stringSchema(),
				"before_snapshot_id": stringSchema(),
				"after_snapshot_id":  stringSchema(),
				"page_id":            stringSchema(),
				"element_ref":        stringSchema(),
				"session_generation": integerSchema(),
				"state_changed":      booleanSchema(),
				"before_digest":      stringSchema(),
				"after_digest":       stringSchema(),
				"click_count":        integerSchema(),
			}),
			Risk: app.RiskRead, RequiresApproval: false, Idempotent: true,
			TimeoutMS: 5000, Sandbox: "forbidden", Audit: "always",
		},
		{
			Name:        "browser.assess_goal",
			Description: "Assess the frozen browser goal against one current snapshot using explicit snapshot-owned evidence citations. A matching clickable control proves only that a next action is available, so use progress until current page state proves the requested effect. Use satisfied, success, or succeeded only when the cited snapshot proves the frozen goal is met.",
			InputSchema: schema("object", []string{"snapshot_id", "verdict", "evidence_refs"}, map[string]any{
				"snapshot_id":   stringSchema(),
				"verdict":       map[string]any{"type": "string", "enum": []any{"satisfied", "success", "succeeded", "progress", "failure"}},
				"evidence_refs": stringArraySchema(),
				"reason":        stringSchema(),
			}),
			OutputSchema: objectSchema([]string{"schema_version", "status", "code", "snapshot_id", "goal_satisfied", "evidence_refs", "reason_code"}, map[string]any{
				"schema_version":     integerSchema(),
				"status":             stringSchema(),
				"code":               stringSchema(),
				"snapshot_id":        stringSchema(),
				"page_id":            stringSchema(),
				"session_generation": integerSchema(),
				"goal_satisfied":     booleanSchema(),
				"evidence_refs":      stringArraySchema(),
				"reason":             stringSchema(),
				"reason_code":        stringSchema(),
				"click_count":        integerSchema(),
			}),
			Risk: app.RiskRead, RequiresApproval: false, Idempotent: true,
			TimeoutMS: 5000, Sandbox: "forbidden", Audit: "always",
		},
		browserAutomationDefinition("browser.type", "Fill one ordinary draftable field using a ref from the latest bound snapshot.", app.RiskDraft, true, []string{"uid", "page_id", "snapshot_id", "session_generation", "page_generation", "text"}, nil, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		browserAutomationDefinition("browser.select", "Select one ordinary reversible value using a ref from the latest bound snapshot.", app.RiskDraft, true, []string{"uid", "page_id", "snapshot_id", "session_generation", "page_generation", "value"}, nil, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		{
			Name:        "shell.exec_sandboxed",
			Description: "Request sandboxed shell execution. MVP queues this for approval and does not execute automatically.",
			InputSchema: schema("object", []string{"command"}, map[string]any{
				"command":    map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"status", "backend", "network", "stdout", "stderr"}, map[string]any{
				"status":  stringSchema(),
				"backend": stringSchema(),
				"network": stringSchema(),
				"stdout":  stringSchema(),
				"stderr":  stringSchema(),
			}),
			Risk:             app.RiskDangerous,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        10000,
			Sandbox:          "required",
			Audit:            "always",
		},
		{
			Name:        "notify.ask_approval",
			Description: "Create an approval request for a pending action.",
			InputSchema: schema("object", []string{"summary"}, map[string]any{
				"summary": map[string]any{"type": "string"},
				"reason":  map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"status", "approval_id", "tool_call"}, map[string]any{
				"status":      stringSchema(),
				"approval_id": stringSchema(),
				"tool_call":   stringSchema(),
			}),
			Risk:             app.RiskDraft,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "reminders.create",
			Description: "Create a scheduled owner request. At the due time it re-enters Agent routing and returns the result to the selected reminder endpoint. Default to the current external chat endpoint, otherwise web.",
			InputSchema: schema("object", []string{"text", "due_time"}, map[string]any{
				"text":       stringSchema(),
				"due_time":   stringSchema(),
				"timezone":   stringSchema(),
				"channel":    stringSchema(),
				"recipient":  stringSchema(),
				"recurrence": stringSchema(),
				"dedupe_key": stringSchema(),
			}),
			OutputSchema:     reminderOutputSchema(),
			Risk:             app.RiskReversible,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "reminders.list",
			Description: "List local reminders by optional status and time range.",
			InputSchema: schema("object", []string{}, map[string]any{
				"status":    stringSchema(),
				"from_time": stringSchema(),
				"to_time":   stringSchema(),
				"limit":     map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"reminders", "count"}, map[string]any{
				"reminders": arraySchema(objectValueSchema()),
				"count":     integerSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "reminders.update",
			Description: "Update a pending local reminder without sending it immediately.",
			InputSchema: schema("object", []string{"reminder_id", "expected_updated_at"}, map[string]any{
				"reminder_id":         stringSchema(),
				"expected_updated_at": stringSchema(),
				"text":                stringSchema(),
				"due_time":            stringSchema(),
				"timezone":            stringSchema(),
				"channel":             stringSchema(),
				"recipient":           stringSchema(),
				"recurrence":          stringSchema(),
			}),
			OutputSchema:     reminderOutputSchema(),
			Risk:             app.RiskReversible,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "reminders.cancel",
			Description: "Cancel a pending local reminder.",
			InputSchema: schema("object", []string{"reminder_id", "expected_updated_at"}, map[string]any{
				"reminder_id":         stringSchema(),
				"expected_updated_at": stringSchema(),
				"reason":              stringSchema(),
			}),
			OutputSchema:     reminderOutputSchema(),
			Risk:             app.RiskReversible,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        1000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
	}
}

func reminderOutputSchema() map[string]any {
	return objectSchema([]string{"reminder_id", "text", "text_summary", "due_time", "timezone", "channel", "status", "updated_at"}, map[string]any{
		"reminder_id":      stringSchema(),
		"text":             stringSchema(),
		"text_summary":     stringSchema(),
		"due_time":         stringSchema(),
		"timezone":         stringSchema(),
		"channel":          stringSchema(),
		"recurrence":       stringSchema(),
		"recipient":        stringSchema(),
		"status":           stringSchema(),
		"created_at":       stringSchema(),
		"updated_at":       stringSchema(),
		"canceled_at":      stringSchema(),
		"last_delivery_id": stringSchema(),
		"last_error":       stringSchema(),
	})
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return schema("object", required, properties)
}

func strictObjectSchema(required []string, properties map[string]any) map[string]any {
	out := schema("object", required, properties)
	out["additionalProperties"] = false
	return out
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func integerSchema() map[string]any {
	return map[string]any{"type": "integer"}
}

func booleanSchema() map[string]any {
	return map[string]any{"type": "boolean"}
}

func objectValueSchema() map[string]any {
	return map[string]any{"type": "object"}
}

func arraySchema(item map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

func stringArraySchema() map[string]any {
	return arraySchema(stringSchema())
}

func scalarValueSchema() map[string]any {
	return map[string]any{"type": []any{"string", "number", "integer", "boolean", "null"}}
}

func browserAutomationDefinition(name, description string, risk app.RiskLevel, approval bool, required []string, extraInput []string, outputRequired []string) app.ToolDefinition {
	return app.ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: schema("object", required, browserAutomationInputProperties(required, extraInput)),
		OutputSchema: objectSchema(outputRequired, map[string]any{
			"tool":            stringSchema(),
			"raw_tool":        stringSchema(),
			"arguments":       objectValueSchema(),
			"output":          objectValueSchema(),
			"text":            stringSchema(),
			"pages":           arraySchema(objectValueSchema()),
			"browser_mode":    stringSchema(),
			"presentation":    stringSchema(),
			"surface_visible": booleanSchema(),
			"untrusted":       booleanSchema(),
			"provider":        stringSchema(),
			"duration_ms":     integerSchema(),
		}),
		Risk:             risk,
		RequiresApproval: approval,
		Idempotent:       risk == app.RiskRead,
		TimeoutMS:        30000,
		Sandbox:          "forbidden",
		Audit:            "always",
	}
}

func browserAutomationInputProperties(required []string, extra []string) map[string]any {
	all := slices.Clone(required)
	all = append(all, extra...)
	all = append(all, "mode", "target_kind", "focused", "current_focus", "rich_text", "timeout_ms", "reason", "browser_mode", "presentation", "surface_visible", "disable_hidden_browser", "visible_browser")
	out := map[string]any{}
	for _, field := range all {
		switch field {
		case "page_id":
			out[field] = map[string]any{"type": []any{"string", "number"}}
		case "uid", "url", "text", "value", "mode", "target_kind", "reason", "browser_mode", "presentation", "browser_page_ref", "filePath", "snapshot_id", "expected_effect", "interaction_goal":
			out[field] = stringSchema()
		case "focused", "current_focus", "rich_text", "surface_visible", "disable_hidden_browser", "visible_browser", "verbose":
			out[field] = booleanSchema()
		case "session_generation", "page_generation":
			out[field] = map[string]any{"type": []any{"string", "number"}}
		case "timeout_ms":
			out[field] = map[string]any{"type": "number"}
		}
	}
	return out
}

func schema(kind string, required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 kind,
		"required":             required,
		"properties":           properties,
		"additionalProperties": true,
	}
}

func validateInput(def app.ToolDefinition, args map[string]any) error {
	if args == nil {
		args = map[string]any{}
	}
	schemaArgs := args
	if _, ok := args["_verifier"]; ok {
		schemaArgs = make(map[string]any, len(args)-1)
		for key, value := range args {
			if key != "_verifier" {
				schemaArgs[key] = value
			}
		}
	}
	if err := validateSchemaValue(schemaArgs, def.InputSchema, "arguments"); err != nil {
		return fmt.Errorf("%s %w", def.Name, err)
	}
	if def.Name == "pdf.transform" {
		if err := validatePDFTransformArguments(schemaArgs); err != nil {
			return err
		}
	}
	return nil
}

func validateOutput(def app.ToolDefinition, output any) error {
	if len(def.OutputSchema) == 0 {
		return nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("%s output schema violation: output is not JSON serializable: %w", def.Name, err)
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return fmt.Errorf("%s output schema violation: output is not JSON decodable: %w", def.Name, err)
	}
	if err := validateSchemaValue(normalized, def.OutputSchema, "output"); err != nil {
		return fmt.Errorf("%s output schema violation: %w", def.Name, err)
	}
	return nil
}

func validateSchemaValue(value any, schema map[string]any, path string) error {
	if len(schema) == 0 {
		return nil
	}
	types := schemaTypes(schema["type"])
	if len(types) > 0 && !matchesAnyType(value, types) {
		return fmt.Errorf("%s must be %s", path, strings.Join(types, " or "))
	}
	if rawEnum, ok := schema["enum"]; ok && !matchesEnum(value, rawEnum) {
		return fmt.Errorf("%s must be one of %s", path, enumValues(rawEnum))
	}
	if object, ok := value.(map[string]any); ok {
		if err := validateObject(object, schema, path); err != nil {
			return err
		}
	}
	if items, ok := arrayItems(value); ok {
		if err := validateArray(items, schema, path); err != nil {
			return err
		}
	}
	if text, ok := value.(string); ok {
		if err := validateString(text, schema, path); err != nil {
			return err
		}
	}
	if number, ok := numberValue(value); ok {
		if err := validateNumber(number, schema, path); err != nil {
			return err
		}
	}
	return nil
}

func validateObject(object map[string]any, schema map[string]any, path string) error {
	props := schemaMap(schema["properties"])
	for _, name := range stringList(schema["required"]) {
		if value, ok := object[name]; !ok || value == nil {
			return fmt.Errorf("%s requires %q", path, name)
		}
	}
	additional := schema["additionalProperties"]
	for key, value := range object {
		propSchema, ok := props[key]
		if ok {
			if err := validateSchemaValue(value, propSchema, path+"."+key); err != nil {
				return err
			}
			continue
		}
		if allowed, ok := additional.(bool); ok && !allowed {
			return fmt.Errorf("%s.%s is not allowed", path, key)
		}
		if additionalSchema, ok := additional.(map[string]any); ok {
			if err := validateSchemaValue(value, additionalSchema, path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArray(items []any, schema map[string]any, path string) error {
	if min, ok := intConstraint(schema["minItems"]); ok && len(items) < min {
		return fmt.Errorf("%s must have at least %d item(s)", path, min)
	}
	if max, ok := intConstraint(schema["maxItems"]); ok && len(items) > max {
		return fmt.Errorf("%s must have at most %d item(s)", path, max)
	}
	itemSchema, ok := schema["items"].(map[string]any)
	if !ok {
		return nil
	}
	for i, item := range items {
		if err := validateSchemaValue(item, itemSchema, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}

func validateString(value string, schema map[string]any, path string) error {
	length := len([]rune(value))
	if min, ok := intConstraint(schema["minLength"]); ok && length < min {
		return fmt.Errorf("%s must be at least %d character(s)", path, min)
	}
	if max, ok := intConstraint(schema["maxLength"]); ok && length > max {
		return fmt.Errorf("%s must be at most %d character(s)", path, max)
	}
	return nil
}

func validateNumber(value float64, schema map[string]any, path string) error {
	if min, ok := numberConstraint(schema["minimum"]); ok && value < min {
		return fmt.Errorf("%s must be >= %s", path, formatNumber(min))
	}
	if max, ok := numberConstraint(schema["maximum"]); ok && value > max {
		return fmt.Errorf("%s must be <= %s", path, formatNumber(max))
	}
	return nil
}

func schemaTypes(raw any) []string {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := []string{}
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

func matchesAnyType(v any, types []string) bool {
	for _, typ := range types {
		if matchesType(v, typ) {
			return true
		}
	}
	return false
}

func matchesType(v any, typ string) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := numberValue(v)
		return ok
	case "integer":
		return isInteger(v)
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := arrayItems(v)
		return ok
	case "null":
		return v == nil
	default:
		return true
	}
}

func schemaMap(raw any) map[string]map[string]any {
	out := map[string]map[string]any{}
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for key, value := range rawMap {
		if nested, ok := value.(map[string]any); ok {
			out[key] = nested
		}
	}
	return out
}

func stringList(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := []string{}
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func arrayItems(raw any) ([]any, bool) {
	if raw == nil {
		return nil, false
	}
	value := reflect.ValueOf(raw)
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, false
	}
	items := make([]any, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		items = append(items, value.Index(i).Interface())
	}
	return items, true
}

func numberValue(raw any) (float64, bool) {
	switch v := raw.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func isInteger(raw any) bool {
	switch v := raw.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return float64(v) == float64(int64(v))
	case float64:
		return v == float64(int64(v))
	case json.Number:
		_, err := v.Int64()
		return err == nil
	default:
		return false
	}
}

func numberConstraint(raw any) (float64, bool) {
	return numberValue(raw)
}

func intConstraint(raw any) (int, bool) {
	n, ok := numberValue(raw)
	if !ok {
		return 0, false
	}
	return int(n), true
}

func matchesEnum(value, rawEnum any) bool {
	items, ok := arrayItems(rawEnum)
	if !ok {
		return true
	}
	for _, item := range items {
		if valuesEqual(value, item) {
			return true
		}
	}
	return false
}

func valuesEqual(a, b any) bool {
	if an, ok := numberValue(a); ok {
		if bn, ok := numberValue(b); ok {
			return an == bn
		}
	}
	return reflect.DeepEqual(a, b)
}

func enumValues(rawEnum any) string {
	items, ok := arrayItems(rawEnum)
	if !ok {
		return "[]"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprint(item))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func (h *ToolHub) filesSearch(ctx context.Context, args map[string]any) (Result, error) {
	query := strings.TrimSpace(stringArg(args, "query", ""))
	if query == "" {
		return Result{}, errors.New("query cannot be empty")
	}
	root, err := h.resolveRoot(stringArg(args, "root", ""))
	if err != nil {
		return Result{}, err
	}
	maxResults := intArg(args, "max_results", 20)
	if maxResults <= 0 || maxResults > 100 {
		maxResults = 20
	}
	search, err := workspacefiles.Search(ctx, root, workspacefiles.SearchRequest{
		Mode: workspacefiles.MatchFuzzy, Term: query, MaxResults: maxResults,
		MaxEntries: 20000, MaxDepth: 32, Timeout: 3 * time.Second,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"query": query, "results": search.Matches, "count": len(search.Matches), "complete": search.Complete, "truncated": search.Truncated,
	}}, nil
}

func (h *ToolHub) filesRead(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	path, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	maxBytes := intArg(args, "max_bytes", document.SmallExtractedMaxBytes)
	if maxBytes <= 0 || maxBytes > document.SmallExtractedMaxBytes {
		maxBytes = document.SmallExtractedMaxBytes
	}
	read, err := h.readDocumentWorkflow(withDocumentOCRExecution(ctx, sessionID, runID), path, maxBytes, document.EnrichmentOptions{
		ImageAnalysis: stringArg(args, "image_analysis", "targeted"),
		TargetPaths:   outputStringArray(args["image_target_paths"]),
		Question:      stringArg(args, "image_question", ""),
		Required:      boolArg(args, "image_required", false),
	})
	if err != nil {
		return Result{}, err
	}
	output, err := documentReadOutput(read, maxBytes)
	return Result{Output: output}, err
}

func textDocumentReadEnvelope(content string, truncated bool, maxBytes int) map[string]any {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if normalized == "" {
		lines = []string{}
	}
	blocks := []map[string]any{}
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lineNumber := index + 1
		blocks = append(blocks, map[string]any{
			"text": line,
			"location": map[string]any{
				"part":        "document",
				"block_type":  "line",
				"block_index": len(blocks) + 1,
				"line_start":  lineNumber,
				"line_end":    lineNumber,
				"path":        fmt.Sprintf("document.line[%d]", lineNumber),
			},
		})
	}
	evidenceBlocks := evidenceBlocksFromDocumentBlocks("", "text", blocks)
	mode := "full"
	reason := "default_full_read"
	if truncated {
		mode = "byte_limited"
		reason = "max_bytes_exceeded"
	}
	newlineStyle := "lf"
	if strings.Contains(content, "\r\n") {
		newlineStyle = "crlf"
	} else if strings.Contains(content, "\r") {
		newlineStyle = "cr"
	}
	return map[string]any{
		"schema_version": "document_read_v1",
		"format":         "text",
		"source":         "plain_text",
		"content_scope":  map[string]any{"kind": "full_document", "complete": !truncated},
		"strategy": map[string]any{
			"mode":       mode,
			"reason":     reason,
			"complete":   !truncated,
			"max_bytes":  maxBytes,
			"extensible": true,
		},
		"blocks":          blocks,
		"evidence_blocks": evidenceBlocks,
		"enrichment": map[string]any{
			"schema_version": document.EnrichmentSchemaVersion,
			"assets":         map[string]any{"images": []any{}, "charts": []any{}, "embedded_objects": []any{}},
			"annotations":    map[string]any{"comments": []any{}, "notes": []any{}, "hyperlinks": []any{}},
			"layout": map[string]any{
				"sections": []any{}, "page_settings": []any{map[string]any{
					"encoding": "utf-8", "bom": strings.HasPrefix(content, "\ufeff"), "newline_style": newlineStyle,
				}}, "slide_layouts": []any{}, "merged_ranges": []any{},
			},
			"extensions": map[string]any{"status": "deferred", "parts": []any{}},
			"coverage":   map[string]any{"content": "complete", "assets": "complete", "annotations": "complete", "layout": "complete", "extensions": "deferred"},
		},
		"stats": map[string]any{
			"blocks":   len(blocks),
			"complete": !truncated,
		},
	}
}

func attachEvidenceBlocks(document map[string]any, documentID, fileType string) {
	if document == nil {
		return
	}
	blocks := documentAnySlice(document["blocks"])
	if len(blocks) == 0 {
		return
	}
	document["evidence_blocks"] = evidenceBlocksFromAnyBlocks(documentID, fileType, blocks)
}

func evidenceBlocksFromDocumentBlocks(documentID, fileType string, blocks []map[string]any) []map[string]any {
	raw := make([]any, 0, len(blocks))
	for _, block := range blocks {
		raw = append(raw, block)
	}
	return evidenceBlocksFromAnyBlocks(documentID, fileType, raw)
}

func evidenceBlocksFromAnyBlocks(documentID, fileType string, blocks []any) []map[string]any {
	out := []map[string]any{}
	headingPath := []string{}
	for i, item := range blocks {
		block, ok := documentAnyMap(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(documentStringValue(block["text"]))
		if text == "" || text == "<nil>" {
			continue
		}
		location, _ := documentAnyMap(block["location"])
		blockType := evidenceBlockType(fileType, block, location, text)
		if blockType == "heading" {
			headingPath = appendHeadingPath(headingPath, text)
		}
		blockID := evidenceBlockID(location, i+1)
		normalizedLocation := evidenceBlockLocation(location, headingPath)
		evidence := map[string]any{
			"blockId":    blockID,
			"documentId": documentID,
			"fileType":   fileType,
			"type":       blockType,
			"text":       text,
			"location":   normalizedLocation,
			"sourceHash": sourceHash(text),
		}
		out = append(out, evidence)
	}
	return out
}

func evidenceBlockType(fileType string, block map[string]any, location map[string]any, text string) string {
	if blockType := strings.TrimSpace(documentStringValue(location["block_type"])); blockType == "table_cell" {
		return "table_cell"
	}
	style := strings.ToLower(strings.TrimSpace(documentStringValue(block["style"])))
	if strings.Contains(style, "heading") || looksDocumentHeading(text) {
		return "heading"
	}
	switch fileType {
	case "pdf":
		return "pdf_text"
	case "pptx":
		return "slide_text"
	default:
		return "paragraph"
	}
}

func looksDocumentHeading(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 40 {
		return false
	}
	prefixes := []string{"一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func appendHeadingPath(path []string, heading string) []string {
	next := append([]string(nil), path...)
	if len(next) == 0 {
		return []string{heading}
	}
	next[len(next)-1] = heading
	return next
}

func evidenceBlockID(location map[string]any, fallback int) string {
	if path := strings.TrimSpace(documentStringValue(location["path"])); path != "" && path != "<nil>" {
		return path
	}
	return fmt.Sprintf("blk_%d", fallback)
}

func evidenceBlockLocation(location map[string]any, headingPath []string) map[string]any {
	out := map[string]any{}
	if value := documentIntValue(location["page_number"]); value > 0 {
		out["pageNumber"] = value
	}
	if value := documentIntValue(location["paragraph_index"]); value > 0 {
		out["paragraphIndex"] = value
	}
	if value := documentIntValue(location["table_index"]); value > 0 {
		out["tableId"] = fmt.Sprintf("table_%d", value)
	}
	if value := documentIntValue(location["row_index"]); value > 0 {
		out["rowIndex"] = value
	}
	if value := documentIntValue(location["cell_index"]); value > 0 {
		out["columnIndex"] = value
	}
	if value := documentIntValue(location["slide_number"]); value > 0 {
		out["slideNumber"] = value
	}
	if len(headingPath) > 0 {
		out["headingPath"] = append([]string(nil), headingPath...)
		out["sectionPath"] = append([]string(nil), headingPath...)
	}
	if path := strings.TrimSpace(documentStringValue(location["path"])); path != "" && path != "<nil>" {
		out["path"] = path
	}
	return out
}

func sourceHash(text string) string {
	sum := sha1.Sum([]byte(text))
	return "sha1:" + hex.EncodeToString(sum[:])
}

func (h *ToolHub) filesWriteDraft(ctx context.Context, args map[string]any) (Result, error) {
	content := stringArg(args, "content", "")
	if content == "" {
		return Result{}, errors.New("content cannot be empty")
	}
	pathArg := stringArg(args, "path", "")
	if pathArg == "" {
		pathArg = filepath.Join(".sparkclaw", "drafts", "draft-"+strconv.FormatInt(time.Now().Unix(), 10)+".md")
	}
	path, err := h.resolveDraftPath(pathArg)
	if err != nil {
		return Result{}, err
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{"path": path, "bytes": len(content), "status": "draft_written"}}, nil
}

func (h *ToolHub) memorySearch(ctx context.Context, args map[string]any, sessionID string) (Result, error) {
	if _, err := h.applyMemoryRetention(ctx); err != nil {
		return Result{}, err
	}
	query := stringArg(args, "query", "")
	memories, err := h.store.SearchMemories(ctx, query)
	if err != nil {
		return Result{}, err
	}
	ownerID, err := h.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	filtered := memories[:0]
	for _, memory := range memories {
		visible, err := h.memoryVisibleToOwner(ctx, memory, ownerID)
		if err != nil {
			return Result{}, err
		}
		if visible {
			filtered = append(filtered, memory)
		}
	}
	memories = filtered
	return Result{Output: map[string]any{"query": query, "results": memories, "count": len(memories)}}, nil
}

func (h *ToolHub) ownerIDForSession(ctx context.Context, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" || h.store == nil {
		return app.DefaultOwnerID, nil
	}
	session, ok, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("resolve session owner: %w", err)
	}
	if ok && strings.TrimSpace(session.OwnerID) != "" {
		return strings.TrimSpace(session.OwnerID), nil
	}
	return app.DefaultOwnerID, nil
}

func (h *ToolHub) sessionVisibleToOwner(ctx context.Context, sessionID, ownerID string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return ownerID == "" || ownerID == app.DefaultOwnerID, nil
	}
	session, ok, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("resolve visible session: %w", err)
	}
	if !ok {
		return ownerID == app.DefaultOwnerID, nil
	}
	sessionOwner := strings.TrimSpace(session.OwnerID)
	if sessionOwner == "" {
		sessionOwner = app.DefaultOwnerID
	}
	if strings.TrimSpace(ownerID) == "" {
		ownerID = app.DefaultOwnerID
	}
	return sessionOwner == ownerID, nil
}

func (h *ToolHub) memoryVisibleToOwner(ctx context.Context, memory app.Memory, ownerID string) (bool, error) {
	if strings.TrimSpace(memory.SourceID) == "" {
		return ownerID == "" || ownerID == app.DefaultOwnerID, nil
	}
	run, ok, err := h.store.GetRun(ctx, memory.SourceID)
	if err != nil {
		return false, fmt.Errorf("load memory source run: %w", err)
	}
	if !ok {
		return ownerID == "" || ownerID == app.DefaultOwnerID, nil
	}
	return h.sessionVisibleToOwner(ctx, run.SessionID, ownerID)
}

func (h *ToolHub) memoryWriteCandidate(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	content := stringArg(args, "content", "")
	if content == "" {
		return Result{}, errors.New("content cannot be empty")
	}
	if !h.cfg.Memory.AllowSensitiveMemory {
		if pattern, ok := h.memorySensitivePattern(content, stringArg(args, "sensitivity", "")); ok {
			return Result{}, fmt.Errorf("memory candidate appears sensitive (%s); sensitive memory is disabled", pattern)
		}
	}
	candidate, err := h.store.AddMemoryCandidate(ctx, app.MemoryCandidate{
		SessionID:   sessionID,
		RunID:       runID,
		Kind:        stringArg(args, "kind", "profile"),
		Content:     content,
		Sensitivity: stringArg(args, "sensitivity", "normal"),
		Reason:      stringArg(args, "reason", "User asked SparkClaw to remember this."),
		Status:      "pending",
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: candidate}, nil
}

func (h *ToolHub) memoryWriteSensitive(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	content := stringArg(args, "content", "")
	if content == "" {
		return Result{}, errors.New("content cannot be empty")
	}
	kind := stringArg(args, "kind", "profile")
	memoryCandidate, err := h.store.AddMemoryCandidate(ctx, app.MemoryCandidate{
		SessionID:   sessionID,
		RunID:       runID,
		Kind:        kind,
		Content:     content,
		Sensitivity: "sensitive",
		Reason:      stringArg(args, "reason", "Owner approved writing sensitive memory."),
		Status:      "pending",
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return Result{}, err
	}
	candidate, memory, err := h.store.ResolveMemoryCandidate(ctx, memoryCandidate.ID, "accepted")
	if err != nil {
		return Result{}, err
	}
	if memory == nil {
		return Result{}, errors.New("sensitive memory was not accepted")
	}
	out := map[string]any{
		"id":            memory.ID,
		"kind":          memory.Kind,
		"content":       memory.Content,
		"source_run_id": memory.SourceID,
		"created_at":    memory.CreatedAt.Format(time.RFC3339),
		"sensitivity":   candidate.Sensitivity,
	}
	return Result{Output: out}, nil
}

func (h *ToolHub) memorySensitivePattern(content, sensitivity string) (string, bool) {
	if strings.EqualFold(strings.TrimSpace(sensitivity), "sensitive") {
		return "sensitivity", true
	}
	lower := strings.ToLower(content)
	for _, pattern := range h.cfg.Memory.RedactPatterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(lower, pattern) {
			return pattern, true
		}
	}
	return "", false
}

func (h *ToolHub) applyMemoryRetention(ctx context.Context) ([]app.Memory, error) {
	if h.cfg.Memory.RetentionDays <= 0 {
		return []app.Memory{}, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -h.cfg.Memory.RetentionDays)
	return h.store.PruneMemories(ctx, cutoff)
}

func (h *ToolHub) resolveRoot(root string) (string, error) {
	if root == "" {
		root = h.cfg.Workspaces.DefaultRoot
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if !h.allowed(abs) {
		return "", fmt.Errorf("path %s is outside workspace allowlist", abs)
	}
	return abs, nil
}

func (h *ToolHub) resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	path = h.normalizePossiblyMissingRootSlash(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.cfg.Workspaces.DefaultRoot, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !h.allowed(abs) {
		return "", fmt.Errorf("path %s is outside workspace allowlist", abs)
	}
	return abs, nil
}

func (h *ToolHub) normalizePossiblyMissingRootSlash(path string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	if filepath.IsAbs(clean) {
		return path
	}
	candidate := string(os.PathSeparator) + clean
	abs, err := filepath.Abs(candidate)
	if err != nil || !h.allowed(abs) {
		return path
	}
	return candidate
}

func (h *ToolHub) resolveDraftPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return h.resolvePath(path)
	}
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, "..") {
		return "", errors.New("draft path cannot escape workspace")
	}
	return h.resolvePath(clean)
}

func (h *ToolHub) allowed(abs string) bool {
	for _, root := range h.cfg.Workspaces.Allowlist {
		cleanRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if abs == cleanRoot || strings.HasPrefix(abs, cleanRoot+string(os.PathSeparator)) {
			return true
		}
	}
	return false
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
