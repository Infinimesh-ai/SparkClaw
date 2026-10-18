package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscplocalenroll"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscplocalissuer"
	ref "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscprelayreference"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	iscpconfig "github.com/Infinimesh-ai/ISCP/pkg/iscp/config"
	"github.com/Infinimesh-ai/ISCP/pkg/server/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nativeStreamBatch struct {
	Batch      int     `json:"batch"`
	Bytes      int     `json:"bytes"`
	Digest     string  `json:"digest"`
	ProducedMS float64 `json:"produced_unix_ms"`
}
type nativeStreamRun struct {
	RequestID   string              `json:"request_id"`
	Batches     []nativeStreamBatch `json:"batches"`
	CompletedMS float64             `json:"completed_unix_ms,omitempty"`
	StartedMS   float64             `json:"started_unix_ms,omitempty"`
	FinalBytes  int                 `json:"final_bytes,omitempty"`
	FinalDigest string              `json:"final_digest,omitempty"`
	Failed      bool                `json:"failed,omitempty"`
}

// Opt-in disposable fixture. Controlled mode substitutes the producer; real
// model mode calls the production Agent workflow with explicitly supplied model
// configuration. Service, Gateway, encrypted Endpoint, issuer and continuous
// PostgreSQL Relay are production code. No business HTTP route exists.
func TestExecutionStreamNativeQualificationFixture(t *testing.T) {
	root := os.Getenv("SPARKCLAW_STREAM_FIXTURE_ROOT")
	if root == "" {
		t.Skip("opt-in continuous Relay/helper/native renderer qualification")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !filepath.IsAbs(root) || !strings.HasPrefix(root, "/private/tmp/") {
		t.Fatal("private disposable root required")
	}
	dsn, token := os.Getenv("SPARKCLAW_STREAM_FIXTURE_DSN"), os.Getenv("SPARKCLAW_STREAM_FIXTURE_TOKEN")
	if dsn == "" || token == "" {
		t.Fatal("isolated database and private fixture token required")
	}
	repetitions := 1
	if value := os.Getenv("SPARKCLAW_STREAM_FIXTURE_REPETITIONS"); value != "" {
		repetitions, err = strconv.Atoi(value)
		if err != nil || (repetitions != 1 && repetitions != 10) {
			t.Fatal("controlled fixture repetitions must be 1 or 10")
		}
	}
	batchText := strings.Repeat("流式输出验证", repetitions)
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(startup, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migrations, err := postgres.EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = postgres.ApplyMigrations(startup, pool, migrations); err != nil {
		t.Fatal(err)
	}
	if err = ref.PrepareDatabase(startup, pool); err != nil {
		t.Fatal(err)
	}
	listenAddress := "127.0.0.1:0"
	if value := os.Getenv("SPARKCLAW_STREAM_FIXTURE_LISTEN"); value != "" {
		host, port, splitErr := net.SplitHostPort(value)
		if splitErr != nil || host != "0.0.0.0" || port == "0" {
			t.Fatal("explicit disposable container listen address invalid")
		}
		listenAddress = value
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	if listenAddress != "127.0.0.1:0" {
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		origin = "http://127.0.0.1:" + port
	}
	relay, err := ref.New(ref.Config{SignerFile: filepath.Join(root, "relay-signer.json"), DomainID: "stream-native-domain", RelayID: "stream-native-relay", BaseURL: origin, WebSocketURL: strings.Replace(origin, "http:", "ws:", 1) + "/v2/relay/connect", ProfileGate: iscpconfig.DefaultGate(iscpconfig.ProfileLocalLab), DB: pool})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	var mu sync.Mutex
	runs := map[string]*nativeStreamRun{}
	executionStarts := map[string]int{}
	operations := map[string]int{}
	modelMetadata := map[string]any{"kind": "controlled", "batch_interval_ms": 100, "chars_per_batch": 6 * repetitions}
	connects, directBusiness := 0, 0
	nowMS := func() float64 { return float64(time.Now().UnixNano()) / 1e6 }
	evidence := func() map[string]any {
		return map[string]any{"runs": runs, "execution_starts": executionStarts, "iscp_operations": operations, "relay_connect_requests": connects, "direct_business_attempts": directBusiness, "model": modelMetadata}
	}
	relayHandler := relay.Handler()
	httpServer := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fixture/") {
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "fixture authorization required", 403)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/fixture/clock" {
				_ = json.NewEncoder(w).Encode(map[string]float64{"unix_ms": nowMS()})
				return
			}
			if r.URL.Path == "/fixture/evidence" {
				mu.Lock()
				defer mu.Unlock()
				_ = json.NewEncoder(w).Encode(evidence())
				return
			}
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		if r.URL.Path == "/v2/relay/connect" {
			connects++
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			directBusiness++
		}
		mu.Unlock()
		relayHandler.ServeHTTP(w, r)
	})}
	defer httpServer.Close()
	go func() { _ = httpServer.Serve(listener) }()
	write := func(name string, value any) {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(root, name), raw, 0600) != nil {
			t.Fatal("private fixture write failed", name, err)
		}
	}
	peers := map[string]iscplocalenroll.Summary{}
	for _, name := range []string{"desktop", "gateway"} {
		peer, err := iscplocalenroll.Enroll(startup, iscplocalenroll.Options{RelayURL: origin, RelayID: "stream-native-relay", DomainID: "stream-native-domain", DeviceID: "stream-native-" + name, IdentityDirectory: filepath.Join(root, name), EnrollmentFile: filepath.Join(root, name, "enrollment.json")})
		if err != nil {
			t.Fatal(err)
		}
		peers[name] = peer
	}
	issuerDir := filepath.Join(root, "issuer")
	if _, err = iscplocalissuer.Initialize(issuerDir, peers["desktop"].IdentityFile, peers["gateway"].IdentityFile, "stream-native-relay"); err != nil {
		t.Fatal(err)
	}
	issuer, err := iscplocalissuer.Load(filepath.Join(issuerDir, "issuer.json"))
	if err != nil {
		t.Fatal(err)
	}
	grant, err := issuer.Sign(30 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	write("grant.json", grant)
	scopes := []string{}
	qualified := []string{}
	for _, op := range wb.OperationRegistry() {
		scopes = append(scopes, op.Scope)
		qualified = append(qualified, op.Name)
	}
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	if err = issuer.AuthorizePermanentScopes(filepath.Join(root, "grant.json"), scopes); err != nil {
		t.Fatal(err)
	}
	issuerHTTP := httptest.NewUnstartedServer(issuer.Handler())
	if value := os.Getenv("SPARKCLAW_STREAM_FIXTURE_ISSUER_LISTEN"); value != "" {
		host, port, splitErr := net.SplitHostPort(value)
		if splitErr != nil || host != "0.0.0.0" || port == "0" {
			t.Fatal("explicit disposable container issuer address invalid")
		}
		issuerHTTP.Listener.Close()
		issuerHTTP.Listener, err = net.Listen("tcp", value)
		if err != nil {
			t.Fatal(err)
		}
	}
	issuerHTTP.Start()
	defer issuerHTTP.Close()
	issuerOrigin := issuerHTTP.URL
	if os.Getenv("SPARKCLAW_STREAM_FIXTURE_ISSUER_LISTEN") != "" {
		_, port, _ := net.SplitHostPort(issuerHTTP.Listener.Addr().String())
		issuerOrigin = "http://127.0.0.1:" + port
	}
	binding := &wb.Binding{DeploymentID: "stream-native", OwnerID: "owner", ClientID: "stream-native-client"}
	profiles := map[string]wb.Config{}
	for _, name := range []string{"desktop", "gateway"} {
		role, other := wb.RoleInitiator, "gateway"
		if name == "gateway" {
			role, other = wb.RoleResponder, "desktop"
		}
		profile := wb.Config{SchemaVersion: 1, Mode: "local-test", Role: role, RelayProfile: "local-lab", IdentityDirectory: filepath.Join(root, name), IdentityKeyBackend: "file", EnrollmentFile: peers[name].EnrollmentFile, PeerIdentityFile: peers[other].IdentityFile, IssuerIdentityFile: filepath.Join(issuerDir, "issuer.identity.json"), GrantFile: filepath.Join(root, "grant.json"), Permission: "sparkclaw.workbench.v1", Binding: binding, ApplicationProfiles: []string{wb.ProfileV2, wb.Profile}, QualifiedCapabilities: qualified, GrantRenewal: &wb.GrantRenewalConfig{URL: issuerOrigin, AuthorizationLifetime: "until_revoked", PendingFile: filepath.Join(root, name, "pending.json"), PollIntervalSeconds: 10}}
		write(name+"-helper.json", profile)
		profiles[name] = profile
	}
	public, err := profiles["desktop"].PublicIdentity()
	if err != nil {
		t.Fatal(err)
	}
	write("desktop-profile.json", map[string]any{"schema_version": 1, "transport": "iscp", "test_mode": true, "helper_config": filepath.Join(root, "desktop-helper.json"), "backend": map[string]any{"schema_version": 3, "transport": "iscp", "origin": "https://iscp.invalid", "deployment_id": binding.DeploymentID, "owner_id": binding.OwnerID, "client_id": binding.ClientID, "relay_profile": "local-lab", "domain_id": public.DomainID, "initiator_device_id": public.InitiatorDeviceID, "responder_device_id": public.ResponderDeviceID, "responder_key_thumbprint": public.ResponderKeyThumbprint, "relay_url": origin, "test_mode": true}})
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.WorkbenchISCPLocalTest = true
	cfg.Gateway.DeploymentID = binding.DeploymentID
	realModel := os.Getenv("SPARKCLAW_STREAM_FIXTURE_MODE") == "real-model"
	if realModel {
		modelConfig, loadErr := config.Load(os.Getenv("SPARKCLAW_STREAM_FIXTURE_MODEL_CONFIG"))
		if loadErr != nil || modelConfig.Model.Mock {
			t.Fatal("explicit non-mock model configuration required")
		}
		// Copy only the requested model configuration. All business state,
		// tool configuration and identity remain inside this disposable lab.
		cfg.Model = modelConfig.Model
		modelMetadata = map[string]any{"kind": "production_agent", "mock": false, "fast_model": cfg.Model.Fast.Model, "deep_model": cfg.Model.Deep.Model}
	}
	repository, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.RegisterClient(ctx, app.Client{ID: binding.ClientID, OwnerID: binding.OwnerID, ActorID: binding.OwnerID, Name: "isolated stream fixture", TokenHash: hashSecret("unused-business-http-token")}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, repository)
	defer tools.Close()
	execute := func(ctx context.Context, e execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		run := &nativeStreamRun{RequestID: e.RequestID, Batches: []nativeStreamBatch{}}
		mu.Lock()
		executionStarts[e.RequestID]++
		runs[e.RequestID] = run
		mu.Unlock()
		text := ""
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for batch := 1; batch <= 100; batch++ {
			select {
			case <-ctx.Done():
				return execution.Output{}, ctx.Err()
			case <-ticker.C:
			}
			text += batchText
			produced := nowMS()
			execution.PublishAnswer(ctx, batchText)
			mu.Lock()
			run.Batches = append(run.Batches, nativeStreamBatch{Batch: batch, Bytes: len(text), Digest: execution.Digest([]byte(text)), ProducedMS: produced})
			mu.Unlock()
		}
		mu.Lock()
		run.CompletedMS = nowMS()
		mu.Unlock()
		return execution.Output{Content: text}, nil
	}
	var server *Server
	runtime := agent.Runtime{}
	if realModel {
		runtime = agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), nil)
		execute = func(ctx context.Context, e execution.Envelope, inputs map[string][]byte) (execution.Output, error) {
			run := &nativeStreamRun{RequestID: e.RequestID, StartedMS: nowMS(), Batches: []nativeStreamBatch{}}
			mu.Lock()
			executionStarts[e.RequestID]++
			runs[e.RequestID] = run
			mu.Unlock()
			output, err := server.executeWorkbenchWorkflow(ctx, e, inputs)
			mu.Lock()
			run.CompletedMS, run.FinalBytes, run.FinalDigest, run.Failed = nowMS(), len(output.Content), execution.Digest([]byte(output.Content)), err != nil
			mu.Unlock()
			return output, err
		}
	}
	server = New(cfg, repository, tools, runtime, WithExecutions(filepath.Join(root, "execution"), execute))
	server.BindLifecycleContext(ctx)
	defer func() {
		if server.executions != nil {
			server.executions.Close()
		}
	}()
	handler, err := server.NewWorkbenchISCPHandler(profiles["gateway"])
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := wb.NewEndpoint(profiles["gateway"], func(ctx context.Context, r wb.Request) wb.Response {
		mu.Lock()
		operations[r.Operation]++
		mu.Unlock()
		return handler(ctx, r)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	go func() { _ = endpoint.Run(ctx) }()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"fixture_origin": origin, "profile": filepath.Join(root, "desktop-profile.json")})
	<-ctx.Done()
	mu.Lock()
	write("source-evidence.json", evidence())
	mu.Unlock()
	fmt.Fprintln(os.Stdout, "stream native fixture stopped")
}
