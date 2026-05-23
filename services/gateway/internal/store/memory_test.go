package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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
