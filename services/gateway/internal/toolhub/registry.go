package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

// toolExecutor runs one tool against a (possibly session-scoped) hub.
type toolExecutor func(h *ToolHub, ctx context.Context, name string, args map[string]any, sessionID, runID string) (Result, error)

// toolRegistration is the single place a tool declares how it is executed and
// when it is available. Definitions (schemas) stay in defaultDefinitions();
// New() refuses to register a definition without a matching entry here, so the
// two lists cannot drift apart silently.
type toolRegistration struct {
	// enabled gates the tool on config; nil means always available.
	enabled        func(cfg config.Config) bool
	run            toolExecutor
	capabilities   []app.CapabilityDescriptor
	outcomeAdapter app.ToolOutcomeAdapter
	directory      app.ToolDirectoryMetadata
}

// Adapters that lift the common handler signatures into toolExecutor.

func ctxArgs(fn func(*ToolHub, context.Context, map[string]any) (Result, error)) toolExecutor {
	return func(h *ToolHub, ctx context.Context, _ string, args map[string]any, _, _ string) (Result, error) {
		return fn(h, ctx, args)
	}
}

func ctxArgsSessionRun(fn func(*ToolHub, context.Context, map[string]any, string, string) (Result, error)) toolExecutor {
	return func(h *ToolHub, ctx context.Context, _ string, args map[string]any, sessionID, runID string) (Result, error) {
		return fn(h, ctx, args, sessionID, runID)
	}
}

func argsSession(fn func(*ToolHub, map[string]any, string) (Result, error)) toolExecutor {
	return func(h *ToolHub, _ context.Context, _ string, args map[string]any, sessionID, _ string) (Result, error) {
		return fn(h, args, sessionID)
	}
}

func argsSessionRun(fn func(*ToolHub, map[string]any, string, string) (Result, error)) toolExecutor {
	return func(h *ToolHub, _ context.Context, _ string, args map[string]any, sessionID, runID string) (Result, error) {
		return fn(h, args, sessionID, runID)
	}
}

// structureOp binds an operation name for the docx/pptx/xlsx structure editors.
func structureOp(fn func(*ToolHub, context.Context, string, map[string]any) (Result, error), operation string) toolExecutor {
	return func(h *ToolHub, ctx context.Context, _ string, args map[string]any, _, _ string) (Result, error) {
		return fn(h, ctx, operation, args)
	}
}

func remindersEnabled(cfg config.Config) bool {
	return cfg.Tools.Reminders.Enabled
}

func browserAutomationEnabled(cfg config.Config) bool {
	return cfg.Tools.BrowserAutomation.Enabled
}

func browserAutomationPassthrough() toolExecutor {
	return func(h *ToolHub, ctx context.Context, name string, args map[string]any, sessionID, _ string) (Result, error) {
		return h.browserAutomationTool(ctx, name, args, sessionID)
	}
}

func workflowRegistration(base toolRegistration, capability string, qualifiers map[string]string, adapter app.ToolOutcomeAdapter, summary, whenToUse, whenNotToUse string, effects ...app.ToolEffect) toolRegistration {
	base.capabilities = []app.CapabilityDescriptor{{Name: capability, Qualifiers: qualifiers}}
	base.outcomeAdapter = adapter
	base.directory = app.ToolDirectoryMetadata{
		Summary: summary, WhenToUse: whenToUse, WhenNotToUse: whenNotToUse, Effects: effects,
	}
	return base
}

