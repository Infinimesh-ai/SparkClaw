package delivery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestEndpointResourceResolverPrefersGovernedArtifactFromSourceWorkspace(t *testing.T) {
	st := store.NewMemoryStore()
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	source := storetest.MustCreateSessionWithScope(t, st, "source", app.DefaultOwnerID, sourceRoot, "webchat", false)
	target := storetest.MustCreateSessionWithScope(t, st, "target", "external-owner", targetRoot, "weixin", true)

	relPath := filepath.Join("uploads", "result.docx")
	sourcePath := filepath.Join(sourceRoot, relPath)
	targetPath := filepath.Join(targetRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("source workflow output"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("unrelated target file"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := app.ArtifactObject{
		ID: "obj_workflow_output", Kind: "workflow_output", RunID: "run_source", SessionID: source.ID,
		Backend: "workspace", Key: filepath.ToSlash(relPath), URI: "workspace://" + filepath.ToSlash(relPath),
		Path: sourcePath, ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Bytes: len("source workflow output"), CreatedAt: time.Now().UTC(),
	}
	st.SaveArtifactObject(artifact)

	resolved, err := NewEndpointResourceResolver(st, app.MessageEndpoint{SessionID: target.ID}).Resolve(t.Context(), app.MessagePart{
		ID: "file", Kind: app.MessagePartFile, Disposition: app.MessageDispositionAttachment,
		ArtifactID: artifact.ID, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: filepath.ToSlash(relPath)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != sourcePath {
		t.Fatalf("selected target changed the workflow output source: got %q want %q", resolved, sourcePath)
	}
}
