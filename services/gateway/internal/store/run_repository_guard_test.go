package store

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestRunRepositoryProductionCallsUseCallerContextAndObserveCriticalWrites(t *testing.T) {
	methods := map[string]int{
		"SaveRunFeedback": 2, "ListRunFeedback": 2, "SaveRun": 2, "GetRun": 2, "ListRuns": 2,
		"SaveModelCall": 2, "ListModelCalls": 3, "SaveToolCall": 2, "GetToolCall": 2, "ListToolCalls": 2,
		"SaveEpisodeSummary": 2, "ListEpisodeSummaries": 2,
	}
	files := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		stack := []ast.Node{}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			var parent ast.Node
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, node)
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			wantArgs, tracked := methods[selector.Sel.Name]
			if !tracked {
				return true
			}
			position := files.Position(call.Pos())
			if len(call.Args) != wantArgs {
				t.Errorf("%s:%d %s has %d arguments, want caller context plus %d domain arguments", position.Filename, position.Line, selector.Sel.Name, len(call.Args), wantArgs-1)
				return true
			}
			if isRunRepositoryBackgroundCall(call.Args[0]) {
				t.Errorf("%s:%d %s replaces its production caller context with context.Background", position.Filename, position.Line, selector.Sel.Name)
			}
			if _, ignored := parent.(*ast.ExprStmt); ignored && (selector.Sel.Name == "SaveRun" || selector.Sel.Name == "SaveToolCall") {
				t.Errorf("%s:%d ignores critical %s persistence result", position.Filename, position.Line, selector.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isRunRepositoryBackgroundCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Background" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "context"
}

func TestRunWriteReconciliationRequiresExactCandidate(t *testing.T) {
	repository := NewMemoryStore()
	run, err := repository.SaveRun(t.Context(), app.AgentRun{ID: "run-reconcile", SessionID: "session", State: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	unknownRun := storeError(context.Background(), OperationRunSave, StoreErrorUnknownOutcome, errors.New("commit uncertain"))
	if reconciled, err := ReconcileRunWrite(t.Context(), repository, run, unknownRun); err != nil || !runRecordsEqual(reconciled, run) {
		t.Fatalf("exact Run reconciliation = %#v err=%v", reconciled, err)
	}
	mismatchedRun := run
	mismatchedRun.State = "failed"
	if reconciled, err := ReconcileRunWrite(t.Context(), repository, mismatchedRun, unknownRun); reconciled.ID != "" || !errors.Is(err, unknownRun) {
		t.Fatalf("mismatched Run reconciliation = %#v err=%v", reconciled, err)
	}

	toolCall, err := repository.SaveToolCall(t.Context(), app.ToolCall{
		ID: "tool-reconcile", SessionID: "session", RunID: run.ID, Tool: "files.read", Status: "completed", Arguments: map[string]any{"path": "a.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	unknownTool := storeError(context.Background(), OperationToolCallSave, StoreErrorUnknownOutcome, errors.New("commit uncertain"))
	if reconciled, err := ReconcileToolCallWrite(t.Context(), repository, toolCall, unknownTool); err != nil || !runRecordsEqual(reconciled, toolCall) {
		t.Fatalf("exact ToolCall reconciliation = %#v err=%v", reconciled, err)
	}
	mismatchedTool := toolCall
	mismatchedTool.Status = "failed"
	if reconciled, err := ReconcileToolCallWrite(t.Context(), repository, mismatchedTool, unknownTool); reconciled.ID != "" || !errors.Is(err, unknownTool) {
		t.Fatalf("mismatched ToolCall reconciliation = %#v err=%v", reconciled, err)
	}
}
