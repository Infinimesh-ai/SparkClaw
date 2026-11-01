package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
)

func TestWorkbenchSignedFileChoiceSurvivesRestartAndPublishesOnce(t *testing.T) {
	root := t.TempDir()
	s, st := admissionFileServer(t, root)
	session, err := st.CreateSessionWithScope(t.Context(), "signed selection", app.DefaultOwnerID, root, "webchat", false)
	if err != nil {
		t.Fatal(err)
	}
	var attachments []app.MessageAttachment
	for _, id := range []string{"file_a", "file_b"} {
		rel := "conversation-files/" + id + "/report.md"
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(id), 0400); err != nil {
			t.Fatal(err)
		}
		attachments = append(attachments, app.MessageAttachment{ArtifactID: id, Name: "report.md", RelPath: rel, Source: "conversation_result", SHA256: clarification.TextDigest(id), Bytes: len(id), ContentType: "text/markdown"})
	}
	_, err = st.AddMessage(t.Context(), app.Message{ID: "source_files", SessionID: session.ID, Role: "assistant", Content: "两个文件。", CreatedAt: time.Now().Add(-time.Minute), Attachments: attachments})
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/sessions/" + session.ID + "/messages"
	firstID := testWorkbenchRequestID()
	first := admissionHTTP(s, "POST", route, admissionToken, `{"request_id":"`+firstID+`","content":"把刚才的文件发给我"}`)
	var question agent.Result
	if first.Code != 201 || json.Unmarshal(first.Body.Bytes(), &question) != nil || question.Clarification == nil || len(question.Clarification.Options) != 2 {
		t.Fatalf("question=%d %s", first.Code, first.Body.String())
	}
	if question.Clarification.SchemaVersion != 2 || question.Message.Clarification == nil || question.Clarification.RequestID != firstID || question.Clarification.PromptSHA256 != clarification.TextDigest(question.Message.Content) {
		t.Fatalf("question binding missing: %+v", question.Clarification)
	}
	if _, err := os.Stat(filepath.Join(root, "report.md")); !os.IsNotExist(err) {
		t.Fatal("clarification performed publication")
	}
	s.executions.Close()
	restarted, _ := admissionFileServer(t, root)
	secondID := testWorkbenchRequestID()
	body := `{"request_id":"` + secondID + `","content":"2"}`
	second := admissionHTTP(restarted, "POST", route, admissionToken, body)
	var result agent.Result
	if second.Code != 201 || json.Unmarshal(second.Body.Bytes(), &result) != nil || result.Run.State != "completed" || len(result.Message.Attachments) != 1 {
		t.Fatalf("selection after %s=%d %s", question.Run.State, second.Code, second.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, result.Message.Attachments[0].RelPath))
	if err != nil || string(got) != "file_b" {
		t.Fatalf("selected bytes=%q %v", got, err)
	}
	replay := readAdmissionStatus(t, admissionHTTP(restarted, "POST", route, admissionToken, body))
	if replay.Result == nil || replay.Result.Message.ID != result.Message.ID {
		t.Fatalf("selection replay changed result: %+v", replay)
	}
	for _, id := range []string{"file_a", "file_b"} {
		raw, err := os.ReadFile(filepath.Join(root, "conversation-files", id, "report.md"))
		if err != nil || string(raw) != id {
			t.Fatalf("original mutated: %q %v", raw, err)
		}
	}
	control, err := os.ReadFile(filepath.Join(restarted.executions.Root(), "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct {
		Claims map[string]struct {
			Successor string `json:"successor_request_id"`
		} `json:"clarification_claims"`
	}
	if err := json.Unmarshal(control, &ledger); err != nil || len(ledger.Claims) != 1 {
		t.Fatalf("claim not persisted: %d %v", len(ledger.Claims), err)
	}
	for _, claim := range ledger.Claims {
		if claim.Successor != secondID {
			t.Fatalf("claim successor=%s", claim.Successor)
		}
	}
}
