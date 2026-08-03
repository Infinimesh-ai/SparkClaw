package browserautomation

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestAgentBrowserEnvironmentRequiresRealDisplayForVisibleSession(t *testing.T) {
	t.Setenv("DISPLAY", ":19")
	t.Setenv("XAUTHORITY", "/tmp/sparkclaw-test-xauthority")
	t.Setenv("AGENT_BROWSER_NO_XVFB", "false")

	env := environmentMap(agentBrowserEnvironmentResolved(
		agentBrowserAdapterConfig{DaemonIdleTimeoutMS: 2500},
		"namespace",
		"session",
		"/tmp/profile",
		"/usr/bin/chromium",
		false,
		&visibleBrowserEnvironment{display: ":19", xauthority: "/tmp/sparkclaw-test-xauthority"},
	))

	for key, want := range map[string]string{
		"DISPLAY":                       ":19",
		"XAUTHORITY":                    "/tmp/sparkclaw-test-xauthority",
		"AGENT_BROWSER_HEADED":          "true",
		"AGENT_BROWSER_NO_XVFB":         "true",
		"AGENT_BROWSER_EXECUTABLE_PATH": "/usr/bin/chromium",
		// Visible sessions get a generous idle bound (6x the hidden one) so an
		// abandoned visible Chromium does not survive until gateway exit.
		"AGENT_BROWSER_IDLE_TIMEOUT_MS": "15000",
	} {
		if got := env[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestAgentBrowserEnvironmentKeepsHiddenSessionHeadless(t *testing.T) {
	t.Setenv("DISPLAY", ":19")
	t.Setenv("XAUTHORITY", "/tmp/sparkclaw-test-xauthority")
	t.Setenv("AGENT_BROWSER_NO_XVFB", "true")

	env := environmentMap(agentBrowserEnvironmentResolved(
		agentBrowserAdapterConfig{},
		"namespace",
		"session",
		"/tmp/profile",
		"",
		true,
		nil,
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
	if got := env["AGENT_BROWSER_IDLE_TIMEOUT_MS"]; got != "1200000" {
		t.Fatalf("AGENT_BROWSER_IDLE_TIMEOUT_MS = %q, want 1200000", got)
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
	profileDir, err := resolveSharedProfileDir(profileRoot, "owner-a\x00work")
	if err != nil {
		t.Fatal(err)
	}
	writeChromiumSingletonSymlinks(
		t,
		profileDir,
		"retired-container-424242",
		filepath.Join(t.TempDir(), "missing", "SingletonSocket"),
	)
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
	if len(adapter.entries) != 0 {
		t.Fatalf("passive health started a browser session: entries=%#v", adapter.entries)
	}
	assertChromiumSingletonsPresent(t, profileDir)
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

func TestResolveVisibleBrowserEnvironmentSkipsUnreadableAuthorityOverride(t *testing.T) {
	displayNumber := strconv.Itoa(40000 + os.Getpid()%10000)
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
	t.Setenv("SPARKCLAW_BROWSER_XAUTHORITY", "/dev/null")
	t.Setenv("XAUTHORITY", xauthority)

	resolved, err := resolveVisibleBrowserEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.xauthority != xauthority {
		t.Fatalf("xauthority = %q, want readable fallback %q", resolved.xauthority, xauthority)
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

func TestBrowserExecutableArchitectureResolvesChromiumLauncher(t *testing.T) {
	installRoot := t.TempDir()
	launcherPath := filepath.Join(installRoot, "bin", "chromium")
	binaryPath := filepath.Join(installRoot, "lib", "chromium", "chromium")
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexec /usr/lib/chromium/chromium \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeELFExecutableFixture(t, binaryPath, elf.EM_AARCH64)

	if got := browserExecutableArchitecture(launcherPath); got != "aarch64" {
		t.Fatalf("Chromium launcher architecture = %q, want aarch64", got)
	}
}

func TestBrowserEnvironmentHelpersValidateARM64LocaleAndProfileState(t *testing.T) {
	fixtureDir := t.TempDir()
	arm64Binary := filepath.Join(fixtureDir, "chromium-arm64")
	writeELFExecutableFixture(t, arm64Binary, elf.EM_AARCH64)
	if got := browserExecutableArchitecture(arm64Binary); got != "aarch64" {
		t.Fatalf("aarch64 ELF fixture architecture = %q, want aarch64", got)
	}
	amd64Binary := filepath.Join(fixtureDir, "chromium-amd64")
	writeELFExecutableFixture(t, amd64Binary, elf.EM_X86_64)
	if got := browserExecutableArchitecture(amd64Binary); got != "x86-64" {
		t.Fatalf("x86-64 ELF fixture architecture = %q, want x86-64", got)
	}
	if runtime.GOARCH == "arm64" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if got := browserExecutableArchitecture(executable); got != "aarch64" {
			t.Fatalf("test process architecture = %q, want aarch64", got)
		}
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

func TestBrowserEnvironmentPreflightArchitectureIsAdvisory(t *testing.T) {
	preflight := browserEnvironmentPreflight{
		providerReady:         true,
		providerVersionPinned: true,
		chromiumReady:         true,
		chromiumVersion:       "Chromium 126.0.6478.126",
		chromiumArchitecture:  "x86-64",
		warningCodes:          []string{"chromium_architecture_unexpected"},
		profileAvailable:      true,
		utf8Locale:            true,
		cjkFonts:              true,
	}
	preflight.finalize(false)
	if !preflight.hiddenReady || !preflight.ok {
		t.Fatalf("non-arm64 Chromium gated hidden readiness: %#v", preflight)
	}
	output := preflight.output()
	if output["ok"] != true || output["status"] != "ok" {
		t.Fatalf("non-arm64 preflight status = %#v", output)
	}
	if output["chromium_arm64"] != false || output["chromium_architecture"] != "x86-64" {
		t.Fatalf("detected architecture reporting was lost: %#v", output)
	}
	warnings, ok := output["warning_codes"].([]string)
	if !ok || len(warnings) != 1 || warnings[0] != "chromium_architecture_unexpected" {
		t.Fatalf("warning_codes = %#v, want [chromium_architecture_unexpected]", output["warning_codes"])
	}
	if reasons, _ := output["reason_codes"].([]string); len(reasons) != 0 {
		t.Fatalf("advisory architecture leaked into reason_codes: %#v", reasons)
	}

	preflight.finalize(true)
	if preflight.ok || preflight.visibleReady {
		t.Fatalf("missing display no longer gates visible readiness: %#v", preflight)
	}
}

func TestNewAdapterUsesJSONSafeSessionGenerationSeed(t *testing.T) {
	adapter, ok := NewAdapter(config.Config{}).(*AgentBrowserAdapter)
	if !ok {
		t.Fatal("NewAdapter did not return an AgentBrowserAdapter")
	}
	const maxExactJSONInteger = uint64(1<<53 - 1)
	if adapter.nextGeneration == 0 || adapter.nextGeneration > maxExactJSONInteger {
		t.Fatalf("session generation seed = %d, want a nonzero JSON-safe integer", adapter.nextGeneration)
	}
}

func TestRequestTimeoutMSReservesAgentBrowserTransportHeadroom(t *testing.T) {
	if got := requestTimeoutMS(context.Background(), 30000); got != 25000 {
		t.Fatalf("request timeout = %dms, want 25000ms", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12000*time.Millisecond)
	defer cancel()
	if got := requestTimeoutMS(ctx, 30000); got < 6900 || got > 7000 {
		t.Fatalf("deadline-bounded request timeout = %dms, want about 7000ms", got)
	}
}

// writeELFExecutableFixture writes a minimal but valid 64-bit little-endian ELF
// header (no program or section headers) so browserExecutableArchitecture can
// probe the machine field deterministically on every host platform.
func writeELFExecutableFixture(t *testing.T, path string, machine elf.Machine) {
	t.Helper()
	header := make([]byte, 64)
	copy(header, elf.ELFMAG)
	header[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	header[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	header[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(header[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(header[18:], uint16(machine))
	binary.LittleEndian.PutUint32(header[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint16(header[52:], 64) // e_ehsize
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, header, 0o755); err != nil {
		t.Fatal(err)
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
