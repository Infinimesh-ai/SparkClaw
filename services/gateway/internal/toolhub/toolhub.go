package toolhub

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
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

type ToolHub struct {
	cfg         config.Config
	store       store.Store
	defs        map[string]app.ToolDefinition
	models      modelrouter.Router
	runner      sandbox.Runner
	artifacts   artifact.Store
	reminders   *remindertarget.Resolver
	webSearch   websearch.Adapter
	weatherInfo WeatherInfoAdapter
	browser     browserautomation.Adapter
	ocr         documentocr.Adapter
	documents   *document.Pipeline
}

type Result struct {
	Output any
}

type WeatherInfoAdapter interface {
	Weather(context.Context, infinimeshinfo.WeatherRequest) (infinimeshinfo.WeatherResponse, error)
}

func New(cfg config.Config, st store.Store) *ToolHub {
	infoCfg := cfg.Plugins.Entries.InfinimeshInfo.Config
	// A failed constructor must leave the interface field nil, not hold a
	// typed-nil *Client that defeats the availability guard in lookupWeather.
	var weatherInfo WeatherInfoAdapter
	if client, err := infinimeshinfo.NewClient(infinimeshinfo.Config{
		BaseURL:              infoCfg.BaseURL,
		EntitlementProof:     infoCfg.EntitlementProof,
		DeviceAttestation:    infoCfg.DeviceAttestation,
		LicenseProof:         infoCfg.LicenseProof,
		TokenBatchSize:       infoCfg.TokenBatchSize,
		MaxAttempts:          infoCfg.MaxAttempts,
		RetryBaseDelay:       time.Duration(infoCfg.RetryBaseDelayMS) * time.Millisecond,
		RequestTimeout:       time.Duration(infoCfg.RequestTimeoutSeconds) * time.Second,
		ResponseBodyMaxBytes: infoCfg.ResponseBodyMaxBytes,
	}, nil); err == nil {
		weatherInfo = client
	}
	ocrAdapter, err := documentocr.New(cfg.Adapters.DocumentOCR)
	if err != nil {
		slog.Warn("document OCR adapter unavailable; continuing with OCR disabled", "error", err)
		ocrAdapter = documentocr.Disabled()
	}
	h := &ToolHub{
		cfg:         cfg,
		store:       st,
		defs:        map[string]app.ToolDefinition{},
		models:      modelrouter.New(cfg),
		runner:      sandbox.NewRunner(cfg),
		artifacts:   artifact.NewStore(cfg.Storage),
		reminders:   remindertarget.NewResolver(st),
		webSearch:   websearch.NewAdapter(cfg),
		weatherInfo: weatherInfo,
		browser:     browserautomation.NewAdapter(cfg),
		ocr:         ocrAdapter,
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
		h.defs[def.Name] = def
	}
	return h
}

// Close releases resources held by tool adapters. Safe to call multiple times.
func (h *ToolHub) Close() error {
	var errs []error
	if h.browser != nil {
		errs = append(errs, h.browser.Close())
	}
	if h.ocr != nil {
		errs = append(errs, h.ocr.Close())
	}
	return errors.Join(errs...)
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
	return h
}

func (h *ToolHub) Definitions() []app.ToolDefinition {
	defs := make([]app.ToolDefinition, 0, len(h.defs))
	for _, def := range h.defs {
		defs = append(defs, def)
	}
	slices.SortFunc(defs, func(a, b app.ToolDefinition) int {
		return strings.Compare(a.Name, b.Name)
	})
	return defs
}

func (h *ToolHub) Definition(name string) (app.ToolDefinition, bool) {
	def, ok := h.defs[name]
	return def, ok
}

func (h *ToolHub) Config() config.Config {
	return h.cfg
}

func (h *ToolHub) forSession(sessionID string) *ToolHub {
	if strings.TrimSpace(sessionID) == "" || h.store == nil {
		return h
	}
	session, ok := h.store.GetSession(sessionID)
	if !ok || strings.TrimSpace(session.WorkspaceRoot) == "" {
		return h
	}
	root, err := filepath.Abs(session.WorkspaceRoot)
	if err != nil {
		return h
	}
	clone := *h
	clone.cfg = h.cfg
	clone.cfg.Workspaces.DefaultRoot = root
	if !containsPathRoot(clone.cfg.Workspaces.Allowlist, root) {
		clone.cfg.Workspaces.Allowlist = append(append([]string{}, clone.cfg.Workspaces.Allowlist...), root)
	}
	_ = os.MkdirAll(root, 0o755)
	return &clone
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
	def, ok := h.defs[name]
	if !ok {
		return fmt.Errorf("tool %q not found", name)
	}
	return validateInput(def, args)
}

