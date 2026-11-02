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
		approval.Status = app.ApprovalStatusPending
	}
	if existing != nil {
		approval.CreatedAt = existing.CreatedAt
	} else if approval.CreatedAt.IsZero() {
		approval.CreatedAt = now
	}
	approval.CreatedAt = normalizeApprovalTime(approval.CreatedAt)
	approval.ResolvedAt = normalizeApprovalTimePointer(approval.ResolvedAt)
	if approval.Status == app.ApprovalStatusPending {
		approval.ResolvedAt = nil
		approval.ResolutionNote = ""
	}
	if approval.Resources == nil {
		approval.Resources = []string{}
	}
	if approval.Arguments == nil {
		approval.Arguments = map[string]any{}
	}
	if approval.InputForm != nil {
		if err := app.ValidateApprovalInputForm(approval.InputForm); err != nil {
			return app.Approval{}, err
		}
		digest, err := app.ApprovalFormDigest(approval)
		if err != nil || approval.InputFormDigest != "" && approval.InputFormDigest != digest {
			return app.Approval{}, ErrApprovalConflict
		}
		approval.InputFormDigest = digest
		if approval.InputValues != nil || approval.InputsDigest != "" {
			return app.Approval{}, errors.New("approval inputs require atomic resolution")
		}
	} else if approval.InputFormDigest != "" || approval.InputValues != nil || approval.InputsDigest != "" {
		return app.Approval{}, ErrApprovalConflict
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
	if !reflect.DeepEqual(expected, current) || current.Status != app.ApprovalStatusPending {
		return app.Approval{}, ErrApprovalConflict
	}
	candidate, err := prepareApproval(command.Candidate, &current, current.CreatedAt)
	if err != nil {
		return app.Approval{}, err
	}
	if candidate.ID != current.ID || candidate.Source != current.Source || candidate.ExternalID != current.ExternalID ||
		candidate.SessionID != current.SessionID || candidate.RunID != current.RunID || candidate.ToolCallID != current.ToolCallID ||
		candidate.Tool != current.Tool || candidate.Risk != current.Risk || !reflect.DeepEqual(candidate.PolicyContext, current.PolicyContext) ||
		!reflect.DeepEqual(candidate.Presentation, current.Presentation) ||
		!reflect.DeepEqual(candidate.InputForm, current.InputForm) || candidate.InputFormDigest != current.InputFormDigest {
		return app.Approval{}, ErrApprovalConflict
	}
	candidate.Status = app.ApprovalStatusPending
	candidate.ResolvedAt = nil
	candidate.ResolutionNote = ""
	return candidate, nil
}

func prepareApprovalResolution(current app.Approval, status app.ApprovalStatus, note string, now time.Time) (app.Approval, bool, error) {
	return prepareApprovalInputResolution(current, status, note, now, app.ApprovalInputSubmission{})
}

func prepareApprovalInputResolution(current app.Approval, status app.ApprovalStatus, note string, now time.Time, input app.ApprovalInputSubmission) (app.Approval, bool, error) {
	status = app.ApprovalStatus(strings.TrimSpace(string(status)))
	if status != app.ApprovalStatusApproved && status != app.ApprovalStatusRejected && status != app.ApprovalStatusResolvedElsewhere {
		return app.Approval{}, false, errors.New("approval resolution status is invalid")
	}
	current, err := normalizePersistedApproval(current)
	if err != nil {
		return app.Approval{}, false, err
	}
	valuesDigest := ""
	if current.InputForm != nil {
		expected, err := app.ApprovalFormDigest(current)
		if err != nil || expected != current.InputFormDigest {
			return app.Approval{}, false, ErrApprovalConflict
		}
		if status == app.ApprovalStatusApproved {
			if input.Digest != expected || input.SchemaVersion != app.ApprovalInputSchemaVersion {
				return app.Approval{}, false, ErrApprovalConflict
			}
			if err := app.ValidateApprovalInputs(current.InputForm, input.Inputs); err != nil {
				return app.Approval{}, false, err
			}
			valuesDigest, err = app.ApprovalInputsDigest(input.Inputs)
			if err != nil {
				return app.Approval{}, false, err
			}
		} else if len(input.Inputs) > 0 || input.SchemaVersion != 0 || input.Digest != "" && input.Digest != expected {
			return app.Approval{}, false, ErrApprovalConflict
		}
	} else if input.SchemaVersion != 0 || len(input.Inputs) > 0 {
		return app.Approval{}, false, ErrApprovalConflict
	}
	if current.Status != app.ApprovalStatusPending {
		if current.Status == status && current.ResolutionNote == note && current.InputsDigest == valuesDigest {
			return current, true, nil
		}
		return app.Approval{}, false, ErrApprovalConflict
	}
	if valuesDigest != "" {
		raw, _ := json.Marshal(input.Inputs)
		if err := json.Unmarshal(raw, &current.InputValues); err != nil {
			return app.Approval{}, false, err
		}
		current.InputsDigest = valuesDigest
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
