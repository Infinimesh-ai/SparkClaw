package agent

import (
	"context"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestScheduleSemanticRoutingTreatsTemporalChineseReminderAsCreate(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for _, goal := range []string{
		"一分钟后提醒吃饭",
		"明天上午提醒提交周报",
		"每天下午提醒站起来活动",
	} {
		decision := mustRouteIntent(t, runtime, goal)
		if decision.Status != app.RouteMatched || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityScheduleManage ||
			decision.Slots.Operation != app.RouteOperationCreate || decision.Slots.Query != goal {
			t.Fatalf("temporal reminder did not deterministically route to schedule creation: goal=%q route=%#v", goal, decision)
		}
	}
	read := mustRouteIntent(t, runtime, "查看明天的提醒")
	if read.Status != app.RouteMatched || read.Slots.Operation != app.RouteOperationRead {
		t.Fatalf("temporal schedule query was misclassified as creation: route=%#v", read)
	}
	discussion := mustRouteIntent(t, runtime, "提醒功能为什么没有触发")
	if discussion.Status == app.RouteMatched && len(discussion.CapabilityPath) == 2 && discussion.CapabilityPath[1] == app.CapabilityScheduleManage {
		t.Fatalf("reminder discussion was misclassified as schedule management: route=%#v", discussion)
	}
	paraphrase := mustRouteIntent(t, runtime, "一分钟后告知我吃饭")
	if paraphrase.Status != app.RouteMatched || paraphrase.Slots.Operation != app.RouteOperationCreate {
		t.Fatalf("semantic paraphrase was not covered by fusion routing: route=%#v", paraphrase)
	}
}

func TestScheduleSemanticParaphraseIsSelectedByFusionRouting(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()

	const goal = "一分钟后告知我吃饭"
	routing, err := runtime.routeIntent(t.Context(), session.ID, "run_schedule_semantic_paraphrase", goal)
	if err != nil {
		t.Fatal(err)
	}
	if routing.Route.Status != app.RouteMatched || len(routing.Route.CapabilityPath) != 2 || routing.Route.CapabilityPath[1] != app.CapabilityScheduleManage ||
		routing.Route.Slots.Operation != app.RouteOperationCreate || routing.Route.Slots.Query != goal {
		t.Fatalf("Fast semantic route did not select schedule creation and freeze the owner query: %#v", routing.Route)
	}
	if routing.Fusion == nil || len(routing.Fusion.Candidates) == 0 || routing.Fusion.Candidates[0].CandidateID != "schedule.manage#create" {
		t.Fatalf("semantic schedule route did not persist fusion evidence: routing=%#v audit=%#v", routing, mustAgentListAudit(t, st, session.ID))
	}
}

func TestScheduleEditWorkflowListsResolvesAndVersionBindsMutation(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()

	created, err := runtime.tools.Execute(t.Context(), "reminders.create", map[string]any{
		"text": "旧提醒", "due_time": "2026-08-01T09:00:00+08:00", "timezone": "Asia/Shanghai",
	}, session.ID, "run_create")
	if err != nil {
		t.Fatal(err)
	}
	createdOutput := created.Output.(map[string]any)
	newText, newDue, timezone, recurrence := "新提醒", "2026-08-02T10:30", "Asia/Shanghai", "weekly"
	route, err := runtime.scheduleActionRoute(ScheduleAction{
		Operation: app.RouteOperationEdit, ScheduleID: createdOutput["reminder_id"].(string),
		ExpectedUpdatedAt: createdOutput["updated_at"].(string), Text: &newText, DueTime: &newDue,
		Timezone: &timezone, Recurrence: &recurrence,
	}, "修改定时任务")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	user := storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: "修改定时任务", CreatedAt: started})
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "received", Risk: app.RiskReversible, StartedAt: started,
		MessageContext: &app.MessageRunContext{OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID}, Route: route},
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !exactVisibleToolNames(dispatch.Tools, "reminders.list", "observation.read") {
		t.Fatalf("edit workflow must expose list first: %#v", visibleToolNames(dispatch.Tools))
	}

	listCall, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "reminders.list", Args: map[string]any{"status": "pending"}, WorkflowID: app.WorkflowScheduleManage,
		WorkflowNodeID: "schedule_manage", ScopeRevision: 1, Capability: app.ToolCapabilityScheduleManage,
	})
	if approval != nil || !toolCallCompleted(listCall) {
		t.Fatalf("schedule discovery failed: call=%#v approval=%#v", listCall, approval)
	}
	definition, _ := runtime.tools.Definition(listCall.Tool)
	outcome, err := adaptWorkflowOutcome(definition, listCall)
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := testGetRun(st, dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(storedRun.Workflow, outcome)
	if assessment.ReasonCode != "schedule_target_resolved" || len(assessment.SelectedRefs) != 1 {
		t.Fatalf("schedule target was not uniquely resolved: %#v", assessment)
	}
	changed, err := applyWorkflowOutcome(&storedRun, outcome, assessment)
	if err != nil || !changed {
		t.Fatalf("schedule workflow did not enter mutation stage: changed=%t err=%v", changed, err)
	}
	testSaveRun(st, storedRun)
	stageContext := dispatch.Profile.StageContext(storedRun.Workflow)
	mutationTools, err := runtime.materializeActiveWorkflowTools(context.Background(), storedRun, runtime.workflowActorRef(storedRun), &stageContext)
	if err != nil {
		t.Fatal(err)
	}
	if !exactVisibleToolNames(mutationTools, "reminders.update", "observation.read") {
		t.Fatalf("edit workflow exposed the wrong mutation: %#v", visibleToolNames(mutationTools))
	}
	node := storedRun.Workflow.Nodes["schedule_manage"]
	updateCall, updateApproval, _, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "reminders.update", Args: map[string]any{
			"reminder_id": "model-invented-id", "expected_updated_at": "2000-01-01T00:00:00Z",
			"text": "model invented text", "due_time": "2000-01-01T00:00", "timezone": "UTC", "recurrence": "daily",
		}, WorkflowID: app.WorkflowScheduleManage, WorkflowNodeID: "schedule_manage",
		ScopeRevision: node.ScopeRevision, Capability: app.ToolCapabilityScheduleManage,
	})
	if updateApproval != nil || !toolCallCompleted(updateCall) {
		t.Fatalf("bound schedule update failed: call=%#v approval=%#v", updateCall, updateApproval)
	}
	for key, expected := range map[string]string{
		"reminder_id": createdOutput["reminder_id"].(string), "expected_updated_at": createdOutput["updated_at"].(string),
		"text": newText, "due_time": newDue, "timezone": timezone, "recurrence": recurrence,
	} {
		if updateCall.Arguments[key] != expected {
			t.Fatalf("workflow did not bind %s: got %#v want %q", key, updateCall.Arguments[key], expected)
		}
	}
	definition, _ = runtime.tools.Definition(updateCall.Tool)
	outcome, err = adaptWorkflowOutcome(definition, updateCall)
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ = testGetRun(st, storedRun.ID)
	assessment = dispatch.Profile.Assess(storedRun.Workflow, outcome)
	if assessment.Status != app.AssessmentComplete || assessment.ReasonCode != "schedule_changed" {
		t.Fatalf("schedule mutation did not produce completion evidence: %#v", assessment)
	}
	if _, err := applyWorkflowOutcome(&storedRun, outcome, assessment); err != nil || storedRun.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("schedule workflow did not complete: status=%q err=%v", storedRun.Workflow.Status, err)
	}
	reminder, _ := st.GetReminder(createdOutput["reminder_id"].(string))
	if reminder.Text != newText || reminder.Recurrence != recurrence || reminder.DueTime.Format(time.RFC3339) != "2026-08-02T02:30:00Z" {
		t.Fatalf("schedule update mismatch: %#v", reminder)
	}
}

