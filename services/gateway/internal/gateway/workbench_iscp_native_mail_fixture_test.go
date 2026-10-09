package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type nativeMailSinkAttachment struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	BytesBase64 string `json:"bytes_base64"`
}
type nativeMailSinkDelivery struct {
	Request     app.EmailSendRequest       `json:"request"`
	Attachments []nativeMailSinkAttachment `json:"attachments"`
}
type nativeMailSinkEvidence struct {
	Sends      []nativeMailSinkDelivery `json:"sends"`
	Reconciles int                      `json:"reconciles"`
}

// This controlled provider reads the exact staged bytes handed to the real
// emailmanagement service. It never reads the original path again and cannot
// deliver to an external mailbox: its only route is an httptest loopback sink.
type nativeMailSinkProvider struct {
	noEmailBrowser
	workspace string
	endpoint  string
	client    *http.Client
}

func (p *nativeMailSinkProvider) call(ctx context.Context, route string, request app.EmailSendRequest) (app.EmailSendResult, error) {
	input := nativeMailSinkDelivery{Request: request}
	if route == "/send" {
		for _, attachment := range request.Attachments {
			if filepath.IsAbs(attachment.StagedPath) || strings.Contains(attachment.StagedPath, "..") || !strings.HasPrefix(attachment.StagedPath, ".sparkclaw-mail-send-") {
				return app.EmailSendResult{}, errors.New("fixture received an invalid staged attachment path")
			}
			data, err := os.ReadFile(filepath.Join(p.workspace, attachment.StagedPath))
			if err != nil {
				return app.EmailSendResult{}, err
			}
			digest := sha256.Sum256(data)
			if int64(len(data)) != attachment.SizeBytes || "sha256:"+hex.EncodeToString(digest[:]) != attachment.SHA256 {
				return app.EmailSendResult{}, errors.New("fixture staged attachment differs from saved manifest")
			}
			input.Attachments = append(input.Attachments, nativeMailSinkAttachment{Path: attachment.Path, Name: attachment.Name, SizeBytes: attachment.SizeBytes, SHA256: attachment.SHA256, BytesBase64: base64.StdEncoding.EncodeToString(data)})
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return app.EmailSendResult{}, err
	}
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+route, bytes.NewReader(raw))
	if err != nil {
		return app.EmailSendResult{}, err
	}
	response, err := p.client.Do(call)
	if err != nil {
		return app.EmailSendResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return app.EmailSendResult{}, errors.New("controlled provider receipt lost after accepting the message")
	}
	var receipt app.EmailSendResult
	err = json.NewDecoder(response.Body).Decode(&receipt)
	return receipt, err
}
func (p *nativeMailSinkProvider) SendForOwner(ctx context.Context, _ string, request app.EmailSendRequest) (app.EmailSendResult, error) {
	return p.call(ctx, "/send", request)
}
func (p *nativeMailSinkProvider) ReconcileSendForOwner(ctx context.Context, _ string, request app.EmailSendRequest) (app.EmailSendResult, error) {
	return p.call(ctx, "/reconcile", request)
}

