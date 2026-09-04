package browserautomation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const validHostCDPEndpointJSON = `{
  "version": 1,
  "profileID": "default",
  "presentation": "headed",
  "browserPID": 4242,
  "generation": 9,
  "browserVersion": "148.0.7778.0",
  "webSocketURL": "ws://host.docker.internal:18791/abcdefghijklmnopqrstuvwxyz123456/devtools/browser/browser-id",
  "hostWebSocketURL": "ws://127.0.0.1:18791/abcdefghijklmnopqrstuvwxyz123456/devtools/browser/browser-id"
}`

func writeHostCDPEndpointFixture(t *testing.T, raw string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cdp-endpoint")
	if err := os.WriteFile(path, []byte(raw), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadHostCDPEndpointAcceptsProtectedRuntimeFile(t *testing.T) {
	path := writeHostCDPEndpointFixture(t, validHostCDPEndpointJSON, 0o600)
	endpoint, err := readHostCDPEndpoint(config.HostCDPConfig{EndpointFile: path, ProfileID: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.BrowserPID != 4242 || endpoint.Generation != 9 || endpoint.Presentation != "headed" {
		t.Fatalf("unexpected endpoint: %#v", endpoint)
	}
}

func TestReadHostCDPEndpointRejectsUnsafeFilesAndValues(t *testing.T) {
	t.Run("group readable", func(t *testing.T) {
		path := writeHostCDPEndpointFixture(t, validHostCDPEndpointJSON, 0o640)
		if _, err := readHostCDPEndpoint(config.HostCDPConfig{EndpointFile: path, ProfileID: "default"}); err == nil || !strings.Contains(err.Error(), "group or other") {
			t.Fatalf("group-readable endpoint error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		link := filepath.Join(dir, "link")
		if err := os.WriteFile(target, []byte(validHostCDPEndpointJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := readHostCDPEndpoint(config.HostCDPConfig{EndpointFile: link, ProfileID: "default"}); err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("symlink endpoint error = %v", err)
		}
	})

	t.Run("profile mismatch", func(t *testing.T) {
		path := writeHostCDPEndpointFixture(t, validHostCDPEndpointJSON, 0o600)
		if _, err := readHostCDPEndpoint(config.HostCDPConfig{EndpointFile: path, ProfileID: "other"}); err == nil || !strings.Contains(err.Error(), "profile") {
			t.Fatalf("profile mismatch error = %v", err)
		}
	})

	t.Run("unprotected host", func(t *testing.T) {
		raw := strings.Replace(validHostCDPEndpointJSON, "host.docker.internal", "192.168.1.10", 1)
		path := writeHostCDPEndpointFixture(t, raw, 0o600)
		if _, err := readHostCDPEndpoint(config.HostCDPConfig{EndpointFile: path, ProfileID: "default"}); err == nil || !strings.Contains(err.Error(), "protected host boundary") {
			t.Fatalf("unprotected host error = %v", err)
		}
	})

	t.Run("trailing JSON", func(t *testing.T) {
		path := writeHostCDPEndpointFixture(t, validHostCDPEndpointJSON+"\n{}\n", 0o600)
		if _, err := readHostCDPEndpoint(config.HostCDPConfig{EndpointFile: path, ProfileID: "default"}); err == nil || !strings.Contains(err.Error(), "trailing") {
			t.Fatalf("trailing JSON error = %v", err)
		}
	})
}
