package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	stored := mustSaveReminder(t, st, app.Reminder{
		ID: "reminder-cas", SessionID: "session-cas", Text: "before", DueTime: now.Add(time.Hour),
		Status: "pending", CreatedAt: now, UpdatedAt: now,
	})
	updated := stored
	updated.Text = "after"
	updated.UpdatedAt = stored.UpdatedAt.Add(time.Nanosecond)
	if _, err := testUpdatePendingReminder(t, st, updated, stored.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	updated.Text = "stale overwrite"
	if _, err := testUpdatePendingReminder(t, st, updated, stored.UpdatedAt); !errors.Is(err, ErrReminderConflict) {
		t.Fatalf("expected stale compare-and-swap conflict, got %v", err)
	}
	current, _ := mustGetReminder(t, st, stored.ID)
	if current.Text != "after" {
		t.Fatalf("stale update changed reminder: %#v", current)
	}
}

func TestMemoryStoreFindsExternalApprovalByStableReference(t *testing.T) {
	st := NewMemoryStore()
	approval := app.Approval{
		ID: "ap_happy_task_one", Source: app.ApprovalSourceHappyTeamPlan,
		ExternalID: "task-one", Tool: "mcp.happy-tasks.approve_plan",
		Risk: app.RiskDangerous, Status: app.ApprovalStatusPending, Summary: "Review task plan",
		ExternalContext: &app.ExternalApprovalContext{
			Provider: "happy-team", Title: "Task one", GoalPrompt: "Implement one",
			Plan: "# Plan\n\nDo one.", PlanAvailability: app.ExternalPlanAvailable,
		},
	}
	mustSaveApproval(t, st, approval)
	found, ok := mustFindApprovalByExternalRef(t, st, app.ApprovalSourceHappyTeamPlan, "task-one")
	if !ok || found.ID != approval.ID || found.ExternalContext == nil || found.ExternalContext.Plan != approval.ExternalContext.Plan {
		t.Fatalf("external approval lookup mismatch: %#v ok=%v", found, ok)
	}
	byID, ok := mustGetApproval(t, st, approval.ID)
	if !ok || byID.Source != app.ApprovalSourceHappyTeamPlan {
		t.Fatalf("external approval id lookup mismatch: %#v ok=%v", byID, ok)
	}
	found.ExternalContext.Plan = "caller-only mutation"
	stored, _ := mustGetApproval(t, st, approval.ID)
	if stored.ExternalContext.Plan != approval.ExternalContext.Plan {
		t.Fatalf("approval lookup leaked mutable external context: %#v", stored.ExternalContext)
	}
	if _, err := st.ResolveApproval(t.Context(), approval.ID, app.ApprovalStatusApproved, "done"); err != nil {
		t.Fatal(err)
	}
	approval.ExternalContext.Plan = "stale background update"
	if _, err := st.UpdatePendingApproval(t.Context(), NewApprovalUpdate(approval, approval)); err == nil {
		t.Fatal("stale pending update reopened a resolved approval")
	}
	stored, _ = mustGetApproval(t, st, approval.ID)
	if stored.Status != app.ApprovalStatusApproved || stored.ExternalContext.Plan != "# Plan\n\nDo one." {
		t.Fatalf("resolved approval changed after stale update: %#v", stored)
	}
}

