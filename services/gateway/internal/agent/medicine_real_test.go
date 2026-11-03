package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// This gate uses real Fast + embedding endpoints, synthetic requests and an
// isolated file repository. It neither reads personal inventory nor calls Info.
func TestRealMedicineRoutingAndPlans(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_MEDICINE") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_MEDICINE=1 and SPARKCLAW_MODEL_CAPACITY_PROFILE=sparkclaw-product-v1 to call configured real Fast and embedding models")
	}
	cfg, err := config.Load(filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = false
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseID = ""
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseKey = ""
	root := t.TempDir()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = filepath.Join(root, "trace")
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	st, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	hub := toolhub.New(cfg, st).WithMedicineRepository(st)
	defer hub.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	runtime, err := NewRuntimeWithContext(ctx, st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("graph=%s catalog=%s mock=false capacity=%s", runtime.semanticRouter.graph.Revision(), runtime.capabilities.Revision(), cfg.Model.CapacityProfile)
	t.Run("routing", func(t *testing.T) {
		cases := []struct {
			text, leaf string
			op         app.RouteOperation
		}{
			{"药箱里还剩多少布洛芬？", "medicine.box_query", app.RouteOperationRead},
			{"家里有哪些药已经过期了？", "medicine.box_query", app.RouteOperationRead},
			{"我头痛发烧，吃什么药好？", "medicine.box_query", app.RouteOperationAnswer},
			{"我药箱里的布洛芬能和感冒灵一起吃吗？", "medicine.box_query", app.RouteOperationAnswer},
			{"把布洛芬24粒和感冒灵6袋一起记到药箱", "medicine.box_edit", app.RouteOperationCreate},
			{"药箱布洛芬减两粒，感冒灵减一袋，帮我一起更新", "medicine.box_edit", app.RouteOperationEdit},
			{"把药箱里的过期阿莫西林删除", "medicine.box_edit", app.RouteOperationDelete},
			{"布洛芬属于哪一类药物？", "medicine.drug_info", app.RouteOperationAnswer},
			{"正在吃华法林，可以同时服用布洛芬吗？", "medicine.drug_info", app.RouteOperationAnswer},
			{"不要联网，解释一下布洛芬的作用原理", "medicine.drug_info", app.RouteOperationAnswer},
			{"碘伏目前多少钱一瓶？", "medicine.drug_info", app.RouteOperationAnswer},
			{"孕期可以吃对乙酰氨基酚吗？", "medicine.drug_info", app.RouteOperationAnswer},
			{"明早八点提醒我吃药", "schedule.manage", app.RouteOperationCreate},
			{"我刚吃了布洛芬", "conversation.answer", app.RouteOperationAnswer},
			{"请解释什么是免疫系统", "conversation.answer", app.RouteOperationAnswer},
			{"查一下今天的公共卫生新闻", "browser.internet_search", app.RouteOperationSearch},
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, 3)
		for i, c := range cases {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				session := storetest.MustCreateSessionWithScope(t, st, "medicine qualification", app.DefaultOwnerID, root, "web", false)
				routing, e := runtime.routeIntentWithRequest(ctx, session.ID, app.NewID("turn"), c.text, nil, nil, app.MessageSourceWeb)
				route := routing.Route
				leaf := ""
				if len(route.CapabilityPath) > 0 {
					leaf = string(route.CapabilityPath[len(route.CapabilityPath)-1])
				}
				t.Logf("case=%d expected=%s/%s actual=%s/%s status=%s query=%q", i, c.leaf, c.op, leaf, route.Slots.Operation, route.Status, route.Slots.Query)
				if e != nil || route.Status != app.RouteMatched || leaf != c.leaf || route.Slots.Operation != c.op {
					t.Errorf("case=%d routing=%+v fusion=%+v err=%v", i, route, routing.Fusion, e)
				}
				if routing.Fusion == nil || routing.Fusion.Channels.Embedding.Status != "healthy" || routing.Fusion.Channels.Tree.Status != "healthy" {
					t.Errorf("case=%d unhealthy semantic channels", i)
				}
			}()
		}
		wg.Wait()
	})
	t.Run("plans", func(t *testing.T) {
		session := storetest.MustCreateSessionWithScope(t, st, "medicine plan qualification", app.DefaultOwnerID, root, "web", false)
		for _, c := range []struct {
			text       string
			needsInput bool
		}{{medicineBatchText, false}, {"添加布洛芬24粒和感冒灵6袋", true}} {
			run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, Workflow: &app.WorkflowState{Route: app.RouteDecision{Slots: app.RouteSlots{Operation: app.RouteOperationCreate, Query: c.text}}}}
			p, e := runtime.planMedicineEdit(ctx, run, nil)
			if e != nil {
				t.Error(e)
				continue
			}
			draft, form, e := freezeMedicinePlan(p, nil, run.ID, c.text)
			if e != nil || len(draft.Items) != 2 || (form != nil) != c.needsInput {
				t.Errorf("plan=%+v form=%+v err=%v", p, form, e)
			} else {
				t.Logf("plan entries=2 needs_input=%v", c.needsInput)
				if !c.needsInput {
					batch, err := materializeMedicineBatch(draft, nil)
					if err != nil || len(batch.Items) != 2 {
						t.Fatalf("batch=%+v err=%v", batch, err)
					}
					a, b := batch.Items[0].Fields, batch.Items[1].Fields
					if a.Name != "布洛芬" || a.Quantity != 24 || a.Unit != "capsule" || a.ExpiresOn != "2027-05-31" || a.NoExpiry || a.Usage != "" || a.Location != "" || a.Notes != "" || b.Name != "碘伏" || b.Quantity != 1 || b.Unit != "bottle" || !b.NoExpiry || b.ExpiresOn != "" || b.Usage != "" || b.Location != "" || b.Notes != "" {
						t.Errorf("unexpected model fields: %+v %+v", a, b)
					}
				}
			}
		}
	})

	t.Run("edit_plans", func(t *testing.T) {
		session := storetest.MustCreateSessionWithScope(t, st, "medicine edit qualification", app.DefaultOwnerID, root, "web", false)
		records := []medicine.Item{
			{ID: "ibuprofen", Version: 1, Fields: medicine.Fields{Name: "布洛芬缓释胶囊", Category: "fever", Quantity: 12, Unit: "capsule", ExpiresOn: "2027-08-31", Location: "客厅抽屉"}},
			{ID: "cold", Version: 1, Fields: medicine.Fields{Name: "感冒灵颗粒", Category: "cold", Quantity: 3, Unit: "sachet", ExpiresOn: "2026-11-30", Location: "客厅抽屉"}},
			{ID: "smecta", Version: 1, Fields: medicine.Fields{Name: "蒙脱石散", Category: "digestive", Quantity: 6, Unit: "sachet", ExpiresOn: "2026-03-31", Location: "卧室柜子"}},
			{ID: "iodine", Version: 1, Fields: medicine.Fields{Name: "碘伏", Category: "wound", Quantity: 1, Unit: "bottle", NoExpiry: true, Location: "卫生间"}},
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, 3)
		for i, c := range []struct {
			text  string
			op    app.RouteOperation
			want  map[string]medicine.Fields // record ID -> fields after the batch; nil for delete
			mixed bool
		}{
			{"药箱里的布洛芬减两粒", app.RouteOperationEdit, map[string]medicine.Fields{"ibuprofen": {Quantity: 10, Location: "客厅抽屉"}}, false},
			{"药箱里的布洛芬吃掉了两粒，感冒灵用掉一袋，帮我更新一下", app.RouteOperationEdit, map[string]medicine.Fields{"ibuprofen": {Quantity: 10, Location: "客厅抽屉"}, "cold": {Quantity: 2, Location: "客厅抽屉"}}, false},
			{"把药箱里布洛芬改成10粒，感冒灵改成2袋", app.RouteOperationEdit, map[string]medicine.Fields{"ibuprofen": {Quantity: 10, Location: "客厅抽屉"}, "cold": {Quantity: 2, Location: "客厅抽屉"}}, false},
			{"把卧室柜子的蒙脱石散挪到客厅抽屉", app.RouteOperationEdit, map[string]medicine.Fields{"smecta": {Quantity: 6, Location: "客厅抽屉"}}, false},
			{"碘伏又买了一瓶", app.RouteOperationEdit, map[string]medicine.Fields{"iodine": {Quantity: 2, Location: "卫生间"}}, false},
			{"把药箱里过期的蒙脱石散删掉", app.RouteOperationDelete, map[string]medicine.Fields{"smecta": {}}, false},
			{"新增阿莫西林1盒无有效期，再把布洛芬减两粒", app.RouteOperationCreate, nil, true},
			{"删掉碘伏，另外感冒灵减一袋", app.RouteOperationDelete, nil, true},
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, Workflow: &app.WorkflowState{Route: app.RouteDecision{Slots: app.RouteSlots{Operation: c.op, Query: c.text}}}}
				p, err := runtime.planMedicineEdit(ctx, run, records)
				if c.mixed {
					if !errors.Is(err, errMedicineMixedOperations) {
						t.Errorf("case=%d mixed request plan=%+v err=%v", i, p, err)
					}
					return
				}
				if err != nil {
					t.Errorf("case=%d plan err=%v", i, err)
					return
				}
				draft, form, err := freezeMedicinePlan(p, records, run.ID, c.text)
				if err != nil || form != nil {
					t.Errorf("case=%d plan=%+v form=%+v err=%v", i, p, form, err)
					return
				}
				batch, err := materializeMedicineBatch(draft, nil)
				if err != nil || len(batch.Items) != len(c.want) {
					t.Errorf("case=%d batch=%+v err=%v", i, batch, err)
					return
				}
				for _, entry := range batch.Items {
					want, ok := c.want[entry.ID]
					got := entry.Fields
					if !ok || batch.Operation != medicine.OperationDelete && (got.Quantity != want.Quantity || got.Location != want.Location) {
						t.Errorf("case=%d op=%s id=%s fields=%+v want=%+v", i, batch.Operation, entry.ID, got, want)
					}
				}
				t.Logf("case=%d op=%s entries=%d", i, batch.Operation, len(batch.Items))
			}()
		}
		wg.Wait()
	})

	t.Run("batch_limits", func(t *testing.T) {
		session := storetest.MustCreateSessionWithScope(t, st, "medicine limits qualification", app.DefaultOwnerID, root, "web", false)
		for _, count := range []int{20, 21} {
			parts := []string{}
			for i := 1; i <= count; i++ {
				parts = append(parts, fmt.Sprintf("药品%02d 1盒，无有效期", i))
			}
			query := "全部添加到药箱：" + strings.Join(parts, "；")
			run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, Workflow: &app.WorkflowState{Route: app.RouteDecision{Slots: app.RouteSlots{Operation: app.RouteOperationCreate, Query: query}}}}
			plan, err := runtime.planMedicineEdit(ctx, run, nil)
			if count > 20 {
				if !errors.Is(err, errMedicineBatchLimit) {
					t.Errorf("oversize batch count=%d plan=%+v err=%v", count, plan, err)
				}
				continue
			}
			if err != nil || len(plan.Items) != count {
				t.Errorf("count=%d plan=%+v err=%v", count, plan, err)
				continue
			}
			draft, form, err := freezeMedicinePlan(plan, nil, run.ID, query)
			if err != nil || form != nil || len(draft.Items) != 20 {
				t.Errorf("count=%d form=%+v err=%v", count, form, err)
			}
			t.Logf("batch_count=%d parsed_entries=%d", count, len(plan.Items))
		}
	})

	t.Run("answers", func(t *testing.T) {
		session := storetest.MustCreateSessionWithScope(t, st, "medicine answer qualification", app.DefaultOwnerID, root, "web", false)
		_, err := medicine.New(st).Apply(ctx, session.OwnerID, medicine.Batch{OperationID: "synthetic_answer_seed", Operation: "create", Items: []medicine.Entry{{EntryID: "one", Fields: medicine.Fields{Name: "甲药", Category: "other", Quantity: 7, Unit: "box", ExpiresOn: "2030-01-01", Location: "合成柜"}}, {EntryID: "two", Fields: medicine.Fields{Name: "布洛芬", Category: "pain", Quantity: 0, Unit: "capsule", ExpiresOn: "2020-01-01", Location: "抽屉"}}}})
		if err != nil {
			t.Fatal(err)
		}
		before, _ := st.ReadMedicineBox(ctx, session.OwnerID)
		for _, c := range []struct {
			leaf    app.CapabilityID
			op      app.RouteOperation
			text    string
			closing bool
		}{
			{app.CapabilityMedicineBoxQuery, app.RouteOperationRead, "药箱里的甲药还剩多少，放在哪里？", false},
			{app.CapabilityMedicineBoxQuery, app.RouteOperationAnswer, "我头痛发烧，药箱里的布洛芬还可以吃吗？", true},
			{app.CapabilityMedicineDrugInfo, app.RouteOperationAnswer, "不要联网，介绍布洛芬的常见副作用", true},
		} {
			route := app.RouteDecision{SchemaVersion: 1, Status: app.RouteMatched, CatalogRevision: runtime.capabilities.Revision(), CapabilityPath: []app.CapabilityID{"medicine", c.leaf}, Slots: app.RouteSlots{Operation: c.op, Query: c.text}}
			if c.leaf == app.CapabilityMedicineBoxQuery {
				route.Slots.FactScope = app.RouteFactScopeMedicineBox
			}
			d, err := runtime.dispatchMatchedWorkflow(ctx, app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}, route, app.ReturnRoute{Mode: app.ReturnToSource}, app.NewID("turn"))
			if err != nil {
				t.Error(err)
				continue
			}
			result := runtime.runWorkflow(ctx, session.ID, d.Run, c.text, d.Profile, d.Context, d.Tools)
			t.Logf("answer leaf=%s op=%s text=%s", c.leaf, c.op, result.FinalAnswer)
			if !result.Completed || strings.HasSuffix(result.FinalAnswer, toolhub.MedicineClosing) != c.closing {
				t.Errorf("answer did not complete or closing mismatch: %+v", result)
			}
			if c.op == app.RouteOperationRead && (!strings.Contains(result.FinalAnswer, "7") || !strings.Contains(result.FinalAnswer, "合成柜")) {
				t.Error("read answer omitted recorded quantity/location")
			}
			if c.leaf == app.CapabilityMedicineBoxQuery && c.op == app.RouteOperationAnswer && !strings.Contains(result.FinalAnswer, "过期") {
				t.Error("advice failed to mark expired stock")
			}
			for _, call := range result.ToolCalls {
				if call.Tool == "medicine.box.write" {
					t.Fatal("read workflow attempted write")
				}
			}
		}
		after, _ := st.ReadMedicineBox(ctx, session.OwnerID)
		if before.Sequence != after.Sequence {
			t.Fatal("answer mutated inventory")
		}
	})
}
