package toolhub

import (
	"context"

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
	enabled func(cfg config.Config) bool
	run     toolExecutor
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

// toolRegistry maps tool name -> execution + availability. Adding a tool means
// one entry here plus its definition in defaultDefinitions().
var toolRegistry = map[string]toolRegistration{
	"files.search":              {run: ctxArgs((*ToolHub).filesSearch)},
	"files.read":                {run: ctxArgs((*ToolHub).filesRead)},
	"images.inspect":            {run: ctxArgs((*ToolHub).imageInspect)},
	"media.render_weather_card": {run: ctxArgsSessionRun((*ToolHub).renderWeatherCard)},
	"files.write_draft":         {run: ctxArgs((*ToolHub).filesWriteDraft)},
	"file.delete":               {run: ctxArgs((*ToolHub).fileDelete)},
	"office.replace_text":       {run: ctxArgs((*ToolHub).officeReplaceText)},
	"docx.replace_paragraph":    {run: structureOp((*ToolHub).docxStructureEdit, "replace_paragraph")},
	"docx.insert_paragraph":     {run: structureOp((*ToolHub).docxStructureEdit, "insert_paragraph")},
	"docx.delete_paragraph":     {run: structureOp((*ToolHub).docxStructureEdit, "delete_paragraph")},
	"docx.set_text_style":       {run: structureOp((*ToolHub).docxStructureEdit, "set_text_style")},
	"pptx.add_slide":            {run: structureOp((*ToolHub).pptxSlideEdit, "add_slide")},
	"pptx.duplicate_slide":      {run: structureOp((*ToolHub).pptxSlideEdit, "duplicate_slide")},
	"pptx.delete_slide":         {run: structureOp((*ToolHub).pptxSlideEdit, "delete_slide")},
	"xlsx.update_cell":          {run: structureOp((*ToolHub).xlsxStructureEdit, "update_cell")},
	"xlsx.insert_row":           {run: structureOp((*ToolHub).xlsxStructureEdit, "insert_row")},
	"xlsx.delete_row":           {run: structureOp((*ToolHub).xlsxStructureEdit, "delete_row")},
	"xlsx.update_row":           {run: structureOp((*ToolHub).xlsxStructureEdit, "update_row")},
	"xlsx.append_row":           {run: structureOp((*ToolHub).xlsxStructureEdit, "append_row")},
	"pdf.extract_text":          {run: ctxArgs((*ToolHub).pdfExtractText)},
	"pdf.transform":             {run: ctxArgs((*ToolHub).pdfTransform)},
	"memory.search":             {run: argsSession((*ToolHub).memorySearch)},
	"memory.write_candidate":    {run: argsSessionRun((*ToolHub).memoryWriteCandidate)},
	"memory.propose":            {run: argsSessionRun((*ToolHub).memoryWriteCandidate)},
	"memory.write_sensitive":    {run: argsSessionRun((*ToolHub).memoryWriteSensitive)},
	"knowledge.index_workspace": {run: ctxArgsSessionRun((*ToolHub).knowledgeIndexWorkspace)},
	"knowledge.search":          {run: ctxArgsSessionRun((*ToolHub).knowledgeSearch)},
	"browser.read":              {run: ctxArgsSessionRun((*ToolHub).browserRead)},
	"web.search": {
		enabled: func(cfg config.Config) bool { return cfg.Tools.Web.Search.Enabled },
		run:     ctxArgs((*ToolHub).webSearchTool),
	},
	"browser.status": {
		enabled: browserAutomationEnabled,
		run: func(h *ToolHub, ctx context.Context, _ string, args map[string]any, _, _ string) (Result, error) {
			return h.browserAutomationHealth(ctx, args)
		},
	},
	"browser.list_tabs":      {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.open":           {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.focus":          {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.close":          {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.navigate":       {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.snapshot":       {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.screenshot":     {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.wait":           {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.click":          {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.type":           {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"browser.select":         {enabled: browserAutomationEnabled, run: browserAutomationPassthrough()},
	"email.search":           {run: ctxArgs((*ToolHub).emailSearch)},
	"email.read_thread":      {run: ctxArgs((*ToolHub).emailReadThread)},
	"email.draft_reply":      {run: ctxArgs((*ToolHub).emailDraftReply)},
	"email.send":             {run: ctxArgs((*ToolHub).emailSend)},
	"calendar.read":          {run: ctxArgs((*ToolHub).calendarRead)},
	"calendar.propose_event": {run: ctxArgs((*ToolHub).calendarProposeEvent)},
	"calendar.create":        {run: ctxArgs((*ToolHub).calendarCreate)},
	"reminders.create":       {enabled: remindersEnabled, run: argsSessionRun((*ToolHub).remindersCreate)},
	"reminders.list":         {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersList)},
	"reminders.update":       {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersUpdate)},
	"reminders.cancel":       {enabled: remindersEnabled, run: argsSession((*ToolHub).remindersCancel)},
	"code.apply_patch":       {run: ctxArgs((*ToolHub).codeApplyPatch)},
	"shell.exec_sandboxed":   {run: ctxArgs((*ToolHub).shellExecSandboxed)},
	"notify.ask_approval":    {run: argsSessionRun((*ToolHub).notifyAskApproval)},
}
