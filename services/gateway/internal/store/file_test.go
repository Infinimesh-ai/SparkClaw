package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileStorePersistsDocumentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "documents")
	saved := mustSaveDocumentRecord(t, st, app.DocumentRecord{
		ID: "doc_file", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "reports/report.pdf", Name: "report.pdf", Format: app.DocumentFormatPDF,
		Status: app.DocumentStatusAvailable, Source: app.DocumentSourceAttachment,
		SourceMessageID: "m_file", LastActivity: app.DocumentActivityAttached,
		LastActivityID: "m_file", LastActivityAt: time.Now().UTC(),
	})
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := mustGetDocumentRecord(t, reloaded, saved.ID)
	if !ok || got.GovernedPath != saved.GovernedPath || got.SourceMessageID != "m_file" {
		t.Fatalf("file snapshot omitted document record: %#v ok=%v", got, ok)
	}
}

func TestFileStoreNotificationBindingSaveRollsBackOnPersistenceFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	initial := mustCreateNotificationBindingFixture(t, st, app.NotificationBinding{
		ID: "bind-file-rollback", OwnerID: "owner-file-rollback", Channel: "weixin", Status: "waiting_confirm",
	})
	if initial.ID == "" {
		t.Fatal("initial notification binding was not persisted")
	}
	initialAuditCount := len(mustListAudit(t, st, ""))
	initialEventCount := len(st.inner.snapshot().Events)

	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	st.path = filepath.Join(blocker, "state.json")
	candidate := initial
	candidate.Status = "active"
	candidate.CredentialRef = "cred_must_remain_uncommitted"
	candidate.CredentialKind = "test-secret"
	if saved, updateErr := st.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(initial, candidate)); updateErr == nil || saved.ID != "" {
		t.Fatalf("failed persistence returned a committed binding: %#v", saved)
	}
	got, found := mustGetNotificationBindingFixture(t, st, initial.ID)
	if !found || got.Status != initial.Status || got.CredentialRef != "" {
		t.Fatalf("failed persistence remained visible in memory: %#v found=%v", got, found)
	}
	if got := len(mustListAudit(t, st, "")); got != initialAuditCount {
		t.Fatalf("failed persistence retained audit entries: got %d want %d", got, initialAuditCount)
	}
	if got := len(st.inner.snapshot().Events); got != initialEventCount {
		t.Fatalf("failed persistence retained events: got %d want %d", got, initialEventCount)
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	durable, found := mustGetNotificationBindingFixture(t, reloaded, initial.ID)
	if !found || durable.Status != initial.Status || durable.CredentialRef != "" {
		t.Fatalf("failed persistence changed the durable binding: %#v found=%v", durable, found)
	}
}

func TestFileStorePersistsExternalApprovalContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approval-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	mustSaveApproval(t, st, app.Approval{
		ID: "ap_happy_task_file", Source: app.ApprovalSourceHappyTeamPlan,
		ExternalID: "task-file", Tool: "mcp.happy-tasks.approve_plan",
		Risk: app.RiskDangerous, Status: "pending", Summary: "Review file-backed plan",
		ExternalContext: &app.ExternalApprovalContext{
			Provider: "happy-team", Title: "File task", GoalPrompt: "Persist this",
			Plan: "Persisted plan", PlanAvailability: app.ExternalPlanAvailable, PlanEdited: true,
		},
	})
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := mustFindApprovalByExternalRef(t, reloaded, app.ApprovalSourceHappyTeamPlan, "task-file")
	if !ok || got.ExternalContext == nil || got.ExternalContext.Plan != "Persisted plan" || !got.ExternalContext.PlanEdited {
		t.Fatalf("external approval context did not survive file reload: %#v ok=%v", got, ok)
	}
	if _, err := reloaded.ResolveApproval(t.Context(), got.ID, "approved", "done"); err != nil {
		t.Fatal(err)
	}
	got.ExternalContext.Plan = "stale update"
	if _, err := reloaded.UpdatePendingApproval(t.Context(), NewApprovalUpdate(got, got)); err == nil {
		t.Fatal("stale file-backed update reopened a resolved approval")
	}
	finalStore, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	finalApproval, _ := mustGetApproval(t, finalStore, got.ID)
	if finalApproval.Status != "approved" || finalApproval.ExternalContext.Plan != "Persisted plan" {
		t.Fatalf("resolved file-backed approval changed after stale update: %#v", finalApproval)
	}
}

func TestFileStoreBrowserHandoffCASRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser-handoff-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "browser handoff round trip")
	run := app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked",
		ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC(),
	}
	testSaveRun(st, run)
	leaseUntil := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
		SessionID: session.ID, RunID: run.ID,
		WorkflowID: app.WorkflowBrowserInteraction, WorkflowRevision: app.BrowserWorkflowRevision2,
		WorkflowNodeID: "browser_result", SessionGeneration: 11,
		Status:            app.BrowserHandoffStatusValidatingVisible,
		TransitionOwnerID: "runtime-file", TransitionLeaseUntil: &leaseUntil,
		Target: app.BrowserTargetDescriptor{
			TargetKind:    app.BrowserTargetRegisteredDestination,
			DestinationID: "qq_mail", CanonicalURL: "https://wx.mail.qq.com/home/index#/list/1/1",
			RedactedURL: "https://wx.mail.qq.com/home/index#/list/1/1",
		},
		VisibleEvidence: &app.BrowserResultEvidence{
			ID: "visible-file", SchemaVersion: app.BrowserHandoffSchemaVersion,
			VisiblePageID: "page-file", VisibleSnapshotID: "snapshot-file",
		},
	})
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.GetBrowserLoginBlock(block.ID)
	if !ok || got.Version != block.Version || got.WorkflowID != app.WorkflowBrowserInteraction ||
		got.WorkflowRevision != app.BrowserWorkflowRevision2 || got.Target.DestinationID != "qq_mail" ||
		got.VisibleEvidence == nil || got.VisibleEvidence.VisibleSnapshotID != "snapshot-file" ||
		got.TransitionOwnerID != "runtime-file" || got.TransitionLeaseUntil == nil ||
		!got.TransitionLeaseUntil.Equal(leaseUntil) {
		t.Fatalf("file handoff round trip mismatch: %#v ok=%v", got, ok)
	}
	update := got
	update.Status = app.BrowserHandoffStatusTransferring
	updated, err := reloaded.UpdateBrowserLoginBlock(update, got.Version)
	if err != nil {
		t.Fatal(err)
	}
	stale := got
	stale.LastError = "stale"
	if _, err := reloaded.UpdateBrowserLoginBlock(stale, got.Version); !errors.Is(err, ErrBrowserHandoffConflict) {
		t.Fatalf("stale file handoff update error = %v", err)
	}
	afterReload, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	current, ok := afterReload.GetBrowserLoginBlock(block.ID)
	if !ok || current.Version != updated.Version || current.Status != app.BrowserHandoffStatusTransferring ||
		current.LastError == "stale" {
		t.Fatalf("file CAS result did not persist: %#v ok=%v", current, ok)
	}
}

func TestFileStoreMigratesLegacyBrowserLoginBlocksAtLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-browser-state.json")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updatedWaiting := created.Add(time.Hour)
	updatedResuming := created.Add(2 * time.Hour)
	snapshot := Snapshot{
		BrowserLoginBlocks: map[string]app.BrowserLoginBlock{
			"blogin-legacy-waiting": {
				ID: "blogin-legacy-waiting", SessionID: "session-legacy", RunID: "run-legacy",
				Status:     legacyBrowserHandoffStatusWaiting,
				ResumeArgs: map[string]any{"url": "https://example.com/a"},
				CreatedAt:  created, UpdatedAt: updatedWaiting,
			},
			"blogin-legacy-resuming": {
				ID: "blogin-legacy-resuming", SessionID: "session-legacy-2", RunID: "run-legacy-2",
				Status:    legacyBrowserHandoffStatusResuming,
				CreatedAt: created, UpdatedAt: updatedResuming,
			},
		},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	waiting, ok := st.GetBrowserLoginBlock("blogin-legacy-waiting")
	if !ok || waiting.Status != app.BrowserHandoffStatusWaitingOwner ||
		waiting.SchemaVersion != app.BrowserHandoffSchemaVersion || waiting.Version != 1 ||
		!waiting.CreatedAt.Equal(created) || !waiting.UpdatedAt.Equal(updatedWaiting) {
		t.Fatalf("legacy waiting block was not migrated at load: %#v ok=%v", waiting, ok)
	}
	resuming, ok := st.GetBrowserLoginBlock("blogin-legacy-resuming")
	if !ok || resuming.Status != app.BrowserHandoffStatusValidatingVisible ||
		resuming.SchemaVersion != app.BrowserHandoffSchemaVersion || resuming.Version != 1 ||
		!resuming.UpdatedAt.Equal(updatedResuming) {
		t.Fatalf("legacy resuming block was not migrated at load: %#v ok=%v", resuming, ok)
	}
	if active, ok := st.FindActiveBrowserLoginBlock("session-legacy"); !ok || active.ID != "blogin-legacy-waiting" {
		t.Fatalf("migrated legacy block is not active: %#v ok=%v", active, ok)
	}
	update := waiting
	update.Status = app.BrowserHandoffStatusValidatingVisible
	if _, err := st.UpdateBrowserLoginBlock(update, waiting.Version); err != nil {
		t.Fatalf("migrated legacy block rejected CAS update: %v", err)
	}
}

