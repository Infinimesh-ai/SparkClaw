package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestBrowserLoginResumeUsesValidatedRegisteredPostLoginURL(t *testing.T) {
	root := t.TempDir()
	cfg := agentTestConfig()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = filepath.Join(root, ".sparkclaw", "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "registered browser login redirect")
	adapter := &loginBlockBrowserAdapter{
		openAuthChallenge: true,
		selectedTabURL:    "https://example.com/unrelated",
	}
	tools := toolhub.New(cfg, st).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)

	first, err := runtime.HandleMessage(context.Background(), session.ID, "Use browser automation to open https://mail.qq.com/")
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.State != "browser_login_blocked" || first.Run.Workflow == nil {
		t.Fatalf("QQ Mail login challenge did not pause its Workflow: %#v", first.Run)
	}
	stored, ok := st.GetRun(first.Run.ID)
	if !ok || stored.Workflow == nil {
		t.Fatal("blocked browser Workflow was not persisted")
	}
	stored.Workflow.Route.Facts = cloneFacts(stored.Workflow.Route.Facts)
	if stored.Workflow.Route.Facts == nil {
		stored.Workflow.Route.Facts = map[string]string{}
	}
	stored.Workflow.Route.Facts["browser_destination"] = "qq_mail"
	stored.Workflow.Browser.Target.TargetKind = app.BrowserTargetRegisteredDestination
	stored.Workflow.Browser.Target.DestinationID = "qq_mail"
	stored.Workflow.Browser.Target.QueryProvenance = app.BrowserQueryDestinationStatic
	st.SaveRun(stored)
	first.Run = stored
	frozenTarget := stored.Workflow.Route.Slots.TargetRef

	adapter.selectedTabURL = "https://wx.mail.qq.com/home/index?sid=redacted#/list/1/1"
	second, err := runtime.HandleMessage(context.Background(), session.ID, "登陆成功‘")
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ID != first.Run.ID || second.Run.State != "completed" {
		node := second.Run.Workflow.Nodes["browser_result"]
		t.Fatalf("validated QQ Mail page did not resume the original run: run=%#v node=%#v calls=%#v", second.Run, node, workflowCallDebug(toolCallsForRun(st.ListToolCalls(session.ID), first.Run.ID)))
	}
	if second.Run.Workflow == nil || second.Run.Workflow.Route.Slots.TargetRef != frozenTarget ||
		frozenTarget != "https://mail.qq.com/" {
		t.Fatalf("login resume changed the frozen Workflow target: %#v", second.Run.Workflow)
	}
	if adapter.readCalls != 0 || adapter.lastSnapshotArgs["presentation"] != "visible" ||
		adapter.lastSnapshotArgs["page_id"] != "page_2" {
		t.Fatalf("authentication validation did not use a visible current-page snapshot: %#v", adapter)
	}
	if adapter.lastOpenArgs["url"] != "https://wx.mail.qq.com/home/index#/list/1/1" ||
		adapter.lastOpenArgs["browser_mode"] != "collaborative" ||
		adapter.lastOpenArgs["presentation"] != "visible" ||
		!boolValue(adapter.lastOpenArgs["surface_visible"]) ||
		adapter.lastOpenArgs["reason"] != browserResultPresentationReason {
		t.Fatalf("final presentation did not open the validated post-login page visibly: %#v", adapter.lastOpenArgs)
	}
	if adapter.closeCalls != 0 {
		t.Fatalf("successful browser completion closed %d production tabs", adapter.closeCalls)
	}
	if !hasAgentAuditField(st.ListAudit(session.ID), "browser_login_block.post_login_target_validated",
		"post_login_url", "https://wx.mail.qq.com/home/index#/list/1/1") {
		t.Fatalf("validated post-login target was not audited: %#v", st.ListAudit(session.ID))
	}
	blocks := st.ListBrowserLoginBlocks(session.ID, app.BrowserHandoffStatusResolved)
	if len(blocks) != 1 || strings.Contains(blocks[0].LoginHandoffURL, "sid=") ||
		blocks[0].VisibleEvidence == nil || blocks[0].VisibleEvidence.VisibleSession.Generation != 2 {
		t.Fatalf("resolved handoff persisted volatile query data or lost visible evidence: %#v", blocks)
	}
	for _, call := range st.ListToolCalls(session.ID) {
		if call.RunID == first.Run.ID && call.Tool == "browser.read" {
			t.Fatalf("revision-2 login recovery unexpectedly used the retired browser.read preflight: %#v", call)
		}
	}
	for label, value := range map[string]any{
		"tool calls": st.ListToolCalls(session.ID),
		"audits":     st.ListAudit(session.ID),
		"handoffs":   st.ListBrowserLoginBlocks(session.ID, ""),
		"episodes":   st.ListEpisodeSummaries(session.ID),
		"result":     second,
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "sid=") {
			t.Fatalf("%s persisted provider query material: %s", label, raw)
		}
	}
}

