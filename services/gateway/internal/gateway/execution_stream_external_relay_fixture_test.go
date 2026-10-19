package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func independentNativeRelay(t *testing.T, ctx context.Context, origin string) http.Handler {
	t.Helper()
	binary := os.Getenv("SPARKCLAW_STREAM_FIXTURE_RELAY_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute independently built Relay fixture required")
	}
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = append(os.Environ(), "ISCP_NATIVE_FIXTURE_BASE_URL="+origin)
	cmd.Stderr = os.Stderr
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var ready struct {
		URL string `json:"relay_url"`
	}
	if err = json.NewDecoder(output).Decode(&ready); err != nil {
		t.Fatal("independent Relay startup", err)
	}
	target, err := url.Parse(ready.URL)
	if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || target.User != nil {
		t.Fatal("invalid independent Relay address")
	}
	return httputil.NewSingleHostReverseProxy(target)
}

func bootstrapIndependentNativeRelay(t *testing.T, ctx context.Context) {
	t.Helper()
	binary := os.Getenv("SPARKCLAW_STREAM_FIXTURE_MAINTENANCE_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute independently built Relay maintenance required")
	}
	history := make([]byte, 32)
	if _, err := rand.Read(history); err != nil {
		t.Fatal(err)
	}
	evidence := sha256.Sum256([]byte("new disposable never-served native qualification domain:" + os.Getenv("SPARKCLAW_STREAM_FIXTURE_ROOT")))
	cmd := exec.CommandContext(ctx, binary, "bootstrap-empty", "--relay-id", "stream-native-relay", "--domain-id", "stream-native-domain", "--history-id", base64.RawURLEncoding.EncodeToString(history), "--evidence-sha256", base64.RawURLEncoding.EncodeToString(evidence[:]), "--confirm-never-served")
	cmd.Env = append(os.Environ(), "ISCP_DATABASE_URL="+os.Getenv("SPARKCLAW_STREAM_FIXTURE_DSN"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("independent new-domain bootstrap: %v %s", err, output)
	}
}