func TestMemoryStoreDocumentRecordsAreRecentAndSessionScoped(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "documents")
	older := time.Now().UTC().Add(-time.Minute)
	mustSaveDocumentRecord(t, st, app.DocumentRecord{
		ID: "doc_old", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "old.pdf", Name: "old.pdf", Format: app.DocumentFormatPDF,
		Status: app.DocumentStatusAvailable, Source: app.DocumentSourceAttachment,
		LastActivity: app.DocumentActivityAttached, LastActivityID: "m_old", LastActivityAt: older,
	})
	latest := mustSaveDocumentRecord(t, st, app.DocumentRecord{
		ID: "doc_new", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "new.docx", Name: "new.docx", Format: app.DocumentFormatDOCX,
		Status: app.DocumentStatusAvailable, Source: app.DocumentSourceToolOutput,
		LastActivity: app.DocumentActivityEdited, LastActivityID: "tc_new", LastActivityAt: older.Add(time.Minute),
	})
	records := mustListDocumentRecords(t, st, session.OwnerID, session.ID, 1)
	if len(records) != 1 || records[0].ID != latest.ID {
		t.Fatalf("document records were not returned by recent activity: %#v", records)
	}
	if got, ok := mustGetDocumentRecord(t, st, latest.ID); !ok || got.GovernedPath != "new.docx" {
		t.Fatalf("document record did not round trip: %#v ok=%v", got, ok)
	}
	if _, err := st.DeleteSession(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	if records := mustListDocumentRecords(t, st, "", session.ID, 10); len(records) != 0 {
		t.Fatalf("session deletion retained document records: %#v", records)
	}
}