func TestBrowserLoginResumeRejectsUnrelatedVisiblePageBeforeHiddenRead(t *testing.T) {
	root := t.TempDir()
	cfg := agentTestConfig()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"example.com", "other.example"}
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = filepath.Join(root, ".sparkclaw", "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "reject unrelated post-login page")
	adapter := &loginBlockBrowserAdapter{
		openAuthChallenge: true,
		selectedTabURL:    "https://other.example/",
	}
	tools := toolhub.New(cfg, st).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)

	first, err := runtime.HandleMessage(context.Background(), session.ID, "打开 https://example.com/protected")
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.State != "browser_login_blocked" {
		t.Fatalf("expected initial browser login block, got %#v", first.Run)
	}

	second, err := runtime.HandleMessage(context.Background(), session.ID, "登录完成")
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ID != first.Run.ID || second.Run.State != "browser_login_blocked" || second.Run.CompletedAt != nil {
		t.Fatalf("unrelated page must keep the original run waiting: %#v", second.Run)
	}
	if adapter.readCalls != 0 {
		t.Fatalf("unrelated visible page triggered %d hidden reads", adapter.readCalls)
	}
	for _, expected := range []string{
		"当前页面不符合原任务要求",
		"原任务目标：https://example.com/protected",
		"当前页面：https://other.example/",
	} {
		if !strings.Contains(second.Message.Content, expected) {
			t.Fatalf("target mismatch message lost %q:\n%s", expected, second.Message.Content)
		}
	}
	block, ok := st.FindActiveBrowserLoginBlock(session.ID)
	if !ok || block.ResumeArgs["url"] != "https://example.com/protected" ||
		block.LastError != "browser_login_post_login_target_mismatch" {
		t.Fatalf("target mismatch overwrote the authorized resume target: %#v", block)
	}
	if !hasAgentAuditField(st.ListAudit(session.ID), "browser_login_block.post_login_target_rejected",
		"post_login_url", "https://other.example/") {
		t.Fatalf("rejected post-login target was not audited: %#v", st.ListAudit(session.ID))
	}
}

func TestBrowserLoginReplyIntentAcceptsLoginPunctuationVariants(t *testing.T) {
	for _, reply := range []string{"登陆成功‘", "登陆完成。", "登录成功！"} {
		if got := browserLoginReplyIntent(reply); got != browserLoginReplyCompleted {
			t.Fatalf("reply %q classified as %q", reply, got)
		}
	}
}

func TestBrowserLoginAmbiguousReplyDoesNotCallBrowser(t *testing.T) {
	runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "ambiguous browser login reply")
	first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
	before := browserLoginAdapterCallCounts(adapter)

	second, err := runtime.HandleMessage(context.Background(), sessionID, "我已经看到了")
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ID != first.Run.ID || second.Run.State != "browser_login_blocked" {
		t.Fatalf("ambiguous reply changed the blocked run: %#v", second.Run)
	}
	if got := browserLoginAdapterCallCounts(adapter); got != before {
		t.Fatalf("ambiguous reply called browser tools: before=%#v after=%#v", before, got)
	}
	block, ok := st.FindActiveBrowserLoginBlock(sessionID)
	if !ok || block.Status != app.BrowserHandoffStatusWaitingOwner ||
		block.LastError != "browser_login_explicit_confirmation_required" {
		t.Fatalf("ambiguous reply did not remain waiting: %#v ok=%v", block, ok)
	}
}