// The runner executes this process in Linux Docker on the lab's isolated
// network. There is no Gateway HTTP listener or published business port. Only
// the production ISCP Endpoint receives native renderer business operations.
func TestWorkbenchISCPNativeMailQualificationFixture(t *testing.T) {
	lab := os.Getenv("SPARKCLAW_MAIL_ISCP_FIXTURE_ROOT")
	if lab == "" {
		t.Skip("opt-in native Electron/Linux Gateway controlled mail qualification")
	}
	root := filepath.Join(lab, "native-mail")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	initial := []byte("Initial native workspace attachment before explicit review.\n")
	if err := os.WriteFile(filepath.Join(workspace, "reports/native-attachment.txt"), initial, 0600); err != nil {
		t.Fatal(err)
	}
	transport, err := iscpworkbench.LoadConfig(filepath.Join(lab, "gateway-container-helper.json"))
	if err != nil || transport.Binding == nil {
		t.Fatal("fixture transport is missing", err)
	}
	repository, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ownerID := transport.Binding.OwnerID
	if _, err = repository.RegisterClient(t.Context(), app.Client{ID: transport.Binding.ClientID, OwnerID: ownerID, ActorID: ownerID, Name: "isolated native mail", TokenHash: hashSecret("unused-native-mail-business-credential")}); err != nil {
		t.Fatal(err)
	}
	writeEvidence := func(filename string, value any) error {
		raw, marshalErr := json.MarshalIndent(value, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		if writeErr := os.WriteFile(filepath.Join(root, filename+".tmp"), raw, 0600); writeErr != nil {
			return writeErr
		}
		return os.Rename(filepath.Join(root, filename+".tmp"), filepath.Join(root, filename))
	}
	var mu sync.Mutex
	sinkEvidence := nativeMailSinkEvidence{Sends: []nativeMailSinkDelivery{}}
	if err := writeEvidence("sink-evidence.json", sinkEvidence); err != nil {
		t.Fatal(err)
	}
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input nativeMailSinkDelivery
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&input) != nil || len(input.Request.To) != 1 || input.Request.To[0] != "sink@example.test" || input.Request.InvocationID == "" {
			http.Error(w, "fixture accepts only the controlled test recipient", 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var index int
		switch r.URL.Path {
		case "/send":
			if len(input.Attachments) != len(input.Request.Attachments) || len(input.Attachments) != 1 {
				http.Error(w, "attachment manifest missing", 400)
				return
			}
			sinkEvidence.Sends = append(sinkEvidence.Sends, input)
			index = len(sinkEvidence.Sends)
		case "/reconcile":
			for i, sent := range sinkEvidence.Sends {
				original := input.Request
				original.Mode = sent.Request.Mode
				if input.Request.Mode == "reconcile" && reflect.DeepEqual(sent.Request, original) {
					index = i + 1
					break
				}
			}
			if index == 0 {
				http.Error(w, "reconciliation does not match the original immutable request", 409)
				return
			}
			sinkEvidence.Reconciles++
		default:
			http.NotFound(w, r)
			return
		}
		if err := writeEvidence("sink-evidence.json", sinkEvidence); err != nil {
			http.Error(w, "evidence could not be persisted", 500)
			return
		}
		if r.URL.Path == "/send" && strings.HasPrefix(input.Request.Subject, "Lost receipt") {
			http.Error(w, "deliberately lost provider receipt after effect", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(app.EmailSendResult{Provider: app.EmailProviderGmail, Status: "sent", ProviderMessageID: fmt.Sprintf("controlled-native-message-%d", index)})
	}))
	defer sink.Close()
	provider := &nativeMailSinkProvider{workspace: workspace, endpoint: sink.URL, client: &http.Client{Timeout: 10 * time.Second}}
	service, err := emailmanagement.New(repository, provider, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: workspace, QualifiedProviderModes: map[string]string{app.EmailProviderGmail: app.EmailProviderModeTimeRange}})
	if err != nil {
		t.Fatal(err)
	}
	box, err := repository.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: ownerID, CommandKey: "native-controlled-box"}, Provider: app.EmailProviderGmail, Address: "owner@example.test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	checked := time.Now().UTC()
	if _, err := repository.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: ownerID, Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked}, 0); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ResolveDefault(filepath.Join(lab, "model.profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = workspace, []string{workspace}
	cfg.State.Path = filepath.Join(root, "workbench-state.json")
	cfg.Storage.TraceDir, cfg.Storage.ArtifactDir = filepath.Join(root, "traces"), filepath.Join(root, "artifacts")
	cfg.Gateway.PairingRequired, cfg.Gateway.WorkbenchISCPLocalTest = true, true
	cfg.Gateway.DeploymentID = transport.Binding.DeploymentID
	tools := toolhub.New(cfg, repository)
	defer tools.Close()
	server := New(cfg, repository, tools, agent.Runtime{}, WithEmailManagement(service), WithExecutions(filepath.Join(root, "execution"), nil))
	defer server.executions.Close()
	lifecycle, cancel := context.WithCancel(t.Context())
	defer cancel()
	server.BindLifecycleContext(lifecycle)
	handler, err := server.NewWorkbenchISCPHandler(transport)
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]int{}
	endpoint, err := iscpworkbench.NewEndpoint(transport, func(ctx context.Context, request iscpworkbench.Request) iscpworkbench.Response {
		response := handler(ctx, request)
		mu.Lock()
		operations[request.Operation]++
		_ = writeEvidence("operations.json", operations)
		mu.Unlock()
		return response
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	go func() { _ = endpoint.Run(lifecycle) }()
	digest := sha256.Sum256(initial)
	if err := writeEvidence("ready.json", map[string]any{"mailbox_id": box.ID, "initial_sha256": "sha256:" + hex.EncodeToString(digest[:]), "gateway_business_listener": false}); err != nil {
		t.Fatal(err)
	}
	<-lifecycle.Done()
}