func TestMemoryStoreUpdatesAndDeletesAcceptedMemory(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "Memory editor")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	candidate := mustAddMemoryCandidate(t, st, app.MemoryCandidate{
		SessionID:   session.ID,
		RunID:       run.ID,
		Kind:        "profile",
		Content:     "SparkClaw likes old memory",
		Sensitivity: "normal",
		Reason:      "test",
	})

	_, memory, err := testResolveMemoryCandidate(t, st, candidate.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if memory == nil {
		t.Fatal("accepted candidate did not create a memory")
	}

	updated, err := testUpdateMemory(t, st, memory.ID, "procedural", "SparkClaw likes edited memory")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Kind != "procedural" || updated.Content != "SparkClaw likes edited memory" || updated.SourceID != run.ID {
		t.Fatalf("memory did not update cleanly: %#v", updated)
	}
	if oldMatches := mustSearchMemories(t, st, "old memory"); len(oldMatches) != 0 {
		t.Fatalf("old memory content still searchable: %#v", oldMatches)
	}
	if newMatches := mustSearchMemories(t, st, "edited memory"); len(newMatches) != 1 || newMatches[0].ID != memory.ID {
		t.Fatalf("updated memory not searchable: %#v", newMatches)
	}

	deleted, err := testDeleteMemory(t, st, memory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ID != memory.ID {
		t.Fatalf("delete returned wrong memory: %#v", deleted)
	}
	if matches := mustSearchMemories(t, st, "edited memory"); len(matches) != 0 {
		t.Fatalf("deleted memory still searchable: %#v", matches)
	}
	if _, err := testUpdateMemory(t, st, memory.ID, "profile", "missing"); err == nil {
		t.Fatal("expected updating a deleted memory to fail")
	}

	audit := mustListAudit(t, st, session.ID)
	if !hasAuditType(audit, "memory.updated") || !hasAuditType(audit, "memory.deleted") {
		t.Fatalf("memory editor events missing from audit: %#v", audit)
	}
	events := mustEventsAfter(t, st, session.ID, "")
	if !hasEventType(events, "memory.updated") || !hasEventType(events, "memory.deleted") {
		t.Fatalf("memory editor events missing from event stream: %#v", events)
	}
}

func TestMemoryStoreUpdatesOwnerProfile(t *testing.T) {
	st := NewMemoryStore()
	initial := mustGetOwnerProfile(t, st)
	if initial.ID != app.DefaultOwnerID || initial.DisplayName == "" {
		t.Fatalf("default owner profile missing: %#v", initial)
	}

	updated := mustUpdateOwnerProfile(t, st, app.OwnerProfile{
		DisplayName: "Ada Owner",
		Email:       "ada@example.test",
		Preferences: map[string]string{"tone": "concise", "locale": "en-US"},
	})
	if updated.ID != app.DefaultOwnerID || updated.DisplayName != "Ada Owner" || updated.Email != "ada@example.test" {
		t.Fatalf("owner profile did not update: %#v", updated)
	}
	if updated.Preferences["tone"] != "concise" || updated.CreatedAt.IsZero() || updated.UpdatedAt.IsZero() {
		t.Fatalf("owner profile metadata missing: %#v", updated)
	}

	updated.Preferences["tone"] = "mutated"
	reloaded := mustGetOwnerProfile(t, st)
	if reloaded.Preferences["tone"] != "concise" {
		t.Fatalf("owner profile preferences were not cloned: %#v", reloaded)
	}
	if !hasAuditType(mustListAudit(t, st, ""), "owner_profile.updated") || !hasEventType(mustEventsAfter(t, st, "", ""), "owner_profile.updated") {
		t.Fatalf("owner profile update was not audited")
	}
}

func TestMemoryStoreManagesMultipleOwnerProfiles(t *testing.T) {
	st := NewMemoryStore()
	profile := mustSaveOwnerProfile(t, st, app.OwnerProfile{
		ID:               "wx_owner",
		Source:           "weixin",
		ExternalRef:      "bind:user",
		WorkspaceRoot:    "/tmp/sparkclaw/users/wx_owner",
		DefaultChannel:   "weixin",
		DefaultBindingID: "bind",
		DisplayName:      "微信用户",
		Preferences:      map[string]string{"locale": "zh-CN"},
	})
	if profile.ID != "wx_owner" || profile.Source != "weixin" || profile.WorkspaceRoot == "" {
		t.Fatalf("profile did not save extended fields: %#v", profile)
	}
	found, ok := mustFindOwnerProfileByExternalRef(t, st, "weixin", "bind:user")
	if !ok || found.ID != profile.ID {
		t.Fatalf("profile external ref lookup failed: %#v ok=%v", found, ok)
	}
	if _, ok := mustGetOwnerProfileByID(t, st, "missing"); ok {
		t.Fatalf("missing profile should not be found")
	}
	profiles := mustListOwnerProfiles(t, st)
	if len(profiles) != 2 {
		t.Fatalf("expected default and weixin profiles, got %#v", profiles)
	}
}

func TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite(t *testing.T) {
	st := NewMemoryStore()
	record := mustSaveBrowserAuthRecord(t, st, app.BrowserAuthRecord{
		OwnerID:          "owner-a",
		BrowserProfileID: "work",
		SiteOrigin:       "https://Example.COM/",
		AccountHint:      "Ada@Example.COM",
		CredentialRef:    "browser-auth:work",
		CookieJarRef:     "browser-auth:work",
	})
	if record.ID == "" || record.Status != app.BrowserAuthStatusActive || record.SiteOrigin != "https://example.com" || record.AccountHint != "ada@example.com" {
		t.Fatalf("browser auth record was not normalized: %#v", record)
	}
	if found, ok := mustFindBrowserAuthRecord(t, st, "owner-a", "work", "https://example.com", "", "ada@example.com"); !ok || found.ID != record.ID {
		t.Fatalf("expected scoped browser auth record, got %#v ok=%v", found, ok)
	}
	for _, tc := range []struct {
		name             string
		ownerID          string
		browserProfileID string
		accountHint      string
	}{
		{name: "other owner", ownerID: "owner-b", browserProfileID: "work", accountHint: "ada@example.com"},
		{name: "other profile", ownerID: "owner-a", browserProfileID: "personal", accountHint: "ada@example.com"},
		{name: "other account", ownerID: "owner-a", browserProfileID: "work", accountHint: "other@example.com"},
	} {
		if found, ok := mustFindBrowserAuthRecord(t, st, tc.ownerID, tc.browserProfileID, "https://example.com", "", tc.accountHint); ok {
			t.Fatalf("%s should not match browser auth record: %#v", tc.name, found)
		}
	}
	revoked, err := st.RevokeBrowserAuthRecord(t.Context(), record.ID, "owner requested")
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Status != app.BrowserAuthStatusRevoked || revoked.RevokedAt == nil {
		t.Fatalf("record was not revoked: %#v", revoked)
	}
	if _, ok := mustFindBrowserAuthRecord(t, st, "owner-a", "work", "https://example.com", "", "ada@example.com"); ok {
		t.Fatalf("revoked browser auth record should not be active")
	}
	if !hasAuditType(mustListAudit(t, st, ""), "browser_auth.record_saved") || !hasAuditType(mustListAudit(t, st, ""), "browser_auth.record_revoked") {
		t.Fatalf("browser auth audit events missing: %#v", mustListAudit(t, st, ""))
	}
}

func TestMemoryStoreTracksActiveBrowserLoginBlock(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "login block")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	block := mustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{
		SessionID:          session.ID,
		RunID:              run.ID,
		OriginalGoal:       "Read https://example.com/protected",
		ResumeTool:         "browser.read",
		ResumeArgs:         map[string]any{"url": "https://example.com/protected"},
		LoginHandoffURL:    "https://example.com/protected",
		LoginHandoffPageID: "page-1",
		LastVisiblePageID:  "page-2",
		OwnerID:            "owner-a",
		BrowserProfileID:   "work",
		SiteOrigin:         "https://Example.COM/",
	})
	if block.ID == "" || block.Status != app.BrowserLoginBlockStatusWaiting || block.SiteOrigin != "https://example.com" || block.LastVisiblePageID != "page-2" {
		t.Fatalf("browser login block was not normalized: %#v", block)
	}
	if found, ok := mustFindActiveBrowserLoginBlock(t, st, session.ID); !ok || found.ID != block.ID {
		t.Fatalf("expected active browser login block, got %#v ok=%v", found, ok)
	}
	now := time.Now().UTC()
	block.Status = app.BrowserLoginBlockStatusResolved
	block.ResolvedAt = &now
	mustSaveBrowserLoginBlock(t, st, block)
	if _, ok := mustFindActiveBrowserLoginBlock(t, st, session.ID); ok {
		t.Fatalf("resolved browser login block should not remain active")
	}
	if blocks := mustListBrowserLoginBlocks(t, st, session.ID, app.BrowserLoginBlockStatusResolved); len(blocks) != 1 || blocks[0].ID != block.ID {
		t.Fatalf("resolved browser login block should be listed: %#v", blocks)
	}
}

func TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "browser handoff CAS")
	run := app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked",
		ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC(),
	}
	testSaveRun(st, run)
	leaseUntil := time.Now().UTC().Add(time.Minute)
	block := mustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{
		SessionID: session.ID, RunID: run.ID,
		WorkflowID: app.WorkflowBrowserAutomation, WorkflowRevision: app.BrowserWorkflowRevision2,
		WorkflowNodeID: "browser_result", SessionGeneration: 7,
		Status:            app.BrowserHandoffStatusValidatingVisible,
		TransitionOwnerID: "runtime-a", TransitionLeaseUntil: &leaseUntil,
		Target: app.BrowserTargetDescriptor{
			TargetKind:    app.BrowserTargetRegisteredDestination,
			DestinationID: "qq_mail", CanonicalURL: "https://wx.mail.qq.com/home/index#/list/1/1",
			RedactedURL: "https://wx.mail.qq.com/home/index#/list/1/1",
		},
		VisibleEvidence: &app.BrowserResultEvidence{
			ID: "visible-evidence", SchemaVersion: app.BrowserHandoffSchemaVersion,
			VisiblePageID: "page-visible", VisibleSnapshotID: "snapshot-visible",
		},
	})
	if block.Version != 1 || block.SchemaVersion != app.BrowserHandoffSchemaVersion {
		t.Fatalf("new handoff version/schema = %d/%d", block.Version, block.SchemaVersion)
	}
	update := block
	update.Status = app.BrowserHandoffStatusTransferring
	updated, err := st.UpdateBrowserLoginBlock(t.Context(), update, block.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != block.Version+1 || updated.Target.DestinationID != "qq_mail" ||
		updated.VisibleEvidence == nil || updated.VisibleEvidence.VisibleSnapshotID != "snapshot-visible" ||
		updated.TransitionOwnerID != "runtime-a" || updated.TransitionLeaseUntil == nil {
		t.Fatalf("revision-two handoff fields did not survive CAS: %#v", updated)
	}
	stale := block
	stale.LastError = "stale overwrite"
	if _, err := st.UpdateBrowserLoginBlock(t.Context(), stale, block.Version); !errors.Is(err, ErrBrowserHandoffConflict) {
		t.Fatalf("stale browser handoff update error = %v", err)
	}
	current, _ := mustGetBrowserLoginBlock(t, st, block.ID)
	if current.Version != updated.Version || current.LastError == "stale overwrite" {
		t.Fatalf("stale browser handoff update changed current state: %#v", current)
	}
}

