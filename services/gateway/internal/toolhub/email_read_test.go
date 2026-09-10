package toolhub

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type fakeEmailReader struct {
	owner    string
	requests []app.EmailReadRequest
}

func (f *fakeEmailReader) ReadForOwner(_ context.Context, owner string, request app.EmailReadRequest) (app.EmailReadResult, error) {
	f.owner = owner
	f.requests = append(f.requests, request)
	return app.EmailReadResult{Provider: request.Provider, Status: "empty", BrowserCredentialGeneration: request.BrowserCredentialGeneration, ScriptRevision: request.ScriptRevision}, nil
}

func TestEmailReadUsesSessionOwnerAndBoundedQueryWithoutSendApproval(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "email", "owner-email", t.TempDir(), "web", false)
	reader := &fakeEmailReader{}
	hub := New(config.Default(), st).WithEmailReader(reader)
	t.Cleanup(func() { _ = hub.Close() })
	args := validEmailSendArgs()
	delete(args, "recipient")
	delete(args, "subject")
	delete(args, "body")
	delete(args, "send_script_revision")
	args["read_script_revision"] = "1"
	if _, err := hub.Execute(t.Context(), app.ToolEmailRead, args, session.ID, "read-run"); err != nil {
		t.Fatal(err)
	}
	if reader.owner != "owner-email" || len(reader.requests) != 1 || reader.requests[0].OwnerScope != "" || reader.requests[0].ScriptRevision != 1 || reader.requests[0].SettingVersion != 4 {
		t.Fatalf("reader=%#v", reader)
	}
	definition, ok := hub.Definition(app.ToolEmailRead)
	if !ok || definition.Idempotent || definition.Risk != app.RiskRead || definition.RequiresApproval || definition.OutcomeAdapter != app.OutcomeAdapterBrowserEmailRead {
		t.Fatalf("definition=%#v", definition)
	}
	for _, key := range []string{"limit", "unread_only", "owner_scope", "workspace_root"} {
		args[key] = "model controlled"
		if _, err := hub.Execute(t.Context(), app.ToolEmailRead, args, session.ID, "read-run"); err == nil {
			t.Fatalf("accepted unexpected %s", key)
		}
		delete(args, key)
	}
	if len(reader.requests) != 1 {
		t.Fatal("invalid queries reached reader")
	}
}
