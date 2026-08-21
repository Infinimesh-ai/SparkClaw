package happyapproval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	ServerName          = "happy-tasks"
	defaultPollInterval = 60 * time.Second
	defaultSyncTimeout  = 55 * time.Second
)

type ToolCaller interface {
	CallTool(context.Context, string, string, map[string]any) (mcpclient.ToolResult, error)
}

type Repository interface {
	store.ApprovalRepository
}

type Service struct {
	store        Repository
	caller       ToolCaller
	pollInterval time.Duration
	syncTimeout  time.Duration
	syncMu       sync.Mutex
}

type task struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	GoalPrompt string `json:"goalPrompt"`
	Status     string `json:"status"`
}

func New(st Repository, caller ToolCaller, pollInterval time.Duration) *Service {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	return &Service{store: st, caller: caller, pollInterval: pollInterval, syncTimeout: defaultSyncTimeout}
}

// Run keeps the ticker free of network work. Slow MCP calls execute in one
// bounded worker, and overlapping ticks collapse into one pending refresh.
func (s *Service) Run(ctx context.Context) {
	if s == nil || s.store == nil || s.caller == nil {
		return
	}
	trigger := make(chan struct{}, 1)
	trigger <- struct{}{}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-trigger:
				_, _ = s.Sync(ctx)
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(s.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case trigger <- struct{}{}:
				default:
				}
			}
		}
	}()
}

func (s *Service) Sync(ctx context.Context) (int, error) {
	if s == nil || s.store == nil || s.caller == nil {
		return 0, errors.New("Happy approval service is unavailable")
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	syncCtx, cancel := context.WithTimeout(ctx, s.syncTimeout)
	defer cancel()

	waiting, err := s.listWaitingTasks(syncCtx)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{}, len(waiting))
	changed := 0
	var syncErr error
	for _, item := range waiting {
		if err := syncCtx.Err(); err != nil {
			return changed, err
		}
		seen[item.ID] = struct{}{}
		updated, err := s.syncWaitingTask(syncCtx, item)
		if err != nil {
			syncErr = errors.Join(syncErr, err)
			continue
		}
		if updated {
			changed++
		}
	}
	approvals, err := s.store.ListApprovals(syncCtx, "pending")
	if err != nil {
		return changed, errors.Join(syncErr, fmt.Errorf("list pending approvals: %w", err))
	}
	for _, approval := range approvals {
		if approval.Source != app.ApprovalSourceHappyTeamPlan {
			continue
		}
		if _, ok := seen[approval.ExternalID]; ok {
			continue
		}
		updated, err := s.reconcileMissing(syncCtx, approval)
		if err != nil {
			syncErr = errors.Join(syncErr, err)
			continue
		}
		if updated {
			changed++
		}
	}
	return changed, syncErr
}

func (s *Service) Resolve(ctx context.Context, approval app.Approval, status string) (bool, error) {
	if s == nil || s.caller == nil {
		return false, errors.New("Happy approval service is unavailable")
	}
	if approval.Source != app.ApprovalSourceHappyTeamPlan || strings.TrimSpace(approval.ExternalID) == "" {
		return false, errors.New("approval is not a Happy Team plan approval")
	}
	tool := ""
	args := map[string]any{"taskId": approval.ExternalID}
	switch status {
	case "approved":
		if approval.ExternalContext == nil || approval.ExternalContext.PlanAvailability != app.ExternalPlanAvailable {
			return false, errors.New("Happy task plan is temporarily unavailable; retry after the member machine reconnects")
		}
		tool = "approve_plan"
		if approval.ExternalContext.PlanEdited {
			args["editedPlan"] = approval.ExternalContext.Plan
		}
	case "rejected":
		tool = "reject_plan"
	default:
		return false, fmt.Errorf("unsupported Happy approval resolution %q", status)
	}
	result, err := s.caller.CallTool(ctx, ServerName, tool, args)
	if err != nil {
		return false, fmt.Errorf("Happy Team approval call failed: %w", err)
	}
	if !result.IsError {
		return false, nil
	}
	current, currentErr := s.getTask(ctx, approval.ExternalID)
	if currentErr == nil && current.Status != "WAITING_APPROVAL" {
		return true, nil
	}
	return false, errors.New("Happy Team rejected the approval action while the task is still awaiting approval")
}

