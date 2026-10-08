package agent

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestTextOnlyExecutionScopeRejectsLiveBrowserEmailAdmission(t *testing.T) {
	host, _, _, closeHost := newWorkflowE2ERuntime(t, nil)
	defer closeHost()
	admission := &fakeEmailAdmission{binding: app.EmailAdmissionBinding{Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, SettingVersion: 4, BrowserCredentialGeneration: 7, ProbeRevision: 2, ReadScriptRevision: 3, ValidatedAt: time.Now().UTC()}}
	host = host.WithEmailAdmission(admission)
	local := store.NewMemoryStore()
	root := t.TempDir()
	storage := host.tools.Config().Storage
	storage.ArtifactDir = t.TempDir()
	runtime, release, err := host.WithExecutionScope(local, toolhub.ExecutionResources{OwnerID: "text-owner", WorkspaceRoot: root, Artifacts: artifact.NewStore(storage), TextOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	session := storetest.MustCreateSessionWithScope(t, local, "text scope", "text-owner", root, "webchat", false)
	route := emailSendRoute(runtime.capabilities.Revision())
	route.Slots.Operation = app.RouteOperationRead
	if _, err := runtime.admitEmailRoute(t.Context(), session.ID, "text-run", session.OwnerID, "read a Gmail message", route); err == nil || len(admission.owners) != 0 {
		t.Fatalf("text-only profile reached live browser email admission: %v owners=%v", err, admission.owners)
	}
}
