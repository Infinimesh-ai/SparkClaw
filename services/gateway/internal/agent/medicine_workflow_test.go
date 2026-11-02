package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const medicineBatchText = "添加布洛芬 24 粒，有效期2027-05；碘伏 1 瓶，无有效期"

func testMedicinePlan(missing bool) string {
	p := medicineEditPlan{Operation: "create", Items: []medicinePlannedEntry{
		{Changes: map[string]any{"name": "布洛芬", "quantity": 24.0, "unit": "capsule", "expires_on": "2027-05"}, Unresolved: []medicineUnresolved{}, Evidence: map[string]string{"name": "布洛芬", "quantity": "24 粒", "unit": "24 粒", "expires_on": "2027-05"}},
		{Changes: map[string]any{"name": "碘伏", "quantity": 1.0, "unit": "bottle", "no_expiry": true}, Unresolved: []medicineUnresolved{}, Evidence: map[string]string{"name": "碘伏", "quantity": "1 瓶", "unit": "1 瓶", "no_expiry": "无有效期"}},
	}}
	if missing {
		delete(p.Items[0].Changes, "expires_on")
		delete(p.Items[0].Evidence, "expires_on")
	}
	raw, _ := json.Marshal(p)
	return string(raw)
}
func newMedicineRuntime(t *testing.T, response string) (Runtime, *store.MemoryStore, app.Session) {
	t.Helper()
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	t.Cleanup(closeRuntime)
	runtime.tools.WithMedicineRepository(st)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": medicineModelFixture(response)}}}})
	}))
	t.Cleanup(server.Close)
	cfg := agentTestConfig()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	cfg.Model.Fast.Model = "test-fast"
	runtime.models = modelrouter.New(cfg)
	return runtime, st, session
}
func dispatchMedicineTest(t *testing.T, r Runtime, st *store.MemoryStore, session app.Session, op app.RouteOperation, text string, source app.MessageSourceKind) (app.AgentRun, workflowExecutionResult) {
	t.Helper()
	now := time.Now().UTC()
	message := app.Message{ID: app.NewID("msg"), SessionID: session.ID, Role: "user", Content: text, CreatedAt: now}
	if _, err := st.AddMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: now, State: "executing", MessageContext: &app.MessageRunContext{OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID}, Source: app.MessageSourceContext{Kind: source}}}
	route := app.RouteDecision{SchemaVersion: 1, Status: app.RouteMatched, CatalogRevision: r.capabilities.Revision(), CapabilityPath: []app.CapabilityID{"medicine", app.CapabilityMedicineBoxEdit}, Slots: app.RouteSlots{Operation: op, Query: text}}
	d, err := r.dispatchMatchedWorkflow(t.Context(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := r.runWorkflow(t.Context(), session.ID, d.Run, text, d.Profile, d.Context, d.Tools)
	stored, ok, err := st.GetRun(t.Context(), run.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if len(result.Approvals) > 0 {
		stored.State = "approval_pending"
		if _, err = st.SaveRun(t.Context(), stored); err != nil {
			t.Fatal(err)
		}
	}
	return stored, result
}
func TestMedicineCompleteBatchWritesDirectly(t *testing.T) {
	r, st, s := newMedicineRuntime(t, testMedicinePlan(false))
	run, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationCreate, medicineBatchText, app.MessageSourceWeb)
	if len(result.Approvals) != 0 || run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("run=%+v result=%+v", run.Workflow, result)
	}
	box, err := st.ReadMedicineBox(t.Context(), s.OwnerID)
	if err != nil || len(box.Items) != 2 {
		t.Fatalf("box=%+v err=%v", box, err)
	}
	if box.Items[0].ExpiresOn != "2027-05-31" && box.Items[1].ExpiresOn != "2027-05-31" {
		t.Fatal("month was not normalized")
	}
	calls := toolCallsForRun(testListToolCalls(st, s.ID), run.ID)
	if len(calls) != 2 || calls[0].Tool != "medicine.box.read" || calls[1].Tool != "medicine.box.write" {
		t.Fatal(calls)
	}
}
func TestMedicineCompletionApprovalResumesFrozenBatchExactlyOnce(t *testing.T) {
	r, st, s := newMedicineRuntime(t, testMedicinePlan(true))
	run, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationCreate, medicineBatchText, app.MessageSourceWeb)
	if len(result.Approvals) != 1 {
		t.Fatalf("no completion form: %+v", result)
	}
	pending := result.Approvals[0]
	if pending.InputForm == nil || len(pending.InputForm.Entries) != 2 || len(pending.InputForm.Entries[0].Fields) != 1 || len(pending.InputForm.Entries[1].Fields) != 0 {
		t.Fatal(pending)
	}
	box, _ := st.ReadMedicineBox(t.Context(), s.OwnerID)
	if len(box.Items) != 0 {
		t.Fatal("partial batch was committed")
	}
	approved, err := st.ResolveApprovalWithInputs(t.Context(), pending.ID, app.ApprovalStatusApproved, "", app.ApprovalInputSubmission{Digest: pending.InputFormDigest, SchemaVersion: 1, Inputs: app.ApprovalInputValues{"entry_1": {"expiry": "2028-01-31"}}})
	if err != nil {
		t.Fatal(err)
	}
	call, err := r.ExecuteApprovedToolCall(t.Context(), approved)
	if err != nil || !call.Status.Completed() {
		t.Fatalf("call=%+v err=%v", call, err)
	}
	resultAfter, resumed, err := r.ResumeRunAfterApproval(t.Context(), s.ID, run.ID)
	if err != nil || !resumed || resultAfter.Run.State != "completed" {
		t.Fatalf("resumed=%v result=%+v err=%v", resumed, resultAfter, err)
	}
	box, _ = st.ReadMedicineBox(t.Context(), s.OwnerID)
	if len(box.Items) != 2 {
		t.Fatal(box)
	}
	if _, err = r.ExecuteApprovedToolCall(t.Context(), approved); err == nil {
		t.Fatal("duplicate approval executed")
	}
	_, resumed, err = r.ResumeRunAfterApproval(t.Context(), s.ID, run.ID)
	if err != nil || resumed {
		t.Fatalf("duplicate resume: %v %v", resumed, err)
	}
	box, _ = st.ReadMedicineBox(t.Context(), s.OwnerID)
	if len(box.Items) != 2 {
		t.Fatal(box)
	}
}
func TestMedicineMissingFieldsOnNonFormChannelNeverWrites(t *testing.T) {
	r, st, s := newMedicineRuntime(t, testMedicinePlan(true))
	_, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationCreate, medicineBatchText, app.MessageSourceKind("wechat"))
	if len(result.Approvals) != 0 || !strings.Contains(result.FinalAnswer, "需要补全") {
		t.Fatalf("result=%+v", result)
	}
	box, _ := st.ReadMedicineBox(t.Context(), s.OwnerID)
	if len(box.Items) != 0 {
		t.Fatal(box)
	}
}
func TestMedicinePlanBatchAndFieldValidation(t *testing.T) {
	for _, n := range []int{1, 2, 20, 21} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			plan, _ := decodeMedicinePlan(testMedicinePlan(false))
			base := plan.Items[0]
			plan.Items = nil
			for i := 0; i < n; i++ {
				plan.Items = append(plan.Items, base)
			}
			raw, _ := json.Marshal(plan)
			parsed, err := decodeMedicinePlan(string(raw))
			if n > 20 {
				if err == nil {
					t.Fatal("oversized batch accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			draft, form, err := freezeMedicinePlan(parsed, nil, "run", medicineBatchText)
			if err != nil || form != nil || len(draft.Items) != n {
				t.Fatalf("draft=%+v form=%+v err=%v", draft, form, err)
			}
		})
	}
	plan, _ := decodeMedicinePlan(testMedicinePlan(false))
	plan.Items[0].Evidence["quantity"] = "fabricated"
	_, form, err := freezeMedicinePlan(plan, nil, "run", medicineBatchText)
	if err != nil || form == nil || form.Entries[0].Fields[0].Name != "quantity" {
		t.Fatalf("unsupported value was not requested: %+v %v", form, err)
	}
	for _, changes := range []map[string]any{{"quantity_delta": 0.001}, {"expires_on": "2027-01-01", "no_expiry": true}, {"quantity": "5"}} {
		if _, err := applyMedicineChanges(medicine.Fields{}, changes); err == nil {
			t.Fatal(changes)
		}
	}
}
func TestMedicineDuplicateTargetsAndVersionConflictsAreAtomic(t *testing.T) {
	r, st, s := newMedicineRuntime(t, "")
	seeded, err := medicine.New(st).Apply(t.Context(), s.OwnerID, medicine.Batch{OperationID: "seed", Operation: "create", Items: []medicine.Entry{{EntryID: "a", Fields: medicine.Fields{Name: "同名药", Category: "other", Quantity: 5, Unit: "tablet", ExpiresOn: "2027-01-01"}}, {EntryID: "b", Fields: medicine.Fields{Name: "同名药", Category: "other", Quantity: 6, Unit: "tablet", ExpiresOn: "2028-01-01"}}}})
	if err != nil {
		t.Fatal(err)
	}
	p := medicineEditPlan{Operation: "update", Items: []medicinePlannedEntry{{Target: &medicineTarget{Name: "同名药"}, Changes: map[string]any{"quantity_delta": -1.0}, Unresolved: []medicineUnresolved{}, Evidence: map[string]string{"target": "同名药", "quantity_delta": "减1片"}}}}
	raw, _ := json.Marshal(p)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": medicineModelFixture(string(raw))}}}})
	}))
	defer server.Close()
	cfg := agentTestConfig()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	r.models = modelrouter.New(cfg)
	run, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationEdit, "同名药减1片", app.MessageSourceWeb)
	if len(result.Approvals) != 1 {
		t.Fatal(result)
	}
	pending := result.Approvals[0]
	target := *seeded.Items[0].After
	target.Quantity = 9
	_, err = medicine.New(st).Apply(t.Context(), s.OwnerID, medicine.Batch{OperationID: "concurrent", Operation: "update", Items: []medicine.Entry{{EntryID: "c", ID: target.ID, BaseVersion: target.Version, Fields: target.Fields}}})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := st.ResolveApprovalWithInputs(t.Context(), pending.ID, app.ApprovalStatusApproved, "", app.ApprovalInputSubmission{Digest: pending.InputFormDigest, SchemaVersion: 1, Inputs: app.ApprovalInputValues{"entry_1": {"target": target.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ExecuteApprovedToolCall(t.Context(), approved); err != nil {
		t.Fatal(err)
	}
	resumed, ok, err := r.ResumeRunAfterApproval(t.Context(), s.ID, run.ID)
	if err != nil || !ok || resumed.Run.State != "blocked" {
		t.Fatalf("%+v %v %v", resumed, ok, err)
	}
	box, _ := st.ReadMedicineBox(t.Context(), s.OwnerID)
	for _, item := range box.Items {
		if item.ID == target.ID && item.Quantity != 9 {
			t.Fatal(box)
		}
	}
}

func TestMedicineDeleteReportsAndRemovesExactlyOneRecord(t *testing.T) {
	p := medicineEditPlan{Operation: "delete", Items: []medicinePlannedEntry{{Target: &medicineTarget{Name: "碘伏"}, Changes: map[string]any{}, Unresolved: []medicineUnresolved{}, Evidence: map[string]string{"target": "碘伏"}}}}
	raw, _ := json.Marshal(p)
	r, st, s := newMedicineRuntime(t, string(raw))
	_, err := medicine.New(st).Apply(t.Context(), s.OwnerID, medicine.Batch{OperationID: "seed", Operation: "create", Items: []medicine.Entry{{EntryID: "one", Fields: medicine.Fields{Name: "碘伏", Category: "wound", Quantity: 1, Unit: "bottle", NoExpiry: true, Location: "抽屉"}}}})
	if err != nil {
		t.Fatal(err)
	}
	run, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationDelete, "删除碘伏", app.MessageSourceWeb)
	if run.Workflow.Status != app.WorkflowStatusSucceeded || len(result.Approvals) != 0 || !strings.Contains(result.FinalAnswer, "已删除：碘伏") || !strings.Contains(result.FinalAnswer, "抽屉") {
		t.Fatalf("%+v", result)
	}
	box, _ := st.ReadMedicineBox(t.Context(), s.OwnerID)
	if len(box.Items) != 0 {
		t.Fatal(box)
	}
}
func TestMedicineCancelCompletionWritesNothing(t *testing.T) {
	r, st, s := newMedicineRuntime(t, testMedicinePlan(true))
	run, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationCreate, medicineBatchText, app.MessageSourceWeb)
	if len(result.Approvals) != 1 {
		t.Fatal(result)
	}
	if _, err := st.ResolveApprovalWithInputs(t.Context(), result.Approvals[0].ID, app.ApprovalStatusRejected, "cancel", app.ApprovalInputSubmission{}); err != nil {
		t.Fatal(err)
	}
	if err := r.CompleteRunIfApprovalsResolved(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, resumed, err := r.ResumeRunAfterApproval(t.Context(), s.ID, run.ID); err != nil || resumed {
		t.Fatalf("%v %v", resumed, err)
	}
	box, _ := st.ReadMedicineBox(t.Context(), s.OwnerID)
	if len(box.Items) != 0 {
		t.Fatal(box)
	}
}

func TestMedicineQueryProjectionAndAdviceReadOnly(t *testing.T) {
	for _, advise := range []bool{false, true} {
		t.Run(fmt.Sprint(advise), func(t *testing.T) {
			r, st, s := newMedicineRuntime(t, "记录回答")
			now := time.Now().UTC()
			op := app.RouteOperationRead
			if advise {
				op = app.RouteOperationAnswer
			}
			route := app.RouteDecision{SchemaVersion: 1, Status: app.RouteMatched, CatalogRevision: r.capabilities.Revision(), CapabilityPath: []app.CapabilityID{"medicine", app.CapabilityMedicineBoxQuery}, Slots: app.RouteSlots{Operation: op, Query: "药箱问题", FactScope: app.RouteFactScopeMedicineBox}}
			d, err := r.dispatchMatchedWorkflow(t.Context(), app.AgentRun{ID: app.NewID("run"), SessionID: s.ID, StartedAt: now}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
			if err != nil {
				t.Fatal(err)
			}
			result := r.runWorkflow(t.Context(), s.ID, d.Run, "药箱问题", d.Profile, d.Context, d.Tools)
			if !result.Completed || len(result.ToolCalls) != 1 || result.ToolCalls[0].Tool != "medicine.box.read" || strings.HasSuffix(result.FinalAnswer, "以上仅供参考，用药请以说明书或医生、药师意见为准。") != advise {
				t.Fatalf("%+v", result)
			}
			box, _ := st.ReadMedicineBox(t.Context(), s.OwnerID)
			if len(box.Items) != 0 {
				t.Fatal("query mutated inventory")
			}
		})
	}
	payload := medicineQueryProjection([]medicine.Item{{Fields: medicine.Fields{Name: "过期药", ExpiresOn: "2026-11-01", Notes: strings.Repeat("注", 200)}}, {Fields: medicine.Fields{Name: "临期药", ExpiresOn: "2026-12-02"}}, {Fields: medicine.Fields{Name: "未知"}}, {Fields: medicine.Fields{Name: "无期限", NoExpiry: true}}}, "2026-11-02")
	if !strings.Contains(payload, `"status":"expired"`) || !strings.Contains(payload, `"days_left":-1`) || !strings.Contains(payload, `"status":"expiring"`) || !strings.Contains(payload, `"status":"unknown"`) || strings.Contains(payload, strings.Repeat("注", 121)) {
		t.Fatal(payload)
	}
	rows := make([]medicine.Item, 2000)
	for i := range rows {
		rows[i].Name = fmt.Sprintf("药品%d%s", i, strings.Repeat("长", 60))
		rows[i].Usage = strings.Repeat("用", 80)
		rows[i].Notes = strings.Repeat("注", 500)
	}
	payload = medicineQueryProjection(rows, "2026-11-02")
	if len(payload) > 48<<10 || !strings.Contains(payload, `"truncated":true`) || !strings.Contains(payload, `"total":2000`) {
		t.Fatal("unbounded or silently truncated query")
	}
}

func medicineModelFixture(raw string) string {
	var p medicineEditPlan
	if json.Unmarshal([]byte(raw), &p) != nil || p.Operation == "" {
		return raw
	}
	wire := medicineModelPlan{Operation: p.Operation}
	for _, item := range p.Items {
		row := medicineModelEntry{Changes: []medicineModelChange{}, Unresolved: item.Unresolved}
		for _, key := range medicineChangeFields {
			if value, ok := item.Changes[key]; ok {
				row.Changes = append(row.Changes, medicineModelChange{Field: key, Value: value, Evidence: item.Evidence[key]})
			}
		}
		if item.Target != nil {
			row.Target = &medicineModelTarget{Name: item.Target.Name, ExpiresOn: item.Target.ExpiresOn, Location: item.Target.Location, Evidence: item.Evidence["target"]}
		}
		wire.Items = append(wire.Items, row)
	}
	data, _ := json.Marshal(wire)
	return string(data)
}

func TestMedicineTwentyCompleteRowsCommitAsOneBatch(t *testing.T) {
	p, _ := decodeMedicinePlan(testMedicinePlan(false))
	base := p.Items[0]
	p.Items = nil
	for i := 0; i < 20; i++ {
		p.Items = append(p.Items, base)
	}
	raw, _ := json.Marshal(p)
	r, st, s := newMedicineRuntime(t, string(raw))
	run, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationCreate, medicineBatchText, app.MessageSourceWeb)
	if run.Workflow.Status != app.WorkflowStatusSucceeded || len(result.Approvals) != 0 {
		t.Fatalf("%+v", result)
	}
	box, err := st.ReadMedicineBox(t.Context(), s.OwnerID)
	if err != nil || len(box.Items) != 20 {
		t.Fatalf("box=%+v err=%v", box, err)
	}
	calls := toolCallsForRun(testListToolCalls(st, s.ID), run.ID)
	if len(calls) != 2 || calls[1].Tool != "medicine.box.write" {
		t.Fatal(calls)
	}
}
func TestMedicineModelPlanRejectsDuplicateFields(t *testing.T) {
	if _, err := decodeMedicineModelPlan(`{"operation":"create","items":[{"changes":[{"field":"name","value":"甲","evidence":"甲"},{"field":"name","value":"乙","evidence":"乙"}],"unresolved":[]}]}`); err == nil {
		t.Fatal("duplicate changed field accepted")
	}
	if _, err := decodeMedicinePlan(`{"operation":"create","operation":"delete","items":[]}`); err == nil {
		t.Fatal("duplicate operation accepted")
	}
}

func TestMedicineMixedOperationsNeverWrite(t *testing.T) {
	raw := `{"operation":"mixed","items":[{"changes":{},"unresolved":[]}]}`
	r, st, s := newMedicineRuntime(t, raw)
	_, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationCreate, "添加甲药并减少乙药", app.MessageSourceWeb)
	if !strings.Contains(result.FinalAnswer, "一种") || len(result.Approvals) != 0 {
		t.Fatal(result)
	}
	box, _ := st.ReadMedicineBox(t.Context(), s.OwnerID)
	if len(box.Items) != 0 {
		t.Fatal(box)
	}
}
