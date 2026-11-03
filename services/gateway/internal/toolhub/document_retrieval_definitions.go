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
				Description: "Keyword-search an indexed large document and return ranked snippets with chunk IDs, pages and heading paths, plus outline sections whose titles match. Pass several keywords, including Chinese and English synonyms; optionally limit to one outline section. Scanned pages have no text until a range read recognizes them, so use matching sections for them.",
				InputSchema: schema("object", []string{"path", "keywords"}, map[string]any{
					"path": stringSchema(), "keywords": stringArraySchema(), "top_k": integerSchema(), "section": stringSchema(),
				}),
				OutputSchema: objectSchema([]string{"path", "keywords", "hits", "count", "truncated", "untrusted"}, map[string]any{
					"path": stringSchema(), "keywords": stringArraySchema(), "terms": stringArraySchema(), "hits": arraySchema(objectValueSchema()),
					"sections": arraySchema(objectValueSchema()), "unrecognized_pages": arraySchema(integerSchema()),
					"count": integerSchema(), "truncated": booleanSchema(), "untrusted": booleanSchema(),
				}),
				// A scanned file without an outline is recognized whole first.
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 900000, Sandbox: "forbidden", Audit: "always",
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
				Description: "Read selected passages of an indexed large document by chunk_ids from search (with neighbouring context), by page range, or by outline section. Scanned pages in a page range or section are recognized first, at most 8 per call; unrecognized_pages lists the rest. Text is bounded; next_chunk_id continues a cut read.",
				InputSchema: schema("object", []string{"path"}, map[string]any{
					"path": stringSchema(), "chunk_ids": stringArraySchema(), "page_start": integerSchema(), "page_end": integerSchema(),
					"section": stringSchema(), "context": integerSchema(),
				}),
				OutputSchema: objectSchema([]string{"path", "content", "bytes", "spans", "truncated", "untrusted"}, map[string]any{
					"path": stringSchema(), "rel_path": stringSchema(), "kind": stringSchema(), "content": stringSchema(), "bytes": integerSchema(),
					"spans": arraySchema(objectValueSchema()), "truncated": booleanSchema(), "next_chunk_id": stringSchema(),
					"read_complete": booleanSchema(), "unrecognized_pages": arraySchema(integerSchema()),
					"missing_page_indexes": arraySchema(integerSchema()), "untrusted": booleanSchema(),
				}),
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 600000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentReadRange)},
				app.ToolCapabilityDocumentReadRange, nil, app.OutcomeAdapterDocumentRetrieval,
				"Read selected passages of an indexed large document.",
				"Use in a large-document retrieval stage after search, with the chunk_ids that answer the request.",
				"Do not use to read a whole large document or a small document.", app.ToolEffectWorkspaceRead),
		},
		{
			Definition: app.ToolDefinition{
				Name:        "document.summarize",
				Description: "Summarize a whole indexed large document section by section (chapter-level map-reduce, cached per section). Use for requests about the entire document, such as summarizing a whole book or report.",
				InputSchema: schema("object", []string{"path"}, pathSchema),
				OutputSchema: objectSchema([]string{"path", "content", "sections", "read_complete", "untrusted"}, map[string]any{
					"path": stringSchema(), "rel_path": stringSchema(), "kind": stringSchema(), "content": stringSchema(), "bytes": integerSchema(),
					"sections": arraySchema(objectValueSchema()), "count": integerSchema(), "read_complete": booleanSchema(),
					"missing_page_indexes": arraySchema(integerSchema()), "untrusted": booleanSchema(),
				}),
				// One Fast call per section, plus whole-file OCR of a scanned
				// file within the upload limit.
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 1200000, Sandbox: "forbidden", Audit: "always",
			},
			Registration: workflowRegistration(toolRegistration{run: ctxArgsSessionRun((*ToolHub).documentSummarize)},
				app.ToolCapabilityDocumentSummarize, nil, app.OutcomeAdapterDocumentRetrieval,
				"Summarize a whole indexed large document section by section.",
				"Use in a large-document retrieval stage when the request is about the entire document rather than specific passages.",
				"Do not use for questions that a search and range read answer.", app.ToolEffectWorkspaceRead),
		},
	}
}
