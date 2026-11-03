package agent

import (
	"encoding/json"
	"errors"
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
	wire := medicineModelPlan{}
	for _, item := range p.Items {
		row := medicineModelEntry{Action: p.Operation, Changes: []medicineModelChange{}, Unresolved: item.Unresolved}
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
	if _, err := decodeMedicineModelPlan(`{"items":[{"action":"create","changes":[{"field":"name","value":"甲","evidence":"甲"},{"field":"name","value":"乙","evidence":"乙"}],"unresolved":[]}]}`); err == nil {
		t.Fatal("duplicate changed field accepted")
	}
	if _, err := decodeMedicinePlan(`{"operation":"create","operation":"delete","items":[]}`); err == nil {
		t.Fatal("duplicate operation accepted")
	}
}

func TestMedicineMixedOperationsNeverWrite(t *testing.T) {
	raw := `{"items":[{"action":"create","changes":[{"field":"name","value":"甲药","evidence":"甲药"}],"unresolved":[]},{"action":"update","changes":[{"field":"quantity_delta","value":-1,"evidence":"减少"}],"target":{"name":"乙药","expires_on":"","location":"","evidence":"乙药"},"unresolved":[]}]}`
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

func TestMedicineAmbiguousRequestedCategoryRequiresCompletion(t *testing.T) {
	base := medicine.Item{ID: "medicine_one", Version: 3, Fields: medicine.Fields{Name: "布洛芬", Category: "pain", Quantity: 1, Unit: "box", NoExpiry: true}}
	plan := medicineEditPlan{Operation: "update", Items: []medicinePlannedEntry{{Target: &medicineTarget{Name: "布洛芬"}, Changes: map[string]any{}, Unresolved: []medicineUnresolved{{Field: "category", Reason: "ambiguous"}}, Evidence: map[string]string{"target": "布洛芬"}}}}
	draft, form, err := freezeMedicinePlan(plan, []medicine.Item{base}, "run", "布洛芬分类改一下")
	if err != nil || form == nil || len(form.Entries) != 1 || len(form.Entries[0].Fields) != 1 || form.Entries[0].Fields[0].Name != "category" {
		t.Fatalf("form=%+v err=%v", form, err)
	}
	batch, err := materializeMedicineBatch(draft, app.ApprovalInputValues{"entry_1": {"category": "fever"}})
	if err != nil || batch.Items[0].BaseVersion != 3 || batch.Items[0].Fields.Category != "fever" || batch.Items[0].Fields.Quantity != 1 {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
}

func TestMedicineModelPlanTakesActionPerEntry(t *testing.T) {
	update := `{"action":"update","changes":[{"field":"quantity_delta","value":-2,"evidence":"减两粒"}],"target":{"name":"布洛芬","expires_on":"","location":"","evidence":"布洛芬"},"unresolved":[]}`
	plan, err := decodeMedicineModelPlan(`{"items":[` + update + `,` + strings.ReplaceAll(update, "布洛芬", "感冒灵") + `]}`)
	if err != nil || plan.Operation != "update" || len(plan.Items) != 2 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if _, err := decodeMedicineModelPlan(`{"items":[` + update + `,{"action":"delete","changes":[],"target":{"name":"碘伏","expires_on":"","location":"","evidence":"碘伏"},"unresolved":[]}]}`); !errors.Is(err, errMedicineMixedOperations) {
		t.Fatalf("mixed actions err=%v", err)
	}
	if _, err := decodeMedicineModelPlan(`{"items":[` + strings.Replace(update, `"update"`, `"mixed"`, 1) + `]}`); err == nil || errors.Is(err, errMedicineMixedOperations) || errors.Is(err, errMedicineBatchLimit) {
		t.Fatalf("unknown action must be repairable, err=%v", err)
	}
	// A delete writes no fields, so stray changes and field gaps cannot block it.
	plan, err = decodeMedicineModelPlan(`{"items":[{"action":"delete","changes":[{"field":"location","value":"","evidence":"药箱里"}],"target":{"name":"蒙脱石散","expires_on":"","location":"","evidence":"蒙脱石散"},"unresolved":[{"field":"expiry","reason":"missing"},{"field":"target","reason":"ambiguous"}]}]}`)
	if err != nil || plan.Operation != "delete" || len(plan.Items[0].Changes) != 0 || len(plan.Items[0].Unresolved) != 1 || plan.Items[0].Unresolved[0].Field != "target" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestMedicineTargetCandidatesNarrowByTier(t *testing.T) {
	items := []medicine.Item{
		{ID: "a", Fields: medicine.Fields{Name: "布洛芬缓释胶囊", Location: "客厅抽屉"}},
		{ID: "b", Fields: medicine.Fields{Name: "布洛芬混悬液", Location: "卧室柜子"}},
		{ID: "c", Fields: medicine.Fields{Name: "感冒灵颗粒", Location: "客厅抽屉"}},
		{ID: "d", Fields: medicine.Fields{Name: "碘伏", Location: "卫生间", NoExpiry: true}},
	}
	ids := func(target medicineTarget, items []medicine.Item) string {
		out := []string{}
		for _, item := range medicineTargetCandidates(target, items) {
			out = append(out, item.ID)
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		target medicineTarget
		want   string
	}{
		{medicineTarget{Name: "感冒灵"}, "c"},
		{medicineTarget{Name: "布洛芬"}, "a,b"},
		{medicineTarget{Name: "布洛芬", Location: "卧室"}, "b"},
		{medicineTarget{Name: "碘伏消毒液"}, "d"},
		{medicineTarget{Name: "阿莫西林"}, ""},
		{medicineTarget{Name: "感冒灵", Location: "药箱"}, ""},
	} {
		if got := ids(c.target, items); got != c.want {
			t.Errorf("target=%+v got=%q want=%q", c.target, got, c.want)
		}
	}
	// An exact or normalized name wins over longer names that contain it.
	withExact := append([]medicine.Item{{ID: "e", Fields: medicine.Fields{Name: "布 洛芬"}}}, items...)
	if got := ids(medicineTarget{Name: "布洛芬"}, withExact); got != "e" {
		t.Fatalf("got=%q", got)
	}
}

func TestMedicineShortNameUpdateWritesDirectly(t *testing.T) {
	p := medicineEditPlan{Operation: "update", Items: []medicinePlannedEntry{
		{Target: &medicineTarget{Name: "布洛芬"}, Changes: map[string]any{"quantity_delta": -2.0}, Unresolved: []medicineUnresolved{}, Evidence: map[string]string{"target": "布洛芬", "quantity_delta": "吃掉了两粒"}},
		{Target: &medicineTarget{Name: "感冒灵"}, Changes: map[string]any{"quantity_delta": -1.0}, Unresolved: []medicineUnresolved{}, Evidence: map[string]string{"target": "感冒灵", "quantity_delta": "用掉一袋"}},
	}}
	raw, _ := json.Marshal(p)
	r, st, s := newMedicineRuntime(t, string(raw))
	if _, err := medicine.New(st).Apply(t.Context(), s.OwnerID, medicine.Batch{OperationID: "seed", Operation: "create", Items: []medicine.Entry{
		{EntryID: "a", Fields: medicine.Fields{Name: "布洛芬缓释胶囊", Category: "fever", Quantity: 12, Unit: "capsule", ExpiresOn: "2027-08-31"}},
		{EntryID: "b", Fields: medicine.Fields{Name: "感冒灵颗粒", Category: "cold", Quantity: 3, Unit: "sachet", ExpiresOn: "2026-11-30"}},
	}}); err != nil {
		t.Fatal(err)
	}
	run, result := dispatchMedicineTest(t, r, st, s, app.RouteOperationEdit, "药箱里的布洛芬吃掉了两粒，感冒灵用掉一袋", app.MessageSourceWeb)
	if run.Workflow.Status != app.WorkflowStatusSucceeded || len(result.Approvals) != 0 || !strings.Contains(result.FinalAnswer, "已更新：布洛芬缓释胶囊") || !strings.Contains(result.FinalAnswer, "已更新：感冒灵颗粒") {
		t.Fatalf("%+v", result)
	}
	box, _ := st.ReadMedicineBox(t.Context(), s.OwnerID)
	for _, item := range box.Items {
		if item.Name == "布洛芬缓释胶囊" && item.Quantity != 10 || item.Name == "感冒灵颗粒" && item.Quantity != 2 {
			t.Fatal(box)
		}
	}
}

func TestMedicineUnclearUpdateNameAsksForTargetNotRename(t *testing.T) {
	base := medicine.Item{ID: "medicine_one", Version: 2, Fields: medicine.Fields{Name: "布洛芬缓释胶囊", Category: "fever", Quantity: 12, Unit: "capsule", NoExpiry: true}}
	plan := medicineEditPlan{Operation: "update", Items: []medicinePlannedEntry{{Target: &medicineTarget{Name: "布洛芬"}, Changes: map[string]any{"quantity_delta": -2.0}, Unresolved: []medicineUnresolved{{Field: "name", Reason: "ambiguous"}}, Evidence: map[string]string{"target": "布洛芬", "quantity_delta": "减两粒"}}}}
	draft, form, err := freezeMedicinePlan(plan, []medicine.Item{base}, "run", "布洛芬减两粒")
	if err != nil || form == nil || len(form.Entries[0].Fields) != 1 || form.Entries[0].Fields[0].Name != "target" || len(form.Entries[0].Fields[0].Options) != 1 {
		t.Fatalf("form=%+v err=%v", form, err)
	}
	batch, err := materializeMedicineBatch(draft, app.ApprovalInputValues{"entry_1": {"target": base.ID}})
	if err != nil || batch.Items[0].Fields.Name != base.Name || batch.Items[0].Fields.Quantity != 10 {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
}

func TestMedicineReceiptNotesZeroOnlyWhenQuantityReachesIt(t *testing.T) {
	item := func(quantity float64, location string) *medicine.Item {
		return &medicine.Item{Fields: medicine.Fields{Name: "氯雷他定片", Quantity: quantity, Unit: "tablet", Location: location}}
	}
	moved := medicineReceiptText(medicine.Receipt{Items: []medicine.Change{{Before: item(0, "卧室柜子"), After: item(0, "客厅抽屉")}}})
	used := medicineReceiptText(medicine.Receipt{Items: []medicine.Change{{Before: item(2, "客厅抽屉"), After: item(0, "客厅抽屉")}}})
	if strings.Contains(moved, "数量已变为 0") || !strings.Contains(used, "数量已变为 0") {
		t.Fatalf("moved=%q used=%q", moved, used)
	}
}