func (h *ToolHub) Execute(ctx context.Context, name string, args map[string]any, sessionID, runID string) (Result, error) {
	def, ok := h.defs[name]
	if !ok {
		return Result{}, fmt.Errorf("tool %q not found", name)
	}
	if err := validateInput(def, args); err != nil {
		return Result{}, err
	}
	h = h.forSession(sessionID)
	reg, ok := toolRegistry[name]
	if !ok {
		return Result{}, fmt.Errorf("tool %q has no executor in MVP", name)
	}
	result, err := reg.run(h, ctx, name, args, sessionID, runID)
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
	for _, formatDefinitions := range [][]app.ToolDefinition{
		docxToolDefinitions(),
		pptxToolDefinitions(),
		xlsxToolDefinitions(),
		pdfToolDefinitions(),
	} {
		definitions = append(definitions, formatDefinitions...)
	}
	return append(definitions, defaultDefinitionsAfterDocumentFormats()...)
}

func defaultDefinitionsBeforeDocumentFormats() []app.ToolDefinition {
	return []app.ToolDefinition{
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
			Description: "Search file names and small text content inside an allowed workspace.",
			InputSchema: schema("object", []string{"query"}, map[string]any{
				"query":       map[string]any{"type": "string"},
				"root":        map[string]any{"type": "string"},
				"max_results": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"root", "query", "results", "count"}, map[string]any{
				"root":    stringSchema(),
				"query":   stringSchema(),
				"results": arraySchema(objectValueSchema()),
				"count":   integerSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        5000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "files.read",
			Description: "Inspect and completely parse one small workspace document into stable blocks, format-specific locations, and categorized high-level evidence. Optional OvisOCR2 page parsing augments explicitly selected images and scanned PDF pages; Fast remains responsible for visual semantics.",
			InputSchema: schema("object", []string{"path"}, map[string]any{
				"path":               map[string]any{"type": "string"},
				"max_bytes":          map[string]any{"type": "number"},
				"image_analysis":     map[string]any{"type": "string", "enum": []string{"none", "targeted", "all"}},
				"image_target_paths": stringArraySchema(),
				"image_question":     stringSchema(),
				"image_required":     booleanSchema(),
			}),
			OutputSchema: objectSchema([]string{"path", "kind", "content", "bytes", "source_bytes", "max_bytes", "truncated", "untrusted", "document"}, map[string]any{
				"path":         stringSchema(),
				"kind":         stringSchema(),
				"content":      stringSchema(),
				"bytes":        integerSchema(),
				"source_bytes": integerSchema(),
				"max_bytes":    integerSchema(),
				"truncated":    booleanSchema(),
				"untrusted":    booleanSchema(),
				"document":     objectValueSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        125000,
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
		{
			Name:        "text.replace_text",
			Description: "Replace explicit text pairs in a governed plain-text file and write a new file without overwriting the original.",
			InputSchema: schema("object", []string{"path", "replacements", "output_path"}, map[string]any{
				"path":        stringSchema(),
				"output_path": stringSchema(),
				"replacements": arraySchema(map[string]any{
					"type":     "object",
					"required": []string{"find", "replace"},
					"properties": map[string]any{
						"find":    stringSchema(),
						"replace": stringSchema(),
					},
				}),
				"expected_replacements": integerSchema(),
			}),
			OutputSchema: objectSchema([]string{"status", "path", "output_path", "replacements", "bytes", "change_summary", "untrusted"}, map[string]any{
				"status":         stringSchema(),
				"path":           stringSchema(),
				"output_path":    stringSchema(),
				"replacements":   integerSchema(),
				"bytes":          integerSchema(),
				"change_summary": objectValueSchema(),
				"untrusted":      booleanSchema(),
			}),
			Risk:             app.RiskReversible,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        5000,
			Sandbox:          "optional",
			Audit:            "always",
		},
		{
			Name:        "office.replace_text",
			Description: "Replace explicit text pairs in a workspace docx/xlsx/pptx and write a new Office file without overwriting the original.",
			InputSchema: schema("object", []string{"path", "replacements", "output_path"}, map[string]any{
				"path":          stringSchema(),
				"output_path":   stringSchema(),
				"source_sha256": stringSchema(),
				"replacements": arraySchema(map[string]any{
					"type":     "object",
					"required": []string{"find", "replace"},
					"properties": map[string]any{
						"find":    stringSchema(),
						"replace": stringSchema(),
					},
				}),
				"expected_replacements": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"status", "path", "output_path", "replacements", "bytes", "change_summary", "untrusted"}, map[string]any{
				"status":         stringSchema(),
				"path":           stringSchema(),
				"output_path":    stringSchema(),
				"replacements":   integerSchema(),
				"bytes":          integerSchema(),
				"details":        arraySchema(objectValueSchema()),
				"change_summary": objectValueSchema(),
				"untrusted":      booleanSchema(),
			}),
			Risk:             app.RiskReversible,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        5000,
			Sandbox:          "optional",
			Audit:            "always",
		},
	}
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
			OutputSchema: objectSchema([]string{"request_id", "query", "summary", "answer", "provider", "count", "results", "key_facts", "retrieved_at", "untrusted"}, map[string]any{
				"request_id":   stringSchema(),
				"query":        stringSchema(),
				"summary":      stringSchema(),
				"answer":       stringSchema(),
				"provider":     stringSchema(),
				"model":        stringSchema(),
				"count":        integerSchema(),
				"results":      arraySchema(objectValueSchema()),
				"key_facts":    arraySchema(objectValueSchema()),
				"citations":    stringArraySchema(),
				"retrieved_at": stringSchema(),
				"took_ms":      integerSchema(),
				"untrusted":    booleanSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        30000,
			Sandbox:          "forbidden",
			Audit:            "always",
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
			InputSchema: schema("object", []string{"snapshot_id", "verdict", "evidence_refs", "reason"}, map[string]any{
				"snapshot_id":   stringSchema(),
				"verdict":       map[string]any{"type": "string", "enum": []any{"satisfied", "success", "succeeded", "progress", "failure"}},
				"evidence_refs": stringArraySchema(),
				"reason":        stringSchema(),
			}),
			OutputSchema: objectSchema([]string{"schema_version", "status", "code", "snapshot_id", "goal_satisfied", "evidence_refs", "reason"}, map[string]any{
				"schema_version":     integerSchema(),
				"status":             stringSchema(),
				"code":               stringSchema(),
				"snapshot_id":        stringSchema(),
				"page_id":            stringSchema(),
				"session_generation": integerSchema(),
				"goal_satisfied":     booleanSchema(),
				"evidence_refs":      stringArraySchema(),
				"reason":             stringSchema(),
				"click_count":        integerSchema(),
			}),
			Risk: app.RiskRead, RequiresApproval: false, Idempotent: true,
			TimeoutMS: 5000, Sandbox: "forbidden", Audit: "always",
		},
		browserAutomationDefinition("browser.type", "Type or fill text into a clear element ref or current focus.", app.RiskDraft, true, []string{"text"}, []string{"uid"}, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
		browserAutomationDefinition("browser.select", "Select a dropdown or select-like value using a clear element ref.", app.RiskDraft, true, []string{"uid", "value"}, []string{"value"}, []string{"tool", "raw_tool", "output", "untrusted", "provider"}),
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
	if err := validateSchemaValue(args, def.InputSchema, "arguments"); err != nil {
		return fmt.Errorf("%s %w", def.Name, err)
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
	query := strings.ToLower(stringArg(args, "query", ""))
	if strings.TrimSpace(query) == "" {
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
	type fileResult struct {
		Path    string `json:"path"`
		Reason  string `json:"reason"`
		Preview string `json:"preview,omitempty"`
	}
	results := []fileResult{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name := d.Name()
		if d.IsDir() && skipDir(name) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		lowerRel := strings.ToLower(rel)
		if strings.Contains(lowerRel, query) {
			results = append(results, fileResult{Path: path, Reason: "filename"})
			return stopIfEnough(len(results), maxResults)
		}
		if !looksText(path) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) > 1_000_000 {
			return nil
		}
		content := strings.ToLower(string(raw))
		if idx := strings.Index(content, query); idx >= 0 {
			results = append(results, fileResult{Path: path, Reason: "content", Preview: preview(string(raw), idx, len(query))})
			return stopIfEnough(len(results), maxResults)
		}
		return nil
	})
	if errors.Is(err, errEnough) {
		err = nil
	}
	return Result{Output: map[string]any{"root": root, "query": query, "results": results, "count": len(results)}}, err
}

func (h *ToolHub) filesRead(ctx context.Context, args map[string]any) (Result, error) {
	path, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	maxBytes := intArg(args, "max_bytes", document.SmallExtractedMaxBytes)
	if maxBytes <= 0 || maxBytes > document.SmallExtractedMaxBytes {
		maxBytes = document.SmallExtractedMaxBytes
	}
	read, err := h.readDocumentWorkflow(ctx, path, maxBytes, document.EnrichmentOptions{
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

func (h *ToolHub) memorySearch(args map[string]any, sessionID string) (Result, error) {
	h.applyMemoryRetention()
	query := stringArg(args, "query", "")
	memories := h.store.SearchMemories(query)
	ownerID := h.ownerIDForSession(sessionID)
	filtered := memories[:0]
	for _, memory := range memories {
		if h.memoryVisibleToOwner(memory, ownerID) {
			filtered = append(filtered, memory)
		}
	}
	memories = filtered
	return Result{Output: map[string]any{"query": query, "results": memories, "count": len(memories)}}, nil
}

func (h *ToolHub) ownerIDForSession(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" || h.store == nil {
		return app.DefaultOwnerID
	}
	if session, ok := h.store.GetSession(sessionID); ok && strings.TrimSpace(session.OwnerID) != "" {
		return strings.TrimSpace(session.OwnerID)
	}
	return app.DefaultOwnerID
}

func (h *ToolHub) sessionVisibleToOwner(sessionID, ownerID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return ownerID == "" || ownerID == app.DefaultOwnerID
	}
	session, ok := h.store.GetSession(sessionID)
	if !ok {
		return ownerID == app.DefaultOwnerID
	}
	sessionOwner := strings.TrimSpace(session.OwnerID)
	if sessionOwner == "" {
		sessionOwner = app.DefaultOwnerID
	}
	if strings.TrimSpace(ownerID) == "" {
		ownerID = app.DefaultOwnerID
	}
	return sessionOwner == ownerID
}

func (h *ToolHub) memoryVisibleToOwner(memory app.Memory, ownerID string) bool {
	if strings.TrimSpace(memory.SourceID) == "" {
		return ownerID == "" || ownerID == app.DefaultOwnerID
	}
	run, ok := h.store.GetRun(memory.SourceID)
	if !ok {
		return ownerID == "" || ownerID == app.DefaultOwnerID
	}
	return h.sessionVisibleToOwner(run.SessionID, ownerID)
}

func (h *ToolHub) memoryWriteCandidate(args map[string]any, sessionID, runID string) (Result, error) {
	content := stringArg(args, "content", "")
	if content == "" {
		return Result{}, errors.New("content cannot be empty")
	}
	if !h.cfg.Memory.AllowSensitiveMemory {
		if pattern, ok := h.memorySensitivePattern(content, stringArg(args, "sensitivity", "")); ok {
			return Result{}, fmt.Errorf("memory candidate appears sensitive (%s); sensitive memory is disabled", pattern)
		}
	}
	candidate := h.store.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   sessionID,
		RunID:       runID,
		Kind:        stringArg(args, "kind", "profile"),
		Content:     content,
		Sensitivity: stringArg(args, "sensitivity", "normal"),
		Reason:      stringArg(args, "reason", "User asked SparkClaw to remember this."),
		Status:      "pending",
		CreatedAt:   time.Now().UTC(),
	})
	return Result{Output: candidate}, nil
}

func (h *ToolHub) memoryWriteSensitive(args map[string]any, sessionID, runID string) (Result, error) {
	content := stringArg(args, "content", "")
	if content == "" {
		return Result{}, errors.New("content cannot be empty")
	}
	kind := stringArg(args, "kind", "profile")
	memoryCandidate := h.store.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   sessionID,
		RunID:       runID,
		Kind:        kind,
		Content:     content,
		Sensitivity: "sensitive",
		Reason:      stringArg(args, "reason", "Owner approved writing sensitive memory."),
		Status:      "pending",
		CreatedAt:   time.Now().UTC(),
	})
	candidate, memory, err := h.store.ResolveMemoryCandidate(memoryCandidate.ID, "accepted")
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

func (h *ToolHub) applyMemoryRetention() []app.Memory {
	if h.cfg.Memory.RetentionDays <= 0 {
		return []app.Memory{}
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -h.cfg.Memory.RetentionDays)
	return h.store.PruneMemories(cutoff)
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

func (h *ToolHub) workspaceRelPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(path))
	}
	root, err := filepath.Abs(h.cfg.Workspaces.DefaultRoot)
	if err != nil || root == "" {
		return filepath.ToSlash(filepath.Clean(path))
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(rel)
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

func skipDir(name string) bool {
	switch name {
	case ".git", ".sparkclaw", "node_modules", "dist", "build", ".next", "vendor", ".venv":
		return true
	default:
		return false
	}
}

func looksText(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".go", ".ts", ".tsx", ".js", ".jsx", ".json", ".yaml", ".yml", ".toml", ".css", ".html", ".sh", ".py":
		return true
	default:
		return false
	}
}

var errEnough = errors.New("enough results")

func stopIfEnough(count, max int) error {
	if count >= max {
		return errEnough
	}
	return nil
}

func preview(content string, idx, queryLen int) string {
	start := idx - 80
	if start < 0 {
		start = 0
	}
	end := idx + queryLen + 160
	if end > len(content) {
		end = len(content)
	}
	return strings.TrimSpace(content[start:end])
}
