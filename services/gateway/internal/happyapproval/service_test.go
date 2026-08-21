package happyapproval

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type approvalRepositoryFaultStore struct {
	store.Store
	listErr      error
	saveErr      error
	contextKey   any
	contextValue any
}

func (s *approvalRepositoryFaultStore) ListApprovals(ctx context.Context, status string) ([]app.Approval, error) {
	if s.contextKey != nil {
		s.contextValue = ctx.Value(s.contextKey)
	}
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Store.ListApprovals(ctx, status)
}

func (s *approvalRepositoryFaultStore) SaveApproval(ctx context.Context, approval app.Approval) (app.Approval, error) {
	if s.contextKey != nil {
		s.contextValue = ctx.Value(s.contextKey)
	}
	if s.saveErr != nil {
		return app.Approval{}, s.saveErr
	}
	return s.Store.SaveApproval(ctx, approval)
}

type recordedCall struct {
	Server string
	Tool   string
	Args   map[string]any
}

type fakeCaller struct {
	calls   []recordedCall
	handler func(string, map[string]any) (mcpclient.ToolResult, error)
}

func (f *fakeCaller) CallTool(_ context.Context, server, tool string, args map[string]any) (mcpclient.ToolResult, error) {
	cloned := map[string]any{}
	for key, value := range args {
		cloned[key] = value
	}
	f.calls = append(f.calls, recordedCall{Server: server, Tool: tool, Args: cloned})
	return f.handler(tool, args)
}

func TestSyncCreatesAndDeduplicatesWaitingPlanApproval(t *testing.T) {
	st := store.NewMemoryStore()
	planCalls := 0
	caller := &fakeCaller{handler: func(tool string, _ map[string]any) (mcpclient.ToolResult, error) {
		switch tool {
		case "list_tasks":
			item := map[string]any{
				"id": "task-1", "title": "Implement feature", "goalPrompt": "Implement feature safely", "status": "WAITING_APPROVAL",
			}
			return jsonResult(map[string]any{"tasks": []any{item, item}}), nil
		case "get_task_plan":
			planCalls++
			return jsonResult(map[string]any{"plan": "# Plan\n\n1. Inspect\n2. Implement"}), nil
		default:
			t.Fatalf("unexpected tool %q", tool)
			return mcpclient.ToolResult{}, nil
		}
	}}
	service := New(st, caller, time.Minute)
	changed, err := service.Sync(t.Context())
	if err != nil || changed != 1 {
		t.Fatalf("first sync changed=%d err=%v", changed, err)
	}
	changed, err = service.Sync(t.Context())
	if err != nil || changed != 0 {
		t.Fatalf("second sync changed=%d err=%v", changed, err)
	}
	approvals := storetest.MustListApprovals(t, st, "pending")
	if len(approvals) != 1 || approvals[0].Source != app.ApprovalSourceHappyTeamPlan || approvals[0].ExternalID != "task-1" ||
		approvals[0].ExternalContext == nil || approvals[0].ExternalContext.GoalPrompt != "Implement feature safely" ||
		approvals[0].ExternalContext.Plan != "# Plan\n\n1. Inspect\n2. Implement" || approvals[0].ExternalContext.PlanAvailability != app.ExternalPlanAvailable {
		t.Fatalf("waiting plan approval mismatch: %#v", approvals)
	}
	if planCalls != 1 {
		t.Fatalf("duplicate task triggered %d plan calls", planCalls)
	}
}

