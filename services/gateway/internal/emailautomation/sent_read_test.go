package emailautomation

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type sentMailboxRepo struct {
	Repository
	boxes []app.EmailMailbox
}

func (s *sentMailboxRepo) ListEmailMailboxes(context.Context, string) ([]app.EmailMailbox, error) {
	return s.boxes, nil
}

type sentReadRunner struct {
	fakeScriptRunner
	during func()
}

type revokedSentBrowser struct{ fakePlaywrightController }

func (s *revokedSentBrowser) RunScript(ctx context.Context, request browsercontrol.RunScriptRequest) (browsercontrol.ScriptExecutionResult, error) {
	result, err := s.fakePlaywrightController.RunScript(ctx, request)
	s.status.CredentialGeneration++
	return result, err
}

func TestSentReadRejectsCredentialRevocationDuringItsSingleTask(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	request := validReadRequest()
	request.SentMatch = sentMatchFixture()
	request.AccountAddress = "owner@example.test"
	browser := &revokedSentBrowser{fakePlaywrightController: fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: json.RawMessage(validReadOutput())}}}
	if _, err := NewPlaywrightRunner(browser).Read(t.Context(), provider, request); ErrorCode(err) != app.ToolErrorEmailAdmissionStale || len(browser.requests) != 1 {
		t.Fatal("revoked task returned a successful receipt or repeated its read")
	}
}

func (s *sentReadRunner) Read(ctx context.Context, p Provider, r ReadRequest) (ReadResult, error) {
	s.readCalls = append(s.readCalls, r)
	if s.during != nil {
		s.during()
	}
	return s.readResult, s.readErr
}

func TestSentReadControllerPinsUniqueOwnerMailboxAndPreservesSource(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			root, request, result, _, _ := sentCaptureFixture(t)
			var repo interface {
				Repository
				mailboxSignalReader
				BindEmailMailbox(context.Context, store.EmailBindCommand) (app.EmailMailbox, error)
			} = store.NewMemoryStore()
			if backend == "file" {
				f, err := store.NewFileStore(filepath.Join(t.TempDir(), "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				repo = f
			}
			checked := time.Now().UTC()
			setting, err := repo.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: "gmail", Account: "default", Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked}, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: "gmail", Address: "owner@example.test", Enabled: true, Boundary: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			request.SettingVersion = setting.Version
			request.AccountAddress = "attacker@example.test"
			runner := &fakeScriptRunner{readResult: result}
			c := NewController(repo, DefaultRegistry(), nil, runner).WithCaptureWorkspaceRoot(root)
			if _, err := c.ReadForOwner(t.Context(), "owner", request); err != nil {
				t.Fatal(err)
			}
			if len(runner.readCalls) != 1 || runner.readCalls[0].AccountAddress != "owner@example.test" || !reflect.DeepEqual(runner.readCalls[0].SentMatch, request.SentMatch) {
				t.Fatal("account or selector not pinned")
			}
			if _, err := c.ReadForOwner(t.Context(), "other", request); ErrorCode(err) != app.ToolErrorEmailAdmissionStale || len(runner.readCalls) != 1 {
				t.Fatal("cross owner admitted")
			}
		})
	}
}

func TestSentReadControllerRejectsAmbiguityRevocationAndReplacement(t *testing.T) {
	for _, mode := range []string{"missing", "ambiguous", "wrong owner", "invalid account", "disabled", "setting revoked during task", "account replaced during task", "mailbox rebound during task", "partial", "empty"} {
		t.Run(mode, func(t *testing.T) {
			root, req, result, _, _ := sentCaptureFixture(t)
			st := store.NewMemoryStore()
			checked := time.Now().UTC()
			setting, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: "gmail", Account: "default", Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked}, 0)
			if err != nil {
				t.Fatal(err)
			}
			req.SettingVersion = setting.Version
			repo := &sentMailboxRepo{Repository: st, boxes: []app.EmailMailbox{{ID: "mailbox", BindingGeneration: 1, OwnerID: "owner", Provider: "gmail", Address: "owner@example.test", Active: true}}}
			runner := &sentReadRunner{fakeScriptRunner: fakeScriptRunner{readResult: result}}
			switch mode {
			case "missing":
				repo.boxes = nil
			case "ambiguous":
				repo.boxes = append(repo.boxes, repo.boxes[0])
			case "wrong owner":
				repo.boxes[0].OwnerID = "another"
			case "invalid account":
				repo.boxes[0].Address = "o***@example.test"
			case "disabled":
				repo.boxes[0].Active = false
			case "setting revoked during task":
				runner.during = func() {
					setting.Enabled = false
					if _, err := st.UpdateEmailProviderSetting(t.Context(), setting, setting.Version); err != nil {
						t.Fatal(err)
					}
				}
			case "account replaced during task":
				runner.during = func() { repo.boxes[0].Address = "replaced@example.test" }
			case "mailbox rebound during task":
				runner.during = func() { repo.boxes[0].BindingGeneration++ }
			case "partial":
				runner.readResult.Status = "partial"
			case "empty":
				runner.readResult = ReadResult{Provider: "gmail", Status: "empty"}
			}
			c := NewController(repo, DefaultRegistry(), nil, runner).WithCaptureWorkspaceRoot(root)
			if _, err := c.ReadForOwner(t.Context(), "owner", req); err == nil {
				t.Fatal("invalid account/result accepted")
			}
			if len(runner.readCalls) > 1 || len(runner.sendCalls) != 0 || len(runner.probeCalls) != 0 || len(runner.discoveryCalls) != 0 {
				t.Fatal("extra operation")
			}
		})
	}
}

func TestSentReadRunnerOneReadAndClosedSelectorBinding(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	for _, mode := range []string{"valid", "unsupported", "with target", "with discovery", "without account", "bad selector", "revoked", "empty"} {
		t.Run(mode, func(t *testing.T) {
			request := validReadRequest()
			request.SentMatch = sentMatchFixture()
			request.AccountAddress = "owner@example.test"
			browser := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: json.RawMessage(validReadOutput())}}
			p := provider
			switch mode {
			case "unsupported":
				request.Provider = "outlook"
				p, _ = DefaultRegistry().Get("outlook")
			case "with target":
				request.Target = &app.EmailCaptureTarget{}
			case "with discovery":
				request.Discovery = &app.EmailDiscoveryOptions{}
			case "without account":
				request.AccountAddress = ""
			case "bad selector":
				request.SentMatch.Folder = "inbox"
			case "revoked":
				browser.status.CredentialGeneration = 8
			case "empty":
				browser.result.Result = json.RawMessage(`{"schema_version":1,"provider":"gmail","status":"empty","capture":null}`)
			}
			_, err := NewPlaywrightRunner(browser).Read(t.Context(), p, request)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("accepted=%v", err == nil)
			}
			if mode != "valid" && mode != "empty" {
				if len(browser.requests) != 0 {
					t.Fatal("invalid input reached browser")
				}
				return
			}
			if len(browser.requests) != 1 || browser.requests[0].Operation != "read" || browser.requests[0].TaskID != request.InvocationID {
				t.Fatal("not one original read task")
			}
			raw, _ := json.Marshal(browser.requests[0].Input)
			var input map[string]any
			_ = json.Unmarshal(raw, &input)
			if len(input) != 8 || input["account_address"] != request.AccountAddress || input["sent_match"].(map[string]any)["subject"] != request.SentMatch.Subject {
				t.Fatal("private payload changed")
			}
		})
	}
}