func TestBrowserLoginCancelKeepsVisiblePageOpen(t *testing.T) {
	runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "cancel browser login")
	first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
	before := browserLoginAdapterCallCounts(adapter)

	second, err := runtime.HandleMessage(context.Background(), sessionID, "取消")
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ID != first.Run.ID || second.Run.State != "cancelled" {
		t.Fatalf("cancel did not terminate the original run: %#v", second.Run)
	}
	if got := browserLoginAdapterCallCounts(adapter); got != before || adapter.closeCalls != 0 {
		t.Fatalf("cancel changed the visible browser page: before=%#v after=%#v closes=%d", before, got, adapter.closeCalls)
	}
	if _, ok := st.FindActiveBrowserLoginBlock(sessionID); ok {
		t.Fatal("canceled handoff remained active")
	}
	blocks := st.ListBrowserLoginBlocks(sessionID, app.BrowserHandoffStatusCanceled)
	if len(blocks) != 1 || blocks[0].ResolvedAt == nil {
		t.Fatalf("canceled handoff was not persisted terminally: %#v", blocks)
	}
}

func TestBrowserLoginVisibleAuthenticationRejectReturnsToWaiting(t *testing.T) {
	runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "visible auth rejection")
	first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
	adapter.selectedTabURL = "https://example.com/protected"
	adapter.visibleAuthReject = true
	before := browserLoginAdapterCallCounts(adapter)

	second, err := runtime.HandleMessage(context.Background(), sessionID, "登录完成")
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ID != first.Run.ID || second.Run.State != "browser_login_blocked" {
		t.Fatalf("rejected visible authentication advanced the run: %#v", second.Run)
	}
	after := browserLoginAdapterCallCounts(adapter)
	if after.listTabs != before.listTabs+1 || after.snapshots != before.snapshots+1 ||
		after.waits != before.waits || adapter.closeCalls != 0 {
		t.Fatalf("visible rejection crossed the profile-transfer boundary: before=%#v after=%#v", before, after)
	}
	block, ok := st.FindActiveBrowserLoginBlock(sessionID)
	if !ok || block.Status != app.BrowserHandoffStatusWaitingOwner || block.VisibleEvidence != nil ||
		block.LastError != "browser_login_block_still_unauthenticated" {
		t.Fatalf("visible authentication rejection state mismatch: %#v ok=%v", block, ok)
	}
}

func TestBrowserLoginDuplicateReplyDoesNotReenterOwnedTransition(t *testing.T) {
	runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "duplicate browser login reply")
	first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
	block, ok := st.FindActiveBrowserLoginBlock(sessionID)
	if !ok {
		t.Fatal("active handoff missing")
	}
	block.Status = app.BrowserHandoffStatusValidatingVisible
	block.LastUserReply = "登录完成"
	beginBrowserHandoffTransition(&block, runtime.instanceID)
	block, err := st.UpdateBrowserLoginBlock(block, block.Version)
	if err != nil {
		t.Fatal(err)
	}
	before := browserLoginAdapterCallCounts(adapter)

	second, err := runtime.HandleMessage(context.Background(), sessionID, "登录完成")
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ID != first.Run.ID {
		t.Fatalf("duplicate reply created another run: first=%s second=%s", first.Run.ID, second.Run.ID)
	}
	if got := browserLoginAdapterCallCounts(adapter); got != before {
		t.Fatalf("duplicate reply reentered browser transition: before=%#v after=%#v", before, got)
	}
	current, _ := st.GetBrowserLoginBlock(block.ID)
	if current.Version != block.Version || current.Status != app.BrowserHandoffStatusValidatingVisible {
		t.Fatalf("duplicate reply changed owned handoff: before=%#v after=%#v", block, current)
	}
}

