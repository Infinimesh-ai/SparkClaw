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
	stored := st.SaveReminder(app.Reminder{
		ID: "reminder-cas", SessionID: "session-cas", Text: "before", DueTime: now.Add(time.Hour),
		Status: "pending", CreatedAt: now, UpdatedAt: now,
	})
	updated := stored
	updated.Text = "after"
	updated.UpdatedAt = stored.UpdatedAt.Add(time.Nanosecond)
	if _, err := st.UpdatePendingReminder(updated, stored.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	updated.Text = "stale overwrite"
	if _, err := st.UpdatePendingReminder(updated, stored.UpdatedAt); !errors.Is(err, ErrReminderConflict) {
		t.Fatalf("expected stale compare-and-swap conflict, got %v", err)
	}
	current, _ := st.GetReminder(stored.ID)
	if current.Text != "after" {
		t.Fatalf("stale update changed reminder: %#v", current)
	}
}

func TestMemoryStoreDocumentRecordsAreRecentAndSessionScoped(t *testing.T) {
	st := NewMemoryStore()
	session := st.CreateSession("documents")
	older := time.Now().UTC().Add(-time.Minute)
	st.SaveDocumentRecord(app.DocumentRecord{
		ID: "doc_old", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "old.pdf", Name: "old.pdf", Format: app.DocumentFormatPDF,
		Status: app.DocumentStatusAvailable, Source: app.DocumentSourceAttachment,
		LastActivity: app.DocumentActivityAttached, LastActivityID: "m_old", LastActivityAt: older,
	})
	latest := st.SaveDocumentRecord(app.DocumentRecord{
		ID: "doc_new", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "new.docx", Name: "new.docx", Format: app.DocumentFormatDOCX,
		Status: app.DocumentStatusAvailable, Source: app.DocumentSourceToolOutput,
		LastActivity: app.DocumentActivityEdited, LastActivityID: "tc_new", LastActivityAt: older.Add(time.Minute),
	})
	records := st.ListDocumentRecords(session.OwnerID, session.ID, 1)
	if len(records) != 1 || records[0].ID != latest.ID {
		t.Fatalf("document records were not returned by recent activity: %#v", records)
	}
	if got, ok := st.GetDocumentRecord(latest.ID); !ok || got.GovernedPath != "new.docx" {
		t.Fatalf("document record did not round trip: %#v ok=%v", got, ok)
	}
	if _, err := st.DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if records := st.ListDocumentRecords("", session.ID, 10); len(records) != 0 {
		t.Fatalf("session deletion retained document records: %#v", records)
	}
}