func TestSyncPropagatesApprovalRepositoryFailuresAndCallerContext(t *testing.T) {
	privateCause := errors.New("private approval database failure")
	key := struct{ name string }{"approval-context"}
	t.Run("list", func(t *testing.T) {
		fault := &approvalRepositoryFaultStore{
			Store: store.NewMemoryStore(), listErr: &store.StoreError{
				Code: store.StoreErrorUnavailable, Operation: store.OperationApprovalList, Err: privateCause,
			}, contextKey: key,
		}
		caller := &fakeCaller{handler: func(tool string, _ map[string]any) (mcpclient.ToolResult, error) {
			if tool != "list_tasks" {
				t.Fatalf("unexpected tool %q", tool)
			}
			return jsonResult(map[string]any{"tasks": []any{}}), nil
		}}
		ctx := context.WithValue(t.Context(), key, "caller-value")
		if _, err := New(fault, caller, time.Minute).Sync(ctx); !errors.Is(err, privateCause) || fault.contextValue != "caller-value" {
			t.Fatalf("Sync list err=%v context=%#v", err, fault.contextValue)
		}
	})
	t.Run("save", func(t *testing.T) {
		fault := &approvalRepositoryFaultStore{
			Store: store.NewMemoryStore(), saveErr: &store.StoreError{
				Code: store.StoreErrorUnavailable, Operation: store.OperationApprovalSave, Err: privateCause,
			}, contextKey: key,
		}
		caller := &fakeCaller{handler: func(tool string, _ map[string]any) (mcpclient.ToolResult, error) {
			switch tool {
			case "list_tasks":
				return jsonResult(map[string]any{"tasks": []any{map[string]any{"id": "task-fault", "status": "WAITING_APPROVAL"}}}), nil
			case "get_task":
				return jsonResult(map[string]any{"task": map[string]any{"id": "task-fault", "status": "WAITING_APPROVAL"}}), nil
			case "get_task_plan":
				return mcpclient.ToolResult{IsError: true}, nil
			default:
				t.Fatalf("unexpected tool %q", tool)
				return mcpclient.ToolResult{}, nil
			}
		}}
		ctx := context.WithValue(t.Context(), key, "caller-value")
		if _, err := New(fault, caller, time.Minute).Sync(ctx); !errors.Is(err, privateCause) || fault.contextValue != "caller-value" {
			t.Fatalf("Sync save err=%v context=%#v", err, fault.contextValue)
		}
	})
}

func TestResolveRejectsNonHappyApprovalBeforeCallingIntegration(t *testing.T) {
	caller := &fakeCaller{handler: func(tool string, _ map[string]any) (mcpclient.ToolResult, error) {
		t.Fatalf("unexpected integration call %q", tool)
		return mcpclient.ToolResult{}, nil
	}}
	approval := app.Approval{ID: "approval-tool", Source: app.ApprovalSourceTool, ExternalID: "task"}
	if _, err := New(store.NewMemoryStore(), caller, time.Minute).Resolve(t.Context(), approval, "approved"); err == nil {
		t.Fatal("non-Happy approval reached the Happy integration")
	}
}

func TestSyncRetriesPlanAfterMemberMachineReconnects(t *testing.T) {
	st := store.NewMemoryStore()
	planOnline := false
	caller := &fakeCaller{handler: func(tool string, _ map[string]any) (mcpclient.ToolResult, error) {
		switch tool {
		case "list_tasks":
			return jsonResult(map[string]any{"tasks": []any{map[string]any{
				"id": "task-offline", "title": "Offline task", "goalPrompt": "Wait for machine", "status": "WAITING_APPROVAL",
			}}}), nil
		case "get_task_plan":
			if !planOnline {
				return mcpclient.ToolResult{IsError: true, Content: []mcpclient.ContentBlock{{"type": "text", "text": "machine offline"}}}, nil
			}
			return jsonResult(map[string]any{"plan": "Machine is back"}), nil
		default:
			t.Fatalf("unexpected tool %q", tool)
			return mcpclient.ToolResult{}, nil
		}
	}}
	service := New(st, caller, time.Minute)
	if _, err := service.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	approval, _ := storetest.MustFindApprovalByExternalRef(t, st, app.ApprovalSourceHappyTeamPlan, "task-offline")
	if approval.ExternalContext == nil || approval.ExternalContext.PlanAvailability != app.ExternalPlanTemporarilyUnavailable {
		t.Fatalf("offline plan state = %#v", approval.ExternalContext)
	}
	planOnline = true
	if _, err := service.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	approval, _ = storetest.MustFindApprovalByExternalRef(t, st, app.ApprovalSourceHappyTeamPlan, "task-offline")
	if approval.ExternalContext.PlanAvailability != app.ExternalPlanAvailable || approval.ExternalContext.Plan != "Machine is back" {
		t.Fatalf("retried plan state = %#v", approval.ExternalContext)
	}
}

func TestSyncDoesNotPersistOversizedPlan(t *testing.T) {
	st := store.NewMemoryStore()
	caller := &fakeCaller{handler: func(tool string, _ map[string]any) (mcpclient.ToolResult, error) {
		switch tool {
		case "list_tasks":
			return jsonResult(map[string]any{"tasks": []any{map[string]any{
				"id": "task-large", "title": "Large plan", "goalPrompt": "Bound untrusted content", "status": "WAITING_APPROVAL",
			}}}), nil
		case "get_task_plan":
			return jsonResult(map[string]any{"plan": string(make([]byte, app.MaxExternalApprovalPlanBytes+1))}), nil
		default:
			t.Fatalf("unexpected tool %q", tool)
			return mcpclient.ToolResult{}, nil
		}
	}}
	service := New(st, caller, time.Minute)
	if _, err := service.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	approval, ok := storetest.MustFindApprovalByExternalRef(t, st, app.ApprovalSourceHappyTeamPlan, "task-large")
	if !ok || approval.ExternalContext == nil || approval.ExternalContext.Plan != "" ||
		approval.ExternalContext.PlanAvailability != app.ExternalPlanTemporarilyUnavailable {
		t.Fatalf("oversized plan was not bounded: %#v ok=%v", approval, ok)
	}
}