func TestBrowserLoginRestartRecoversValidatingVisible(t *testing.T) {
	runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "restart visible validation")
	first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
	adapter.selectedTabURL = "https://example.com/protected"
	block, _ := st.FindActiveBrowserLoginBlock(sessionID)
	block.Status = app.BrowserHandoffStatusValidatingVisible
	block.LastUserReply = "登录完成"
	beginBrowserHandoffTransition(&block, "retired-runtime")
	if _, err := st.UpdateBrowserLoginBlock(block, block.Version); err != nil {
		t.Fatal(err)
	}

	restarted := NewRuntime(st, runtime.tools, runtime.policy, runtime.models, runtime.traces)
	result, err := restarted.HandleMessage(context.Background(), sessionID, "继续")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.ID != first.Run.ID || result.Run.State != "completed" {
		t.Fatalf("restart did not recover visible validation: %#v", result.Run)
	}
}

func TestBrowserLoginRestartRecoversProfileTransfer(t *testing.T) {
	runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "restart profile transfer")
	first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
	adapter.selectedTabURL = "https://example.com/protected"
	adapter.visibleValidated = true
	block, _ := st.FindActiveBrowserLoginBlock(sessionID)
	block.Status = app.BrowserHandoffStatusTransferring
	block.SessionGeneration = 2
	block.VisibleEvidence = browserLoginTestVisibleEvidence(block)
	beginBrowserHandoffTransition(&block, "retired-runtime")
	if _, err := st.UpdateBrowserLoginBlock(block, block.Version); err != nil {
		t.Fatal(err)
	}

	restarted := NewRuntime(st, runtime.tools, runtime.policy, runtime.models, runtime.traces)
	result, err := restarted.HandleMessage(context.Background(), sessionID, "继续")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.ID != first.Run.ID || result.Run.State != "completed" {
		t.Fatalf("restart did not recover profile transfer: %#v", result.Run)
	}
}

func TestBrowserLoginRestartRecoversHiddenValidation(t *testing.T) {
	runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "restart hidden validation")
	first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
	adapter.selectedTabURL = "https://example.com/protected"
	adapter.visibleValidated = true
	block, _ := st.FindActiveBrowserLoginBlock(sessionID)
	run, _ := st.GetRun(first.Run.ID)
	if err := resetBrowserRevision2AfterHandoff(&run, block.WorkflowNodeID); err != nil {
		t.Fatal(err)
	}
	run.State = "executing"
	st.SaveRun(run)
	block.Status = app.BrowserHandoffStatusValidatingHidden
	block.SessionGeneration = 2
	block.VisibleEvidence = browserLoginTestVisibleEvidence(block)
	beginBrowserHandoffTransition(&block, "retired-runtime")
	if _, err := st.UpdateBrowserLoginBlock(block, block.Version); err != nil {
		t.Fatal(err)
	}

	restarted := NewRuntime(st, runtime.tools, runtime.policy, runtime.models, runtime.traces)
	result, err := restarted.HandleMessage(context.Background(), sessionID, "继续")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.ID != first.Run.ID || result.Run.State != "completed" {
		t.Fatalf("restart did not recover hidden validation: %#v", result.Run)
	}
}

