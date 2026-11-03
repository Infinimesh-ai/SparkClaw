package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

// documentRetrievalToolProviders are the index query tools of the
// large-document retrieval stage. Each is read-only and bound to the
// preflighted document path.
func documentRetrievalToolProviders() []documentToolProvider {
	pathSchema := map[string]any{"path": stringSchema()}
	return []documentToolProvider{
		{
			Definition: app.ToolDefinition{
				Name:        "document.outline",
				Description: "List the heading outline of an indexed large document with node IDs and pages, to choose a section to search or read.",
				InputSchema: schema("object", []string{"path"}, pathSchema),
				OutputSchema: objectSchema([]string{"path", "outline", "content", "untrusted"}, map[string]any{
					"path": stringSchema(), "pages": integerSchema(), "chunks": integerSchema(), "unindexed_pages": arraySchema(integerSchema()),
					"outline": arraySchema(objectValueSchema()), "content": stringSchema(), "untrusted": booleanSchema(),
				}),
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 125000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentOutline)},
				app.ToolCapabilityDocumentOutline, nil, app.OutcomeAdapterDocumentRetrieval,
				"List the outline of an indexed large document.",
				"Use in a large-document retrieval stage when keyword search misses or a section must be chosen by title.",
				"Do not use for small documents that a document read returns whole.", app.ToolEffectWorkspaceRead),
		},
		{
			Definition: app.ToolDefinition{
				Name:        "document.search",
				Description: "Keyword-search an indexed large document and return ranked snippets with chunk IDs, pages and heading paths. Pass several keywords, including Chinese and English synonyms; optionally limit to one outline section.",
				InputSchema: schema("object", []string{"path", "keywords"}, map[string]any{
					"path": stringSchema(), "keywords": stringArraySchema(), "top_k": integerSchema(), "section": stringSchema(),
				}),
				OutputSchema: objectSchema([]string{"path", "keywords", "hits", "count", "truncated", "untrusted"}, map[string]any{
					"path": stringSchema(), "keywords": stringArraySchema(), "terms": stringArraySchema(), "hits": arraySchema(objectValueSchema()),
					"count": integerSchema(), "truncated": booleanSchema(), "untrusted": booleanSchema(),
				}),
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 125000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentSearch)},
				app.ToolCapabilityDocumentSearch, nil, app.OutcomeAdapterDocumentRetrieval,
				"Search an indexed large document by keywords.",
				"Use in a large-document retrieval stage to locate the passages the owner request needs.",
				"Do not use for workspace file-name search or the public Web.", app.ToolEffectWorkspaceRead),
		},
		{
			Definition: app.ToolDefinition{
				Name:        "document.read_range",
				Description: "Read selected passages of an indexed large document by chunk_ids from search (with neighbouring context), by page range, or by outline section. Text is bounded; next_chunk_id continues a cut read.",
				InputSchema: schema("object", []string{"path"}, map[string]any{
					"path": stringSchema(), "chunk_ids": stringArraySchema(), "page_start": integerSchema(), "page_end": integerSchema(),
					"section": stringSchema(), "context": integerSchema(),
				}),
				OutputSchema: objectSchema([]string{"path", "content", "bytes", "spans", "truncated", "untrusted"}, map[string]any{
					"path": stringSchema(), "rel_path": stringSchema(), "kind": stringSchema(), "content": stringSchema(), "bytes": integerSchema(),
					"spans": arraySchema(objectValueSchema()), "truncated": booleanSchema(), "next_chunk_id": stringSchema(),
					"read_complete": booleanSchema(), "untrusted": booleanSchema(),
				}),
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 125000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentReadRange)},
				app.ToolCapabilityDocumentReadRange, nil, app.OutcomeAdapterDocumentRetrieval,
				"Read selected passages of an indexed large document.",
				"Use in a large-document retrieval stage after search, with the chunk_ids that answer the request.",
				"Do not use to read a whole large document or a small document.", app.ToolEffectWorkspaceRead),
		},
	}
}