func TestMemoryStoreFindActiveBrowserLoginBlockMatchesSharedActivePredicate(t *testing.T) {
	statuses := append(app.BrowserHandoffActiveStatuses(),
		app.BrowserHandoffStatusResolved,
		app.BrowserHandoffStatusCanceled,
		app.BrowserHandoffStatusFailed,
	)
	for _, status := range statuses {
		st := NewMemoryStore()
		session := mustCreateSession(t, st, "active predicate "+status)
		run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
		testSaveRun(st, run)
		block := mustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{
			SessionID: session.ID, RunID: run.ID, Status: status, SiteOrigin: "https://example.com",
		})
		found, ok := mustFindActiveBrowserLoginBlock(t, st, session.ID)
		if want := app.BrowserHandoffStatusActive(status); ok != want {
			t.Fatalf("status %q: FindActiveBrowserLoginBlock ok=%v, shared predicate active=%v", status, ok, want)
		} else if ok && found.ID != block.ID {
			t.Fatalf("status %q: FindActiveBrowserLoginBlock returned %q, want %q", status, found.ID, block.ID)
		}
	}
}

func TestMemoryStoreBrowserLoginBlockReadsDoNotMutateStoredState(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "read stability")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	saved := mustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{
		SessionID: session.ID, RunID: run.ID, SiteOrigin: "https://example.com",
	})
	time.Sleep(2 * time.Millisecond)
	for i := 0; i < 3; i++ {
		listed := mustListBrowserLoginBlocks(t, st, session.ID, "")
		if len(listed) != 1 || listed[0].Version != saved.Version ||
			listed[0].SchemaVersion != saved.SchemaVersion || !listed[0].UpdatedAt.Equal(saved.UpdatedAt) {
			t.Fatalf("list read did not return stored values: %#v want %#v", listed, saved)
		}
		found, ok := mustFindActiveBrowserLoginBlock(t, st, session.ID)
		if !ok || found.Version != saved.Version || !found.UpdatedAt.Equal(saved.UpdatedAt) {
			t.Fatalf("find-active read did not return stored values: %#v ok=%v want %#v", found, ok, saved)
		}
	}
	found, _ := mustFindActiveBrowserLoginBlock(t, st, session.ID)
	update := found
	update.Status = app.BrowserHandoffStatusValidatingVisible
	if _, err := st.UpdateBrowserLoginBlock(t.Context(), update, found.Version); err != nil {
		t.Fatalf("version observed on a read path failed CAS: %v", err)
	}
}