func TestBrowserLoginHiddenContinuityLossReturnsToVisibleWaiting(t *testing.T) {
	runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "hidden continuity loss")
	first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
	adapter.selectedTabURL = "https://example.com/protected"
	adapter.hiddenAuthReject = true

	second, err := runtime.HandleMessage(context.Background(), sessionID, "登录完成")
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ID != first.Run.ID || second.Run.State != "browser_login_blocked" ||
		!strings.Contains(second.Message.Content, "登录状态没有从可见浏览器连续传递") {
		t.Fatalf("hidden continuity loss did not return an explicit waiting result: run=%#v message=%q", second.Run, second.Message.Content)
	}
	block, ok := st.FindActiveBrowserLoginBlock(sessionID)
	if !ok || block.Status != app.BrowserHandoffStatusWaitingOwner ||
		block.LastError != "browser_login_profile_continuity_lost" {
		t.Fatalf("hidden continuity loss state mismatch: %#v ok=%v", block, ok)
	}
	if adapter.closeCalls != 0 {
		t.Fatalf("hidden continuity recovery closed %d owner-visible tabs", adapter.closeCalls)
	}
}

func TestPersistedBrowserRevision1LoginHandoffsRetireBeforeBrowserAccess(t *testing.T) {
	for _, workflowID := range []app.WorkflowID{app.WorkflowBrowserAutomation, app.WorkflowBrowserInteraction} {
		t.Run(string(workflowID), func(t *testing.T) {
			runtime, st, sessionID, adapter := newBrowserLoginStateMachineTest(t, "retire persisted browser r1 handoff")
			first := startBrowserLoginStateMachineTest(t, runtime, sessionID)
			run, ok := st.GetRun(first.Run.ID)
			if !ok || run.Workflow == nil {
				t.Fatal("persisted browser workflow fixture is missing")
			}
			run.Workflow.Plan.ProfileID = workflowID
			run.Workflow.Plan.ProfileRevision = 1
			run.Workflow.PlanDigest = workflowPlanDigest(run.Workflow.Plan)
			run.Workflow.Status = app.WorkflowStatusRunning
			st.SaveRun(run)

			block, ok := st.FindActiveBrowserLoginBlock(sessionID)
			if !ok {
				t.Fatal("persisted browser handoff fixture is missing")
			}
			block.WorkflowID = workflowID
			block.WorkflowRevision = 1
			if _, err := st.UpdateBrowserLoginBlock(block, block.Version); err != nil {
				t.Fatal(err)
			}
			before := browserLoginAdapterCallCounts(adapter)

			result, err := runtime.HandleMessage(context.Background(), sessionID, "登录完成")
			if err != nil {
				t.Fatal(err)
			}
			if result.Run.ID != first.Run.ID || result.Run.State != "blocked" || result.Run.CompletedAt == nil ||
				result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusBlocked {
				t.Fatalf("persisted r1 handoff did not terminate as retired: %#v", result.Run)
			}
			if result.Message.Content != retiredLegacyRunMessage {
				t.Fatalf("retired r1 handoff returned an ambiguous owner message: %q", result.Message.Content)
			}
			if got := browserLoginAdapterCallCounts(adapter); got != before {
				t.Fatalf("retired r1 handoff accessed the browser: before=%#v after=%#v", before, got)
			}
			if _, ok := st.FindActiveBrowserLoginBlock(sessionID); ok {
				t.Fatal("retired r1 handoff remained active")
			}
			blocks := st.ListBrowserLoginBlocks(sessionID, app.BrowserHandoffStatusResolved)
			if len(blocks) != 1 || blocks[0].LastError != "browser_login_workflow_revision_retired" ||
				blocks[0].ResolvedAt == nil {
				t.Fatalf("retired r1 handoff was not resolved explicitly: %#v", blocks)
			}
			if !hasAgentAuditType(st.ListAudit(sessionID), "workflow.legacy_login_resume_retired") {
				t.Fatalf("retired r1 handoff audit is missing: %#v", st.ListAudit(sessionID))
			}
		})
	}
}

type browserLoginCallCounts struct {
	listTabs  int
	snapshots int
	waits     int
	focus     int
	open      int
	close     int
}

func browserLoginAdapterCallCounts(adapter *loginBlockBrowserAdapter) browserLoginCallCounts {
	return browserLoginCallCounts{
		listTabs: adapter.listTabsCalls, snapshots: adapter.snapshotCalls,
		waits: adapter.waitCalls, focus: adapter.focusCalls,
		open: adapter.openCalls, close: adapter.closeCalls,
	}
}

