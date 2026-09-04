package browserautomation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const hostCDPEndpointMaxBytes = 16 << 10

// HostCDPEndpoint is the validated, capability-scoped browser endpoint
// published by sparkclaw-browserd. Callers must obtain it through
// ReadHostCDPEndpoint rather than decoding the endpoint file directly.
type HostCDPEndpoint struct {
	Version          int    `json:"version"`
	ProfileID        string `json:"profileID"`
	Presentation     string `json:"presentation"`
	BrowserPID       int    `json:"browserPID"`
	Generation       uint64 `json:"generation"`
	BrowserVersion   string `json:"browserVersion"`
	WebSocketURL     string `json:"webSocketURL"`
	HostWebSocketURL string `json:"hostWebSocketURL"`
}

func ReadHostCDPEndpoint(cfg config.HostCDPConfig) (HostCDPEndpoint, error) {
	path := strings.TrimSpace(cfg.EndpointFile)
	if path == "" {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint file is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return HostCDPEndpoint{}, fmt.Errorf("read Host-CDP endpoint: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint must not be accessible by group or other users")
	}
	if info.Size() <= 0 || info.Size() > hostCDPEndpointMaxBytes {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint has an invalid size")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint must be owned by the Gateway runtime user")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return HostCDPEndpoint{}, fmt.Errorf("read Host-CDP endpoint: %w", err)
	}
	var endpoint HostCDPEndpoint
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&endpoint); err != nil {
		return HostCDPEndpoint{}, fmt.Errorf("decode Host-CDP endpoint: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return HostCDPEndpoint{}, errors.New("decode Host-CDP endpoint: trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return HostCDPEndpoint{}, fmt.Errorf("decode Host-CDP endpoint trailing data: %w", err)
	}
	if endpoint.Version != 1 {
		return HostCDPEndpoint{}, fmt.Errorf("unsupported Host-CDP endpoint version %d", endpoint.Version)
	}
	if strings.TrimSpace(endpoint.ProfileID) == "" || endpoint.ProfileID != strings.TrimSpace(cfg.ProfileID) {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint profile does not match configuration")
	}
	if endpoint.BrowserPID <= 0 || endpoint.Generation == 0 {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint browser identity is invalid")
	}
	if strings.TrimSpace(endpoint.BrowserVersion) == "" {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint browser version is missing")
	}
	if endpoint.Presentation != "headed" && endpoint.Presentation != "headless" {
		return HostCDPEndpoint{}, errors.New("Host-CDP endpoint presentation is invalid")
	}
	if err := validateHostCDPWebSocketURL(endpoint.WebSocketURL, false); err != nil {
		return HostCDPEndpoint{}, err
	}
	if err := validateHostCDPWebSocketURL(endpoint.HostWebSocketURL, true); err != nil {
		return HostCDPEndpoint{}, err
	}
	return endpoint, nil
}

func validateHostCDPWebSocketURL(raw string, host bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "ws" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Host-CDP endpoint contains an invalid WebSocket URL")
	}
	expectedHost := "host.docker.internal"
	if host {
		expectedHost = "127.0.0.1"
	}
	if !strings.EqualFold(parsed.Hostname(), expectedHost) || parsed.Port() == "" {
		return errors.New("Host-CDP endpoint WebSocket URL is outside the protected host boundary")
	}
	segments := strings.Split(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 4 || len(segments[0]) < 32 ||
		segments[1] != "devtools" || segments[2] != "browser" || strings.TrimSpace(segments[3]) == "" {
		return errors.New("Host-CDP endpoint capability path is invalid")
	}
	return nil
}

func HostCDPWebSocketURL(endpoint HostCDPEndpoint) string {
	if runningInContainer() {
		return endpoint.WebSocketURL
	}
	return endpoint.HostWebSocketURL
}

// Backward-compatible package-local aliases keep the browser adapter's
// existing call sites compact while exposing the same validated contract to
// the email automation layer.
func readHostCDPEndpoint(cfg config.HostCDPConfig) (HostCDPEndpoint, error) {
	return ReadHostCDPEndpoint(cfg)
}

func hostCDPWebSocketURL(endpoint HostCDPEndpoint) string {
	return HostCDPWebSocketURL(endpoint)
}

func runningInContainer() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}
