package agent

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestExecutionScopeEquivalentWeatherAcrossWorkbenchRepositories(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			cfg := agentTestConfig()
			cfg.Workspaces.DefaultRoot = t.TempDir()
			cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
			cfg.Storage.ArtifactBackend, cfg.Storage.ArtifactDir = "filesystem", t.TempDir()
			var hostStore ExecutionRepository = store.NewMemoryStore()
			statePath := filepath.Join(t.TempDir(), "workbench.json")
			if backend == "file" {
				var err error
				hostStore, err = store.NewFileStore(statePath)
				if err != nil {
					t.Fatal(err)
				}
			}
			hub := toolhub.New(cfg, hostStore)
			t.Cleanup(func() { _ = hub.Close() })
			hub.ReplaceInfoAdapters(nil, &transientWeatherAdapter{})
			host := NewRuntime(hostStore, hub, policy.New(cfg), modelrouter.New(cfg), nil).WithArtifactStore(hub.ArtifactStore())
			hostSession := storetest.MustCreateSessionWithScope(t, hostStore, "host", "scope-owner", cfg.Workspaces.DefaultRoot, "webchat", false)
			local := store.NewMemoryStore()
			scopeRoot := t.TempDir()
			scopeStorage := cfg.Storage
			scopeStorage.ArtifactDir = t.TempDir()
			scope, release, err := host.WithExecutionScope(local, toolhub.ExecutionResources{OwnerID: hostSession.OwnerID, WorkspaceRoot: scopeRoot, Artifacts: artifact.NewStore(scopeStorage)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := release(t.Context()); err != nil {
					t.Error(err)
				}
			})
			localSession := storetest.MustCreateSessionWithScope(t, local, "local", hostSession.OwnerID, scopeRoot, "webchat", false)
			hostResult, err := host.HandleMessage(t.Context(), hostSession.ID, "今日杭州的天气")
			if err != nil {
				t.Fatal(err)
			}
			scopeResult, err := scope.HandleMessage(t.Context(), localSession.ID, "今日杭州的天气")
			if err != nil {
				t.Fatal(err)
			}
			if hostResult.Run.State != scopeResult.Run.State || hostResult.Run.State != "completed" || hostResult.RouteDecision == nil || scopeResult.RouteDecision == nil || !reflect.DeepEqual(hostResult.RouteDecision.CapabilityPath, scopeResult.RouteDecision.CapabilityPath) {
				t.Fatalf("equivalent routes/states differ: host=%+v scope=%+v", hostResult.Run, scopeResult.Run)
			}
			hostCalls, err := hostStore.ListToolCalls(t.Context(), hostSession.ID)
			if err != nil {
				t.Fatal(err)
			}
			localCalls, err := local.ListToolCalls(t.Context(), localSession.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(hostCalls) != len(localCalls) || len(hostCalls) != 2 {
				t.Fatalf("tool effects differ: host=%v scoped=%v", hostCalls, localCalls)
			}
			for i := range hostCalls {
				if hostCalls[i].Tool != localCalls[i].Tool || hostCalls[i].Status != localCalls[i].Status {
					t.Fatalf("tool effect differs: host=%+v scoped=%+v", hostCalls[i], localCalls[i])
				}
			}
			if len(hostResult.Message.Attachments) != 1 || len(scopeResult.Message.Attachments) != 1 {
				t.Fatal("one entry lost its rendered weather artifact")
			}
			if _, found, err := hostStore.GetRun(t.Context(), scopeResult.Run.ID); err != nil || found {
				t.Fatalf("scope run leaked to host: found=%t err=%v", found, err)
			}
			if backend == "file" {
				restored, err := store.NewFileStore(statePath)
				if err != nil {
					t.Fatal(err)
				}
				if saved, found, err := restored.GetRun(t.Context(), hostResult.Run.ID); err != nil || !found || saved.State != "completed" {
					t.Fatalf("host result did not survive ordinary restart: found=%t err=%v", found, err)
				}
				if _, found, err := restored.GetRun(t.Context(), scopeResult.Run.ID); err != nil || found {
					t.Fatalf("scope content appeared after host restart: found=%t err=%v", found, err)
				}
			}
		})
	}
}