func TestMemoryStoreFindActiveBrowserLoginBlockPicksNewestStoredUpdate(t *testing.T) {
	base := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	st := NewMemoryStore()
	st.loadSnapshot(Snapshot{
		BrowserLoginBlocks: map[string]app.BrowserLoginBlock{
			"blogin-old": {
				ID: "blogin-old", SessionID: "s1", RunID: "r1", Version: 2,
				SchemaVersion: app.BrowserHandoffSchemaVersion,
				Status:        app.BrowserHandoffStatusWaitingOwner,
				CreatedAt:     base, UpdatedAt: base.Add(time.Minute),
			},
			"blogin-new": {
				ID: "blogin-new", SessionID: "s1", RunID: "r2", Version: 4,
				SchemaVersion: app.BrowserHandoffSchemaVersion,
				Status:        app.BrowserHandoffStatusValidatingVisible,
				CreatedAt:     base, UpdatedAt: base.Add(2 * time.Minute),
			},
			"blogin-resolved": {
				ID: "blogin-resolved", SessionID: "s1", RunID: "r3", Version: 9,
				SchemaVersion: app.BrowserHandoffSchemaVersion,
				Status:        app.BrowserHandoffStatusResolved,
				CreatedAt:     base, UpdatedAt: base.Add(3 * time.Minute),
			},
			"blogin-tie-a": {
				ID: "blogin-tie-a", SessionID: "s2", RunID: "r4", Version: 1,
				SchemaVersion: app.BrowserHandoffSchemaVersion,
				Status:        app.BrowserHandoffStatusWaitingOwner,
				CreatedAt:     base, UpdatedAt: base,
			},
			"blogin-tie-b": {
				ID: "blogin-tie-b", SessionID: "s2", RunID: "r5", Version: 1,
				SchemaVersion: app.BrowserHandoffSchemaVersion,
				Status:        app.BrowserHandoffStatusWaitingOwner,
				CreatedAt:     base, UpdatedAt: base,
			},
		},
	})
	for i := 0; i < 5; i++ {
		active, ok := mustFindActiveBrowserLoginBlock(t, st, "s1")
		if !ok || active.ID != "blogin-new" || active.Version != 4 ||
			!active.UpdatedAt.Equal(base.Add(2*time.Minute)) {
			t.Fatalf("find-active did not pick newest stored active block: %#v ok=%v", active, ok)
		}
		tie, ok := mustFindActiveBrowserLoginBlock(t, st, "s2")
		if !ok || tie.ID != "blogin-tie-b" {
			t.Fatalf("find-active tie-break is not deterministic: %#v ok=%v", tie, ok)
		}
	}
}

func TestMemoryStoreBrowserLoginBlockTrimsIDOnWrite(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "trim id")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	saved := mustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{
		ID: "  blogin-trim  ", SessionID: session.ID, RunID: run.ID, SiteOrigin: "https://example.com",
	})
	if saved.ID != "blogin-trim" {
		t.Fatalf("save did not trim block ID: %q", saved.ID)
	}
	if _, ok := mustGetBrowserLoginBlock(t, st, "blogin-trim"); !ok {
		t.Fatal("block is not stored under the trimmed ID")
	}
	resaved := mustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{
		ID: " blogin-trim ", SessionID: session.ID, RunID: run.ID,
		Status: app.BrowserHandoffStatusValidatingVisible, SiteOrigin: "https://example.com",
	})
	if resaved.Version != saved.Version+1 {
		t.Fatalf("padded-ID save forked a new record instead of updating: %#v", resaved)
	}
	if blocks := mustListBrowserLoginBlocks(t, st, session.ID, ""); len(blocks) != 1 {
		t.Fatalf("padded-ID writes produced duplicate records: %#v", blocks)
	}
	update := resaved
	update.ID = "  blogin-trim "
	update.Status = app.BrowserHandoffStatusTransferring
	updated, err := st.UpdateBrowserLoginBlock(t.Context(), update, resaved.Version)
	if err != nil {
		t.Fatal(err)
	}
	current, ok := mustGetBrowserLoginBlock(t, st, "blogin-trim")
	if !ok || updated.ID != "blogin-trim" || current.Version != updated.Version ||
		current.Status != app.BrowserHandoffStatusTransferring {
		t.Fatalf("padded-ID CAS update wrote back under the wrong key: %#v ok=%v", current, ok)
	}
}

func TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "delete blocked browser session")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	block := mustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{
		SessionID:  session.ID,
		RunID:      run.ID,
		SiteOrigin: "https://example.com",
	})

	if _, err := st.DeleteSession(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustGetBrowserLoginBlock(t, st, block.ID); ok {
		t.Fatal("session deletion retained browser login block")
	}
	if _, ok := testGetRun(st, run.ID); ok {
		t.Fatal("session deletion retained agent run")
	}
	if _, ok := mustGetSession(t, st, session.ID); ok {
		t.Fatal("session deletion retained session")
	}
}

func TestMemoryStoreSavesRunFeedback(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "Feedback")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	message := mustAddMessage(t, st, app.Message{SessionID: session.ID, RunID: run.ID, Role: "assistant", Content: "first answer"})

	feedback := testSaveRunFeedback(st, app.RunFeedback{
		SessionID:  session.ID,
		RunID:      run.ID,
		MessageID:  message.ID,
		Rating:     "down",
		Correction: "Use the cited file.",
	})

	if feedback.ID == "" || feedback.Rating != "down" || feedback.Correction == "" {
		t.Fatalf("feedback did not save: %#v", feedback)
	}
	updated := testSaveRunFeedback(st, app.RunFeedback{
		SessionID: session.ID,
		RunID:     run.ID,
		MessageID: message.ID,
		Rating:    "up",
		Note:      "looks better",
	})

	if updated.ID != feedback.ID || updated.CreatedAt != feedback.CreatedAt {
		t.Fatalf("feedback should update existing message feedback: %#v vs %#v", updated, feedback)
	}
	items := testListRunFeedback(st, run.ID)
	if len(items) != 1 || items[0].Rating != "up" || items[0].Note != "looks better" {
		t.Fatalf("feedback did not list cleanly: %#v", items)
	}
	if !hasAuditType(mustListAudit(t, st, session.ID), "run_feedback.saved") || !hasEventType(mustEventsAfter(t, st, session.ID, ""), "run_feedback.saved") {
		t.Fatalf("feedback was not audited")
	}
}

func TestMemoryStorePrunesExpiredMemories(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "Retention")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	old := app.Memory{
		ID:        app.NewID("mem"),
		Kind:      "profile",
		Content:   "old retention memory",
		SourceID:  run.ID,
		CreatedAt: time.Now().UTC().AddDate(0, 0, -30),
	}
	fresh := app.Memory{
		ID:        app.NewID("mem"),
		Kind:      "profile",
		Content:   "fresh retention memory",
		SourceID:  run.ID,
		CreatedAt: time.Now().UTC(),
	}
	st.memories[old.ID] = old
	st.memories[fresh.ID] = fresh

	pruned := mustPruneMemories(t, st, time.Now().UTC().AddDate(0, 0, -7))
	if len(pruned) != 1 || pruned[0].ID != old.ID {
		t.Fatalf("unexpected pruned memories: %#v", pruned)
	}
	if matches := mustSearchMemories(t, st, "retention memory"); len(matches) != 1 || matches[0].ID != fresh.ID {
		t.Fatalf("retention pruning left wrong memories: %#v", matches)
	}
	if !hasAuditType(mustListAudit(t, st, session.ID), "memory.pruned") || !hasEventType(mustEventsAfter(t, st, session.ID, ""), "memory.pruned") {
		t.Fatalf("memory retention pruning was not audited")
	}
}

