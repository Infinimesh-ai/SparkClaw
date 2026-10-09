package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Opt-in process fixture for run-host-iscp-native-qualification.mjs. The TLS
// listener serves a controlled page and test driver only. No Gateway business
// HTTP/WebSocket route is mounted: the real host adapters receive encrypted
// requests from the production ISCP Endpoint and reference Relay.
func TestWorkbenchISCPNativeHostQualificationFixture(t *testing.T) {
	root := os.Getenv("SPARKCLAW_HOST_ISCP_FIXTURE_ROOT")
	if root == "" {
		t.Skip("opt-in native Electron/reference Relay qualification fixture")
	}
	transport, err := iscpworkbench.LoadConfig(filepath.Join(root, "gateway-helper.json"))
	if err != nil || transport.Binding == nil || os.Getenv("SPARKCLAW_HOST_ISCP_FIXTURE_TOKEN") == "" {
		t.Fatal("private qualification configuration unavailable", err)
	}
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired, cfg.Gateway.WorkbenchISCPLocalTest = true, true
	cfg.Gateway.DeploymentID = transport.Binding.DeploymentID
	repository, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.RegisterClient(t.Context(), app.Client{ID: transport.Binding.ClientID, OwnerID: transport.Binding.OwnerID, ActorID: transport.Binding.OwnerID, Name: "isolated native ISCP host", TokenHash: hashSecret("unused-native-host-fixture-business-credential")}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, repository)
	t.Cleanup(func() { _ = tools.Close() })
	server := New(cfg, repository, tools, agent.Runtime{}, WithExecutions(filepath.Join(root, "execution"), nil))
	lifecycle, cancel := context.WithCancel(t.Context())
	defer cancel()
	server.BindLifecycleContext(lifecycle)
	broker, err := server.browserHostBroker()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close(); server.executions.Close() })
	handler, err := server.NewWorkbenchISCPHandler(transport)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	counts := map[string]int{}
	directBusiness := 0
	endpoint, err := iscpworkbench.NewEndpoint(transport, func(ctx context.Context, request iscpworkbench.Request) iscpworkbench.Response {
		mu.Lock()
		counts[request.Operation]++
		mu.Unlock()
		return handler(ctx, request)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	go func() { _ = endpoint.Run(lifecycle) }()
	identity := browserhost.Identity{OwnerID: transport.Binding.OwnerID, ClientID: transport.Binding.ClientID, InstallationID: os.Getenv("SPARKCLAW_HOST_ISCP_INSTALLATION")}
	bindings := map[string]browserhost.Binding{}
	mux := http.NewServeMux()
	mux.HandleFunc("/fixture", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>ISCP native qualification</title><h1>Controlled native ISCP page</h1><label>Name<input id="name"></label><div id="draft">Draft:</div><button id="increment">Increment</button><div id="counter">Counter: 0</div><script>let n=0;document.querySelector('#increment').onclick=()=>document.querySelector('#counter').textContent='Counter: '+(++n);document.querySelector('#name').oninput=()=>document.querySelector('#draft').textContent='Draft: '+document.querySelector('#name').value;</script>`)
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	authorized := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+os.Getenv("SPARKCLAW_HOST_ISCP_FIXTURE_TOKEN") {
			http.Error(w, "fixture authorization required", 401)
			return false
		}
		return true
	}
	mux.HandleFunc("POST /qualify/step", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		var input struct {
			Operation string         `json:"operation"`
			CommandID string         `json:"command_id"`
			Arguments map[string]any `json:"arguments"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || input.CommandID == "" {
			http.Error(w, "invalid fixture command", 400)
			return
		}
		ctx, stop := context.WithTimeout(r.Context(), 12*time.Second)
		defer stop()
		mu.Lock()
		binding, found := bindings["native"]
		mu.Unlock()
		var err error
		if !found {
			binding, err = broker.Acquire(ctx, browserhost.Scope{Identity: identity, ConversationID: "native", TaskID: "native-task"})
			if err == nil {
				mu.Lock()
				bindings["native"] = binding
				mu.Unlock()
			}
		}
		var output json.RawMessage
		if err == nil {
			if input.Operation == "acquire" {
				output, _ = json.Marshal(binding)
			} else {
				output, err = broker.Dispatch(ctx, binding, input.CommandID, input.Operation, input.Arguments)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_, _ = w.Write(output)
	})
	mux.HandleFunc("GET /qualify/evidence", func(w http.ResponseWriter, r *http.Request) {
		if authorized(w, r) {
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"iscp_operations": counts, "direct_business_attempts": directBusiness, "fences": broker.Fences(identity)})
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		directBusiness++
		mu.Unlock()
		http.Error(w, "business HTTP and WebSocket are unavailable in this fixture", 404)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"fixture_origin": "https://" + listener.Addr().String()})
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	defer httpServer.Close()
	if err = httpServer.ServeTLS(listener, filepath.Join(root, "cert.pem"), filepath.Join(root, "key.pem")); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
