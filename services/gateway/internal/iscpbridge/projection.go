// Read-only phone projections (WS-5 Phase C): agent.activity.list.v1 and
// agent.snapshot.get.v1 aggregate EXISTING store state (approvals, runs,
// notifications) into the JingSi home-screen shapes. Deliberately a
// projection, not a new store entity — no second ingress, result path, or
// message store (docs/jingsi-lan-connection-design.md invariants).
//
// Scaling, deliberately deferred: both handlers scan every run of every
// visible session per request and filter in Go, because Store.ListRuns takes
// only a session id. That is the right trade at a household's scale (tens of
// sessions), and the honest alternative is a time-bounded store query, which
// means an interface change across all three backends plus the operationSpecs
// guards. Half-measures — capping sessions scanned, or stopping early — would
// silently skew a feed that claims to be newest-first, which is worse than a
// linear scan. Revisit when a deployment's run count makes the scan visible,
// and do it in the store, not here.
package iscpbridge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	activityListMaxLimit     = 200
	activityListDefaultLimit = 50
)

// ActivityKind is an open vocabulary; consumers must tolerate unknown kinds.
const (
	ActivityKindApprovalPending = "approval_pending"
	ActivityKindRunRunning      = "run_running"
	ActivityKindRunCompleted    = "run_completed"
	ActivityKindRunFailed       = "run_failed"
)

type ActivityListPayload struct {
	// Date selects the activity day (UTC, "2006-01-02"); empty means today.
	Date  string `json:"date,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type ActivityView struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Detail     string    `json:"detail,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	RunID      string    `json:"run_id,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type SnapshotCard struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
	Priority int    `json:"priority"`
}

// activityKindForRun buckets a run for the phone feed. The run-state
// vocabulary is NOT restated here: it is derived from operationStateForRun,
// the package's single normalizer for app.AgentRun.State (adapter.go), which
// already lowercases, trims, and covers every state the runtime writes
// (received/routing/executing/workflow_step/approval_pending/blocked/
// browser_login_blocked/clarification_required/...). Bucketing on raw
// run.State instead silently dropped every in-flight run, because the
// runtime never writes "running" or "pending" at all.
func activityKindForRun(run app.AgentRun) string {
	switch operationStateForRun(run) {
	case "completed":
		return ActivityKindRunCompleted
	case "failed", "cancelled":
		return ActivityKindRunFailed
	default:
		// running + approval_required: still in flight from the phone's view.
		return ActivityKindRunRunning
	}
}

// visibleSessionIDs returns the principal's visible session ids, the scope
// every projection below is limited to.
func (a *GatewayAdapter) visibleSessionIDs(ctx context.Context, principal Principal) (map[string]struct{}, error) {
	listed, err := a.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	visible := map[string]struct{}{}
	for _, session := range listed {
		if ownerIDForSession(session) == principal.OwnerID && !session.Hidden {
			visible[session.ID] = struct{}{}
		}
	}
	return visible, nil
}

func (a *GatewayAdapter) listActivities(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	var payload ActivityListPayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	day := now.UTC().Truncate(24 * time.Hour)
	if strings.TrimSpace(payload.Date) != "" {
		parsed, err := time.Parse("2006-01-02", payload.Date)
		if err != nil {
			return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "date must be YYYY-MM-DD", false), now)
		}
		day = parsed.UTC()
	}
	limit := payload.Limit
	if limit <= 0 {
		limit = activityListDefaultLimit
	}
	if limit > activityListMaxLimit {
		limit = activityListMaxLimit
	}
	dayEnd := day.Add(24 * time.Hour)

	visible, err := a.visibleSessionIDs(ctx, principal)
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "sessions are temporarily unavailable", true), now)
	}

	activities := make([]ActivityView, 0)

	// Pending approvals surface regardless of their day: they are actionable
	// until resolved.
	approvals, err := a.store.ListApprovals(ctx, app.ApprovalStatusPending)
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "approval state is temporarily unavailable", true), now)
	}
	for _, approval := range approvals {
		if _, ok := visible[approval.SessionID]; !ok {
			continue
		}
		activities = append(activities, ActivityView{
			ID:         "approval-" + approval.ID,
			Kind:       ActivityKindApprovalPending,
			Title:      approval.Summary,
			Detail:     approval.Reason,
			SessionID:  approval.SessionID,
			RunID:      approval.RunID,
			OccurredAt: now,
		})
	}

	for sessionID := range visible {
		runs, err := a.store.ListRuns(ctx, sessionID)
		if err != nil {
			continue
		}
		for _, run := range runs {
			occurred := run.StartedAt.UTC()
			if run.CompletedAt != nil {
				occurred = run.CompletedAt.UTC()
			}
			if occurred.Before(day) || !occurred.Before(dayEnd) {
				continue
			}
			title := run.Summary
			if strings.TrimSpace(title) == "" {
				title = "Agent run " + run.ID
			}
			activities = append(activities, ActivityView{
				ID:         "run-" + run.ID,
				Kind:       activityKindForRun(run),
				Title:      title,
				SessionID:  run.SessionID,
				RunID:      run.ID,
				OccurredAt: occurred,
			})
		}
	}

	// Newest first; actionable approvals ahead of same-time run entries.
	sort.SliceStable(activities, func(i, j int) bool {
		if !activities[i].OccurredAt.Equal(activities[j].OccurredAt) {
			return activities[i].OccurredAt.After(activities[j].OccurredAt)
		}
		return activities[i].Kind == ActivityKindApprovalPending && activities[j].Kind != ActivityKindApprovalPending
	})
	if len(activities) > limit {
		activities = activities[:limit]
	}
	return newResponse(req, "ok", map[string]any{
		"date":       day.Format("2006-01-02"),
		"activities": activities,
	}, nil, nil, now)
}

