package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestDocumentReadPreflightDispatchesOnlyCompatibleReader(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestOfficePackage(t, filepath.Join(root, "report.docx"), "word/document.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report.xlsx"), "xl/workbook.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report.pptx"), "ppt/presentation.xml")

	for _, test := range []struct {
		path, format, tool string
	}{
		{path: "note.txt", format: app.DocumentFormatText, tool: "files.read"},
		{path: "report.docx", format: app.DocumentFormatDOCX, tool: "files.read"},
		{path: "report.xlsx", format: app.DocumentFormatXLSX, tool: "files.read"},
		{path: "report.pptx", format: app.DocumentFormatPPTX, tool: "files.read"},
		{path: "report.pdf", format: app.DocumentFormatPDF, tool: "pdf.extract_text"},
	} {
		t.Run(test.format, func(t *testing.T) {
			runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
			defer closeRuntime()
			route, err := runtime.recognizeCapabilityRoute(session.ID, "turn", "Summarize "+test.path, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentRead || route.Facts["document_format"] != test.format {
				t.Fatalf("read preflight chose the wrong format: %#v", route)
			}
			run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
			dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
			if err != nil {
				t.Fatal(err)
			}
			if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != test.tool {
				t.Fatalf("read stage exposed an incompatible reader: %#v", visibleToolNames(dispatch.Tools))
			}
			node := dispatch.Run.Workflow.Nodes["document_read"]
			if node.Stage != "read_by_type" || node.ScopeRevision != 2 || len(node.CurrentScope.Requirements) != 1 ||
				node.CurrentScope.Requirements[0].Qualifiers[app.CapabilityQualifierFormat] != test.format {
				t.Fatalf("read type transition was not frozen: %#v", node)
			}
		})
	}
}

func TestDocumentEditPreflightDispatchesFormatAndOperationCompatibleEditor(t *testing.T) {
	root := t.TempDir()
	writeTestOfficePackage(t, filepath.Join(root, "report.docx"), "word/document.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report.xlsx"), "xl/workbook.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report.pptx"), "ppt/presentation.xml")
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		request, format, operation, tool, output string
	}{
		{request: "Replace a paragraph in report.docx", format: app.DocumentFormatDOCX, operation: "replace_paragraph", tool: "docx.replace_paragraph", output: "report-sparkclaw-edit.docx"},
		{request: "Update a cell in report.xlsx", format: app.DocumentFormatXLSX, operation: "update_cell", tool: "xlsx.update_cell", output: "report-sparkclaw-edit.xlsx"},
		{request: "Replace text in report.pptx", format: app.DocumentFormatPPTX, operation: "replace_text", tool: "office.replace_text", output: "report-sparkclaw-edit.pptx"},
		{request: "Rotate pages in report.pdf", format: app.DocumentFormatPDF, operation: "rotate_pages", tool: "pdf.transform", output: "report-sparkclaw-edit.pdf"},
	} {
		t.Run(test.format, func(t *testing.T) {
			runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
			defer closeRuntime()
			route, err := runtime.recognizeCapabilityRoute(session.ID, "turn", test.request, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
				route.Facts["document_format"] != test.format || route.Facts["document_operation"] != test.operation || route.Facts["output_path"] != test.output {
				t.Fatalf("edit preflight did not freeze its typed copy operation: %#v", route)
			}
			run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
			dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
			if err != nil {
				t.Fatal(err)
			}
			if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != test.tool {
				t.Fatalf("edit stage exposed an incompatible editor: %#v", visibleToolNames(dispatch.Tools))
			}
			node := dispatch.Run.Workflow.Nodes["document_edit"]
			if node.Stage != "edit_by_type" || node.ScopeRevision != 2 || len(node.CurrentScope.Requirements) != 1 {
				t.Fatalf("edit type transition was not frozen: %#v", node)
			}
		})
	}
}

func TestDocumentPreflightRejectsExtensionSignatureMismatchAndTextEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fake.pdf"), []byte("not a PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := preflightDocumentPath(root, "fake.pdf", false); err == nil {
		t.Fatal("mismatched PDF signature passed preflight")
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := preflightDocumentPath(root, "note.txt", true); err == nil {
		t.Fatal("text file entered document.edit revision 1")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := preflightDocumentPath(root, "linked/outside.txt", false); err == nil {
		t.Fatal("workspace path traversed a symlinked parent")
	}
	writeTestOfficePackage(t, filepath.Join(root, "report.docx"), "word/document.xml")
	writeTestOfficePackage(t, filepath.Join(root, "report-sparkclaw-edit.docx"), "word/document.xml")
	if _, err := preflightDocumentPath(root, "report.docx", true); err == nil {
		t.Fatal("document edit accepted an existing output copy")
	}
}

func TestDocumentEditRejectsOperationContradictingMaterializedQualifier(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	route, err := runtime.recognizeCapabilityRoute(session.ID, "turn", "Rotate pages in report.pdf", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := runtime.tools.Definition("pdf.transform")
	if !ok {
		t.Fatal("pdf.transform is unavailable")
	}
	plan := toolPlan{
		Name: "pdf.transform", WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 2,
		Capability: app.ToolCapabilityDocumentEdit,
		Args:       map[string]any{"path": "report.pdf", "output_path": "report-sparkclaw-edit.pdf", "operation": "rotate_pages"},
	}
	if err := runtime.validateWorkflowToolPlan(dispatch.Run.ID, plan, definition); err != nil {
		t.Fatalf("matching PDF operation was rejected: %v", err)
	}
	plan.Args["operation"] = "delete_pages"
	if err := runtime.validateWorkflowToolPlan(dispatch.Run.ID, plan, definition); err == nil {
		t.Fatal("PDF operation escaped the materialized rotate_pages qualifier")
	}
}

func newDocumentDispatchRuntime(t *testing.T, root string) (Runtime, *store.MemoryStore, app.Session, func()) {
	t.Helper()
	cfg := agentTestConfig()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	st := store.NewMemoryStore()
	session := st.CreateSessionWithScope("document dispatch", app.DefaultOwnerID, root, "web", false)
	hub := toolhub.New(cfg, st)
	return NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil), st, session, func() { _ = hub.Close() }
}