func (s *Service) listWaitingTasks(ctx context.Context) ([]task, error) {
	result, err := s.caller.CallTool(ctx, ServerName, "list_tasks", map[string]any{"status": "WAITING_APPROVAL"})
	if err != nil {
		return nil, fmt.Errorf("list Happy tasks: %w", err)
	}
	if result.IsError {
		return nil, errors.New("Happy Team could not list waiting approvals")
	}
	var payload struct {
		Tasks []task `json:"tasks"`
	}
	if err := decodeToolResult(result, &payload); err != nil {
		return nil, fmt.Errorf("decode Happy task list: %w", err)
	}
	out := make([]task, 0, len(payload.Tasks))
	seen := make(map[string]struct{}, len(payload.Tasks))
	for _, item := range payload.Tasks {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" || item.Status != "WAITING_APPROVAL" {
			continue
		}
		if _, duplicate := seen[item.ID]; duplicate {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) syncWaitingTask(ctx context.Context, item task) (bool, error) {
	approval, exists, err := s.store.FindApprovalByExternalRef(ctx, app.ApprovalSourceHappyTeamPlan, item.ID)
	if err != nil {
		return false, fmt.Errorf("find Happy approval %q: %w", item.ID, err)
	}
	if exists && approval.Status != "pending" {
		return false, nil
	}
	if strings.TrimSpace(item.GoalPrompt) == "" {
		if detail, err := s.getTask(ctx, item.ID); err == nil {
			item = detail
		}
	}
	if !exists {
		approval = newApproval(item)
	}
	plan := ""
	planAvailable := approval.ExternalContext != nil && approval.ExternalContext.PlanAvailability == app.ExternalPlanAvailable
	if planAvailable {
		plan = approval.ExternalContext.Plan
	} else {
		plan, planAvailable = s.getPlan(ctx, item.ID)
	}
	expected := approval
	before, _ := json.Marshal(expected)
	approval.Source = app.ApprovalSourceHappyTeamPlan
	approval.ExternalID = item.ID
	approval.Status = "pending"
	approval.Summary = approvalSummary(item.Title)
	contextCopy := app.ExternalApprovalContext{Provider: "happy-team"}
	if approval.ExternalContext != nil {
		contextCopy = *approval.ExternalContext
	}
	approval.ExternalContext = &contextCopy
	approval.ExternalContext.Title = strings.TrimSpace(item.Title)
	approval.ExternalContext.GoalPrompt = strings.TrimSpace(item.GoalPrompt)
	if planAvailable {
		if !approval.ExternalContext.PlanEdited {
			approval.ExternalContext.Plan = plan
		}
		approval.ExternalContext.PlanAvailability = app.ExternalPlanAvailable
	} else {
		approval.ExternalContext.PlanAvailability = app.ExternalPlanTemporarilyUnavailable
	}
	after, _ := json.Marshal(approval)
	if exists && string(before) == string(after) {
		return false, nil
	}
	if exists {
		candidate, err := s.store.UpdatePendingApproval(ctx, store.NewApprovalUpdate(expected, approval))
		if _, err = store.ReconcileApprovalWrite(ctx, s.store, candidate, err); err != nil {
			return false, err
		}
	} else {
		candidate, err := s.store.SaveApproval(ctx, approval)
		if _, err = store.ReconcileApprovalWrite(ctx, s.store, candidate, err); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Service) getPlan(ctx context.Context, taskID string) (string, bool) {
	result, err := s.caller.CallTool(ctx, ServerName, "get_task_plan", map[string]any{"taskId": taskID})
	if err != nil || result.IsError {
		return "", false
	}
	var payload struct {
		Plan string `json:"plan"`
	}
	if decodeToolResult(result, &payload) != nil {
		return "", false
	}
	if len(payload.Plan) > app.MaxExternalApprovalPlanBytes {
		return "", false
	}
	return payload.Plan, true
}

func (s *Service) getTask(ctx context.Context, taskID string) (task, error) {
	result, err := s.caller.CallTool(ctx, ServerName, "get_task", map[string]any{"taskId": taskID})
	if err != nil {
		return task{}, err
	}
	if result.IsError {
		return task{}, errors.New("Happy Team could not read the task")
	}
	var payload struct {
		Task task `json:"task"`
	}
	if err := decodeToolResult(result, &payload); err != nil {
		return task{}, err
	}
	if strings.TrimSpace(payload.Task.ID) == "" {
		return task{}, errors.New("Happy task detail omitted id")
	}
	return payload.Task, nil
}

func (s *Service) reconcileMissing(ctx context.Context, approval app.Approval) (bool, error) {
	current, err := s.getTask(ctx, approval.ExternalID)
	if err != nil || current.Status == "WAITING_APPROVAL" {
		return false, nil
	}
	candidate, err := s.store.ResolveApproval(ctx, approval.ID, "resolved_elsewhere", "Happy task left WAITING_APPROVAL")
	if _, err = store.ReconcileApprovalWrite(ctx, s.store, candidate, err); err != nil {
		return false, fmt.Errorf("resolve missing Happy approval %q: %w", approval.ID, err)
	}
	return true, nil
}

func newApproval(item task) app.Approval {
	now := time.Now().UTC()
	return app.Approval{
		ID: stableApprovalID(item.ID), Source: app.ApprovalSourceHappyTeamPlan, ExternalID: item.ID,
		Tool: "happy-team.review_plan", Risk: app.RiskDangerous, Status: "pending",
		Summary:   approvalSummary(item.Title),
		Reason:    "A supervised Happy Team task requires the owner's plan decision.",
		Resources: []string{"happy-task:" + item.ID}, Arguments: map[string]any{"taskId": item.ID},
		ExternalContext: &app.ExternalApprovalContext{
			Provider: "happy-team", Title: strings.TrimSpace(item.Title), GoalPrompt: strings.TrimSpace(item.GoalPrompt),
			PlanAvailability: app.ExternalPlanTemporarilyUnavailable,
		},
		CreatedAt: now,
	}
}

func stableApprovalID(taskID string) string {
	digest := sha256.Sum256([]byte("happy-team-plan:" + taskID))
	return "ap_happy_" + hex.EncodeToString(digest[:12])
}

func approvalSummary(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Review Happy Team task plan"
	}
	return "Review Happy Team plan: " + title
}

func decodeToolResult(result mcpclient.ToolResult, target any) error {
	var value any
	if result.StructuredContent != nil {
		value = result.StructuredContent
		if canonical, ok := result.StructuredContent["result"]; ok {
			value = canonical
		}
	} else if len(result.Content) == 1 {
		text, ok := result.Content[0]["text"].(string)
		if !ok {
			return errors.New("MCP tool result omitted JSON text content")
		}
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			return err
		}
	} else {
		return errors.New("MCP tool result omitted structured content")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
