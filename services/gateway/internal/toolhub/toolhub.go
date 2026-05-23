package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/personaldata"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/sandbox"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type ToolHub struct {
	cfg       config.Config
	store     store.Store
	defs      map[string]app.ToolDefinition
	models    modelrouter.Router
	runner    sandbox.Runner
	artifacts artifact.Store
	email     personaldata.EmailAdapter
	cal       personaldata.CalendarAdapter
}

type Result struct {
	Output any
}

func New(cfg config.Config, st store.Store) *ToolHub {
	h := &ToolHub{
		cfg:       cfg,
		store:     st,
		defs:      map[string]app.ToolDefinition{},
		models:    modelrouter.New(cfg),
		runner:    sandbox.NewRunner(cfg),
		artifacts: artifact.NewStore(cfg.Storage),
		email:     personaldata.NewEmailAdapter(cfg.Adapters.Email, cfg.Workspaces.DefaultRoot),
		cal:       personaldata.NewCalendarAdapter(cfg.Adapters.Calendar, cfg.Workspaces.DefaultRoot),
	}
	for _, def := range defaultDefinitions() {
		h.defs[def.Name] = def
	}
	return h
}

func (h *ToolHub) WithArtifactStore(artifacts artifact.Store) *ToolHub {
	h.artifacts = artifacts
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
	var result Result
	var err error
	switch name {
	case "files.search":
		result, err = h.filesSearch(ctx, args)
	case "files.read":
		result, err = h.filesRead(ctx, args)
	case "files.write_draft":
		result, err = h.filesWriteDraft(ctx, args)
	case "file.delete":
		result, err = h.fileDelete(ctx, args)
	case "memory.search":
		result, err = h.memorySearch(args)
	case "memory.write_candidate", "memory.propose":
		result, err = h.memoryWriteCandidate(args, sessionID, runID)
	case "memory.write_sensitive":
		result, err = h.memoryWriteSensitive(args, sessionID, runID)
	case "knowledge.index_workspace":
		result, err = h.knowledgeIndexWorkspace(ctx, args, sessionID, runID)
	case "knowledge.search":
		result, err = h.knowledgeSearch(ctx, args, sessionID, runID)
	case "browser.read":
		result, err = h.browserRead(ctx, args, sessionID, runID)
	case "email.search":
		result, err = h.emailSearch(ctx, args)
	case "email.read_thread":
		result, err = h.emailReadThread(ctx, args)
	case "email.draft_reply":
		result, err = h.emailDraftReply(ctx, args)
	case "email.send":
		result, err = h.emailSend(ctx, args)
	case "calendar.read":
		result, err = h.calendarRead(ctx, args)
	case "calendar.propose_event":
		result, err = h.calendarProposeEvent(ctx, args)
	case "calendar.create":
		result, err = h.calendarCreate(ctx, args)
	case "code.apply_patch":
		result, err = h.codeApplyPatch(ctx, args)
	case "shell.exec_sandboxed":
		result, err = h.shellExecSandboxed(ctx, args)
	case "notify.ask_approval":
		result, err = h.notifyAskApproval(args, sessionID, runID)
	default:
		return Result{}, fmt.Errorf("tool %q has no executor in MVP", name)
	}
	if err != nil {
		return result, err
	}
	if err := validateOutput(def, result.Output); err != nil {
		return Result{}, err
	}
	return result, nil
}

func defaultDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
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
			Description: "Read a UTF-8 text file from an allowed workspace with a byte limit.",
			InputSchema: schema("object", []string{"path"}, map[string]any{
				"path":      map[string]any{"type": "string"},
				"max_bytes": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"path", "content", "bytes", "truncated", "untrusted"}, map[string]any{
				"path":      stringSchema(),
				"content":   stringSchema(),
				"bytes":     integerSchema(),
				"truncated": booleanSchema(),
				"untrusted": booleanSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        3000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
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
			Name:        "knowledge.index_workspace",
			Description: "Build a local keyword chunk index for text files inside an allowed workspace.",
			InputSchema: schema("object", []string{}, map[string]any{
				"root":       map[string]any{"type": "string"},
				"max_files":  map[string]any{"type": "number"},
				"max_bytes":  map[string]any{"type": "number"},
				"chunk_size": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"status", "index_kind", "path", "root", "files", "chunks", "built_at"}, map[string]any{
				"status":                  stringSchema(),
				"index_kind":              stringSchema(),
				"path":                    stringSchema(),
				"root":                    stringSchema(),
				"files":                   integerSchema(),
				"chunks":                  integerSchema(),
				"built_at":                stringSchema(),
				"artifact_archive":        objectValueSchema(),
				"index_object_key":        stringSchema(),
				"index_object_uri":        stringSchema(),
				"artifact_archive_errors": stringArraySchema(),
				"document_store":          objectValueSchema(),
				"document_store_error":    stringSchema(),
				"embedding_error":         stringSchema(),
			}),
			Risk:             app.RiskDraft,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        8000,
			Sandbox:          "optional",
			Audit:            "always",
		},
		{
			Name:        "knowledge.search",
			Description: "Search the local knowledge index and return evidence snippets with stable file-and-line citations.",
			InputSchema: schema("object", []string{"query"}, map[string]any{
				"query":             map[string]any{"type": "string"},
				"max_results":       map[string]any{"type": "number"},
				"rewrite_query":     map[string]any{"type": "boolean"},
				"context_max_bytes": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"query", "original_query", "rewritten_query", "index_kind", "index_path", "built_at", "count", "candidate_count", "results", "citations", "backend", "evidence_context"}, map[string]any{
				"query":                  stringSchema(),
				"original_query":         stringSchema(),
				"rewritten_query":        stringSchema(),
				"query_terms":            stringArraySchema(),
				"index_kind":             stringSchema(),
				"index_path":             stringSchema(),
				"built_at":               stringSchema(),
				"count":                  integerSchema(),
				"candidate_count":        integerSchema(),
				"rerank_candidate_count": integerSchema(),
				"results":                arraySchema(objectValueSchema()),
				"citations":              stringArraySchema(),
				"backend":                stringSchema(),
				"evidence_context":       stringSchema(),
				"context_compression":    objectValueSchema(),
				"embedding_model":        stringSchema(),
				"embedding_error":        stringSchema(),
				"reranker_model":         stringSchema(),
				"reranker_error":         stringSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        3000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "browser.read",
			Description: "Fetch a public HTTP(S) page as read-only untrusted external content.",
			InputSchema: schema("object", []string{"url"}, map[string]any{
				"url":       map[string]any{"type": "string"},
				"max_bytes": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"url", "status_code", "content_type", "title", "text", "bytes", "truncated", "untrusted", "untrusted_external_content", "warning"}, map[string]any{
				"url":                        stringSchema(),
				"status_code":                integerSchema(),
				"content_type":               stringSchema(),
				"title":                      stringSchema(),
				"text":                       stringSchema(),
				"bytes":                      integerSchema(),
				"truncated":                  booleanSchema(),
				"untrusted":                  booleanSchema(),
				"untrusted_external_content": booleanSchema(),
				"warning":                    stringSchema(),
				"snapshot_ref":               stringSchema(),
				"snapshot_object_key":        stringSchema(),
				"snapshot_error":             stringSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        8000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "email.search",
			Description: "Search local mock email threads without external account access.",
			InputSchema: schema("object", []string{"query"}, map[string]any{
				"query":       map[string]any{"type": "string"},
				"max_results": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"query", "count", "results", "adapter"}, map[string]any{
				"query":   stringSchema(),
				"count":   integerSchema(),
				"results": arraySchema(objectValueSchema()),
				"adapter": stringSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        2000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "email.read_thread",
			Description: "Read one local mock email thread as untrusted external content.",
			InputSchema: schema("object", []string{"thread_id"}, map[string]any{
				"thread_id": map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"thread", "adapter", "untrusted_external_content"}, map[string]any{
				"thread":                     objectValueSchema(),
				"adapter":                    stringSchema(),
				"untrusted_external_content": booleanSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        2000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "email.draft_reply",
			Description: "Write a local draft reply file. This never sends email.",
			InputSchema: schema("object", []string{"body"}, map[string]any{
				"thread_id": map[string]any{"type": "string"},
				"body":      map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"path", "bytes", "status", "thread_id"}, map[string]any{
				"path":      stringSchema(),
				"bytes":     integerSchema(),
				"status":    stringSchema(),
				"thread_id": stringSchema(),
			}),
			Risk:             app.RiskDraft,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        2000,
			Sandbox:          "optional",
			Audit:            "always",
		},
		{
			Name:        "email.send",
			Description: "Send an email through the configured adapter only after explicit owner approval.",
			InputSchema: schema("object", []string{"to", "subject", "body"}, map[string]any{
				"thread_id": map[string]any{"type": "string"},
				"to":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
				"subject":   map[string]any{"type": "string"},
				"body":      map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"status", "send", "thread_id", "adapter"}, map[string]any{
				"status":    stringSchema(),
				"send":      objectValueSchema(),
				"thread_id": stringSchema(),
				"adapter":   stringSchema(),
			}),
			Risk:             app.RiskDangerous,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        5000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "calendar.read",
			Description: "Read local mock calendar events.",
			InputSchema: schema("object", []string{}, map[string]any{
				"from": map[string]any{"type": "string"},
				"to":   map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"count", "events", "adapter"}, map[string]any{
				"count":   integerSchema(),
				"events":  arraySchema(objectValueSchema()),
				"adapter": stringSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        2000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
		{
			Name:        "calendar.propose_event",
			Description: "Write a local calendar event proposal. This never creates an external event.",
			InputSchema: schema("object", []string{"title", "start", "end"}, map[string]any{
				"title":     map[string]any{"type": "string"},
				"start":     map[string]any{"type": "string"},
				"end":       map[string]any{"type": "string"},
				"location":  map[string]any{"type": "string"},
				"attendees": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"notes":     map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"path", "bytes", "status", "proposal"}, map[string]any{
				"path":     stringSchema(),
				"bytes":    integerSchema(),
				"status":   stringSchema(),
				"proposal": objectValueSchema(),
			}),
			Risk:             app.RiskDraft,
			RequiresApproval: false,
			Idempotent:       false,
			TimeoutMS:        2000,
			Sandbox:          "optional",
			Audit:            "always",
		},
		{
			Name:        "calendar.create",
			Description: "Create a calendar event through the configured adapter only after explicit owner approval.",
			InputSchema: schema("object", []string{"title", "start", "end"}, map[string]any{
				"title":     map[string]any{"type": "string"},
				"start":     map[string]any{"type": "string"},
				"end":       map[string]any{"type": "string"},
				"location":  map[string]any{"type": "string"},
				"attendees": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"notes":     map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"status", "event", "adapter"}, map[string]any{
				"status":  stringSchema(),
				"event":   objectValueSchema(),
				"adapter": stringSchema(),
			}),
			Risk:             app.RiskDangerous,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        5000,
			Sandbox:          "forbidden",
			Audit:            "always",
		},
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
			Name:        "code.apply_patch",
			Description: "Request a workspace patch. MVP queues reversible code changes for approval.",
			InputSchema: schema("object", []string{"patch"}, map[string]any{
				"patch": map[string]any{"type": "string"},
				"path":  map[string]any{"type": "string"},
			}),
			OutputSchema: objectSchema([]string{"status", "patch_id", "patch_path", "backup_dir", "manifest_path", "rollback_patch_path", "changed_files", "applied_at"}, map[string]any{
				"status":              stringSchema(),
				"patch_id":            stringSchema(),
				"patch_path":          stringSchema(),
				"backup_dir":          stringSchema(),
				"manifest_path":       stringSchema(),
				"rollback_patch_path": stringSchema(),
				"changed_files":       stringArraySchema(),
				"applied_at":          stringSchema(),
			}),
			Risk:             app.RiskReversible,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        5000,
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
	}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return schema("object", required, properties)
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
	maxBytes := intArg(args, "max_bytes", 20000)
	if maxBytes <= 0 || maxBytes > 200000 {
		maxBytes = 20000
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	truncated := false
	if len(raw) > maxBytes {
		raw = raw[:maxBytes]
		truncated = true
	}
	return Result{Output: map[string]any{
		"path":      path,
		"content":   string(raw),
		"bytes":     len(raw),
		"truncated": truncated,
		"untrusted": true,
	}}, nil
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

func (h *ToolHub) memorySearch(args map[string]any) (Result, error) {
	h.applyMemoryRetention()
	query := stringArg(args, "query", "")
	memories := h.store.SearchMemories(query)
	return Result{Output: map[string]any{"query": query, "results": memories, "count": len(memories)}}, nil
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