func TestSyncReconcilesTaskThatLeftWaitingApproval(t *testing.T) {
	st := store.NewMemoryStore()
	storetest.MustSaveApproval(t, st, newApproval(task{ID: "task-resolved", Title: "Resolved", Status: "WAITING_APPROVAL"}))
	caller := &fakeCaller{handler: func(tool string, _ map[string]any) (mcpclient.ToolResult, error) {
		switch tool {
		case "list_tasks":
			return jsonResult(map[string]any{"tasks": []any{}}), nil
		case "get_task":
			return jsonResult(map[string]any{"task": map[string]any{"id": "task-resolved", "status": "RUNNING"}}), nil
		default:
			t.Fatalf("unexpected tool %q", tool)
			return mcpclient.ToolResult{}, nil
		}
	}}
	service := New(st, caller, time.Minute)
	if changed, err := service.Sync(t.Context()); err != nil || changed != 1 {
		t.Fatalf("reconcile changed=%d err=%v", changed, err)
	}
	approval, _ := storetest.MustFindApprovalByExternalRef(t, st, app.ApprovalSourceHappyTeamPlan, "task-resolved")
	if approval.Status != "resolved_elsewhere" || approval.ResolvedAt == nil {
		t.Fatalf("reconciled approval = %#v", approval)
	}
}

func TestResolvePassesOnlyOwnerEditedPlan(t *testing.T) {
	caller := &fakeCaller{handler: func(tool string, args map[string]any) (mcpclient.ToolResult, error) {
		if tool != "approve_plan" || args["taskId"] != "task-edit" || args["editedPlan"] != "Owner-edited plan" {
			t.Fatalf("approve call = %q %#v", tool, args)
		}
		return jsonResult(map[string]any{"task": map[string]any{"id": "task-edit", "status": "RUNNING"}}), nil
	}}
	service := New(store.NewMemoryStore(), caller, time.Minute)
	approval := newApproval(task{ID: "task-edit", Title: "Edit", Status: "WAITING_APPROVAL"})
	approval.ExternalContext.Plan = "Owner-edited plan"
	approval.ExternalContext.PlanAvailability = app.ExternalPlanAvailable
	approval.ExternalContext.PlanEdited = true
	resolvedElsewhere, err := service.Resolve(t.Context(), approval, "approved")
	if err != nil || resolvedElsewhere {
		t.Fatalf("resolvedElsewhere=%v err=%v", resolvedElsewhere, err)
	}
}

func TestResolveBusinessErrorChecksAuthoritativeTaskStatus(t *testing.T) {
	caller := &fakeCaller{handler: func(tool string, _ map[string]any) (mcpclient.ToolResult, error) {
		switch tool {
		case "reject_plan":
			return mcpclient.ToolResult{IsError: true, Content: []mcpclient.ContentBlock{{"type": "text", "text": "untrusted error text"}}}, nil
		case "get_task":
			return jsonResult(map[string]any{"task": map[string]any{"id": "task-race", "status": "CANCELLED"}}), nil
		default:
			t.Fatalf("unexpected tool %q", tool)
			return mcpclient.ToolResult{}, nil
		}
	}}
	service := New(store.NewMemoryStore(), caller, time.Minute)
	resolvedElsewhere, err := service.Resolve(t.Context(), newApproval(task{ID: "task-race"}), "rejected")
	if err != nil || !resolvedElsewhere {
		t.Fatalf("resolvedElsewhere=%v err=%v", resolvedElsewhere, err)
	}
}

func TestResolveBlocksApprovalWhilePlanUnavailable(t *testing.T) {
	caller := &fakeCaller{handler: func(string, map[string]any) (mcpclient.ToolResult, error) {
		t.Fatal("unavailable plan must not call approve_plan")
		return mcpclient.ToolResult{}, nil
	}}
	service := New(store.NewMemoryStore(), caller, time.Minute)
	_, err := service.Resolve(t.Context(), newApproval(task{ID: "task-offline"}), "approved")
	if err == nil {
		t.Fatal("expected unavailable plan to block approval")
	}
}

func jsonResult(value any) mcpclient.ToolResult {
	raw, _ := json.Marshal(value)
	return mcpclient.ToolResult{Content: []mcpclient.ContentBlock{{"type": "text", "text": string(raw)}}}
}
