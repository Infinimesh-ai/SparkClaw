package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var errApprovalJSONDecode = errors.New("decode persisted approval JSON")

type ApprovalUpdateCommand struct {
	Expected  app.Approval
	Candidate app.Approval
	Note      string
}

func NewApprovalUpdate(expected, candidate app.Approval) ApprovalUpdateCommand {
	return ApprovalUpdateCommand{Expected: expected, Candidate: candidate}
}

func NewApprovalUpdateWithNote(expected, candidate app.Approval, note string) ApprovalUpdateCommand {
	return ApprovalUpdateCommand{Expected: expected, Candidate: candidate, Note: note}
}

func prepareApproval(approval app.Approval, existing *app.Approval, now time.Time) (app.Approval, error) {
	approval, err := cloneApproval(approval)
	if err != nil {
		return app.Approval{}, err
	}
	approval.ID = strings.TrimSpace(approval.ID)
	if approval.ID == "" {
		return app.Approval{}, errors.New("approval ID is required")
	}
	if approval.Source == "" {
		approval.Source = app.ApprovalSourceTool
	}
	if approval.Source != app.ApprovalSourceTool && approval.Source != app.ApprovalSourceHappyTeamPlan {
		return app.Approval{}, errors.New("approval source is invalid")
	}
	if approval.Status == "" {
		approval.Status = "pending"
	}
	if existing != nil {
		approval.CreatedAt = existing.CreatedAt
	} else if approval.CreatedAt.IsZero() {
		approval.CreatedAt = now
	}
	approval.CreatedAt = normalizeApprovalTime(approval.CreatedAt)
	approval.ResolvedAt = normalizeApprovalTimePointer(approval.ResolvedAt)
	if approval.Status == "pending" {
		approval.ResolvedAt = nil
		approval.ResolutionNote = ""
	}
	if approval.Resources == nil {
		approval.Resources = []string{}
	}
	if approval.Arguments == nil {
		approval.Arguments = map[string]any{}
	}
	return approval, nil
}

func preparePendingApprovalUpdate(command ApprovalUpdateCommand, current app.Approval) (app.Approval, error) {
	expected, err := normalizePersistedApproval(command.Expected)
	if err != nil {
		return app.Approval{}, err
	}
	current, err = normalizePersistedApproval(current)
	if err != nil {
		return app.Approval{}, err
	}
	if !reflect.DeepEqual(expected, current) || current.Status != "pending" {
		return app.Approval{}, ErrApprovalConflict
	}
	candidate, err := prepareApproval(command.Candidate, &current, current.CreatedAt)
	if err != nil {
		return app.Approval{}, err
	}
	if candidate.ID != current.ID || candidate.Source != current.Source || candidate.ExternalID != current.ExternalID ||
		candidate.SessionID != current.SessionID || candidate.RunID != current.RunID || candidate.ToolCallID != current.ToolCallID ||
		candidate.Tool != current.Tool || candidate.Risk != current.Risk || !reflect.DeepEqual(candidate.PolicyContext, current.PolicyContext) ||
		!reflect.DeepEqual(candidate.Presentation, current.Presentation) {
		return app.Approval{}, ErrApprovalConflict
	}
	candidate.Status = "pending"
	candidate.ResolvedAt = nil
	candidate.ResolutionNote = ""
	return candidate, nil
}

func prepareApprovalResolution(current app.Approval, status, note string, now time.Time) (app.Approval, bool, error) {
	status = strings.TrimSpace(status)
	if status != "approved" && status != "rejected" && status != "resolved_elsewhere" {
		return app.Approval{}, false, errors.New("approval resolution status is invalid")
	}
	current, err := normalizePersistedApproval(current)
	if err != nil {
		return app.Approval{}, false, err
	}
	if current.Status != "pending" {
		if current.Status == status && current.ResolutionNote == note {
			return current, true, nil
		}
		return app.Approval{}, false, ErrApprovalConflict
	}
	resolvedAt := normalizeApprovalTime(now)
	current.Status = status
	current.ResolvedAt = &resolvedAt
	current.ResolutionNote = note
	return current, false, nil
}

func normalizePersistedApproval(approval app.Approval) (app.Approval, error) {
	approval, err := cloneApproval(approval)
	if err != nil {
		return app.Approval{}, err
	}
	if approval.Source == "" {
		approval.Source = app.ApprovalSourceTool
	}
	if approval.Resources == nil {
		approval.Resources = []string{}
	}
	if approval.Arguments == nil {
		approval.Arguments = map[string]any{}
	}
	approval.CreatedAt = normalizeApprovalTime(approval.CreatedAt)
	approval.ResolvedAt = normalizeApprovalTimePointer(approval.ResolvedAt)
	return approval, nil
}

func cloneApproval(approval app.Approval) (app.Approval, error) {
	raw, err := json.Marshal(approval)
	if err != nil {
		return app.Approval{}, err
	}
	var cloned app.Approval
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return app.Approval{}, err
	}
	return cloned, nil
}

func approvalsEqual(left, right app.Approval) bool {
	left, leftErr := normalizePersistedApproval(left)
	right, rightErr := normalizePersistedApproval(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(left, right)
}

func normalizeApprovalTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

func normalizeApprovalTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := normalizeApprovalTime(*value)
	return &normalized
}

func ReconcileApprovalWrite(ctx context.Context, repository ApprovalRepository, candidate app.Approval, writeErr error) (app.Approval, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.Approval{}, writeErr
	}
	current, found, err := repository.GetApproval(ctx, candidate.ID)
	if err != nil {
		return app.Approval{}, errors.Join(writeErr, err)
	}
	if found && approvalsEqual(current, candidate) {
		return current, nil
	}
	return app.Approval{}, writeErr
}

func sortApprovals(values []app.Approval) {
	slices.SortFunc(values, func(left, right app.Approval) int {
		if order := right.CreatedAt.Compare(left.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
}
