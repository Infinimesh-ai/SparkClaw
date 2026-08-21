package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
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

	session := mustCreateSession(t, st, "Postgres Session")
	if got, ok := mustGetSession(t, st, session.ID); !ok || got.Title != "Postgres Session" {
		t.Fatalf("session did not round trip: %#v ok=%v", got, ok)
	}
	defaultOwner := mustGetOwnerProfile(t, st)
	if defaultOwner.ID != app.DefaultOwnerID || defaultOwner.DisplayName == "" {
		t.Fatalf("default owner did not load: %#v", defaultOwner)
	}
	updatedOwner := mustUpdateOwnerProfile(t, st, app.OwnerProfile{
		DisplayName: "Postgres Owner",
		Email:       "pg-owner@example.test",
		Preferences: map[string]string{"timezone": "UTC", "tone": "brief"},
	})
	if updatedOwner.ID != app.DefaultOwnerID || updatedOwner.Preferences["timezone"] != "UTC" {
		t.Fatalf("owner profile did not update: %#v", updatedOwner)
	}
	if got := mustGetOwnerProfile(t, st); got.DisplayName != "Postgres Owner" || got.Email != "pg-owner@example.test" || got.Preferences["tone"] != "brief" {
		t.Fatalf("owner profile did not round trip: %#v", got)
	}
	message := st.AddMessage(app.Message{
		SessionID: session.ID, Role: "user", Content: "remember postgres",
		RequestedMedia: []app.MessageMediaLocator{{Query: "quarterly report", Caption: "Latest report"}},
	})
	if messages := st.ListMessages(session.ID); len(messages) != 1 || messages[0].ID != message.ID || len(messages[0].RequestedMedia) != 1 ||
		messages[0].RequestedMedia[0].Query != "quarterly report" || messages[0].RequestedMedia[0].Caption != "Latest report" {
		t.Fatalf("messages did not round trip: %#v", messages)
	}
	messageHead, err := st.MessageEventHead(session.ID)
	if err != nil || messageHead == "" {
		t.Fatalf("message event head did not round trip: head=%q err=%v", messageHead, err)
	}
	messagePage, err := st.MessageEventsAfter(session.ID, "", 100)
	if err != nil || len(messagePage.Events) != 1 || messagePage.NextCursor != messageHead {
		t.Fatalf("message event page did not round trip: page=%#v err=%v", messagePage, err)
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
	policyContext := &app.PolicyExecutionContext{
		SchemaVersion: 1, PrincipalClass: app.PolicyPrincipalExternalMCPAI,
		ResourceClass: app.PolicyResourceSparkClawWorkspaceData, AccessClass: app.PolicyAccessWorkspaceSourceRead,
		RunID: run.ID, OwnerID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
		WorkflowID: app.WorkflowWebExplicitURL, WorkflowRevision: 1,
		PlanDigest: "sha256:postgres-plan", ContractDigest: "sha256:postgres-policy-contract",
		MCP: &app.MCPInvocationRef{
			InvocationID: "inv-postgres-policy", OperationID: "op-postgres-policy", BindingRef: "binding-postgres-policy",
			BindingRevision: 1, RequesterDeviceID: "device-postgres-policy",
		},
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
		PolicyContext:      policyContext,
		StartedAt:          time.Now().UTC(),
	}
	st.SaveToolCall(call)
	if got, ok := st.GetToolCall(call.ID); !ok || got.Arguments["content"] != "postgres memory" || got.ObservationSummary != call.ObservationSummary ||
		got.WorkflowID != app.WorkflowWebExplicitURL || got.WorkflowNodeID != "read" || got.ScopeRevision != 1 || got.Capability != "web.page.read" ||
		got.PolicyContext == nil || got.PolicyContext.ContractDigest != policyContext.ContractDigest ||
		got.PolicyContext.MCP == nil || got.PolicyContext.MCP.RequesterDeviceID != policyContext.MCP.RequesterDeviceID {
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
		ID:            app.NewID("ap"),
		SessionID:     session.ID,
		RunID:         run.ID,
		ToolCallID:    call.ID,
		Tool:          "memory.write_sensitive",
		Risk:          app.RiskDangerous,
		Status:        "pending",
		Summary:       "Write sensitive memory",
		Reason:        "test",
		Resources:     []string{"memory"},
		Arguments:     map[string]any{"content": "test"},
		PolicyContext: policyContext,
		CreatedAt:     time.Now().UTC(),
	}
	st.SaveApproval(approval)
	resolved, err := st.ResolveApproval(approval.ID, "approved", "ok")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "approved" || resolved.ResolutionNote != "ok" || resolved.PolicyContext == nil ||
		resolved.PolicyContext.ContractDigest != policyContext.ContractDigest {
		t.Fatalf("approval did not resolve: %#v", resolved)
	}
	restarted, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedCall, callOK := restarted.GetToolCall(call.ID)
	restartedApproval, approvalOK := restarted.GetApproval(approval.ID)
	if !callOK || !approvalOK || restartedCall.PolicyContext == nil || restartedApproval.PolicyContext == nil ||
		restartedCall.PolicyContext.ContractDigest != policyContext.ContractDigest ||
		restartedApproval.PolicyContext.ContractDigest != policyContext.ContractDigest {
		t.Fatalf("policy context did not survive PostgreSQL reconnect: call=%#v approval=%#v", restartedCall, restartedApproval)
	}
	externalApproval := app.Approval{
		ID: "ap_happy_postgres", Source: app.ApprovalSourceHappyTeamPlan,
		ExternalID: "task-postgres", Tool: "mcp.happy-tasks.approve_plan",
		Risk: app.RiskDangerous, Status: "pending", Summary: "Review PostgreSQL plan",
		ExternalContext: &app.ExternalApprovalContext{
			Provider: "happy-team", Title: "Postgres task", GoalPrompt: "Persist without a run",
			Plan: "Database-backed plan", PlanAvailability: app.ExternalPlanAvailable,
		},
	}
	st.SaveApproval(externalApproval)
	storedExternal, ok := st.FindApprovalByExternalRef(app.ApprovalSourceHappyTeamPlan, "task-postgres")
	if !ok || storedExternal.SessionID != "" || storedExternal.RunID != "" || storedExternal.ToolCallID != "" ||
		storedExternal.ExternalContext == nil || storedExternal.ExternalContext.Plan != "Database-backed plan" {
		t.Fatalf("external approval did not round trip without agent references: %#v ok=%v", storedExternal, ok)
	}
	if _, err := st.ResolveApproval(externalApproval.ID, "approved", "done"); err != nil {
		t.Fatal(err)
	}
	externalApproval.ExternalContext.Plan = "stale update"
	if _, err := st.UpdatePendingApproval(externalApproval); err == nil {
		t.Fatal("stale PostgreSQL update reopened a resolved approval")
	}
	storedExternal, _ = st.GetApproval(externalApproval.ID)
	if storedExternal.Status != "approved" || storedExternal.ExternalContext.Plan != "Database-backed plan" {
		t.Fatalf("resolved PostgreSQL approval changed after stale update: %#v", storedExternal)
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
	if object, ok := st.FindArtifactObjectByURI("artifact://sparkclaw/eval-failures/eval_pg/broken_case.json", "", ""); !ok || object.ID != "obj_pg" {
		t.Fatalf("artifact lookup by URI failed: %#v ok=%v", object, ok)
	}
	if _, ok := st.FindArtifactObjectByURI("artifact://sparkclaw/eval-failures/eval_pg/broken_case.json", session.ID, ""); ok {
		t.Fatal("artifact lookup matched a session it does not belong to")
	}
	if _, ok := st.FindArtifactObjectByURI("artifact://sparkclaw/missing.json", "", ""); ok {
		t.Fatal("missing URI lookup returned an object")
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

func TestPostgresStoreMCPAccessAtomicityIdempotencyAndRecovery(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	st, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	truncatePostgresStore(t, st)
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "postgres-ticket-hash-only"))
	if err != nil {
		t.Fatal(err)
	}
	if ticket.SecretHash != "postgres-ticket-hash-only" {
		t.Fatalf("PostgreSQL changed the ticket hash: %#v", ticket)
	}
	peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "postgres-device", KeyThumbprint: "postgres-thumb", ISCPSessionID: "postgres-iscp"}

	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, redeemErr := st.RedeemMCPAccessTicket(ticket.SecretHash, peer, now.Add(time.Second))
			results <- redeemErr
		}()
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for redeemErr := range results {
		if redeemErr == nil {
			succeeded++
		} else if !errors.Is(redeemErr, ErrMCPAccessTicketInvalid) {
			t.Fatalf("unexpected PostgreSQL redemption error: %v", redeemErr)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful PostgreSQL redemptions = %d, want 1", succeeded)
	}
	binding, ok := st.FindMCPBindingForPeer(peer.DomainID, peer.DeviceID, peer.KeyThumbprint)
	if !ok || binding.ActorID != ticket.OwnerID || binding.RequesterDeviceID == binding.ActorID {
		t.Fatalf("PostgreSQL binding identity mismatch: %#v ok=%v", binding, ok)
	}
	operation, created, err := st.CreateMCPOperation(app.MCPOperation{
		ID: "mcp_operation_postgres", BindingID: binding.ID, IdempotencyKey: "postgres-idempotency", Fingerprint: "postgres-fingerprint",
		Invocation: app.MCPInvocationContext{ID: "postgres-invocation", BindingRef: binding.ID, RunID: "postgres-run"},
	})
	if err != nil || !created {
		t.Fatalf("create PostgreSQL MCP operation: created=%v err=%v", created, err)
	}
	replayed, created, err := st.CreateMCPOperation(app.MCPOperation{
		BindingID: binding.ID, IdempotencyKey: operation.IdempotencyKey, Fingerprint: operation.Fingerprint,
	})
	if err != nil || created || replayed.ID != operation.ID {
		t.Fatalf("PostgreSQL idempotent replay mismatch: %#v created=%v err=%v", replayed, created, err)
	}
	if _, _, err := st.CreateMCPOperation(app.MCPOperation{
		BindingID: binding.ID, IdempotencyKey: operation.IdempotencyKey, Fingerprint: "changed-fingerprint",
	}); !errors.Is(err, ErrMCPOperationConflict) {
		t.Fatalf("PostgreSQL changed replay error = %v", err)
	}
	first := operation
	first.State = app.MCPOperationSucceeded
	first, err = st.UpdateMCPOperation(first, operation.Version)
	if err != nil {
		t.Fatal(err)
	}
	stale := operation
	stale.State = app.MCPOperationCancelled
	if _, err := st.UpdateMCPOperation(stale, operation.Version); !errors.Is(err, ErrMCPOperationVersionConflict) {
		t.Fatalf("PostgreSQL stale operation update error = %v", err)
	}
	if _, err := st.db.Exec(context.Background(), `UPDATE sessions SET title='External MCP', hidden=true WHERE id=$1`, binding.LinkedSessionID); err != nil {
		t.Fatal(err)
	}
	st.Close()

	restarted, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	storedTicket, ok := restarted.FindMCPAccessTicketBySecretHash(ticket.SecretHash)
	if !ok || storedTicket.Status != app.MCPAccessConsumed || storedTicket.SecretHash != "postgres-ticket-hash-only" {
		t.Fatalf("PostgreSQL ticket did not recover hash-only state: %#v ok=%v", storedTicket, ok)
	}
	storedBinding, ok := restarted.GetMCPBinding(binding.ID)
	if !ok || storedBinding.RequesterDeviceID != peer.DeviceID || storedBinding.LinkedSessionID == "" {
		t.Fatalf("PostgreSQL binding did not recover: %#v ok=%v", storedBinding, ok)
	}
	if linked, ok := mustGetSession(t, restarted, storedBinding.LinkedSessionID); !ok || linked.Hidden || linked.Source != "mcp" || linked.Title != "AI · postgres-dev" {
		t.Fatalf("PostgreSQL legacy MCP conversation was not normalized on restart: %#v ok=%v", linked, ok)
	}
	storedOperation, ok := restarted.GetMCPOperation(operation.ID)
	if !ok || storedOperation.State != app.MCPOperationSucceeded || storedOperation.Version != first.Version {
		t.Fatalf("PostgreSQL operation did not recover its CAS winner: %#v ok=%v", storedOperation, ok)
	}
	if deleted, err := restarted.DeleteMCPAccessTicket(app.DefaultOwnerID, ticket.ID); err != nil || deleted.ID != ticket.ID {
		t.Fatalf("delete PostgreSQL consumed ticket: ticket=%#v err=%v", deleted, err)
	}
	if deleted, err := restarted.DeleteMCPBinding(app.DefaultOwnerID, binding.ID); err != nil || deleted.ID != binding.ID {
		t.Fatalf("delete PostgreSQL binding: binding=%#v err=%v", deleted, err)
	}
	if _, ok := restarted.GetMCPOperation(operation.ID); ok {
		t.Fatal("PostgreSQL binding deletion retained its operation")
	}
	defaultTicket, err := restarted.SaveMCPAccessTicket(testMCPAccessTicket(now, "postgres-bulk-default"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RedeemMCPAccessTicket(defaultTicket.SecretHash, app.MCPPeerIdentity{
		DomainID: defaultTicket.DomainID, DeviceID: "postgres-bulk-device", KeyThumbprint: "postgres-bulk-thumb", ISCPSessionID: "postgres-bulk-iscp",
	}, now); err != nil {
		t.Fatal(err)
	}
	otherTicket := testMCPAccessTicket(now, "postgres-bulk-other")
	otherTicket.OwnerID, otherTicket.ActorID = "owner-other", "owner-other"
	otherTicket, err = restarted.SaveMCPAccessTicket(otherTicket)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := restarted.DeleteMCPAccessRecords(app.DefaultOwnerID); err != nil || deleted.DeletedTickets != 1 || deleted.DeletedBindings != 1 {
		t.Fatalf("delete PostgreSQL owner records: deleted=%#v err=%v", deleted, err)
	}
	if _, ok := restarted.GetMCPAccessTicket(otherTicket.ID); !ok {
		t.Fatal("PostgreSQL owner-scoped deletion removed another owner's ticket")
	}
}

func TestPostgresStorePersistsOnlyISCPOnboardingReceipt(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	st, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	truncatePostgresStore(t, st)
	now := time.Now().UTC()
	receipt, err := st.SaveISCPOnboarding(context.Background(), testISCPOnboarding(now, "iscp_onboarding_postgres", app.DefaultOwnerID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveISCPOnboarding(context.Background(), receipt); !errors.Is(err, ErrISCPOnboardingConflict) {
		t.Fatalf("duplicate onboarding error = %v", err)
	}
	if _, err := st.SaveISCPOnboarding(context.Background(), testISCPOnboarding(now.Add(time.Second), "iscp_onboarding_other", "other-owner")); err != nil {
		t.Fatal(err)
	}
	listed, err := st.ListISCPOnboardings(context.Background(), app.DefaultOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != receipt.ID {
		t.Fatalf("owner-scoped PostgreSQL onboardings = %#v", listed)
	}
	var payload string
	if err := st.db.QueryRow(context.Background(), `SELECT payload::text FROM iscp_onboardings WHERE id=$1`, receipt.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "signature") || strings.Contains(payload, "signed-ticket-value") {
		t.Fatalf("PostgreSQL persisted Pairing Ticket secret material: %s", payload)
	}
	st.Close()

	restarted, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	got, ok, err := restarted.GetISCPOnboarding(context.Background(), receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.AuthorityRef != receipt.AuthorityRef || got.TicketID != receipt.TicketID || got.MaxUses != 1 {
		t.Fatalf("PostgreSQL onboarding receipt did not survive restart: %#v ok=%v", got, ok)
	}
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

	session := mustCreateSession(t, st, "delete blocked browser session")
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

	if _, err := st.DeleteSession(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetBrowserLoginBlock(block.ID); ok {
		t.Fatal("session deletion retained browser login block")
	}
	if _, ok := st.GetRun(run.ID); ok {
		t.Fatal("session deletion retained agent run")
	}
	if _, ok := mustGetSession(t, st, session.ID); ok {
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

	session := mustCreateSession(t, st, "browser handoff CAS")
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

func TestPostgresStoreFindActiveBrowserLoginBlockMatchesSharedActivePredicate(t *testing.T) {
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

	statuses := append(app.BrowserHandoffActiveStatuses(),
		app.BrowserHandoffStatusResolved,
		app.BrowserHandoffStatusCanceled,
		app.BrowserHandoffStatusFailed,
	)
	for _, status := range statuses {
		session := mustCreateSession(t, st, "active predicate "+status)
		run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
		st.SaveRun(run)
		block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
			SessionID: session.ID, RunID: run.ID, Status: status, SiteOrigin: "https://example.com",
		})
		found, ok := st.FindActiveBrowserLoginBlock(session.ID)
		if want := app.BrowserHandoffStatusActive(status); ok != want {
			t.Fatalf("status %q: FindActiveBrowserLoginBlock ok=%v, shared predicate active=%v", status, ok, want)
		} else if ok && found.ID != block.ID {
			t.Fatalf("status %q: FindActiveBrowserLoginBlock returned %q, want %q", status, found.ID, block.ID)
		}
	}
}

func TestPostgresStorePassiveNotificationPruneAndRevision(t *testing.T) {
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

	now := time.Now().UTC()
	seed := []struct {
		id   string
		age  time.Duration
		read bool
	}{
		{id: "stale", age: 10 * 24 * time.Hour},
		{id: "read-old", age: 50 * time.Minute, read: true},
		{id: "read-new", age: 10 * time.Minute, read: true},
		{id: "unread-old", age: 40 * time.Minute},
		{id: "unread-new", age: time.Minute},
	}
	for _, item := range seed {
		notification := testPassiveNotification("notification-"+item.id, "endpoint-pg", "delivery-"+item.id, "fingerprint-"+item.id)
		notification.CreatedAt = now.Add(-item.age)
		if _, inserted, err := st.CreatePassiveNotification(notification); err != nil || !inserted {
			t.Fatalf("create %s = %v, %v", item.id, inserted, err)
		}
		if item.read {
			if _, err := st.MarkPassiveNotificationRead(app.DefaultOwnerID, notification.ID, time.Time{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	revBefore := st.PassiveNotificationRevision(app.DefaultOwnerID)
	if revBefore == 0 {
		t.Fatal("creates did not bump the revision")
	}

	// Retention removes the stale record; the cap then evicts read records
	// oldest-first before the oldest unread one.
	if removed := st.PrunePassiveNotifications(now.AddDate(0, 0, -7), 1); removed != 4 {
		t.Fatalf("prune removed %d, want 4", removed)
	}
	items := st.ListPassiveNotifications(app.DefaultOwnerID, "", 10)
	if len(items) != 1 || items[0].ID != "notification-unread-new" {
		t.Fatalf("survivors = %#v", items)
	}
	if got := st.PassiveNotificationRevision(app.DefaultOwnerID); got == revBefore {
		t.Fatal("prune did not bump the revision")
	}
	// A pruned idempotency key is replayable again.
	replay := testPassiveNotification("notification-stale", "endpoint-pg", "delivery-stale", "fingerprint-stale")
	if _, inserted, err := st.CreatePassiveNotification(replay); err != nil || !inserted {
		t.Fatalf("replay after prune = %v, %v", inserted, err)
	}
}

func TestPostgresStoreListsAllConnectorSettings(t *testing.T) {
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
	for _, setting := range []app.ConnectorSetting{
		{OwnerID: "owner-b", Channel: "weixin", Enabled: true},
		{OwnerID: "owner-a", Channel: "telegram", Enabled: false},
	} {
		if _, err := st.UpdateConnectorSetting(t.Context(), setting, 0); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := st.ListAllConnectorSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 2 || settings[0].OwnerID != "owner-a" || settings[0].Channel != "telegram" ||
		settings[1].OwnerID != "owner-b" || settings[1].Channel != "weixin" {
		t.Fatalf("postgres all-owner connector settings = %#v", settings)
	}
}

func truncatePostgresStore(t *testing.T, st *PostgresStore) {
	t.Helper()
	_, err := st.db.Exec(context.Background(), `
		TRUNCATE mcp_operations, mcp_bindings, mcp_access_tickets, iscp_onboardings, message_delivery_records, message_receive_records, channel_inbox_updates, external_chat_messages, external_chat_sessions, weixin_chat_messages, weixin_chat_sessions, passive_notifications, connector_settings,
			credential_secrets, notification_bindings, reminder_deliveries, reminders, events, audit_events, owners, eval_runs,
			artifact_objects, episode_summaries, memories, memory_candidates, approvals, document_records, tool_calls,
			model_calls, run_feedback, messages, agent_runs, sessions
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.seedDefaultOwner(context.Background()); err != nil {
		t.Fatal(err)
	}
}
