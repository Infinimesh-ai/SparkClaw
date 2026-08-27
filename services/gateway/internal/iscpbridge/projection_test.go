package iscpbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func projectionFixture(t *testing.T) (*GatewayAdapter, Principal, string) {
	t.Helper()
	st := store.NewMemoryStore()
	adapter := NewGatewayAdapter(st, func() AgentRuntime { return &adapterRuntime{started: make(chan struct{}, 1)} })
	principal := Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}
	ctx := context.Background()
	session, err := st.CreateSession(ctx, "Projection fixture")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	completedAt := now.Add(-time.Hour)
	if _, err := st.SaveRun(ctx, app.AgentRun{
		ID: "run_done", SessionID: session.ID, State: "completed",
		StartedAt: now.Add(-2 * time.Hour), CompletedAt: &completedAt,
		Summary: "整理产品评审资料",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRun(ctx, app.AgentRun{
		ID: "run_live", SessionID: session.ID, State: "running",
		StartedAt: now.Add(-10 * time.Minute), Summary: "同步共享日历",
	}); err != nil {
		t.Fatal(err)
	}
	failedAt := now.Add(-30 * time.Minute)
	if _, err := st.SaveRun(ctx, app.AgentRun{
		ID: "run_failed", SessionID: session.ID, State: "failed",
		StartedAt: now.Add(-40 * time.Minute), CompletedAt: &failedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveApproval(ctx, app.Approval{
		ID: "approval_1", SessionID: session.ID, RunID: "run_live",
		Tool: "send_email", Status: app.ApprovalStatusPending,
		Summary: "回复陈敏并确认会议", Reason: "外发邮件需要确认",
	}); err != nil {
		t.Fatal(err)
	}
	return adapter, principal, session.ID
}

func TestActivityListProjection(t *testing.T) {
	adapter, principal, sessionID := projectionFixture(t)
	request := validRequest(TypeActivityList, "req-act", "endpoint-app", "", "", ActivityListPayload{})
	response := adapter.Dispatch(t.Context(), principal, request)
	if response.Status != "ok" {
		t.Fatalf("activity list failed: %#v", response)
	}
	raw, _ := json.Marshal(response.Result)
	var result struct {
		Date       string         `json:"date"`
		Activities []ActivityView `json:"activities"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, activity := range result.Activities {
		kinds[activity.Kind]++
		if activity.SessionID != sessionID {
			t.Fatalf("activity outside the visible session: %#v", activity)
		}
	}
	if kinds[ActivityKindApprovalPending] != 1 || kinds[ActivityKindRunRunning] != 1 ||
		kinds[ActivityKindRunCompleted] != 1 || kinds[ActivityKindRunFailed] != 1 {
		t.Fatalf("unexpected activity kinds: %#v", kinds)
	}
	// Approval carries the actionable title verbatim.
	if result.Activities[0].Kind != ActivityKindApprovalPending || result.Activities[0].Title != "回复陈敏并确认会议" {
		t.Fatalf("first activity = %#v", result.Activities[0])
	}
}

func TestActivityListRejectsBadDate(t *testing.T) {
	adapter, principal, _ := projectionFixture(t)
	request := validRequest(TypeActivityList, "req-bad", "endpoint-app", "", "", ActivityListPayload{Date: "not-a-date"})
	response := adapter.Dispatch(t.Context(), principal, request)
	if response.Status != "error" || response.Error == nil || response.Error.Code != CodeInvalidRequest {
		t.Fatalf("expected invalid_request, got %#v", response)
	}
}

func TestSnapshotProjection(t *testing.T) {
	adapter, principal, _ := projectionFixture(t)
	request := validRequest(TypeSnapshotGet, "req-snap", "endpoint-app", "", "", struct{}{})
	response := adapter.Dispatch(t.Context(), principal, request)
	if response.Status != "ok" {
		t.Fatalf("snapshot failed: %#v", response)
	}
	raw, _ := json.Marshal(response.Result)
	var result struct {
		GeneratedAt time.Time      `json:"generated_at"`
		Cards       []SnapshotCard `json:"cards"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	byID := map[string]SnapshotCard{}
	for _, card := range result.Cards {
		byID[card.ID] = card
	}
	if byID["approvals-pending"].Title != "1 项操作等待确认" || byID["approvals-pending"].Summary != "回复陈敏并确认会议" {
		t.Fatalf("approvals card = %#v", byID["approvals-pending"])
	}
	if byID["runs-running"].Summary != "同步共享日历" {
		t.Fatalf("running card = %#v", byID["runs-running"])
	}
	if byID["runs-today"].Summary != "今天完成 1 项，失败 1 项" {
		t.Fatalf("daily card = %#v", byID["runs-today"])
	}
	// A foreign principal sees nothing.
	foreign := adapter.Dispatch(t.Context(), Principal{OwnerID: "someone-else", ActorID: "someone-else"},
		validRequest(TypeSnapshotGet, "req-snap-2", "endpoint-app", "", "", struct{}{}))
	rawForeign, _ := json.Marshal(foreign.Result)
	var foreignResult struct {
		Cards []SnapshotCard `json:"cards"`
	}
	_ = json.Unmarshal(rawForeign, &foreignResult)
	if len(foreignResult.Cards) != 0 {
		t.Fatalf("foreign owner saw cards: %#v", foreignResult.Cards)
	}
}

// TestActivityKindCoversRuntimeRunStates pins the projection to the run
// states the runtime actually writes. The original switch bucketed on
// "running"/"pending"/"approval_required" — none of which any writer in
// internal/agent produces — so every in-flight run fell through to the
// default and vanished from both the feed and the snapshot.
func TestActivityKindCoversRuntimeRunStates(t *testing.T) {
	inFlight := []string{
		"received", "routing", "executing", "workflow_step",
		"approval_pending", "browser_login_blocked", "clarification_required",
		"accepted",
	}
	for _, state := range inFlight {
		run := app.AgentRun{ID: "r", State: state, StartedAt: time.Now().UTC()}
		if kind := activityKindForRun(run); kind != ActivityKindRunRunning {
			t.Errorf("state %q bucketed as %q, want %q", state, kind, ActivityKindRunRunning)
		}
	}
	terminal := map[string]string{
		"completed": ActivityKindRunCompleted,
		"succeeded": ActivityKindRunCompleted,
		"failed":    ActivityKindRunFailed,
		"blocked":   ActivityKindRunFailed,
		"cancelled": ActivityKindRunFailed,
		"canceled":  ActivityKindRunFailed,
	}
	completedAt := time.Now().UTC()
	for state, want := range terminal {
		run := app.AgentRun{ID: "r", State: state, StartedAt: completedAt, CompletedAt: &completedAt}
		if kind := activityKindForRun(run); kind != want {
			t.Errorf("state %q bucketed as %q, want %q", state, kind, want)
		}
	}
	// Casing and padding are normalized, never dropped.
	padded := app.AgentRun{ID: "r", State: "  Executing  ", StartedAt: time.Now().UTC()}
	if kind := activityKindForRun(padded); kind != ActivityKindRunRunning {
		t.Errorf("padded state bucketed as %q", kind)
	}
}