func TestFileStoreDeleteSessionRemovesPersistedBrowserLoginBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "delete blocked browser session")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
		SessionID:  session.ID,
		RunID:      run.ID,
		SiteOrigin: "https://example.com",
	})

	if _, err := st.DeleteSession(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.GetBrowserLoginBlock(block.ID); ok {
		t.Fatal("session deletion retained persisted browser login block")
	}
	if _, ok := testGetRun(reloaded, run.ID); ok {
		t.Fatal("session deletion retained persisted agent run")
	}
	if _, ok := mustGetSession(t, reloaded, session.ID); ok {
		t.Fatal("session deletion retained persisted session")
	}
}

func TestFileStorePersistsAndReloadsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "Persistent Session")
	mustClaimTestClient(t, st, app.Client{ID: "client_test", Name: "Persistent Client", TokenHash: "hash"})
	if _, err := st.RevokeClient(t.Context(), "client_test"); err != nil {
		t.Fatal(err)
	}
	mustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: "hello"})
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead}
	testSaveRun(st, run)
	testSaveModelCall(st, app.ModelCall{
		ID:          app.NewID("mcall"),
		SessionID:   session.ID,
		RunID:       run.ID,
		Lane:        "fast",
		Profile:     "sparkclaw-fast",
		Model:       "Qwen/Fast",
		Operation:   "chat",
		Mock:        true,
		Status:      "completed",
		TotalTokens: 7,
	})

	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID: session.ID,
		RunID:     run.ID,
		Kind:      "profile",
		Content:   "remember me",
		Status:    "pending",
	})
	_, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := st.UpdateMemory(memory.ID, "procedural", "remember me after edit")
	if err != nil {
		t.Fatal(err)
	}
	mustUpdateOwnerProfile(t, st, app.OwnerProfile{
		DisplayName: "Persistent Owner",
		Email:       "owner@example.test",
		Preferences: map[string]string{"timezone": "Asia/Shanghai", "style": "direct"},
	})
	mustSaveEvalRun(t, st, app.EvalRun{
		ID:      "eval_test",
		Profile: "smoke",
		Status:  "failed",
		Summary: "1/1 failed",
		FailureArchives: []app.EvalArtifact{{
			CaseName:    "broken_case",
			URI:         "artifact://sparkclaw/eval-failures/eval_test/broken_case.json",
			Key:         "eval-failures/eval_test/broken_case.json",
			Backend:     "filesystem",
			ContentType: "application/json",
			Bytes:       128,
		}},
	})
	testSaveEpisodeSummary(st, app.EpisodeSummary{
		ID:        "ep_test",
		SessionID: session.ID,
		RunID:     run.ID,
		Goal:      "hello",
		Outcome:   "completed",
		Risk:      app.RiskRead,
		ModelLane: "fast",
		Tools:     []string{"memory.search:completed"},
		Summary:   "Episode summary",
	})

	st.SaveArtifactObject(app.ArtifactObject{
		ID:          "obj_test",
		Kind:        "trace",
		RunID:       run.ID,
		SessionID:   session.ID,
		Backend:     "filesystem",
		Bucket:      "sparkclaw",
		Key:         "traces/" + run.ID + ".json",
		URI:         "artifact://sparkclaw/traces/" + run.ID + ".json",
		ContentType: "application/json",
		Bytes:       256,
	})
	testSaveRunFeedback(st, app.RunFeedback{
		SessionID:  session.ID,
		RunID:      run.ID,
		MessageID:  "m_feedback",
		Rating:     "corrected",
		Correction: "Persistent correction.",
	})

	block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
		SessionID:          session.ID,
		RunID:              run.ID,
		OriginalGoal:       "Read https://example.com/protected",
		ResumeTool:         "browser.read",
		ResumeArgs:         map[string]any{"url": "https://example.com/protected"},
		LoginHandoffURL:    "https://example.com/protected",
		LoginHandoffPageID: "page-1",
		LastVisiblePageID:  "page-2",
		OwnerID:            app.DefaultOwnerID,
		BrowserProfileID:   "default",
		SiteOrigin:         "https://example.com",
	})

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := mustGetSession(t, reloaded, session.ID); !ok || got.Title != "Persistent Session" {
		t.Fatalf("session did not reload: %#v ok=%v", got, ok)
	}
	clients, err := reloaded.ListClients(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].ID != "client_test" || clients[0].RevokedAt == nil {
		t.Fatalf("client did not reload revoked: %#v", clients)
	}
	if messages := mustListMessages(t, reloaded, session.ID); len(messages) != 1 || messages[0].Content != "hello" {
		t.Fatalf("messages did not reload: %#v", messages)
	}
	modelCalls := testListModelCalls(reloaded, session.ID, run.ID)
	if len(modelCalls) != 1 || modelCalls[0].TotalTokens != 7 || modelCalls[0].Operation != "chat" {
		t.Fatalf("model calls did not reload: %#v", modelCalls)
	}
	candidates := reloaded.ListMemoryCandidates("pending")
	if len(candidates) != 0 {
		t.Fatalf("candidate did not reload: %#v", candidates)
	}
	memories := reloaded.SearchMemories("after edit")
	if len(memories) != 1 || memories[0].ID != updated.ID || memories[0].Kind != "procedural" {
		t.Fatalf("updated memory did not reload: %#v", memories)
	}
	if _, err := reloaded.DeleteMemory(updated.ID); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if memories := afterDelete.SearchMemories("after edit"); len(memories) != 0 {
		t.Fatalf("deleted memory reloaded unexpectedly: %#v", memories)
	}
	owner := mustGetOwnerProfile(t, reloaded)
	if owner.DisplayName != "Persistent Owner" || owner.Email != "owner@example.test" || owner.Preferences["timezone"] != "Asia/Shanghai" {
		t.Fatalf("owner profile did not reload: %#v", owner)
	}
	if evalRun, ok := mustGetEvalRun(t, reloaded, "eval_test"); !ok || evalRun.Status != "failed" || len(evalRun.FailureArchives) != 1 {
		t.Fatalf("eval run did not reload: %#v ok=%v", evalRun, ok)
	}
	evalRuns := mustListEvalRuns(t, reloaded)
	if len(evalRuns) != 1 || evalRuns[0].ID != "eval_test" || len(evalRuns[0].FailureArchives) != 1 {
		t.Fatalf("eval runs did not list from persisted state: %#v", evalRuns)
	}
	episodes := testListEpisodeSummaries(reloaded, session.ID)
	if len(episodes) != 1 || episodes[0].ID != "ep_test" || episodes[0].Tools[0] != "memory.search:completed" {
		t.Fatalf("episode summary did not reload: %#v", episodes)
	}
	objects := reloaded.ListArtifactObjects(10)
	if len(objects) != 1 || objects[0].ID != "obj_test" || objects[0].RunID != run.ID {
		t.Fatalf("artifact object did not reload: %#v", objects)
	}
	if object, ok := reloaded.FindArtifactObjectByURI("artifact://sparkclaw/traces/"+run.ID+".json", session.ID, run.ID); !ok || object.ID != "obj_test" {
		t.Fatalf("artifact lookup by URI did not survive reload: %#v ok=%v", object, ok)
	}
	feedback := testListRunFeedback(reloaded, run.ID)
	if len(feedback) != 1 || feedback[0].Rating != "corrected" || feedback[0].Correction != "Persistent correction." {
		t.Fatalf("run feedback did not reload: %#v", feedback)
	}
	if active, ok := reloaded.FindActiveBrowserLoginBlock(session.ID); !ok || active.ID != block.ID || active.ResumeArgs["url"] != "https://example.com/protected" || active.LoginHandoffPageID != "page-1" || active.LastVisiblePageID != "page-2" {
		t.Fatalf("browser login block did not reload: %#v ok=%v", active, ok)
	}
}

