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

func workflowRegistration(base toolRegistration, capability app.CapabilityID, qualifiers map[string]string, adapter app.ToolOutcomeAdapter, summary, whenToUse, whenNotToUse string, effects ...app.ToolEffect) toolRegistration {
	base.capabilities = []app.CapabilityDescriptor{{Name: string(capability), Qualifiers: qualifiers}}
	base.outcomeAdapter = adapter
	base.directory = app.ToolDirectoryMetadata{
		Summary: summary, WhenToUse: whenToUse, WhenNotToUse: whenNotToUse, Effects: effects,
	}
	return base
}

func browserAutomationRegistration(summary string, riskEffect app.ToolEffect) toolRegistration {
	return workflowRegistration(
		toolRegistration{enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
		app.CapabilityBrowserAutomation, nil, app.OutcomeAdapterGeneric,
		summary, "Use only inside the browser.automation workflow.", "Do not use for public discovery or local document work.", riskEffect,
	)
}

func documentProcessingRegistration(run toolExecutor, summary string) toolRegistration {
	return workflowRegistration(
		toolRegistration{run: run}, app.CapabilityDocumentProcessing, nil, app.OutcomeAdapterGeneric,
		summary, "Use only inside the document.processing workflow.", "Do not use for browser work or read-only document questions.", app.ToolEffectWorkspaceWrite,
	)
}

func browserReadRegistration() toolRegistration {
	registration := workflowRegistration(
		toolRegistration{run: ctxArgsSessionRun((*ToolHub).browserRead)}, app.CapabilityBrowserSearch,
		map[string]string{app.CapabilityQualifierOperation: app.CapabilityOperationRead}, app.OutcomeAdapterWebPage,
		"Read a known URL and extract source-page evidence.",
		"Use for a known URL in browser.search or as bounded evidence in browser.automation.",
		"Do not use for public discovery or local document work.", app.ToolEffectExternalRead,
	)
	registration.capabilities = append(registration.capabilities, app.CapabilityDescriptor{Name: string(app.CapabilityBrowserAutomation)})
	return registration
}

// toolRegistry maps tool name -> execution + availability. Adding a tool means
// one entry here plus its definition in defaultDefinitions().
var toolRegistry = map[string]toolRegistration{
	"files.search": workflowRegistration(toolRegistration{run: ctxArgs((*ToolHub).filesSearch)}, app.CapabilityDocumentInformation, nil, app.OutcomeAdapterWorkspaceSearch,
		"Search file names and bounded text content in the configured workspace.",
		"Use when the owner asks to find local workspace files and no exact path is known.",
		"Do not use for public Web search, knowledge-index search, or file mutation.", app.ToolEffectWorkspaceRead),
	"files.read": workflowRegistration(toolRegistration{run: ctxArgs((*ToolHub).filesRead)}, app.CapabilityDocumentInformation, nil, app.OutcomeAdapterWorkspaceRead,
		"Read one explicitly identified file inside the configured workspace.",
		"Use when the workflow has a deterministic workspace path target.",
		"Do not use to discover unknown files or modify file content.", app.ToolEffectWorkspaceRead),
	"images.inspect":            {run: ctxArgs((*ToolHub).imageInspect)},
	"media.render_weather_card": {run: ctxArgsSessionRun((*ToolHub).renderWeatherCard)},
	"files.write_draft":         documentProcessingRegistration(ctxArgs((*ToolHub).filesWriteDraft), "Create a governed draft file in the workspace."),
	"file.delete":               documentProcessingRegistration(ctxArgs((*ToolHub).fileDelete), "Move a governed workspace file to recoverable trash."),
	"office.replace_text":       documentProcessingRegistration(ctxArgs((*ToolHub).officeReplaceText), "Replace bounded text in an Office document and write a new file."),
	"docx.replace_paragraph":    documentProcessingRegistration(structureOp((*ToolHub).docxStructureEdit, "replace_paragraph"), "Replace one DOCX paragraph and write a new document."),
	"docx.insert_paragraph":     documentProcessingRegistration(structureOp((*ToolHub).docxStructureEdit, "insert_paragraph"), "Insert one DOCX paragraph and write a new document."),
	"docx.delete_paragraph":     documentProcessingRegistration(structureOp((*ToolHub).docxStructureEdit, "delete_paragraph"), "Delete one DOCX paragraph and write a new document."),
	"docx.set_text_style":       documentProcessingRegistration(structureOp((*ToolHub).docxStructureEdit, "set_text_style"), "Apply a bounded DOCX paragraph style and write a new document."),
	"pptx.add_slide":            documentProcessingRegistration(structureOp((*ToolHub).pptxSlideEdit, "add_slide"), "Add one PPTX slide and write a new presentation."),
	"pptx.duplicate_slide":      documentProcessingRegistration(structureOp((*ToolHub).pptxSlideEdit, "duplicate_slide"), "Duplicate one PPTX slide and write a new presentation."),
	"pptx.delete_slide":         documentProcessingRegistration(structureOp((*ToolHub).pptxSlideEdit, "delete_slide"), "Delete one PPTX slide and write a new presentation."),
	"xlsx.update_cell":          documentProcessingRegistration(structureOp((*ToolHub).xlsxStructureEdit, "update_cell"), "Update one XLSX cell and write a new workbook."),
	"xlsx.insert_row":           documentProcessingRegistration(structureOp((*ToolHub).xlsxStructureEdit, "insert_row"), "Insert one XLSX row and write a new workbook."),
	"xlsx.delete_row":           documentProcessingRegistration(structureOp((*ToolHub).xlsxStructureEdit, "delete_row"), "Delete one XLSX row and write a new workbook."),
	"xlsx.update_row":           documentProcessingRegistration(structureOp((*ToolHub).xlsxStructureEdit, "update_row"), "Update one XLSX row and write a new workbook."),
	"xlsx.append_row":           documentProcessingRegistration(structureOp((*ToolHub).xlsxStructureEdit, "append_row"), "Append one XLSX row and write a new workbook."),
	"pdf.extract_text": workflowRegistration(toolRegistration{run: ctxArgs((*ToolHub).pdfExtractText)}, app.CapabilityDocumentInformation, nil, app.OutcomeAdapterWorkspaceRead,
		"Extract bounded text from a workspace PDF.", "Use to read a text PDF inside document.information.", "Do not use for PDF mutation or browser content.", app.ToolEffectWorkspaceRead),
	"pdf.transform":          documentProcessingRegistration(ctxArgs((*ToolHub).pdfTransform), "Apply a bounded PDF transform and write governed output."),
	"memory.search":          {run: argsSession((*ToolHub).memorySearch)},
	"memory.write_candidate": {run: argsSessionRun((*ToolHub).memoryWriteCandidate)},
	"memory.propose":         {run: argsSessionRun((*ToolHub).memoryWriteCandidate)},
	"memory.write_sensitive": {run: argsSessionRun((*ToolHub).memoryWriteSensitive)},
	"browser.read":           browserReadRegistration(),
	"web.search": workflowRegistration(toolRegistration{enabled: func(cfg config.Config) bool { return cfg.Tools.Web.Search.Enabled }, run: ctxArgs((*ToolHub).webSearchTool)}, app.CapabilityBrowserSearch,
		map[string]string{app.CapabilityQualifierOperation: app.CapabilityOperationDiscover}, app.OutcomeAdapterWebSearch,
		"Discover public web sources when the target URL is unknown.",
		"Use for public search, freshness checks, and source discovery.",
		"Do not use when a specific URL is already known or for source-page verification.", app.ToolEffectExternalRead),
	"browser.status": workflowRegistration(toolRegistration{enabled: browserAutomationEnabled, run: func(h *ToolHub, ctx context.Context, _ string, args map[string]any, _, _ string) (Result, error) {
		return h.browserAutomationHealth(ctx, args)
	}}, app.CapabilityBrowserAutomation, nil, app.OutcomeAdapterGeneric, "Check browser automation availability.", "Use before interaction when provider health is unknown.", "Do not use for public search.", app.ToolEffectExternalRead),
	"browser.list_tabs":    browserAutomationRegistration("List tabs in the managed browser session.", app.ToolEffectExternalRead),
	"browser.open":         browserAutomationRegistration("Open a URL in a managed browser tab.", app.ToolEffectExternalRead),
	"browser.focus":        browserAutomationRegistration("Focus a managed browser tab.", app.ToolEffectExternalRead),
	"browser.close":        browserAutomationRegistration("Close a managed browser tab.", app.ToolEffectExternalInteract),
	"browser.navigate":     browserAutomationRegistration("Navigate a managed browser tab.", app.ToolEffectExternalRead),
	"browser.snapshot":     browserAutomationRegistration("Capture structured browser page state.", app.ToolEffectExternalRead),
	"browser.screenshot":   browserAutomationRegistration("Capture a browser screenshot.", app.ToolEffectExternalRead),
	"browser.wait":         browserAutomationRegistration("Wait for observable browser state.", app.ToolEffectExternalRead),
	"browser.click":        browserAutomationRegistration("Click a referenced page control.", app.ToolEffectExternalInteract),
	"browser.type":         browserAutomationRegistration("Type into a referenced page control.", app.ToolEffectExternalInteract),
	"browser.select":       browserAutomationRegistration("Select a value in a referenced page control.", app.ToolEffectExternalInteract),
	"reminders.create":     {enabled: remindersEnabled, run: argsSessionRun((*ToolHub).remindersCreate)},
	"reminders.list":       {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersList)},
	"reminders.update":     {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersUpdate)},
	"reminders.cancel":     {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersCancel)},
	"code.apply_patch":     {run: ctxArgs((*ToolHub).codeApplyPatch)},
	"shell.exec_sandboxed": {run: ctxArgs((*ToolHub).shellExecSandboxed)},
	"notify.ask_approval":  {run: argsSessionRun((*ToolHub).notifyAskApproval)},
}
