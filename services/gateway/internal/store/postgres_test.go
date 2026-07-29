package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestPostgresStoreRoundTrip(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	ctx := context.Background()
	st, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	truncatePostgresStore(t, st)

	session := st.CreateSession("Postgres Session")
	if got, ok := st.GetSession(session.ID); !ok || got.Title != "Postgres Session" {
		t.Fatalf("session did not round trip: %#v ok=%v", got, ok)
	}
	defaultOwner := st.GetOwnerProfile()
	if defaultOwner.ID != app.DefaultOwnerID || defaultOwner.DisplayName == "" {
		t.Fatalf("default owner did not load: %#v", defaultOwner)
	}
	updatedOwner := st.UpdateOwnerProfile(app.OwnerProfile{
		DisplayName: "Postgres Owner",
		Email:       "pg-owner@example.test",
		Preferences: map[string]string{"timezone": "UTC", "tone": "brief"},
	})
	if updatedOwner.ID != app.DefaultOwnerID || updatedOwner.Preferences["timezone"] != "UTC" {
		t.Fatalf("owner profile did not update: %#v", updatedOwner)
	}
	if got := st.GetOwnerProfile(); got.DisplayName != "Postgres Owner" || got.Email != "pg-owner@example.test" || got.Preferences["tone"] != "brief" {
		t.Fatalf("owner profile did not round trip: %#v", got)
	}
	message := st.AddMessage(app.Message{SessionID: session.ID, Role: "user", Content: "remember postgres"})
	if messages := st.ListMessages(session.ID); len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("messages did not round trip: %#v", messages)
	}

	run := app.AgentRun{
		ID:        app.NewID("run"),
		SessionID: session.ID,
		State:     "completed",
		ModelLane: "fast",
		Risk:      app.RiskRead,
		StartedAt: time.Now().UTC(),
		Workflow: &app.WorkflowState{
			SchemaVersion: 1,
			Plan: app.WorkflowPlan{
				SchemaVersion:   1,
				ProfileID:       app.WorkflowWebExplicitURL,
				ProfileRevision: 1,
			},
			PlanDigest:    "sha256:postgres-plan",
			Status:        app.WorkflowStatusSucceeded,
			ActiveNodeIDs: []app.WorkflowNodeID{"read"},
			Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
				"read": {Status: app.WorkflowNodeSucceeded, ScopeRevision: 1},
			},
		},
	}
	st.SaveRun(run)
	if got, ok := st.GetRun(run.ID); !ok || got.Workflow == nil || got.Workflow.PlanDigest != "sha256:postgres-plan" || got.Workflow.Nodes["read"].Status != app.WorkflowNodeSucceeded {
		t.Fatalf("workflow state did not round trip: %#v ok=%v", got, ok)
	}
	call := app.ToolCall{
		ID:                 app.NewID("tc"),
		SessionID:          session.ID,
		RunID:              run.ID,
		WorkflowID:         app.WorkflowWebExplicitURL,
		WorkflowNodeID:     "read",
		ScopeRevision:      1,
		Capability:         "web.page.read",
		Tool:               "memory.write_candidate",
		Risk:               app.RiskDraft,
		Status:             "completed",
		Arguments:          map[string]any{"content": "postgres memory"},
		Result:             map[string]any{"status": "ok"},
		ObservationSummary: "memory.write_candidate returned 1 result(s). Observation bytes=15.",
		StartedAt:          time.Now().UTC(),
	}
	st.SaveToolCall(call)
	if got, ok := st.GetToolCall(call.ID); !ok || got.Arguments["content"] != "postgres memory" || got.ObservationSummary != call.ObservationSummary ||
		got.WorkflowID != app.WorkflowWebExplicitURL || got.WorkflowNodeID != "read" || got.ScopeRevision != 1 || got.Capability != "web.page.read" {
		t.Fatalf("tool call did not round trip: %#v ok=%v", got, ok)
	}
	documentRecord := st.SaveDocumentRecord(app.DocumentRecord{
		ID: "doc_pg", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "report.docx", Name: "report.docx", Format: app.DocumentFormatDOCX,
		Status: app.DocumentStatusAvailable, Source: app.DocumentSourceToolOutput,
		SourceRunID: run.ID, SourceToolCallID: call.ID, LastActivity: app.DocumentActivityEdited,
		LastActivityID: call.ID, LastActivityAt: time.Now().UTC(),
	})
	if got, ok := st.GetDocumentRecord(documentRecord.ID); !ok || got.SourceToolCallID != call.ID {
		t.Fatalf("document record did not round trip: %#v ok=%v", got, ok)
	}
	if records := st.ListDocumentRecords(session.OwnerID, session.ID, 10); len(records) != 1 || records[0].ID != documentRecord.ID {
		t.Fatalf("document records did not list: %#v", records)
	}
	modelCompleted := time.Now().UTC()
	st.SaveModelCall(app.ModelCall{
		ID:             app.NewID("mcall"),
		SessionID:      session.ID,
		RunID:          run.ID,
		Lane:           "fast",
		Profile:        "sparkclaw-fast",
		Model:          "Qwen/Fast",
		Operation:      "chat",
		Mock:           true,
		Status:         "completed",
		PromptTokens:   3,
		ResponseTokens: 2,
		TotalTokens:    5,
		LatencyMS:      12,
		StartedAt:      time.Now().UTC(),
		CompletedAt:    &modelCompleted,
	})
	modelCalls := st.ListModelCalls(session.ID, run.ID)
	if len(modelCalls) != 1 || modelCalls[0].Model != "Qwen/Fast" || modelCalls[0].TotalTokens != 5 {
		t.Fatalf("model call did not round trip: %#v", modelCalls)
	}

	approval := app.Approval{
		ID:         app.NewID("ap"),
		SessionID:  session.ID,
		RunID:      run.ID,
		ToolCallID: call.ID,
		Tool:       "code.apply_patch",
		Risk:       app.RiskReversible,
		Status:     "pending",
		Summary:    "Apply patch",
		Reason:     "test",
		Resources:  []string{"example.txt"},
		Arguments:  map[string]any{"patch": "---"},
		CreatedAt:  time.Now().UTC(),
	}
	st.SaveApproval(approval)
	resolved, err := st.ResolveApproval(approval.ID, "approved", "ok")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "approved" || resolved.ResolutionNote != "ok" {
		t.Fatalf("approval did not resolve: %#v", resolved)
	}

	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   session.ID,
		RunID:       run.ID,
		Kind:        "profile",
		Content:     "postgres is configured",
		Sensitivity: "normal",
		Reason:      "test",
	})
	_, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if memory == nil || memory.Content != "postgres is configured" {
		t.Fatalf("memory did not materialize: %#v", memory)
	}
	if memories := st.SearchMemories("configured"); len(memories) != 1 {
		t.Fatalf("memory search failed: %#v", memories)
	}
	updated, err := st.UpdateMemory(memory.ID, "procedural", "postgres memory editor updated")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Kind != "procedural" || updated.Content != "postgres memory editor updated" {
		t.Fatalf("memory update failed: %#v", updated)
	}
	if memories := st.SearchMemories("configured"); len(memories) != 0 {
		t.Fatalf("old memory content still searchable: %#v", memories)
	}
	if memories := st.SearchMemories("editor updated"); len(memories) != 1 || memories[0].ID != memory.ID {
		t.Fatalf("updated memory search failed: %#v", memories)
	}
	deleted, err := st.DeleteMemory(memory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ID != memory.ID {
		t.Fatalf("delete returned wrong memory: %#v", deleted)
	}
	if memories := st.SearchMemories("editor updated"); len(memories) != 0 {
		t.Fatalf("deleted memory still searchable: %#v", memories)
	}
	retentionCandidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   session.ID,
		RunID:       run.ID,
		Kind:        "profile",
		Content:     "postgres retention memory",
		Sensitivity: "normal",
		Reason:      "test",
	})
	_, retentionMemory, err := st.ResolveMemoryCandidate(retentionCandidate.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	pruned := st.PruneMemories(time.Now().UTC().AddDate(0, 0, 1))
	if len(pruned) != 1 || pruned[0].ID != retentionMemory.ID {
		t.Fatalf("unexpected pruned postgres memories: %#v", pruned)
	}
	if memories := st.SearchMemories("retention memory"); len(memories) != 0 {
		t.Fatalf("pruned postgres memory still searchable: %#v", memories)
	}
	if events := st.EventsAfter(session.ID, ""); len(events) == 0 {
		t.Fatalf("expected event stream entries")
	}
	if audit := st.ListAudit(session.ID); len(audit) == 0 {
		t.Fatalf("expected audit entries")
	}
	st.SaveEvalRun(app.EvalRun{
		ID:      "eval_pg",
		Profile: "smoke",
		Status:  "failed",
		Summary: "1/1 failed",
		FailureArchives: []app.EvalArtifact{{
			CaseName:    "broken_case",
			URI:         "artifact://sparkclaw/eval-failures/eval_pg/broken_case.json",
			Key:         "eval-failures/eval_pg/broken_case.json",
			Backend:     "filesystem",
			ContentType: "application/json",
			Bytes:       128,
		}},
	})
	if evalRun, ok := st.GetEvalRun("eval_pg"); !ok || evalRun.Status != "failed" || len(evalRun.FailureArchives) != 1 {
		t.Fatalf("eval run did not round trip: %#v ok=%v", evalRun, ok)
	}
	evalRuns := st.ListEvalRuns()
	if len(evalRuns) != 1 || evalRuns[0].ID != "eval_pg" || len(evalRuns[0].FailureArchives) != 1 {
		t.Fatalf("eval runs did not list: %#v", evalRuns)
	}
	st.SaveArtifactObject(app.ArtifactObject{
		ID:          "obj_pg",
		Kind:        "eval_failure",
		EvalID:      "eval_pg",
		Backend:     "filesystem",
		Bucket:      "sparkclaw",
		Key:         "eval-failures/eval_pg/broken_case.json",
		URI:         "artifact://sparkclaw/eval-failures/eval_pg/broken_case.json",
		ContentType: "application/json",
		Bytes:       128,
		CreatedAt:   time.Now().UTC(),
	})
	if objects := st.ListArtifactObjects(10); len(objects) != 1 || objects[0].ID != "obj_pg" || objects[0].EvalID != "eval_pg" {
		t.Fatalf("artifact object did not round trip: %#v", objects)
	}
	st.SaveEpisodeSummary(app.EpisodeSummary{
		ID:        "ep_pg",
		SessionID: session.ID,
		RunID:     run.ID,
		Goal:      "postgres episode",
		Outcome:   "completed",
		Risk:      app.RiskRead,
		ModelLane: "fast",
		Tools:     []string{"memory.write_candidate:completed"},
		Summary:   "Postgres episode summary",
		CreatedAt: time.Now().UTC(),
	})
	episodes := st.ListEpisodeSummaries(session.ID)
	if len(episodes) != 1 || episodes[0].ID != "ep_pg" || episodes[0].Tools[0] != "memory.write_candidate:completed" {
		t.Fatalf("episode summary did not round trip: %#v", episodes)
	}

}