func TestFileStorePersistsWorkflowStateAndToolBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "Workflow State")
	run := app.AgentRun{
		ID:        app.NewID("run"),
		SessionID: session.ID,
		State:     "running",
		ModelLane: "fast",
		Risk:      app.RiskRead,
		MessageContext: &app.MessageRunContext{
			IntentFusion: &app.IntentFusionDecision{
				SchemaVersion: app.IntentFusionDecisionSchemaVersion, GraphRevision: "graph.v1", CalibrationRevision: "calibration.v1",
				Channels: app.IntentFusionChannels{
					Embedding: app.IntentFusionChannel{Status: "healthy", Model: "embedding@test"},
					Tree:      app.IntentFusionChannel{Status: "healthy", Model: "fast@test"},
				},
				Candidates: []app.IntentFusionCandidate{{CandidateID: "browser.weather#read", CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserWeather}, FusionScore: 0.91}},
				Verdict:    "clear", Confidence: 0.91, Margin: 0.4, ReasonCode: "top_candidate_clear",
			},
		},
		Workflow: &app.WorkflowState{
			SchemaVersion: 1,
			Plan: app.WorkflowPlan{
				SchemaVersion:   1,
				ProfileID:       app.WorkflowWebPublicResearch,
				ProfileRevision: 3,
				InitialNodeIDs:  []app.WorkflowNodeID{"research"},
			},
			PlanDigest:    "sha256:test-plan",
			Status:        app.WorkflowStatusRunning,
			ActiveNodeIDs: []app.WorkflowNodeID{"research"},
			Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
				"research": {
					Status: app.WorkflowNodeActive,
					CurrentScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
						Name: "web.page.read",
					}}},
					ScopeRevision:     2,
					AppliedOutcomeIDs: []string{"outcome_search"},
					TransitionActivations: map[app.TransitionID]int{
						"source_page": 1,
					},
					LastDirectory: &app.DirectoryViewRef{
						ViewID:            "view_restart",
						DirectoryRevision: "directory_7",
						EntryIDs:          []app.ToolDirectoryEntryID{"entry_browser_read"},
					},
				},
			},
		},
	}
	testSaveRun(st, run)
	testSaveToolCall(st, app.ToolCall{
		ID:             app.NewID("tc"),
		SessionID:      session.ID,
		RunID:          run.ID,
		WorkflowID:     app.WorkflowWebPublicResearch,
		WorkflowNodeID: "research",
		ScopeRevision:  2,
		Capability:     "web.page.read",
		Tool:           "browser.read",
		Risk:           app.RiskRead,
		Status:         "completed",
		Arguments:      map[string]any{"url": "https://example.com/source"},
	})

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	gotRun, ok := testGetRun(reloaded, run.ID)
	if !ok || gotRun.Workflow == nil || gotRun.MessageContext == nil {
		t.Fatalf("workflow state did not reload: %#v ok=%v", gotRun, ok)
	}
	if gotRun.MessageContext.IntentFusion == nil || gotRun.MessageContext.IntentFusion.GraphRevision != "graph.v1" ||
		len(gotRun.MessageContext.IntentFusion.Candidates) != 1 || gotRun.MessageContext.IntentFusion.Candidates[0].CandidateID != "browser.weather#read" {
		t.Fatalf("intent fusion evidence did not reload: %#v", gotRun.MessageContext.IntentFusion)
	}
	gotNode := gotRun.Workflow.Nodes["research"]
	if gotRun.Workflow.PlanDigest != "sha256:test-plan" || gotNode.ScopeRevision != 2 ||
		gotNode.TransitionActivations["source_page"] != 1 || len(gotNode.AppliedOutcomeIDs) != 1 ||
		gotNode.LastDirectory == nil || gotNode.LastDirectory.DirectoryRevision != "directory_7" {
		t.Fatalf("workflow restart state changed: %#v", gotRun.Workflow)
	}
	calls := testListToolCalls(reloaded, session.ID)
	if len(calls) != 1 || calls[0].WorkflowID != app.WorkflowWebPublicResearch ||
		calls[0].WorkflowNodeID != "research" || calls[0].ScopeRevision != 2 || calls[0].Capability != "web.page.read" {
		t.Fatalf("tool workflow binding did not reload: %#v", calls)
	}
}

func TestFileStorePersistsPolicyExecutionContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "Policy context")
	run := app.AgentRun{ID: "run_policy_context", SessionID: session.ID, State: "approval_pending", StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	policyContext := &app.PolicyExecutionContext{
		SchemaVersion: 1, PrincipalClass: app.PolicyPrincipalExternalMCPAI,
		ResourceClass: app.PolicyResourceSparkClawWorkspaceData, AccessClass: app.PolicyAccessWorkspaceSourceRead,
		RunID: run.ID, WorkflowID: app.WorkflowDocumentRead, WorkflowRevision: 4,
		PlanDigest: "plan-digest", OutputClass: "document_content", ContractDigest: "contract-digest",
		MCP: &app.MCPInvocationRef{
			InvocationID: "inv-file", OperationID: "op-file", BindingRef: "binding-file", BindingRevision: 1, RequesterDeviceID: "device-file",
		},
	}
	call := app.ToolCall{
		ID: "tc_policy_context", SessionID: session.ID, RunID: run.ID, Tool: app.ToolWorkspaceDataAccess,
		Risk: app.RiskRead, Status: "approval_pending", Arguments: map[string]any{"request_digest": "digest"},
		PolicyContext: policyContext, StartedAt: time.Now().UTC(),
	}
	approval := app.Approval{
		ID: "ap_policy_context", SessionID: session.ID, RunID: run.ID, ToolCallID: call.ID, Tool: call.Tool,
		Risk: app.RiskRead, Status: "pending", Arguments: call.Arguments, PolicyContext: policyContext, CreatedAt: time.Now().UTC(),
	}
	call.ApprovalID = approval.ID
	testSaveToolCall(st, call)
	mustSaveApproval(t, st, approval)

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	gotCall, callOK := testGetToolCall(reloaded, call.ID)
	gotApproval, approvalOK := mustGetApproval(t, reloaded, approval.ID)
	if !callOK || !approvalOK || gotCall.PolicyContext == nil || gotApproval.PolicyContext == nil ||
		gotCall.PolicyContext.ContractDigest != policyContext.ContractDigest ||
		gotApproval.PolicyContext.MCP == nil || gotApproval.PolicyContext.MCP.RequesterDeviceID != "device-file" {
		t.Fatalf("policy execution context did not survive file-store reload: call=%#v approval=%#v", gotCall, gotApproval)
	}
}

