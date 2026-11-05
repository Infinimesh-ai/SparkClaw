package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

// documentRetrievalToolProviders are the index query tools of the
// large-document retrieval stage. Each is read-only and bound to the
// preflighted document path.
func documentRetrievalToolProviders() []documentToolProvider {
	pathSchema := documentScopeInputSchema(map[string]any{"path": stringSchema()})
	return []documentToolProvider{
		{
			Definition: app.ToolDefinition{
				Name:        "document.outline",
				Description: "Discover source-bound structure and native node IDs for a document of any supported size. Metadata is paginated; next_cursor continues discovery without reading all source text.",
				InputSchema: schema("object", []string{"path"}, pathSchema),
				OutputSchema: objectSchema([]string{"path", "outline", "content", "untrusted"}, map[string]any{
					"path": stringSchema(), "pages": integerSchema(), "chunks": integerSchema(), "unindexed_pages": arraySchema(integerSchema()),
					"outline": arraySchema(objectValueSchema()), "content": stringSchema(), "untrusted": booleanSchema(), "nodes": arraySchema(objectValueSchema()), "structure": objectValueSchema(), "source_sha256": stringSchema(), "structure_generation": stringSchema(), "next_cursor": stringSchema(), "coverage": objectValueSchema(),
				}),
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 125000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentOutline)},
				app.ToolCapabilityDocumentOutline, nil, app.OutcomeAdapterDocumentRetrieval,
				"Discover document structure and continuation.",
				"Use first for supported document formats, then continue metadata discovery when the task needs more nodes.",
				"Do not confuse structure discovery with content acquisition.", app.ToolEffectWorkspaceRead),
		},
		{
			Definition: app.ToolDefinition{
				Name:        "document.search",
				Description: "Keyword-search a document of any supported size and return ranked snippets with chunk IDs, pages and heading paths, plus outline sections whose titles match. Pass several keywords, including Chinese and English synonyms; optionally limit to one outline section. Scanned pages have no text until a range read recognizes them, so use matching sections for them.",
				InputSchema: schema("object", []string{"path", "keywords"}, documentScopeInputSchema(map[string]any{
					"path": stringSchema(), "keywords": stringArraySchema(), "top_k": integerSchema(), "section": stringSchema(),
				})),
				OutputSchema: objectSchema([]string{"path", "keywords", "hits", "count", "truncated", "untrusted"}, map[string]any{
					"path": stringSchema(), "keywords": stringArraySchema(), "terms": stringArraySchema(), "hits": arraySchema(objectValueSchema()),
					"sections": arraySchema(objectValueSchema()), "unrecognized_pages": arraySchema(integerSchema()),
					"count": integerSchema(), "truncated": booleanSchema(), "untrusted": booleanSchema(), "structure": objectValueSchema(), "source_sha256": stringSchema(), "structure_generation": stringSchema(), "coverage": objectValueSchema(),
				}),
				// A scanned file without an outline is recognized whole first.
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 900000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentSearch)},
				app.ToolCapabilityDocumentSearch, nil, app.OutcomeAdapterDocumentRetrieval,
				"Locate document passages by keywords.",
				"Use to locate task-relevant ranges; a top-k search is not exhaustive coverage.",
				"Do not use for workspace file-name search or the public Web.", app.ToolEffectWorkspaceRead),
		},
		{
			Definition: app.ToolDefinition{
				Name:        "document.read_range",
				Description: "Read a declared source scope with exactly one selector: native node_ids, all=true, legacy chunk_ids, page range, section, or continuation cursor. A cursor resumes the original pinned selection and must not be combined with another selector. Read_complete and coverage apply only to that selection, never automatically to the task or whole document. Preserve missing/unrecognized nodes and continue next_cursor.",
				InputSchema: schema("object", []string{"path"}, documentScopeInputSchema(map[string]any{
					"path": stringSchema(), "chunk_ids": stringArraySchema(), "page_start": integerSchema(), "page_end": integerSchema(),
					"section": stringSchema(), "context": integerSchema(), "node_ids": stringArraySchema(), "all": booleanSchema(),
				})),
				OutputSchema: objectSchema([]string{"path", "content", "bytes", "spans", "truncated", "untrusted"}, map[string]any{
					"path": stringSchema(), "rel_path": stringSchema(), "kind": stringSchema(), "content": stringSchema(), "bytes": integerSchema(),
					"spans": arraySchema(objectValueSchema()), "truncated": booleanSchema(), "next_chunk_id": stringSchema(),
					"read_complete": booleanSchema(), "unrecognized_pages": arraySchema(integerSchema()),
					"missing_page_indexes": arraySchema(integerSchema()), "untrusted": booleanSchema(), "structure": objectValueSchema(), "source_sha256": stringSchema(), "structure_generation": stringSchema(), "next_cursor": stringSchema(), "coverage": objectValueSchema(), "nodes": arraySchema(objectValueSchema()), "dependencies": arraySchema(objectValueSchema()), "generation_rebased": booleanSchema(), "previous_structure_generation": stringSchema(), "missing_parts": stringArraySchema(),
				}),
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 600000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentReadRange)},
				app.ToolCapabilityDocumentReadRange, nil, app.OutcomeAdapterDocumentRetrieval,
				"Read a pinned source scope and its dependencies.",
				"Use after structure discovery with the declared task range, including a one-call all=true read when it fits.",
				"Do not change selectors during cursor continuation or infer whole-task completion from one selected node.", app.ToolEffectWorkspaceRead),
		},
		{
			Definition: app.ToolDefinition{
				Name:        "document.summarize",
				Description: "Summarize the whole document across all required sections, including late ranges. Contract version 2 reports explicit part coverage and missing parts; a bounded partial summary must not be presented as complete.",
				InputSchema: schema("object", []string{"path"}, pathSchema),
				OutputSchema: objectSchema([]string{"path", "content", "sections", "read_complete", "untrusted"}, map[string]any{
					"path": stringSchema(), "rel_path": stringSchema(), "kind": stringSchema(), "content": stringSchema(), "bytes": integerSchema(),
					"sections": arraySchema(objectValueSchema()), "count": integerSchema(), "read_complete": booleanSchema(),
					"missing_page_indexes": arraySchema(integerSchema()), "untrusted": booleanSchema(), "structure": objectValueSchema(), "source_sha256": stringSchema(), "structure_generation": stringSchema(), "next_cursor": stringSchema(), "coverage": objectValueSchema(), "nodes": arraySchema(objectValueSchema()), "dependencies": arraySchema(objectValueSchema()), "generation_rebased": booleanSchema(), "previous_structure_generation": stringSchema(), "missing_parts": stringArraySchema(),
				}),
				// One Fast call per section, plus whole-file OCR of a scanned
				// file within the upload limit.
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 1200000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentSummarize)},
				app.ToolCapabilityDocumentSummarize, nil, app.OutcomeAdapterDocumentRetrieval,
				"Summarize the whole document with explicit coverage.",
				"Use when the selected task is a whole-document summary, for any supported document size.",
				"Do not use for questions that a search and range read answer.", app.ToolEffectWorkspaceRead),
		},
	}
}

// New optional fields preserve legacy callers while revision 6 binds versions
// and requires a semantic task scope in its model-visible projection.
func documentScopeInputSchema(properties map[string]any) map[string]any {
	properties["source_sha256"] = stringSchema()
	properties["structure_generation"] = stringSchema()
	properties["cursor"] = stringSchema()
	properties["limit"] = integerSchema()
	properties["max_bytes"] = integerSchema()
	properties["contract_version"] = integerSchema()
	properties["task_mode"] = map[string]any{"type": "string", "enum": []string{"targeted", "exhaustive", "whole_summary", "comparison", "calculation"}}
	properties["scope_reason"] = stringSchema()
	return properties
}
