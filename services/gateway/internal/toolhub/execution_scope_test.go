package toolhub

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func executionScopeFixture(t *testing.T, parent *ToolHub, browser browserautomation.Adapter) (*ToolHub, *store.MemoryStore, app.Session) {
	t.Helper()
	local := store.NewMemoryStore()
	root := t.TempDir()
	storage := parent.Config().Storage
	storage.ArtifactBackend, storage.ArtifactDir = "filesystem", t.TempDir()
	scoped, err := parent.WithExecutionScope(local, ExecutionResources{OwnerID: "scope-owner", WorkspaceRoot: root, Artifacts: artifact.NewStore(storage), Browser: browser})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scoped.Close(); err != nil {
			t.Error(err)
		}
	})
	session := storetest.MustCreateSessionWithScope(t, local, "scoped", "scope-owner", root, "webchat", false)
	return scoped, local, session
}

type ownedOCRFixture struct {
	runtimeOCRAdapter
	closes atomic.Int32
}

func (a *ownedOCRFixture) Close() error { a.closes.Add(1); return nil }

func TestExecutionScopeSharesLiveOCRWithoutSharingContentOrProviderOwnership(t *testing.T) {
	parentStore := store.NewMemoryStore()
	first := &ownedOCRFixture{runtimeOCRAdapter: runtimeOCRAdapter{result: documentocr.Result{Markdown: "first credential result"}}}
	parent := newDocumentOCRRuntimeTestHub(parentStore, first)
	t.Cleanup(func() { _ = parent.Close() })
	browser := &managedBrowserAdapter{}
	scoped, local, session := executionScopeFixture(t, parent, browser)
	if scoped.registry != parent.registry {
		t.Fatal("scope constructed a second tool registry")
	}
	input := documentocr.Request{Content: []byte("scope-private-image")}
	metadata := documentOCRCallMetadata{SessionID: session.ID, RunID: "scope-run"}
	if got := scoped.parseDocumentOCR(t.Context(), input, metadata); got.Err != nil || got.Result.Markdown != first.result.Markdown {
		t.Fatalf("first OCR: %+v", got)
	}
	if len(parent.ocrRuntime.cache) != 0 || len(testListModelCalls(parentStore, "", "")) != 0 {
		t.Fatal("execution OCR content or provenance leaked into the service repository/cache")
	}
	second := &ownedOCRFixture{runtimeOCRAdapter: runtimeOCRAdapter{result: documentocr.Result{Markdown: "current credential result"}}}
	parent.WithDocumentOCRAdapter(second)
	if first.closes.Load() != 1 {
		t.Fatal("replaced OCR provider was not released")
	}
	got := scoped.parseDocumentOCR(t.Context(), input, metadata)
	if got.Err != nil || got.Result.Markdown != second.result.Markdown || got.CacheResult != "miss" || second.calls.Load() != 1 {
		t.Fatalf("scope reused a stale provider or credential cache: %+v", got)
	}
	if len(testListModelCalls(local, "", "")) != 2 {
		t.Fatal("scope-local OCR evidence missing")
	}
	if err := scoped.Close(); err != nil {
		t.Fatal(err)
	}
	if len(scoped.ocrRuntime.cache) != 0 {
		t.Fatal("closed execution retained OCR content")
	}
	if second.closes.Load() != 0 || browser.closeCalls != 1 {
		t.Fatalf("wrong scope ownership: OCR closes=%d browser=%d", second.closes.Load(), browser.closeCalls)
	}
	if _, err := parent.ocr.Parse(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if second.closes.Load() != 1 {
		t.Fatal("service did not close its provider exactly once")
	}
}

func TestExecutionScopeOCRReplacementCancelsOldProviderCall(t *testing.T) {
	blocked := &ownedOCRFixture{runtimeOCRAdapter: runtimeOCRAdapter{started: make(chan struct{}), release: make(chan struct{})}}
	parent := newDocumentOCRRuntimeTestHub(store.NewMemoryStore(), blocked)
	t.Cleanup(func() { _ = parent.Close() })
	scoped, _, session := executionScopeFixture(t, parent, nil)
	done := make(chan documentOCRInvocation, 1)
	go func() {
		done <- scoped.parseDocumentOCR(t.Context(), documentocr.Request{Content: []byte("image")}, documentOCRCallMetadata{SessionID: session.ID})
	}()
	<-blocked.started
	parent.WithDocumentOCRAdapter(&runtimeOCRAdapter{result: documentocr.Result{Markdown: "new"}})
	if result := <-done; result.Err == nil || !strings.Contains(result.Err.Error(), "provider changed") {
		t.Fatalf("old call not cancelled: %+v", result)
	}
	if len(scoped.ocrRuntime.cache) != 0 {
		t.Fatal("cancelled provider output was cached")
	}
}

func TestExecutionScopeRegistryRejectsUnavailableResources(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Reminders.Enabled = true
	parent := New(cfg, store.NewMemoryStore())
	t.Cleanup(func() { _ = parent.Close() })
	scoped, _, session := executionScopeFixture(t, parent, nil)
	for _, definition := range parent.Definitions() {
		if resource := toolRegistry[definition.Name].resource; resource != "" {
			if _, err := scoped.Execute(t.Context(), definition.Name, nil, session.ID, "run"); app.ToolErrorCodeFrom(err) != app.ToolErrorResourceUnavailable {
				t.Errorf("%s did not report unavailable %s: %v", definition.Name, resource, err)
			}
		}
	}
	for _, definition := range scoped.Definitions() {
		if toolRegistry[definition.Name].resource != "" {
			t.Errorf("exposed unavailable tool %s", definition.Name)
		}
	}
	ran := false
	if err := parent.ReplaceDynamicTools("fixture", []DynamicToolRegistration{{Definition: app.ToolDefinition{Name: "fixture.remote", InputSchema: strictObjectSchema(nil, map[string]any{})}, RemoteName: "remote", Execute: func(context.Context, map[string]any, string, string) (Result, error) {
		ran = true
		return Result{}, nil
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, found := scoped.Definition("fixture.remote"); !found {
		t.Fatal("scope did not follow the live registry")
	}
	if _, err := scoped.Execute(t.Context(), "fixture.remote", nil, session.ID, "run"); app.ToolErrorCodeFrom(err) != app.ToolErrorResourceUnavailable || ran {
		t.Fatalf("external connector lacked qualified ledger but executed: %v", err)
	}
}

func TestExecutionScopeRejectsSessionOwnerAndWorkspaceSubstitution(t *testing.T) {
	parent := New(config.Default(), store.NewMemoryStore())
	t.Cleanup(func() { _ = parent.Close() })
	scoped, local, session := executionScopeFixture(t, parent, nil)
	for _, mismatch := range []struct{ owner, root string }{{"other-owner", session.WorkspaceRoot}, {session.OwnerID, t.TempDir()}} {
		other := storetest.MustCreateSessionWithScope(t, local, "other", mismatch.owner, mismatch.root, "webchat", false)
		if _, err := scoped.forSession(t.Context(), other.ID); err == nil {
			t.Fatalf("unadmitted session resources accepted: %+v", mismatch)
		}
	}
}