func TestFileStorePersistsMemoryRetentionPrune(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "Retention")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   session.ID,
		RunID:       run.ID,
		Kind:        "profile",
		Content:     "file retention memory",
		Sensitivity: "normal",
		Status:      "pending",
		Reason:      "test",
		CreatedAt:   time.Now().UTC().AddDate(0, 0, -30),
	})
	_, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	pruned := st.PruneMemories(time.Now().UTC().AddDate(0, 0, 1))
	if len(pruned) != 1 || pruned[0].ID != memory.ID {
		t.Fatalf("unexpected pruned memories: %#v", pruned)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if memories := reloaded.SearchMemories("file retention"); len(memories) != 0 {
		t.Fatalf("pruned memory reloaded unexpectedly: %#v", memories)
	}
	if !hasAuditType(mustListAudit(t, reloaded, session.ID), "memory.pruned") {
		t.Fatalf("pruned memory audit did not persist: %#v", mustListAudit(t, reloaded, session.ID))
	}
}

func TestFileStoreEncryptsStateAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStoreWithOptions(FileStoreOptions{
		Path:          path,
		EncryptAtRest: true,
		EncryptionKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "Encrypted Session")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID: session.ID,
		RunID:     run.ID,
		Kind:      "profile",
		Content:   "super private encrypted memory",
		Status:    "pending",
	})
	if _, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted"); err != nil || memory == nil {
		t.Fatalf("accepted memory missing memory=%#v err=%v", memory, err)
	}
	if _, err := st.SaveCredentialSecret(context.Background(), NewCredentialCreate(app.CredentialSecret{
		Ref:   "browser-auth:test",
		Kind:  "browser-auth-state",
		Value: `{"cookie":"fixture=browser-cookie"}`,
	})); err != nil {
		t.Fatal(err)
	}
	st.SaveBrowserAuthRecord(app.BrowserAuthRecord{
		OwnerID:          app.DefaultOwnerID,
		BrowserProfileID: "default",
		SiteOrigin:       "https://example.com",
		CredentialRef:    "browser-auth:test",
		CookieJarRef:     "browser-auth:test",
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("super private encrypted memory")) || bytes.Contains(raw, []byte("browser-cookie")) || !bytes.Contains(raw, []byte(`"alg": "AES-256-GCM"`)) {
		t.Fatalf("encrypted state leaked plaintext or missing envelope: %s", raw)
	}
	reloaded, err := NewFileStoreWithOptions(FileStoreOptions{
		Path:          path,
		EncryptAtRest: true,
		EncryptionKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if memories := reloaded.SearchMemories("encrypted memory"); len(memories) != 1 {
		t.Fatalf("encrypted memory did not reload: %#v", memories)
	}
	if record, ok := reloaded.FindBrowserAuthRecord(app.DefaultOwnerID, "default", "https://example.com", "", ""); !ok || record.CredentialRef != "browser-auth:test" {
		t.Fatalf("browser auth record did not reload from encrypted state: %#v ok=%v", record, ok)
	}
	if secret, ok, err := reloaded.GetCredentialSecret(context.Background(), "browser-auth:test"); err != nil || !ok || !strings.Contains(secret.Value, "browser-cookie") {
		t.Fatalf("browser auth secret did not reload from encrypted state: %#v ok=%v err=%v", secret, ok, err)
	}
}

func TestFileStoreEncryptionReadsLegacyPlaintextState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "Legacy Plaintext")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID: session.ID,
		RunID:     run.ID,
		Kind:      "profile",
		Content:   "legacy plaintext memory",
		Status:    "pending",
	})
	if _, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted"); err != nil || memory == nil {
		t.Fatalf("accepted legacy memory missing memory=%#v err=%v", memory, err)
	}

	reloaded, err := NewFileStoreWithOptions(FileStoreOptions{
		Path:          path,
		EncryptAtRest: true,
		EncryptionKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if memories := reloaded.SearchMemories("legacy plaintext"); len(memories) != 1 {
		t.Fatalf("legacy plaintext state did not reload with encryption enabled: %#v", memories)
	}
	testSaveRun(reloaded, run)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("legacy plaintext memory")) || !bytes.Contains(raw, []byte(`"alg": "AES-256-GCM"`)) {
		t.Fatalf("legacy state was not rewritten encrypted: %s", raw)
	}
}

func TestFileStoreEncryptionKeyFile(t *testing.T) {
	root := t.TempDir()
	keyFile := filepath.Join(root, "state.key")
	if err := os.WriteFile(keyFile, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gateway-state.json")
	st, err := NewFileStoreWithOptions(FileStoreOptions{
		Path:              path,
		EncryptAtRest:     true,
		EncryptionKeyFile: keyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, st, "Key File")
	mustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: "encrypted via key file"})
	reloaded, err := NewFileStoreWithOptions(FileStoreOptions{
		Path:              path,
		EncryptAtRest:     true,
		EncryptionKeyFile: keyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if messages := mustListMessages(t, reloaded, session.ID); len(messages) != 1 || messages[0].Content != "encrypted via key file" {
		t.Fatalf("key-file encrypted state did not reload: %#v", messages)
	}
}