func TestScheduleDeleteWorkflowRejectsChangedTargetBeforeMutation(t *testing.T) {
	profile := scheduleManageProfile{}
	route := app.RouteDecision{
		Slots: app.RouteSlots{Operation: app.RouteOperationDelete, TargetRef: "reminder-1"},
		Facts: map[string]string{"schedule_expected_updated_at": "2026-07-23T10:00:00Z"},
	}
	_, plan, err := profile.Resolve(route, "turn")
	if err != nil {
		t.Fatal(err)
	}
	state := newWorkflowState(route, app.ReturnRoute{Mode: app.ReturnToSource}, app.IntentEnvelope{}, plan)
	outcome := app.ToolOutcome{NodeID: "schedule_manage", Status: "completed", Signals: []app.OutcomeSignal{app.OutcomeSignalSchedulesListed}, Refs: []app.ResourceRef{{
		Kind: "schedule", Ref: "reminder-1", Attributes: map[string]string{"status": "pending", "updated_at": "2026-07-23T10:01:00Z"},
	}}}
	assessment := profile.Assess(state, outcome)
	if assessment.Status != app.AssessmentBlocked || assessment.ReasonCode != "schedule_target_changed" {
		t.Fatalf("delete workflow accepted a stale target: %#v", assessment)
	}
}