func TestPostgresStoreExternalChatAndInboxParity(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	st, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	truncatePostgresStore(t, st)
	testExternalChatAndInboxParity(t, st)
	testMessageLifecycleParity(t, st)
}

func TestPostgresStoreDeleteSessionRemovesBrowserLoginBlocks(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	st, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	truncatePostgresStore(t, st)

	session := st.CreateSession("delete blocked browser session")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
	block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
		SessionID:  session.ID,
		RunID:      run.ID,
		SiteOrigin: "https://example.com",
	})
	if _, ok := st.GetBrowserLoginBlock(block.ID); !ok {
		t.Fatal("browser login block was not saved")
	}

	if _, err := st.DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetBrowserLoginBlock(block.ID); ok {
		t.Fatal("session deletion retained browser login block")
	}
	if _, ok := st.GetRun(run.ID); ok {
		t.Fatal("session deletion retained agent run")
	}
	if _, ok := st.GetSession(session.ID); ok {
		t.Fatal("session deletion retained session")
	}
}

func TestPostgresStoreBrowserHandoffCASRoundTrip(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	st, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	truncatePostgresStore(t, st)

	session := st.CreateSession("browser handoff CAS")
	run := app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked",
		ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC(),
	}
	st.SaveRun(run)
	leaseUntil := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
		SessionID: session.ID, RunID: run.ID,
		WorkflowID: app.WorkflowBrowserAutomation, WorkflowRevision: app.BrowserWorkflowRevision2,
		WorkflowNodeID: "browser_result", SessionGeneration: 17,
		Status:            app.BrowserHandoffStatusValidatingVisible,
		TransitionOwnerID: "runtime-postgres", TransitionLeaseUntil: &leaseUntil,
		Target: app.BrowserTargetDescriptor{
			TargetKind:    app.BrowserTargetRegisteredDestination,
			DestinationID: "qq_mail", CanonicalURL: "https://wx.mail.qq.com/home/index#/list/1/1",
			RedactedURL: "https://wx.mail.qq.com/home/index#/list/1/1",
		},
		VisibleEvidence: &app.BrowserResultEvidence{
			ID: "visible-postgres", SchemaVersion: app.BrowserHandoffSchemaVersion,
			VisiblePageID: "page-postgres", VisibleSnapshotID: "snapshot-postgres",
		},
	})
	got, ok := st.GetBrowserLoginBlock(block.ID)
	if !ok || got.Version != block.Version || got.Target.DestinationID != "qq_mail" ||
		got.VisibleEvidence == nil || got.VisibleEvidence.VisibleSnapshotID != "snapshot-postgres" ||
		got.TransitionOwnerID != "runtime-postgres" || got.TransitionLeaseUntil == nil ||
		!got.TransitionLeaseUntil.Equal(leaseUntil) {
		t.Fatalf("PostgreSQL handoff round trip mismatch: %#v ok=%v", got, ok)
	}
	update := got
	update.Status = app.BrowserHandoffStatusTransferring
	updated, err := st.UpdateBrowserLoginBlock(update, got.Version)
	if err != nil {
		t.Fatal(err)
	}
	stale := got
	stale.LastError = "stale"
	if _, err := st.UpdateBrowserLoginBlock(stale, got.Version); !errors.Is(err, ErrBrowserHandoffConflict) {
		t.Fatalf("stale PostgreSQL handoff update error = %v", err)
	}
	current, ok := st.GetBrowserLoginBlock(block.ID)
	if !ok || current.Version != updated.Version || current.Status != app.BrowserHandoffStatusTransferring ||
		current.LastError == "stale" {
		t.Fatalf("PostgreSQL CAS result mismatch: %#v ok=%v", current, ok)
	}
}

func truncatePostgresStore(t *testing.T, st *PostgresStore) {
	t.Helper()
	_, err := st.db.Exec(context.Background(), `
		TRUNCATE message_delivery_records, message_receive_records, channel_inbox_updates, external_chat_messages, external_chat_sessions, weixin_chat_messages, weixin_chat_sessions,
			credential_secrets, notification_bindings, reminder_deliveries, reminders, events, audit_events, owners, eval_runs,
			artifact_objects, episode_summaries, memories, memory_candidates, approvals, document_records, tool_calls,
			model_calls, run_feedback, messages, agent_runs, sessions
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatal(err)
	}
}