func browserAutomationRegistration(capability string, adapter app.ToolOutcomeAdapter, summary string, riskEffect app.ToolEffect) toolRegistration {
	return workflowRegistration(
		toolRegistration{enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
		capability, nil, adapter,
		summary, "Use only inside the browser.automation workflow.", "Do not use for public discovery or local document work.", riskEffect,
	)
}

func legacyDocumentMutationRegistration(run toolExecutor, summary string) toolRegistration {
	registration := workflowRegistration(
		toolRegistration{run: run}, "document.legacy_mutation", nil, app.OutcomeAdapterGeneric,
		summary, "Use only in the transitional unmatched document path.", "Do not use for browser work or document.read/edit r1.", app.ToolEffectWorkspaceWrite,
	)
	registration.directory.OutputKinds = []app.OutputKind{app.OutputKindFile}
	return registration
}

func documentDeletionRegistration(run toolExecutor, summary string) toolRegistration {
	registration := legacyDocumentMutationRegistration(run, summary)
	registration.directory.OutputKinds = nil
	return registration
}

func browserScreenshotRegistration() toolRegistration {
	registration := browserAutomationRegistration("browser.legacy", app.OutcomeAdapterGeneric, "Capture a browser screenshot.", app.ToolEffectExternalRead)
	registration.directory.OutputKinds = []app.OutputKind{app.OutputKindImage}
	return registration
}

func browserReadRegistration() toolRegistration {
	registration := workflowRegistration(
		toolRegistration{run: ctxArgsSessionRun((*ToolHub).browserRead)}, "web.page.read",
		map[string]string{app.CapabilityQualifierOperation: app.CapabilityOperationRead}, app.OutcomeAdapterWebPage,
		"Read a known URL and extract source-page evidence.",
		"Use for a known URL in browser.search or as bounded evidence in browser.automation.",
		"Do not use for public discovery or local document work.", app.ToolEffectExternalRead,
	)
	registration.capabilities = append(registration.capabilities, app.CapabilityDescriptor{Name: "browser.legacy"})
	return registration
}

func documentReadRegistration(run toolExecutor, formats []string, summary string) toolRegistration {
	registration := workflowRegistration(
		toolRegistration{run: run}, app.ToolCapabilityDocumentRead,
		map[string]string{app.CapabilityQualifierFormat: formats[0]}, app.OutcomeAdapterWorkspaceRead,
		summary, "Use only for the preflighted exact path and detected format in document.read.", "Do not use for search or mutation.", app.ToolEffectWorkspaceRead,
	)
	registration.capabilities = registration.capabilities[:0]
	for _, format := range formats {
		registration.capabilities = append(registration.capabilities, app.CapabilityDescriptor{
			Name: app.ToolCapabilityDocumentRead, Qualifiers: map[string]string{app.CapabilityQualifierFormat: format},
		})
	}
	return registration
}

func documentEditRegistration(run toolExecutor, format, operation, summary string) toolRegistration {
	registration := workflowRegistration(
		toolRegistration{run: run}, app.ToolCapabilityDocumentEdit,
		map[string]string{app.CapabilityQualifierFormat: format, app.CapabilityQualifierOperation: operation}, app.OutcomeAdapterDocumentEdit,
		summary, "Use only for the preflighted format, operation, input path, and output copy.", "Do not use for another format, read-only work, or verification.", app.ToolEffectWorkspaceWrite,
	)
	registration.directory.OutputKinds = []app.OutputKind{app.OutputKindFile}
	return registration
}

func officeReplaceRegistration() toolRegistration {
	registration := documentEditRegistration(ctxArgs((*ToolHub).officeReplaceText), app.DocumentFormatDOCX, "replace_text", "Replace bounded text and write an Office output copy.")
	registration.capabilities = []app.CapabilityDescriptor{
		{Name: app.ToolCapabilityDocumentEdit, Qualifiers: map[string]string{app.CapabilityQualifierFormat: app.DocumentFormatDOCX, app.CapabilityQualifierOperation: "replace_text"}},
		{Name: app.ToolCapabilityDocumentEdit, Qualifiers: map[string]string{app.CapabilityQualifierFormat: app.DocumentFormatXLSX, app.CapabilityQualifierOperation: "replace_text"}},
		{Name: app.ToolCapabilityDocumentEdit, Qualifiers: map[string]string{app.CapabilityQualifierFormat: app.DocumentFormatPPTX, app.CapabilityQualifierOperation: "replace_text"}},
	}
	return registration
}

func pdfTransformRegistration() toolRegistration {
	registration := documentEditRegistration(ctxArgs((*ToolHub).pdfTransform), app.DocumentFormatPDF, "extract_pages", "Apply a bounded PDF transform and write an output copy.")
	registration.capabilities = registration.capabilities[:0]
	for _, operation := range []string{"extract_pages", "delete_pages", "rotate_pages", "split"} {
		registration.capabilities = append(registration.capabilities, app.CapabilityDescriptor{
			Name: app.ToolCapabilityDocumentEdit, Qualifiers: map[string]string{app.CapabilityQualifierFormat: app.DocumentFormatPDF, app.CapabilityQualifierOperation: operation},
		})
	}
	return registration
}

// toolRegistry maps tool name -> execution + availability. Adding a tool means
// one entry here plus its definition in defaultDefinitions().
var toolRegistry = map[string]toolRegistration{
	"files.search": workflowRegistration(toolRegistration{run: ctxArgs((*ToolHub).filesSearch)}, "workspace.file.search", nil, app.OutcomeAdapterWorkspaceSearch,
		"Search file names and bounded text content in the configured workspace.",
		"Use when the owner asks to find local workspace files and no exact path is known.",
		"Do not use for public Web search, knowledge-index search, or file mutation.", app.ToolEffectWorkspaceRead),
	"files.read": documentReadRegistration(ctxArgs((*ToolHub).filesRead), []string{app.DocumentFormatText, app.DocumentFormatDOCX, app.DocumentFormatXLSX, app.DocumentFormatPPTX},
		"Read one explicitly identified file inside the configured workspace.",
	),
	"images.inspect":            {run: ctxArgs((*ToolHub).imageInspect)},
	"media.render_weather_card": {run: ctxArgsSessionRun((*ToolHub).renderWeatherCard)},
	"files.write_draft":         legacyDocumentMutationRegistration(ctxArgs((*ToolHub).filesWriteDraft), "Create a governed draft file in the workspace."),
	"file.delete":               documentDeletionRegistration(ctxArgs((*ToolHub).fileDelete), "Move a governed workspace file to recoverable trash."),
	"office.replace_text":       officeReplaceRegistration(),
	"docx.replace_paragraph":    documentEditRegistration(structureOp((*ToolHub).docxStructureEdit, "replace_paragraph"), app.DocumentFormatDOCX, "replace_paragraph", "Replace one DOCX paragraph and write a new document."),
	"docx.insert_paragraph":     documentEditRegistration(structureOp((*ToolHub).docxStructureEdit, "insert_paragraph"), app.DocumentFormatDOCX, "insert_paragraph", "Insert one DOCX paragraph and write a new document."),
	"docx.delete_paragraph":     documentEditRegistration(structureOp((*ToolHub).docxStructureEdit, "delete_paragraph"), app.DocumentFormatDOCX, "delete_paragraph", "Delete one DOCX paragraph and write a new document."),
	"docx.set_text_style":       documentEditRegistration(structureOp((*ToolHub).docxStructureEdit, "set_text_style"), app.DocumentFormatDOCX, "set_text_style", "Apply a bounded DOCX paragraph style and write a new document."),
	"pptx.add_slide":            documentEditRegistration(structureOp((*ToolHub).pptxSlideEdit, "add_slide"), app.DocumentFormatPPTX, "add_slide", "Add one PPTX slide and write a new presentation."),
	"pptx.duplicate_slide":      documentEditRegistration(structureOp((*ToolHub).pptxSlideEdit, "duplicate_slide"), app.DocumentFormatPPTX, "duplicate_slide", "Duplicate one PPTX slide and write a new presentation."),
	"pptx.delete_slide":         documentEditRegistration(structureOp((*ToolHub).pptxSlideEdit, "delete_slide"), app.DocumentFormatPPTX, "delete_slide", "Delete one PPTX slide and write a new presentation."),
	"xlsx.update_cell":          documentEditRegistration(structureOp((*ToolHub).xlsxStructureEdit, "update_cell"), app.DocumentFormatXLSX, "update_cell", "Update one XLSX cell and write a new workbook."),
	"xlsx.insert_row":           documentEditRegistration(structureOp((*ToolHub).xlsxStructureEdit, "insert_row"), app.DocumentFormatXLSX, "insert_row", "Insert one XLSX row and write a new workbook."),
	"xlsx.delete_row":           documentEditRegistration(structureOp((*ToolHub).xlsxStructureEdit, "delete_row"), app.DocumentFormatXLSX, "delete_row", "Delete one XLSX row and write a new workbook."),
	"xlsx.update_row":           documentEditRegistration(structureOp((*ToolHub).xlsxStructureEdit, "update_row"), app.DocumentFormatXLSX, "update_row", "Update one XLSX row and write a new workbook."),
	"xlsx.append_row":           documentEditRegistration(structureOp((*ToolHub).xlsxStructureEdit, "append_row"), app.DocumentFormatXLSX, "append_row", "Append one XLSX row and write a new workbook."),
	"pdf.extract_text": documentReadRegistration(ctxArgs((*ToolHub).pdfExtractText), []string{app.DocumentFormatPDF},
		"Extract bounded text from a workspace PDF."),
	"pdf.transform":          pdfTransformRegistration(),
	"memory.search":          {run: argsSession((*ToolHub).memorySearch)},
	"memory.write_candidate": {run: argsSessionRun((*ToolHub).memoryWriteCandidate)},
	"memory.propose":         {run: argsSessionRun((*ToolHub).memoryWriteCandidate)},
	"memory.write_sensitive": {run: argsSessionRun((*ToolHub).memoryWriteSensitive)},
	"browser.read":           browserReadRegistration(),
	"web.search": workflowRegistration(toolRegistration{enabled: func(cfg config.Config) bool { return cfg.Tools.Web.Search.Enabled }, run: ctxArgs((*ToolHub).webSearchTool)}, app.ToolCapabilityWebDiscovery,
		map[string]string{app.CapabilityQualifierProvider: app.CapabilityProviderInfo}, app.OutcomeAdapterWebSearch,
		"Discover public web sources when the target URL is unknown.",
		"Use for public search, freshness checks, and source discovery.",
		"Do not use when a specific URL is already known or for source-page verification.", app.ToolEffectExternalRead),
	"browser.status": workflowRegistration(toolRegistration{enabled: browserAutomationEnabled, run: func(h *ToolHub, ctx context.Context, _ string, args map[string]any, _, _ string) (Result, error) {
		return h.browserAutomationHealth(ctx, args)
	}}, "browser.legacy", nil, app.OutcomeAdapterGeneric, "Check browser automation availability.", "Use before interaction when provider health is unknown.", "Do not use for public search.", app.ToolEffectExternalRead),
	"browser.list_tabs":    browserAutomationRegistration(app.ToolCapabilityBrowserListTabs, app.OutcomeAdapterBrowserTabs, "List tabs in the managed browser session.", app.ToolEffectExternalRead),
	"browser.open":         browserAutomationRegistration(app.ToolCapabilityBrowserOpen, app.OutcomeAdapterBrowserOpen, "Open a URL in a managed browser tab.", app.ToolEffectExternalRead),
	"browser.focus":        browserAutomationRegistration(app.ToolCapabilityBrowserFocus, app.OutcomeAdapterBrowserFocus, "Focus a managed browser tab.", app.ToolEffectExternalRead),
	"browser.close":        browserAutomationRegistration("browser.legacy", app.OutcomeAdapterGeneric, "Close a managed browser tab.", app.ToolEffectExternalInteract),
	"browser.navigate":     browserAutomationRegistration("browser.legacy", app.OutcomeAdapterGeneric, "Navigate a managed browser tab.", app.ToolEffectExternalRead),
	"browser.snapshot":     browserAutomationRegistration("browser.legacy", app.OutcomeAdapterGeneric, "Capture structured browser page state.", app.ToolEffectExternalRead),
	"browser.screenshot":   browserScreenshotRegistration(),
	"browser.wait":         browserAutomationRegistration("browser.legacy", app.OutcomeAdapterGeneric, "Wait for observable browser state.", app.ToolEffectExternalRead),
	"browser.click":        browserAutomationRegistration("browser.legacy", app.OutcomeAdapterGeneric, "Click a referenced page control.", app.ToolEffectExternalInteract),
	"browser.type":         browserAutomationRegistration("browser.legacy", app.OutcomeAdapterGeneric, "Type into a referenced page control.", app.ToolEffectExternalInteract),
	"browser.select":       browserAutomationRegistration("browser.legacy", app.OutcomeAdapterGeneric, "Select a value in a referenced page control.", app.ToolEffectExternalInteract),
	"reminders.create":     {enabled: remindersEnabled, run: argsSessionRun((*ToolHub).remindersCreate)},
	"reminders.list":       {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersList)},
	"reminders.update":     {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersUpdate)},
	"reminders.cancel":     {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersCancel)},
	"code.apply_patch":     {run: ctxArgs((*ToolHub).codeApplyPatch)},
	"shell.exec_sandboxed": {run: ctxArgs((*ToolHub).shellExecSandboxed)},
	"notify.ask_approval":  {run: argsSessionRun((*ToolHub).notifyAskApproval)},
}
