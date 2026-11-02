package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
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
		t.Skip("set SPARKCLAW_RUN_REAL_MEDICINE=1 to call configured real Fast and embedding models")
	}
	cfg, err := config.Load(filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = false
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
			}
		}
	})
}