func (a *GatewayAdapter) snapshot(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	if err := DecodePayload(req.Payload, &struct{}{}); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	visible, err := a.visibleSessionIDs(ctx, principal)
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "sessions are temporarily unavailable", true), now)
	}
	day := now.UTC().Truncate(24 * time.Hour)
	cards := make([]SnapshotCard, 0)

	pendingApprovals := 0
	firstApprovalSummary := ""
	approvals, err := a.store.ListApprovals(ctx, app.ApprovalStatusPending)
	if err == nil {
		for _, approval := range approvals {
			if _, ok := visible[approval.SessionID]; !ok {
				continue
			}
			pendingApprovals++
			if firstApprovalSummary == "" {
				firstApprovalSummary = approval.Summary
			}
		}
	}
	if pendingApprovals > 0 {
		cards = append(cards, SnapshotCard{
			ID: "approvals-pending", Kind: "approvals",
			Title:    fmt.Sprintf("%d 项操作等待确认", pendingApprovals),
			Summary:  firstApprovalSummary,
			Priority: 0,
		})
	}

	running, completed, failed := 0, 0, 0
	latestRunning := ""
	for sessionID := range visible {
		runs, err := a.store.ListRuns(ctx, sessionID)
		if err != nil {
			continue
		}
		for _, run := range runs {
			switch activityKindForRun(run) {
			case ActivityKindRunRunning:
				running++
				if latestRunning == "" && strings.TrimSpace(run.Summary) != "" {
					latestRunning = run.Summary
				}
			case ActivityKindRunCompleted:
				if run.CompletedAt != nil && !run.CompletedAt.UTC().Before(day) {
					completed++
				}
			case ActivityKindRunFailed:
				if run.CompletedAt != nil && !run.CompletedAt.UTC().Before(day) {
					failed++
				}
			}
		}
	}
	if running > 0 {
		cards = append(cards, SnapshotCard{
			ID: "runs-running", Kind: "running",
			Title:    fmt.Sprintf("%d 个任务正在进行", running),
			Summary:  latestRunning,
			Priority: 1,
		})
	}
	if completed > 0 || failed > 0 {
		summary := fmt.Sprintf("今天完成 %d 项", completed)
		if failed > 0 {
			summary += fmt.Sprintf("，失败 %d 项", failed)
		}
		cards = append(cards, SnapshotCard{
			ID: "runs-today", Kind: "daily_summary",
			Title:    "今日执行摘要",
			Summary:  summary,
			Priority: 2,
		})
	}
	if unread, err := a.store.CountUnreadPassiveNotifications(ctx, principal.OwnerID); err == nil && unread > 0 {
		cards = append(cards, SnapshotCard{
			ID: "notifications-unread", Kind: "notifications",
			Title:    fmt.Sprintf("%d 条未读通知", unread),
			Summary:  "来自外部工具的提及与评论",
			Priority: 3,
		})
	}
	return newResponse(req, "ok", map[string]any{
		"generated_at": now,
		"cards":        cards,
	}, nil, nil, now)
}
