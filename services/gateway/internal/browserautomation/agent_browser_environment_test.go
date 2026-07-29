package browserautomation

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestAgentBrowserEnvironmentRequiresRealDisplayForVisibleSession(t *testing.T) {
	t.Setenv("DISPLAY", ":19")
	t.Setenv("XAUTHORITY", "/tmp/sparkclaw-test-xauthority")
	t.Setenv("AGENT_BROWSER_NO_XVFB", "false")

	env := environmentMap(agentBrowserEnvironment(
		agentBrowserAdapterConfig{DaemonIdleTimeoutMS: 2500},
		"namespace",
		"session",
		"/tmp/profile",
		"/usr/bin/chromium",
		false,
	))

	for key, want := range map[string]string{
		"DISPLAY":                       ":19",
		"XAUTHORITY":                    "/tmp/sparkclaw-test-xauthority",
		"AGENT_BROWSER_HEADED":          "true",
		"AGENT_BROWSER_NO_XVFB":         "true",
		"AGENT_BROWSER_EXECUTABLE_PATH": "/usr/bin/chromium",
	} {
		if got := env[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if _, exists := env["AGENT_BROWSER_IDLE_TIMEOUT_MS"]; exists {
		t.Fatal("visible sessions must remain open until the owner or runtime closes them")
	}
}

func TestAgentBrowserEnvironmentKeepsHiddenSessionHeadless(t *testing.T) {
	t.Setenv("DISPLAY", ":19")
	t.Setenv("XAUTHORITY", "/tmp/sparkclaw-test-xauthority")
	t.Setenv("AGENT_BROWSER_NO_XVFB", "true")

	env := environmentMap(agentBrowserEnvironment(
		agentBrowserAdapterConfig{},
		"namespace",
		"session",
		"/tmp/profile",
		"",
		true,
	))

	if got := env["AGENT_BROWSER_HEADED"]; got != "false" {
		t.Fatalf("AGENT_BROWSER_HEADED = %q, want false", got)
	}
	if got := env["DISPLAY"]; got != ":19" {
		t.Fatalf("DISPLAY = %q, want inherited display without changing headless mode", got)
	}
	if _, exists := env["AGENT_BROWSER_NO_XVFB"]; exists {
		t.Fatal("hidden sessions must not inherit the visible-display Xvfb policy")
	}
	if got := env["AGENT_BROWSER_IDLE_TIMEOUT_MS"]; got != "60000" {
		t.Fatalf("AGENT_BROWSER_IDLE_TIMEOUT_MS = %q, want 60000", got)
	}
}

func TestPassiveHealthDoesNotStartBrowserSessionOrExposeEnvironmentPaths(t *testing.T) {
	command, err := resolveAgentBrowserCommand("")
	if err != nil {
		t.Skipf("agent-browser is not installed: %v", err)
	}
	chromium, err := resolveChromiumExecutable("")
	if err != nil {
		t.Skipf("system Chromium is not installed: %v", err)
	}
	profileRoot := t.TempDir()
	xauthority := filepath.Join(t.TempDir(), "Xauthority")
	if err := os.WriteFile(xauthority, []byte("authority"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", xauthority)

	adapter := NewAdapter(config.Config{
		Adapters: config.AdapterConfig{BrowserAutomation: config.BrowserAutomationAdapterConfig{
			Command: command, ChromiumExecutable: chromium, ProfileDir: profileRoot,
		}},
	}).(*AgentBrowserAdapter)
	result, err := adapter.Health(context.Background(), map[string]any{
		"owner_id": "owner-a", "browser_profile_id": "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.session != nil || adapter.sessionGeneration != 0 {
		t.Fatalf("passive health started a browser session: session=%#v generation=%d", adapter.session, adapter.sessionGeneration)
	}
	if result.RawTool != "linux_environment_preflight" || result.ProviderSessionRef != "" || len(result.Pages) != 0 {
		t.Fatalf("passive health returned active browser state: %#v", result)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("passive health output is not structured: %#v", result.Output)
	}
	for _, field := range []string{
		"provider_ready", "provider_version_pinned", "chromium_ready", "chromium_arm64",
		"profile_available", "utf8_locale", "cjk_fonts", "display_ready",
		"xauthority_ready", "hidden_ready", "visible_ready",
	} {
		if _, ok := output[field].(bool); !ok {
			t.Fatalf("passive health field %q is not boolean: %#v", field, output[field])
		}
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{profileRoot, xauthority, os.Getenv("DISPLAY")} {
		if sensitive != "" && strings.Contains(string(encoded), sensitive) {
			t.Fatalf("passive health exposed environment path/value %q: %s", sensitive, encoded)
		}
	}
}

func TestResolveVisibleBrowserEnvironmentUsesRealLinuxSocketAndAuthority(t *testing.T) {
	displayNumber := strconv.Itoa(30000 + os.Getpid()%10000)
	socketPath := filepath.Join("/tmp/.X11-unix", "X"+displayNumber)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("cannot create isolated X socket fixture: %v", err)
	}
	defer listener.Close()
	xauthority := filepath.Join(t.TempDir(), "Xauthority")
	if err := os.WriteFile(xauthority, []byte("authority"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPARKCLAW_BROWSER_DISPLAY", ":"+displayNumber)
	t.Setenv("SPARKCLAW_BROWSER_XAUTHORITY", xauthority)

	resolved, err := resolveVisibleBrowserEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.display != ":"+displayNumber || resolved.xauthority != xauthority {
		t.Fatalf("visible environment resolution mismatch: %#v", resolved)
	}
}

func TestBrowserProfileLeaseRejectsContentionAndReleases(t *testing.T) {
	profileDir, err := resolveSharedProfileDir(t.TempDir(), "owner-a\x00work")
	if err != nil {
		t.Fatal(err)
	}
	first, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	if second, err := acquireBrowserProfileLease(profileDir); !errors.Is(err, errBrowserProfileBusy) {
		if second != nil {
			second.release()
		}
		t.Fatalf("second profile lease error = %v, want %v", err, errBrowserProfileBusy)
	}
	first.release()
	reacquired, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatalf("profile lease was not reusable after release: %v", err)
	}
	reacquired.release()
}

func TestBrowserEnvironmentHelpersValidateARM64LocaleAndProfileState(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := browserExecutableArchitecture(executable); got != "aarch64" {
		t.Fatalf("test process architecture = %q, want aarch64", got)
	}
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "")
	if !browserLocaleIsUTF8() {
		t.Fatal("C.UTF-8 locale was rejected")
	}
	t.Setenv("LC_ALL", "C")
	if browserLocaleIsUTF8() {
		t.Fatal("non-UTF-8 locale was accepted")
	}
	profileDir := t.TempDir()
	if browserProfileInitialized(profileDir) {
		t.Fatal("empty managed profile was reported initialized")
	}
	if err := os.WriteFile(filepath.Join(profileDir, "Local State"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !browserProfileInitialized(profileDir) {
		t.Fatal("managed profile state was not detected")
	}
}

func TestBrowserEnvironmentPreflightReportsDetectedArchitecture(t *testing.T) {
	for _, test := range []struct {
		name         string
		architecture string
		want         string
	}{
		{name: "unsupported architecture", architecture: "x86-64", want: "x86-64"},
		{name: "probe failed", want: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := (browserEnvironmentPreflight{
				chromiumReady:        true,
				chromiumArchitecture: test.architecture,
			}).output()
			if got := output["chromium_architecture"]; got != test.want {
				t.Fatalf("chromium_architecture = %q, want %q", got, test.want)
			}
			if output["chromium_arm64"] != false {
				t.Fatalf("failed architecture probe reported ARM64: %#v", output)
			}
			if output["chromium_architecture"] == "aarch64" {
				t.Fatalf("failed preflight falsely reported aarch64: %#v", output)
			}
		})
	}
}

func environmentMap(entries []string) map[string]string {
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}
