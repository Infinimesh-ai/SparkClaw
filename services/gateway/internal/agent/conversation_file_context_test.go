package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestConversationFilesSelectRecentResultAndNamedOlderHandles(t *testing.T) {
	first := app.Message{ID: "one", Role: "user", CreatedAt: time.Unix(1, 0), Attachments: []app.MessageAttachment{{Name: "report.md", RelPath: "conversation-files/a/report.md", Source: "conversation_attachment"}}}
	second := app.Message{ID: "two", Role: "assistant", CreatedAt: time.Unix(2, 0), Attachments: []app.MessageAttachment{{Name: "result.md", RelPath: "conversation-files/b/result.md", Source: "conversation_result"}}}
	recent := recentDocumentMessageReferences([]app.Message{first, second})
	if len(recent) != 1 || recent[0].Name != "result.md" || recent[0].Source != "conversation_result" {
		t.Fatalf("recent result %#v", recent)
	}
	r := Runtime{store: store.NewMemoryStore()}
	history := invocationHistory{MessageCandidates: []app.Message{second, first}, Selected: agentContextSnapshot{Messages: []app.Message{second}}}
	named, err := r.resolveDocumentContextWithHistory(context.Background(), "session", "修改 report.md", nil, history)
	if err != nil || len(named.References) != 1 || named.References[0].Ref != first.Attachments[0].RelPath {
		t.Fatalf("older named: %#v %v", named, err)
	}
	duplicate := first
	duplicate.ID = "three"
	duplicate.Attachments = []app.MessageAttachment{{Name: "report.md", RelPath: "conversation-files/c/report.md", Source: "conversation_attachment"}}
	history.MessageCandidates = append(history.MessageCandidates, duplicate)
	named, err = r.resolveDocumentContextWithHistory(context.Background(), "session", "修改 report.md", nil, history)
	if err != nil || len(named.References) != 2 {
		t.Fatalf("duplicate names: %#v %v", named, err)
	}
}

func TestConversationFileUnavailableBoundaryDoesNotSelectOlderFile(t *testing.T) {
	older := app.Message{ID: "old", Role: "user", CreatedAt: time.Unix(1, 0), Attachments: []app.MessageAttachment{{ArtifactID: "old-file", Name: "old.md", RelPath: "conversation-files/old/old.md", Source: "conversation_attachment"}}}
	for _, role := range []string{"user", "assistant"} {
		for _, partial := range []bool{false, true} {
			latest := app.Message{ID: "latest", Role: role, CreatedAt: time.Unix(2, 0), FilesUnavailable: true}
			if partial {
				latest.Attachments = []app.MessageAttachment{{ArtifactID: "partial", Name: "partial.md", RelPath: "conversation-files/partial/partial.md", Source: "conversation_result"}}
			}
			messages := []app.Message{older, latest}
			resolution, err := (Runtime{}).resolveDocumentContextWithHistory(t.Context(), "session", "完善一下", nil, invocationHistory{MessageCandidates: messages, Selected: agentContextSnapshot{Messages: messages}})
			if err != nil || !resolution.Unavailable || len(resolution.References) != 0 {
				t.Fatalf("role=%s partial=%t: %#v %v", role, partial, resolution, err)
			}
			if refs := recentDocumentMessageReferences(messages); len(refs) != 0 {
				t.Fatalf("older fallback: %#v", refs)
			}
		}
	}
}

func TestConversationFileNamedIncompleteGroupRequiresReattachment(t *testing.T) {
	message := app.Message{ID: "group", Role: "user", FilesUnavailable: true, Attachments: []app.MessageAttachment{{Name: "report.md", RelPath: "conversation-files/a/report.md", Source: "conversation_attachment"}}}
	history := invocationHistory{MessageCandidates: []app.Message{message}, Selected: agentContextSnapshot{Messages: []app.Message{message}}}
	resolution, err := (Runtime{}).resolveDocumentContextWithHistory(t.Context(), "session", "修改 report.md", nil, history)
	if err != nil || !resolution.Unavailable || len(resolution.References) > 0 {
		t.Fatalf("incomplete same-name group became unique: %#v %v", resolution, err)
	}
	input := app.MessagePart{Kind: app.MessagePartFile, ArtifactID: "new", Name: "report.md", SHA256: strings.Repeat("a", 64), Bytes: 7, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "report.md"}}
	resolution, err = (Runtime{}).resolveDocumentContextWithHistory(t.Context(), "session", "修改 report.md", []app.MessagePart{input}, history)
	if err != nil || resolution.Unavailable || len(resolution.References) != 1 || resolution.References[0].HandleID != "new" {
		t.Fatalf("explicit current file lost precedence: %#v %v", resolution, err)
	}
}

func TestConversationFileMetadataProjectsWithoutReadingBytes(t *testing.T) {
	attachment := app.MessageAttachment{ArtifactID: "immutable-id", Name: "report.md", RelPath: "conversation-files/immutable-id/report.md", Source: "conversation_attachment", SHA256: strings.Repeat("b", 64), Bytes: 123}
	message := app.Message{ID: "owner-file", Role: "user", Attachments: []app.MessageAttachment{attachment}}
	runtime := Runtime{}
	resolution, err := runtime.resolveDocumentContextWithHistory(t.Context(), "session", "完善一下", nil, invocationHistory{Selected: agentContextSnapshot{Messages: []app.Message{message}}})
	if err != nil || len(resolution.References) != 1 {
		t.Fatalf("metadata resolution: %#v %v", resolution, err)
	}
	ref := resolution.References[0]
	if ref.HandleID != attachment.ArtifactID || ref.SHA256 != attachment.SHA256 || ref.Size != attachment.Bytes {
		t.Fatalf("lost immutable binding: %#v", ref)
	}
	projection, err := runtime.projectIntentGrounding(t.Context(), "session", "run", "完善一下", resolution, nil)
	if err != nil || len(projection.Targets) != 1 || projection.Targets[0].Document.HandleID != attachment.ArtifactID {
		t.Fatalf("metadata projection: %#v %v", projection, err)
	}
	// There is no ToolHub, workspace, or backing file: both phases are metadata-only.
}

func TestConversationFileUnavailableGroundingRequiresReattachment(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	candidate, ok := graph.Candidate("document.edit#edit")
	if !ok {
		t.Fatal("missing document edit candidate")
	}
	runtime := Runtime{capabilities: catalog}
	projection, err := runtime.projectIntentGrounding(t.Context(), "session", "run", "完善一下", documentContextResolution{Unavailable: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision := semanticrouting.Decision{Verdict: semanticrouting.VerdictClear, Candidates: []semanticrouting.CandidateScore{{Candidate: candidate}}}
	route, err := runtime.routeFromFusionDecision(t.Context(), "完善一下", projection, decision, "")
	if err != nil || route.Status != app.RouteClarify || route.Reason != "conversation_file_unavailable" || !route.Slots.Empty() {
		t.Fatalf("unavailable route: %#v %v", route, err)
	}
}
func TestConversationFileEditOutputUsesWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report-2.md"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := nextDocumentOutputRef(root, "conversation-files/abc/report.md")
	if err != nil || output != "report-3.md" {
		t.Fatalf("output %q: %v", output, err)
	}
}