func TestMemoryStoreListsArtifactObjectsNewestFirst(t *testing.T) {
	st := NewMemoryStore()
	older := app.ArtifactObject{
		ID:          "obj_old",
		Kind:        "trace",
		RunID:       "run_old",
		Backend:     "filesystem",
		Bucket:      "sparkclaw",
		Key:         "traces/run_old.json",
		URI:         "artifact://sparkclaw/traces/run_old.json",
		ContentType: "application/json",
		Bytes:       10,
		CreatedAt:   time.Now().UTC().Add(-time.Minute),
	}
	newer := older
	newer.ID = "obj_new"
	newer.RunID = "run_new"
	newer.Key = "traces/run_new.json"
	newer.URI = "artifact://sparkclaw/traces/run_new.json"
	newer.CreatedAt = time.Now().UTC()
	mustSaveArtifactObject(t, st, older)
	mustSaveArtifactObject(t, st, newer)

	objects := mustListArtifactObjects(t, st, 1)
	if len(objects) != 1 || objects[0].ID != "obj_new" {
		t.Fatalf("artifact objects did not list newest first: %#v", objects)
	}
	if !hasAuditType(mustListAudit(t, st, ""), "artifact.saved") || !hasEventType(mustEventsAfter(t, st, "", ""), "artifact.saved") {
		t.Fatalf("artifact save was not audited")
	}
}

func TestMemoryStoreFindsArtifactObjectByURI(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "artifact lookup")
	uri := "artifact://sparkclaw/observations/run_a/tc_1.json"
	older := app.ArtifactObject{
		ID:          "obj_a",
		Kind:        "tool_observation",
		RunID:       "run_a",
		SessionID:   session.ID,
		Backend:     "filesystem",
		Bucket:      "sparkclaw",
		Key:         "observations/run_a/tc_1.json",
		URI:         uri,
		ContentType: "application/json",
		Bytes:       10,
		CreatedAt:   time.Now().UTC().Add(-time.Minute),
	}
	newer := older
	newer.ID = "obj_b"
	newer.RunID = "run_b"
	newer.CreatedAt = time.Now().UTC()
	mustSaveArtifactObject(t, st, older)
	mustSaveArtifactObject(t, st, newer)

	if object, ok := mustFindArtifactObjectByURI(t, st, uri, session.ID, "run_a"); !ok || object.ID != "obj_a" {
		t.Fatalf("run-scoped artifact lookup failed: %#v ok=%v", object, ok)
	}
	if object, ok := mustFindArtifactObjectByURI(t, st, uri, session.ID, ""); !ok || object.ID != "obj_b" {
		t.Fatalf("session-scoped lookup did not pick the newest object: %#v ok=%v", object, ok)
	}
	if _, ok := mustFindArtifactObjectByURI(t, st, uri, "s_other", ""); ok {
		t.Fatal("artifact lookup crossed the session boundary")
	}
	if _, ok := mustFindArtifactObjectByURI(t, st, "artifact://sparkclaw/missing.json", session.ID, ""); ok {
		t.Fatal("missing URI lookup returned an object")
	}

	moved := newer
	moved.URI = "artifact://sparkclaw/observations/run_b/tc_1.json"
	mustSaveArtifactObject(t, st, moved)
	if _, ok := mustFindArtifactObjectByURI(t, st, uri, session.ID, "run_b"); ok {
		t.Fatal("stale URI index entry survived a re-save under a new URI")
	}
	if object, ok := mustFindArtifactObjectByURI(t, st, moved.URI, session.ID, "run_b"); !ok || object.ID != "obj_b" {
		t.Fatalf("re-saved artifact lookup failed: %#v ok=%v", object, ok)
	}

	if _, err := st.DeleteSession(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustFindArtifactObjectByURI(t, st, uri, session.ID, ""); ok {
		t.Fatal("artifact lookup survived session deletion")
	}
	if _, ok := mustFindArtifactObjectByURI(t, st, moved.URI, "", ""); ok {
		t.Fatal("URI index entry survived session deletion")
	}
}

func hasAuditType(events []app.AuditEvent, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func hasEventType(events []app.Event, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}
