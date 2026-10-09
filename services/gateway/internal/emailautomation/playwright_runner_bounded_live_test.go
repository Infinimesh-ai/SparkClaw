package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
)

// This opt-in live qualification captures one exact discovery target. It never
// sends mail or calls first-unread / mark-read, and prints no mailbox content.
func TestPlaywrightExtensionBoundedOriginal(t *testing.T) {
	providerID := os.Getenv("SPARKCLAW_TEST_BOUNDED_PROVIDER")
	if providerID == "" {
		t.Skip("requires explicit provider and a bounded historical interval")
	}
	provider, ok := DefaultRegistry().Get(providerID)
	if !ok {
		t.Fatal("invalid provider")
	}
	start, e1 := time.Parse(time.RFC3339, os.Getenv("SPARKCLAW_TEST_BOUNDED_START"))
	end, e2 := time.Parse(time.RFC3339, os.Getenv("SPARKCLAW_TEST_BOUNDED_END"))
	if e1 != nil || e2 != nil || !start.Before(end) || end.Sub(start) > 24*time.Hour || end.After(time.Now()) {
		t.Fatal("requires a past interval of at most 24 hours")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cfg, err := config.Load(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if err != nil {
		t.Fatal("load configuration")
	}
	runtime, err := newPlaywrightEmailLiveStoreRuntime(ctx, cfg)
	if err != nil {
		t.Fatal("open state backend")
	}
	defer runtime.Close(context.Background())
	vault := credential.New(runtime.CredentialRepository(), credential.Options{Key: cfg.State.CredentialKey, KeyFile: cfg.State.CredentialKeyFile})
	if vault.Ready() != nil {
		t.Fatal("credential vault unavailable")
	}
	ext := cfg.Adapters.BrowserAutomation.PlaywrightExtension
	client, err := browsercontrol.NewHTTPControllerClient(ext.ControllerSocket, 15*time.Second)
	if err != nil {
		t.Fatal("controller client unavailable")
	}
	defer client.Close()
	controller := browsercontrol.New(vault, client, ext.ProfileID)
	controller.Initialize(ctx)
	defer controller.Close()
	// Initialize reads the saved credential generation; each runner operation
	// authenticates it normally. An exclusive credential revalidation would
	// conflict with the production resident observers during this live check.
	status := controller.Status(ctx)
	if !status.Configured || status.CredentialGeneration <= 0 {
		t.Fatal("configured credential generation required")
	}
	runner := NewPlaywrightRunner(&liveEmailController{Service: controller, t: t})
	probe, err := runner.Probe(ctx, provider, app.NewID("bounded_probe"), uint64(status.CredentialGeneration))
	if err != nil {
		t.Fatalf("probe: %s", ErrorCode(err))
	}
	scope := sha256.Sum256([]byte("email-management-qualification"))
	req := ReadRequest{Provider: providerID, Account: app.EmailAccountDefault, OwnerScope: hex.EncodeToString(scope[:]), InvocationID: app.NewID("bounded_identity"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision}
	bootstrap, err := runner.Discover(ctx, provider, req)
	if err != nil {
		t.Fatalf("bootstrap: %s", ErrorCode(err))
	}
	if report := os.Getenv("SPARKCLAW_TEST_ACCOUNT_REPORT"); report != "" {
		raw, _ := json.Marshal(map[string]string{"provider": providerID, "account_address": bootstrap.AccountAddress})
		if os.WriteFile(report, raw, 0600) != nil {
			t.Fatal("private account report failed")
		}
	}
	req.InvocationID = app.NewID("bounded_interval")
	req.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: bootstrap.AccountAddress, IntervalStart: start, IntervalEnd: end, Limit: 50, ProviderMode: "time_range"}
	interval, err := runner.Discover(ctx, provider, req)
	if err != nil {
		t.Fatalf("interval: %s", ErrorCode(err))
	}
	t.Logf("bounded discovery: provider=%s candidates=%d scanned=%d complete=%t qualified=%t limited=%t", providerID, len(interval.Candidates), interval.Coverage.ScannedRows, interval.Coverage.ScanComplete, interval.Coverage.BoundaryQualified, interval.Coverage.Limited)
	if len(interval.Candidates) == 0 {
		t.Fatal("no original target in the explicitly bounded interval")
	}
	target := interval.Candidates[0]
	req.InvocationID = app.NewID("bounded_original")
	req.Discovery = nil
	req.Target = &target
	req.ScriptRevision = provider.Capture.Revision
	result, err := runner.Read(ctx, provider, req)
	if err != nil {
		t.Fatalf("capture: %s", ErrorCode(err))
	}
	workspace := os.Getenv("SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT")
	if verifyCapture(ctx, workspace, req, result) != nil {
		t.Fatal("capture contract verification failed")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		t.Fatal("capture root unavailable")
	}
	defer root.Close()
	raw, err := root.ReadFile(result.Capture.ManifestPath)
	if err != nil {
		t.Fatal("capture manifest unavailable")
	}
	var manifest struct {
		Files []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		}
	}
	if json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("capture manifest invalid")
	}
	var total int64
	for _, f := range manifest.Files {
		handle, err := root.Open(f.Path)
		if err != nil {
			t.Fatal("capture source unavailable")
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(handle, maxCaptureFileBytes+1))
		handle.Close()
		if err != nil || n != f.Bytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != f.SHA256 {
			t.Fatal("capture source integrity failed")
		}
		total += n
	}
	t.Logf("bounded original: provider=%s status=%s attachments=%d read_state=%s files=%d bytes=%d all_sha256_verified=true", providerID, result.Status, result.Capture.AttachmentsCount, result.Capture.ReadState, len(manifest.Files), total)
}