func TestMemoryStoreUpdatesAndDeletesAcceptedMemory(t *testing.T) {
	st := NewMemoryStore()
	session := st.CreateSession("Memory editor")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
	candidate := st.AddMemoryCandidate(app.MemoryCandidate{
		SessionID:   session.ID,
		RunID:       run.ID,
		Kind:        "profile",
		Content:     "SparkClaw likes old memory",
		Sensitivity: "normal",
		Reason:      "test",
	})
	_, memory, err := st.ResolveMemoryCandidate(candidate.ID, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if memory == nil {
		t.Fatal("accepted candidate did not create a memory")
	}

	updated, err := st.UpdateMemory(memory.ID, "procedural", "SparkClaw likes edited memory")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Kind != "procedural" || updated.Content != "SparkClaw likes edited memory" || updated.SourceID != run.ID {
		t.Fatalf("memory did not update cleanly: %#v", updated)
	}
	if oldMatches := st.SearchMemories("old memory"); len(oldMatches) != 0 {
		t.Fatalf("old memory content still searchable: %#v", oldMatches)
	}
	if newMatches := st.SearchMemories("edited memory"); len(newMatches) != 1 || newMatches[0].ID != memory.ID {
		t.Fatalf("updated memory not searchable: %#v", newMatches)
	}

	deleted, err := st.DeleteMemory(memory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ID != memory.ID {
		t.Fatalf("delete returned wrong memory: %#v", deleted)
	}
	if matches := st.SearchMemories("edited memory"); len(matches) != 0 {
		t.Fatalf("deleted memory still searchable: %#v", matches)
	}
	if _, err := st.UpdateMemory(memory.ID, "profile", "missing"); err == nil {
		t.Fatal("expected updating a deleted memory to fail")
	}

	audit := st.ListAudit(session.ID)
	if !hasAuditType(audit, "memory.updated") || !hasAuditType(audit, "memory.deleted") {
		t.Fatalf("memory editor events missing from audit: %#v", audit)
	}
	events := st.EventsAfter(session.ID, "")
	if !hasEventType(events, "memory.updated") || !hasEventType(events, "memory.deleted") {
		t.Fatalf("memory editor events missing from event stream: %#v", events)
	}
}

func TestMemoryStoreUpdatesOwnerProfile(t *testing.T) {
	st := NewMemoryStore()
	initial := st.GetOwnerProfile()
	if initial.ID != app.DefaultOwnerID || initial.DisplayName == "" {
		t.Fatalf("default owner profile missing: %#v", initial)
	}

	updated := st.UpdateOwnerProfile(app.OwnerProfile{
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
	reloaded := st.GetOwnerProfile()
	if reloaded.Preferences["tone"] != "concise" {
		t.Fatalf("owner profile preferences were not cloned: %#v", reloaded)
	}
	if !hasAuditType(st.ListAudit(""), "owner_profile.updated") || !hasEventType(st.EventsAfter("", ""), "owner_profile.updated") {
		t.Fatalf("owner profile update was not audited")
	}
}

func TestMemoryStoreManagesMultipleOwnerProfiles(t *testing.T) {
	st := NewMemoryStore()
	profile := st.SaveOwnerProfile(app.OwnerProfile{
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
	found, ok := st.FindOwnerProfileByExternalRef("weixin", "bind:user")
	if !ok || found.ID != profile.ID {
		t.Fatalf("profile external ref lookup failed: %#v ok=%v", found, ok)
	}
	if _, ok := st.GetOwnerProfileByID("missing"); ok {
		t.Fatalf("missing profile should not be found")
	}
	profiles := st.ListOwnerProfiles()
	if len(profiles) != 2 {
		t.Fatalf("expected default and weixin profiles, got %#v", profiles)
	}
}

func TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite(t *testing.T) {
	st := NewMemoryStore()
	record := st.SaveBrowserAuthRecord(app.BrowserAuthRecord{
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
	if found, ok := st.FindBrowserAuthRecord("owner-a", "work", "https://example.com", "", "ada@example.com"); !ok || found.ID != record.ID {
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
		if found, ok := st.FindBrowserAuthRecord(tc.ownerID, tc.browserProfileID, "https://example.com", "", tc.accountHint); ok {
			t.Fatalf("%s should not match browser auth record: %#v", tc.name, found)
		}
	}
	revoked, err := st.RevokeBrowserAuthRecord(record.ID, "owner requested")
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Status != app.BrowserAuthStatusRevoked || revoked.RevokedAt == nil {
		t.Fatalf("record was not revoked: %#v", revoked)
	}
	if _, ok := st.FindBrowserAuthRecord("owner-a", "work", "https://example.com", "", "ada@example.com"); ok {
		t.Fatalf("revoked browser auth record should not be active")
	}
	if !hasAuditType(st.ListAudit(""), "browser_auth.record_saved") || !hasAuditType(st.ListAudit(""), "browser_auth.record_revoked") {
		t.Fatalf("browser auth audit events missing: %#v", st.ListAudit(""))
	}
}

func TestMemoryStoreTracksActiveBrowserLoginBlock(t *testing.T) {
	st := NewMemoryStore()
	session := st.CreateSession("login block")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "browser_login_blocked", ModelLane: "deep", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
	block := st.SaveBrowserLoginBlock(app.BrowserLoginBlock{
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
	if found, ok := st.FindActiveBrowserLoginBlock(session.ID); !ok || found.ID != block.ID {
		t.Fatalf("expected active browser login block, got %#v ok=%v", found, ok)
	}
	now := time.Now().UTC()
	block.Status = app.BrowserLoginBlockStatusResolved
	block.ResolvedAt = &now
	st.SaveBrowserLoginBlock(block)
	if _, ok := st.FindActiveBrowserLoginBlock(session.ID); ok {
		t.Fatalf("resolved browser login block should not remain active")
	}
	if blocks := st.ListBrowserLoginBlocks(session.ID, app.BrowserLoginBlockStatusResolved); len(blocks) != 1 || blocks[0].ID != block.ID {
		t.Fatalf("resolved browser login block should be listed: %#v", blocks)
	}
}

func TestMemoryStoreSavesRunFeedback(t *testing.T) {
	st := NewMemoryStore()
	session := st.CreateSession("Feedback")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
	message := st.AddMessage(app.Message{SessionID: session.ID, RunID: run.ID, Role: "assistant", Content: "first answer"})

	feedback := st.SaveRunFeedback(app.RunFeedback{
		SessionID:  session.ID,
		RunID:      run.ID,
		MessageID:  message.ID,
		Rating:     "down",
		Correction: "Use the cited file.",
	})
	if feedback.ID == "" || feedback.Rating != "down" || feedback.Correction == "" {
		t.Fatalf("feedback did not save: %#v", feedback)
	}
	updated := st.SaveRunFeedback(app.RunFeedback{
		SessionID: session.ID,
		RunID:     run.ID,
		MessageID: message.ID,
		Rating:    "up",
		Note:      "looks better",
	})
	if updated.ID != feedback.ID || updated.CreatedAt != feedback.CreatedAt {
		t.Fatalf("feedback should update existing message feedback: %#v vs %#v", updated, feedback)
	}
	items := st.ListRunFeedback(run.ID)
	if len(items) != 1 || items[0].Rating != "up" || items[0].Note != "looks better" {
		t.Fatalf("feedback did not list cleanly: %#v", items)
	}
	if !hasAuditType(st.ListAudit(session.ID), "run_feedback.saved") || !hasEventType(st.EventsAfter(session.ID, ""), "run_feedback.saved") {
		t.Fatalf("feedback was not audited")
	}
}

func TestMemoryStorePrunesExpiredMemories(t *testing.T) {
	st := NewMemoryStore()
	session := st.CreateSession("Retention")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
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

	pruned := st.PruneMemories(time.Now().UTC().AddDate(0, 0, -7))
	if len(pruned) != 1 || pruned[0].ID != old.ID {
		t.Fatalf("unexpected pruned memories: %#v", pruned)
	}
	if matches := st.SearchMemories("retention memory"); len(matches) != 1 || matches[0].ID != fresh.ID {
		t.Fatalf("retention pruning left wrong memories: %#v", matches)
	}
	if !hasAuditType(st.ListAudit(session.ID), "memory.pruned") || !hasEventType(st.EventsAfter(session.ID, ""), "memory.pruned") {
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
	st.SaveArtifactObject(older)
	st.SaveArtifactObject(newer)

	objects := st.ListArtifactObjects(1)
	if len(objects) != 1 || objects[0].ID != "obj_new" {
		t.Fatalf("artifact objects did not list newest first: %#v", objects)
	}
	if !hasAuditType(st.ListAudit(""), "artifact.saved") || !hasEventType(st.EventsAfter("", ""), "artifact.saved") {
		t.Fatalf("artifact save was not audited")
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
