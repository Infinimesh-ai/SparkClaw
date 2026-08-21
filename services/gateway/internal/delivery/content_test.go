package delivery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestResolveBrowserContentUsesDefaultWorkspaceForUnscopedWebSession(t *testing.T) {
	st := store.NewMemoryStore()
	root := t.TempDir()
	session := storetest.MustCreateSession(t, st, "unscoped web session")
	path := filepath.Join(root, "uploads", "result.docx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("workflow output"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.SaveArtifactObject(app.ArtifactObject{
		ID: "obj_default_workspace", SessionID: session.ID, Backend: "workspace", Key: "uploads/result.docx",
		Path: path, ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Bytes: len("workflow output"),
	})
	content := app.MessageContent{Parts: []app.MessagePart{{
		ID: "file", Kind: app.MessagePartFile, Disposition: app.MessageDispositionAttachment,
		ArtifactID: "obj_default_workspace", Name: "result.docx",
	}}}

	if _, err := ResolveBrowserContent(t.Context(), st, app.DefaultOwnerID, "", content); err == nil {
		t.Fatal("unscoped artifact was accepted without an explicit default workspace")
	}
	resolved, err := ResolveBrowserContent(t.Context(), st, app.DefaultOwnerID, root, content)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Parts) != 1 || resolved.Parts[0].ArtifactID != "obj_default_workspace" || resolved.Parts[0].Bytes != len("workflow output") || resolved.Parts[0].SHA256 == "" {
		t.Fatalf("default-workspace artifact was not governed: %#v", resolved)
	}
}