type scopedEmailReader struct {
	owner   string
	request app.EmailReadRequest
}

func (r *scopedEmailReader) ReadForOwner(_ context.Context, owner string, request app.EmailReadRequest) (app.EmailReadResult, error) {
	r.owner, r.request = owner, request
	return app.EmailReadResult{Provider: request.Provider, Status: "collected", Capture: emailCaptureReceiptFixture(), BrowserCredentialGeneration: request.BrowserCredentialGeneration, ScriptRevision: request.ScriptRevision}, nil
}

func TestExecutionScopeEmailRetainsAuthoritativeAdmissionAndFrozenOwner(t *testing.T) {
	host, persistent, _, closeHost := newWorkflowE2ERuntime(t, nil)
	defer closeHost()
	reader := &scopedEmailReader{}
	host.tools.WithEmailReader(reader)
	admission := &fakeEmailAdmission{binding: app.EmailAdmissionBinding{Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, SettingVersion: 4, BrowserCredentialGeneration: 7, ProbeRevision: 2, ReadScriptRevision: 3, ValidatedAt: time.Now().UTC()}}
	host = host.WithEmailAdmission(admission)
	local := store.NewMemoryStore()
	root := t.TempDir()
	storage := host.tools.Config().Storage
	storage.ArtifactDir = t.TempDir()
	runtime, release, err := host.WithExecutionScope(local, toolhub.ExecutionResources{OwnerID: "authenticated-owner", WorkspaceRoot: root, Artifacts: artifact.NewStore(storage)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	session := storetest.MustCreateSessionWithScope(t, local, "scoped mail", "authenticated-owner", root, "webchat", false)
	route := emailSendRoute(runtime.capabilities.Revision())
	route.Slots.Operation = app.RouteOperationRead
	route.Slots.Query = "读取 Gmail 同步时间范围内的一封邮件"
	route, err = runtime.admitEmailRoute(t.Context(), session.ID, "run-mail", session.OwnerID, route.Slots.Query, route)
	if err != nil {
		t.Fatal(err)
	}
	if len(admission.owners) != 1 || admission.owners[0] != session.OwnerID {
		t.Fatalf("mail admission lost authenticated principal: %v", admission.owners)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(t.Context(), app.AgentRun{ID: "run-mail", SessionID: session.ID, StartedAt: time.Now().UTC()}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn-mail")
	if err != nil {
		t.Fatal(err)
	}
	node := dispatch.Run.Workflow.Nodes["email_read"]
	call, approval, _, err := runtime.runToolPlan(t.Context(), session.ID, dispatch.Run.ID, toolPlan{Name: app.ToolEmailRead, Args: map[string]any{"provider": app.EmailProviderOutlook, "account": "invented", "setting_version": "999"}, WorkflowID: app.WorkflowBrowserEmail, WorkflowNodeID: "email_read", ScopeRevision: node.ScopeRevision, Capability: app.ToolCapabilityBrowserEmailRead})
	if err != nil || approval != nil || call.Status != app.ToolCallStatusCompleted {
		t.Fatalf("scoped mail was unavailable: call=%+v approval=%+v err=%v", call, approval, err)
	}
	if reader.owner != session.OwnerID || reader.request.Provider != app.EmailProviderGmail || reader.request.SettingVersion != 4 || reader.request.BrowserCredentialGeneration != 7 || reader.request.ScriptRevision != 3 {
		t.Fatalf("model changed authoritative mail binding: owner=%s request=%+v", reader.owner, reader.request)
	}
	if _, found, err := persistent.GetRun(t.Context(), dispatch.Run.ID); err != nil || found {
		t.Fatalf("mail workflow leaked workbench history into mail service: found=%t err=%v", found, err)
	}
}
