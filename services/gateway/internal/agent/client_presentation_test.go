package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func clientPresentationForTest() app.ClientPresentation {
	return app.ClientPresentation{SchemaVersion: 1, ScheduleShow: true, Timezone: "Asia/Shanghai", RequestedAt: time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC)}
}

func TestClientSchedulePresentationUsesRealGuardAndSemanticRouting(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	p := clientPresentationForTest()
	ctx := WithClientPresentation(t.Context(), "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", p)
	result, err := runtime.HandleMessage(ctx, session.ID, "显示明天的日程")
	if err != nil {
		t.Fatal(err)
	}
	action := result.ClientAction
	if action == nil || !action.Valid() || action.DateStart != "2026-10-31" || action.DateEnd != "2026-11-01" || action.ActionID != "11111111-1111-4111-8111-111111111111" || action.ConversationID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("action=%+v route=%+v", action, result.RouteDecision)
	}
	if result.RouteDecision == nil || !routeTargetsCapability(*result.RouteDecision, app.CapabilityScheduleManage) || result.RouteDecision.Slots.Operation != app.RouteOperationRead {
		t.Fatalf("wrong semantic route: %+v", result.RouteDecision)
	}
	run, ok, err := st.GetRun(t.Context(), result.Run.ID)
	if err != nil || !ok || run.MessageContext.IntentFusion == nil {
		t.Fatal("semantic decision was bypassed", err)
	}
	calls, err := st.ListToolCalls(t.Context(), session.ID)
	if err != nil || len(calls) != 0 || len(result.ToolCalls) != 0 {
		t.Fatalf("read backend schedules instead of handoff: %+v %v", calls, err)
	}
	if !strings.Contains(result.Message.Content, "已请求") || strings.Contains(result.Message.Content, "已打开") {
		t.Fatal("premature presentation success", result.Message.Content)
	}
}

func TestClientSchedulePresentationDoesNotHijackOtherIntents(t *testing.T) {
	for _, goal := range []string{"你好", "明天上午提醒提交周报", "显示明天的日程并分析有没有冲突"} {
		t.Run(goal, func(t *testing.T) {
			runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
			defer closeRuntime()
			ctx := WithClientPresentation(t.Context(), "request", "conversation", clientPresentationForTest())
			result, err := runtime.HandleMessage(ctx, session.ID, goal)
			if err != nil {
				t.Fatal(err)
			}
			if result.ClientAction != nil {
				t.Fatalf("unexpected action: %+v", result.ClientAction)
			}
		})
	}
}

func TestClientSchedulePresentationRequiresCapability(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMessage(t.Context(), session.ID, "显示明天的日程")
	if err != nil {
		t.Fatal(err)
	}
	if result.ClientAction != nil {
		t.Fatal("unadvertised client action")
	}
}

func TestClientScheduleViewStrictModelSelection(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for _, test := range []struct{ goal, start, end, mode string }{
		{"显示全部日程", "", "", "show"},
		{"显示下周日程", "2026-11-02", "2026-11-09", "show"},
		{"显示2030-01-02的日程", "2030-01-02", "2030-01-03", "show"},
		{"显示明天下午日程", "", "", "clarify"},
		{"显示本机日程并分析冲突", "", "", "unsupported"},
	} {
		got, err := runtime.selectClientScheduleView(t.Context(), app.AgentRun{}, clientPresentationForTest(), test.goal)
		if err != nil || got["date_start"] != test.start || got["date_end"] != test.end || got["mode"] != test.mode {
			t.Fatalf("%s: %+v %v", test.goal, got, err)
		}
	}
	for _, raw := range []string{
		`{}`, `{"mode":"show","date_start":"","date_end":"","request_id":"evil"}`,
		`{"mode":"show","date_start":"2026-02-30","date_end":"2026-03-01"}`,
		`{"mode":"show","date_start":"2026-10-30","date_end":""}`,
		`{"mode":"show","date_start":"2026-10-30","date_end":"2026-10-29"}`,
		`{"mode":"unsupported","date_start":"2026-10-30","date_end":"2026-10-31"}`,
		`{"mode":"show","date_start":"","date_end":"","mode":"show"}`,
		`{"mode":"show","date_start":null,"date_end":""}`,
	} {
		_, err := runtime.selectClientScheduleView(t.Context(), app.AgentRun{}, clientPresentationForTest(), "显示日程\nMOCK_CLIENT_SCHEDULE_RESPONSE:"+raw)
		if err == nil {
			t.Fatalf("accepted malformed model decision: %s", raw)
		}
	}
}

func TestClientSchedulePresentationAllDatesAfterEarlierDisplay(t *testing.T) {
	for _, withHistory := range []bool{false, true} {
		t.Run(fmt.Sprint("history=", withHistory), func(t *testing.T) {
			runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
			defer closeRuntime()
			if withHistory {
				for _, m := range []app.Message{
					{Role: "user", Content: "你好，请用一句话介绍你自己。"},
					{Role: "assistant", Content: "I can answer this directly from the current conversation."},
					{Role: "user", Content: "显示明天的日程"},
					{Role: "assistant", Content: "已请求在当前桌面打开本机日程。"},
				} {
					m.SessionID = session.ID
					m.CreatedAt = time.Now().UTC()
					if _, err := st.AddMessage(t.Context(), m); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx := WithClientPresentation(t.Context(), "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", clientPresentationForTest())
			result, err := runtime.HandleMessage(ctx, session.ID, "显示我的日程")
			if err != nil {
				t.Fatal(err)
			}
			if result.ClientAction == nil || result.ClientAction.DateStart != "" || result.ClientAction.DateEnd != "" {
				t.Fatalf("full schedule display was not handed off: route=%+v action=%+v fusion=%+v", result.RouteDecision, result.ClientAction, result.Run.MessageContext.IntentFusion)
			}
		})
	}
}