func newBrowserLoginStateMachineTest(t *testing.T, title string) (Runtime, *store.MemoryStore, string, *loginBlockBrowserAdapter) {
	t.Helper()
	root := t.TempDir()
	cfg := agentTestConfig()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"example.com"}
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = filepath.Join(root, ".sparkclaw", "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, title)
	adapter := &loginBlockBrowserAdapter{openAuthChallenge: true}
	tools := toolhub.New(cfg, st).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = tools.Close() })
	return NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil), st, session.ID, adapter
}

func startBrowserLoginStateMachineTest(t *testing.T, runtime Runtime, sessionID string) Result {
	t.Helper()
	result, err := runtime.HandleMessage(context.Background(), sessionID, "打开 https://example.com/protected")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "browser_login_blocked" {
		t.Fatalf("browser login fixture did not create a handoff: %#v", result.Run)
	}
	return result
}

func browserLoginTestVisibleEvidence(block app.BrowserLoginBlock) *app.BrowserResultEvidence {
	return &app.BrowserResultEvidence{
		ID: "restart-visible-evidence", SchemaVersion: app.BrowserHandoffSchemaVersion,
		Target: block.Target,
		VisibleSession: app.BrowserSessionRef{
			OwnerID: block.OwnerID, ProfileID: block.BrowserProfileID,
			Presentation: app.BrowserPresentationVisible, Generation: 2,
		},
		VisiblePageID: "page_2", VisibleSnapshotID: "snapshot_visible_restart",
		VisibleSnapshotDigest: "visible-restart-digest", VerifiedAt: time.Now().UTC(),
	}
}

func TestFinishBrowserLoginBlockTerminalRetriesOnCASConflict(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "terminal retry")
	runtime := Runtime{store: st}
	block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
		SessionID: session.ID, RunID: "run-terminal",
		Status: app.BrowserHandoffStatusWaitingOwner, SiteOrigin: "https://example.com",
	})
	stale := block
	concurrent := block
	concurrent.LastUserReply = "concurrent writer"
	if _, err := st.UpdateBrowserLoginBlock(concurrent, block.Version); err != nil {
		t.Fatal(err)
	}
	runtime.finishBrowserLoginBlockTerminal(stale, app.BrowserLoginBlockStatusFailed,
		"original run for browser login block was not found", "done?")
	current, ok := st.GetBrowserLoginBlock(block.ID)
	if !ok || current.Status != app.BrowserLoginBlockStatusFailed || current.ResolvedAt == nil ||
		current.LastError != "original run for browser login block was not found" {
		t.Fatalf("terminal transition was not retried after CAS conflict: %#v ok=%v", current, ok)
	}
	if _, found := st.FindActiveBrowserLoginBlock(session.ID); found {
		t.Fatal("block remained active after terminal transition")
	}
}

func TestFinishBrowserLoginBlockTerminalKeepsExistingTerminalState(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "terminal keep")
	runtime := Runtime{store: st}
	block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
		SessionID: session.ID, RunID: "run-keep",
		Status: app.BrowserHandoffStatusWaitingOwner, SiteOrigin: "https://example.com",
	})
	stale := block
	resolved := block
	resolved.Status = app.BrowserHandoffStatusResolved
	now := time.Now().UTC()
	resolved.ResolvedAt = &now
	if _, err := st.UpdateBrowserLoginBlock(resolved, block.Version); err != nil {
		t.Fatal(err)
	}
	runtime.finishBrowserLoginBlockTerminal(stale, app.BrowserLoginBlockStatusFailed, "should not stomp", "")
	current, _ := st.GetBrowserLoginBlock(block.ID)
	if current.Status != app.BrowserHandoffStatusResolved || current.LastError == "should not stomp" {
		t.Fatalf("terminal helper stomped an already-terminal block: %#v", current)
	}
}
