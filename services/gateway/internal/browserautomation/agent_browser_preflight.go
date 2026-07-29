package browserautomation

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type visibleBrowserEnvironment struct {
	display    string
	xauthority string
}

type browserEnvironmentPreflight struct {
	ok                    bool
	providerReady         bool
	providerVersionPinned bool
	chromiumReady         bool
	chromiumVersion       string
	chromiumARM64         bool
	profileAvailable      bool
	profileInitialized    bool
	utf8Locale            bool
	cjkFonts              bool
	displayReady          bool
	xauthorityReady       bool
	hiddenReady           bool
	visibleReady          bool
	reasonCodes           []string
}

func (p browserEnvironmentPreflight) output() map[string]any {
	status := "unavailable"
	if p.ok {
		status = "ok"
	}
	return map[string]any{
		"ok":                      p.ok,
		"status":                  status,
		"provider":                "agent-browser",
		"provider_ready":          p.providerReady,
		"provider_version":        agentBrowserVersion,
		"provider_version_pinned": p.providerVersionPinned,
		"chromium_ready":          p.chromiumReady,
		"chromium_version":        p.chromiumVersion,
		"chromium_architecture":   "aarch64",
		"chromium_arm64":          p.chromiumARM64,
		"profile_available":       p.profileAvailable,
		"profile_initialized":     p.profileInitialized,
		"utf8_locale":             p.utf8Locale,
		"cjk_fonts":               p.cjkFonts,
		"display_ready":           p.displayReady,
		"xauthority_ready":        p.xauthorityReady,
		"hidden_ready":            p.hiddenReady,
		"visible_ready":           p.visibleReady,
		"reason_codes":            append([]string(nil), p.reasonCodes...),
	}
}

func inspectBrowserEnvironment(
	ctx context.Context,
	cfg agentBrowserAdapterConfig,
	profileKey string,
	commandPath string,
	profileOwned bool,
	requireVisible bool,
) browserEnvironmentPreflight {
	result := browserEnvironmentPreflight{}
	if err := validateAgentBrowserVersion(ctx, commandPath, cfg.StartupTimeoutMS); err != nil {
		result.reasonCodes = append(result.reasonCodes, "agent_browser_version_unavailable")
	} else {
		result.providerReady = true
		result.providerVersionPinned = true
	}

	chromium, err := resolveChromiumExecutable(cfg.ChromiumExecutable)
	if err != nil {
		result.reasonCodes = append(result.reasonCodes, "system_chromium_unavailable")
	} else {
		result.chromiumReady = true
		if browserExecutableArchitecture(chromium) == "aarch64" {
			result.chromiumARM64 = true
		} else {
			result.reasonCodes = append(result.reasonCodes, "chromium_architecture_unsupported")
		}
		version, versionErr := readChromiumVersion(ctx, chromium, cfg.StartupTimeoutMS)
		if versionErr != nil {
			result.reasonCodes = append(result.reasonCodes, "chromium_version_unavailable")
		} else {
			result.chromiumVersion = version
		}
	}

	profileDir, err := resolveSharedProfileDir(cfg.ProfileDir, profileKey)
	if err != nil {
		result.reasonCodes = append(result.reasonCodes, "browser_profile_unavailable")
	} else {
		result.profileInitialized = browserProfileInitialized(profileDir)
		if profileOwned {
			result.profileAvailable = true
		} else if lease, leaseErr := acquireBrowserProfileLease(profileDir); leaseErr == nil {
			result.profileAvailable = true
			lease.release()
		} else if errors.Is(leaseErr, errBrowserProfileBusy) {
			result.reasonCodes = append(result.reasonCodes, "browser_profile_busy")
		} else {
			result.reasonCodes = append(result.reasonCodes, "browser_profile_unavailable")
		}
	}

	result.utf8Locale = browserLocaleIsUTF8()
	if !result.utf8Locale {
		result.reasonCodes = append(result.reasonCodes, "browser_locale_not_utf8")
	}
	result.cjkFonts = browserCJKFontsAvailable(ctx, cfg.StartupTimeoutMS)
	if !result.cjkFonts {
		result.reasonCodes = append(result.reasonCodes, "browser_cjk_font_unavailable")
	}

	if _, displayErr := resolveVisibleBrowserEnvironment(); displayErr == nil {
		result.displayReady = true
		result.xauthorityReady = true
	} else {
		var environmentErr *visibleEnvironmentError
		if errors.As(displayErr, &environmentErr) {
			result.displayReady = environmentErr.displayReady
			result.xauthorityReady = environmentErr.xauthorityReady
			result.reasonCodes = append(result.reasonCodes, environmentErr.reasonCode)
		} else {
			result.reasonCodes = append(result.reasonCodes, "browser_display_unavailable")
		}
	}

	result.hiddenReady = result.providerReady &&
		result.providerVersionPinned &&
		result.chromiumReady &&
		result.chromiumVersion != "" &&
		result.chromiumARM64 &&
		result.profileAvailable &&
		result.utf8Locale &&
		result.cjkFonts
	result.visibleReady = result.hiddenReady && result.displayReady && result.xauthorityReady
	result.ok = result.hiddenReady && (!requireVisible || result.visibleReady)
	return result
}

