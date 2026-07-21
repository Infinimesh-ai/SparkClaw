package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileStorePersistsAndReloadsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := st.CreateSession("Persistent Session")
	st.SaveClient(app.Client{ID: "client_test", Name: "Persistent Client", TokenHash: "hash"})
	if _, err := st.RevokeClient("client_test"); err != nil {
		t.Fatal(err)
	}
	st.AddMessage(app.Message{SessionID: session.ID, Role: "user", Content: "hello"})
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead}
	st.SaveRun(run)
	st.SaveModelCall(app.ModelCall{
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
	st.UpdateOwnerProfile(app.OwnerProfile{
		DisplayName: "Persistent Owner",
		Email:       "owner@example.test",
		Preferences: map[string]string{"timezone": "Asia/Shanghai", "style": "direct"},
	})
	st.SaveEvalRun(app.EvalRun{
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
	st.SaveEpisodeSummary(app.EpisodeSummary{
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
	st.SaveRunFeedback(app.RunFeedback{
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
	if got, ok := reloaded.GetSession(session.ID); !ok || got.Title != "Persistent Session" {
		t.Fatalf("session did not reload: %#v ok=%v", got, ok)
	}
	clients := reloaded.ListClients()
	if len(clients) != 1 || clients[0].ID != "client_test" || clients[0].RevokedAt == nil {
		t.Fatalf("client did not reload revoked: %#v", clients)
	}
	if messages := reloaded.ListMessages(session.ID); len(messages) != 1 || messages[0].Content != "hello" {
		t.Fatalf("messages did not reload: %#v", messages)
	}
	modelCalls := reloaded.ListModelCalls(session.ID, run.ID)
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
	owner := reloaded.GetOwnerProfile()
	if owner.DisplayName != "Persistent Owner" || owner.Email != "owner@example.test" || owner.Preferences["timezone"] != "Asia/Shanghai" {
		t.Fatalf("owner profile did not reload: %#v", owner)
	}
	if evalRun, ok := reloaded.GetEvalRun("eval_test"); !ok || evalRun.Status != "failed" || len(evalRun.FailureArchives) != 1 {
		t.Fatalf("eval run did not reload: %#v ok=%v", evalRun, ok)
	}
	evalRuns := reloaded.ListEvalRuns()
	if len(evalRuns) != 1 || evalRuns[0].ID != "eval_test" || len(evalRuns[0].FailureArchives) != 1 {
		t.Fatalf("eval runs did not list from persisted state: %#v", evalRuns)
	}
	episodes := reloaded.ListEpisodeSummaries(session.ID)
	if len(episodes) != 1 || episodes[0].ID != "ep_test" || episodes[0].Tools[0] != "memory.search:completed" {
		t.Fatalf("episode summary did not reload: %#v", episodes)
	}
	objects := reloaded.ListArtifactObjects(10)
	if len(objects) != 1 || objects[0].ID != "obj_test" || objects[0].RunID != run.ID {
		t.Fatalf("artifact object did not reload: %#v", objects)
	}
	feedback := reloaded.ListRunFeedback(run.ID)
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
	session := st.CreateSession("Workflow State")
	run := app.AgentRun{
		ID:        app.NewID("run"),
		SessionID: session.ID,
		State:     "running",
		ModelLane: "fast",
		Risk:      app.RiskRead,
		MessageContext: &app.MessageRunContext{Request: app.RequestNormalization{
			SchemaVersion: app.RequestNormalizationSchemaVersion,
			Original:      "今天杭州天气", Canonical: "查询今天杭州天气 2026-07-17", Source: "fast_model",
		}},
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
	st.SaveRun(run)
	st.SaveToolCall(app.ToolCall{
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
	gotRun, ok := reloaded.GetRun(run.ID)
	if !ok || gotRun.Workflow == nil || gotRun.MessageContext == nil || gotRun.MessageContext.Request.Canonical != "查询今天杭州天气 2026-07-17" {
		t.Fatalf("workflow state did not reload: %#v ok=%v", gotRun, ok)
	}
	gotNode := gotRun.Workflow.Nodes["research"]
	if gotRun.Workflow.PlanDigest != "sha256:test-plan" || gotNode.ScopeRevision != 2 ||
		gotNode.TransitionActivations["source_page"] != 1 || len(gotNode.AppliedOutcomeIDs) != 1 ||
		gotNode.LastDirectory == nil || gotNode.LastDirectory.DirectoryRevision != "directory_7" {
		t.Fatalf("workflow restart state changed: %#v", gotRun.Workflow)
	}
	calls := reloaded.ListToolCalls(session.ID)
	if len(calls) != 1 || calls[0].WorkflowID != app.WorkflowWebPublicResearch ||
		calls[0].WorkflowNodeID != "research" || calls[0].ScopeRevision != 2 || calls[0].Capability != "web.page.read" {
		t.Fatalf("tool workflow binding did not reload: %#v", calls)
	}
}

func TestFileStorePersistsMemoryRetentionPrune(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := st.CreateSession("Retention")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
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
	if !hasAuditType(reloaded.ListAudit(session.ID), "memory.pruned") {
		t.Fatalf("pruned memory audit did not persist: %#v", reloaded.ListAudit(session.ID))
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
	session := st.CreateSession("Encrypted Session")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
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
	st.SaveCredentialSecret(app.CredentialSecret{
		Ref:   "browser-auth:test",
		Kind:  "browser-auth-state",
		Value: `{"cookie":"fixture=browser-cookie"}`,
	})
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
	if secret, ok := reloaded.GetCredentialSecret("browser-auth:test"); !ok || !strings.Contains(secret.Value, "browser-cookie") {
		t.Fatalf("browser auth secret did not reload from encrypted state: %#v ok=%v", secret, ok)
	}
}

func TestFileStoreEncryptionReadsLegacyPlaintextState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := st.CreateSession("Legacy Plaintext")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
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
	reloaded.SaveRun(run)
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
	session := st.CreateSession("Key File")
	st.AddMessage(app.Message{SessionID: session.ID, Role: "user", Content: "encrypted via key file"})
	reloaded, err := NewFileStoreWithOptions(FileStoreOptions{
		Path:              path,
		EncryptAtRest:     true,
		EncryptionKeyFile: keyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if messages := reloaded.ListMessages(session.ID); len(messages) != 1 || messages[0].Content != "encrypted via key file" {
		t.Fatalf("key-file encrypted state did not reload: %#v", messages)
	}
}