type visibleEnvironmentError struct {
	reasonCode      string
	displayReady    bool
	xauthorityReady bool
}

func (e *visibleEnvironmentError) Error() string {
	return e.reasonCode
}

func resolveVisibleBrowserEnvironment() (visibleBrowserEnvironment, error) {
	display := firstNonEmptyEnvironment("SPARKCLAW_BROWSER_DISPLAY", "DISPLAY")
	if display == "" {
		matches, _ := filepath.Glob("/tmp/.X11-unix/X*")
		sockets := make([]string, 0, len(matches))
		for _, candidate := range matches {
			if info, err := os.Stat(candidate); err == nil && info.Mode()&os.ModeSocket != 0 {
				sockets = append(sockets, candidate)
			}
		}
		if len(sockets) == 1 {
			display = ":" + strings.TrimPrefix(filepath.Base(sockets[0]), "X")
		}
	}
	displayNumber, ok := localDisplayNumber(display)
	if !ok {
		return visibleBrowserEnvironment{}, &visibleEnvironmentError{reasonCode: "browser_display_unavailable"}
	}
	socketPath := filepath.Join("/tmp/.X11-unix", "X"+displayNumber)
	const readWriteAccess = 6
	if info, err := os.Stat(socketPath); err != nil || info.Mode()&os.ModeSocket == 0 ||
		syscall.Access(socketPath, readWriteAccess) != nil {
		return visibleBrowserEnvironment{}, &visibleEnvironmentError{reasonCode: "browser_display_unavailable"}
	}

	xauthority := firstNonEmptyEnvironment("SPARKCLAW_BROWSER_XAUTHORITY", "XAUTHORITY")
	if !readableNonemptyFile(xauthority) {
		xauthority = resolveXauthorityCandidate()
	}
	if !readableNonemptyFile(xauthority) {
		return visibleBrowserEnvironment{}, &visibleEnvironmentError{
			reasonCode: "browser_xauthority_unavailable", displayReady: true,
		}
	}
	return visibleBrowserEnvironment{display: display, xauthority: xauthority}, nil
}

func localDisplayNumber(display string) (string, bool) {
	value := strings.TrimSpace(display)
	if !strings.HasPrefix(value, ":") {
		return "", false
	}
	value = strings.TrimPrefix(value, ":")
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		value = value[:dot]
	}
	if value == "" {
		return "", false
	}
	if _, err := strconv.ParseUint(value, 10, 31); err != nil {
		return "", false
	}
	return value, true
}

func resolveXauthorityCandidate() string {
	runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
	if runtimeDir == "" {
		runtimeDir = filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	}
	candidates := []string{
		filepath.Join(runtimeDir, "gdm", "Xauthority"),
		filepath.Join(runtimeDir, "Xauthority"),
	}
	if matches, _ := filepath.Glob(filepath.Join(runtimeDir, ".mutter-Xwaylandauth.*")); len(matches) > 0 {
		candidates = append(candidates, matches...)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidates = append(candidates, filepath.Join(home, ".Xauthority"))
	}
	for _, candidate := range candidates {
		if readableNonemptyFile(candidate) {
			return candidate
		}
	}
	return ""
}

func readableNonemptyFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func firstNonEmptyEnvironment(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func browserExecutableArchitecture(path string) string {
	binary, err := elf.Open(path)
	if err != nil {
		return ""
	}
	defer binary.Close()
	if binary.Machine == elf.EM_AARCH64 && binary.Class == elf.ELFCLASS64 {
		return "aarch64"
	}
	return strings.ToLower(binary.Machine.String())
}

func readChromiumVersion(ctx context.Context, executable string, startupTimeoutMS int) (string, error) {
	timeout := time.Duration(adapterStartupTimeoutMS(startupTimeoutMS)) * time.Millisecond
	versionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := exec.CommandContext(versionCtx, executable, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read Chromium version: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if !strings.HasPrefix(version, "Chromium ") {
		return "", fmt.Errorf("unexpected system Chromium version %q", version)
	}
	return version, nil
}

func browserLocaleIsUTF8() bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := strings.ToUpper(strings.TrimSpace(os.Getenv(key)))
		if value == "" {
			continue
		}
		return strings.Contains(value, "UTF-8") || strings.Contains(value, "UTF8")
	}
	return false
}

func browserCJKFontsAvailable(ctx context.Context, startupTimeoutMS int) bool {
	command, err := exec.LookPath("fc-list")
	if err != nil {
		return false
	}
	timeout := time.Duration(adapterStartupTimeoutMS(startupTimeoutMS)) * time.Millisecond
	fontCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := exec.CommandContext(fontCtx, command, ":lang=zh-cn", "family").Output()
	return err == nil && strings.TrimSpace(string(output)) != ""
}

func browserProfileInitialized(profileDir string) bool {
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != ".sparkclaw-profile.lock" {
			return true
		}
	}
	return false
}
