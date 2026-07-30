package browserautomation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reclaimProfileFixture(t *testing.T) (string, string) {
	t.Helper()
	profileDir := filepath.Join(t.TempDir(), "user-data")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Unix socket paths must stay below the platform sun_path limit, so bind
	// a short os.MkdirTemp directory instead of the test-named TempDir.
	socketDir, err := os.MkdirTemp("", "sc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	return profileDir, filepath.Join(socketDir, "SingletonSocket")
}

// writeDaemonCloseStub creates a fake agent-browser command that records its
// arguments and namespace, and simulates the daemon shutting Chromium down by
// removing the profile's singleton artifacts.
func writeDaemonCloseStub(t *testing.T, profileDir string, removeSingletons bool) (command, argsFile, namespaceFile string) {
	t.Helper()
	dir := t.TempDir()
	command = filepath.Join(dir, "agent-browser-stub")
	argsFile = filepath.Join(dir, "args")
	namespaceFile = filepath.Join(dir, "namespace")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$*\" > %q\nprintf '%%s' \"$AGENT_BROWSER_NAMESPACE\" > %q\n", argsFile, namespaceFile)
	if removeSingletons {
		script += fmt.Sprintf("rm -f %q %q %q\n",
			filepath.Join(profileDir, "SingletonLock"),
			filepath.Join(profileDir, "SingletonSocket"),
			filepath.Join(profileDir, "SingletonCookie"),
		)
	}
	if err := os.WriteFile(command, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return command, argsFile, namespaceFile
}

func TestReclaimLeakedBrowserProfileClosesRecordedDaemonAndReprobes(t *testing.T) {
	profileDir, socketPath := reclaimProfileFixture(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	writeChromiumSingletonSymlinks(t, profileDir, "retired-container-424242", socketPath)
	recordBrowserDaemonOwner(profileDir, "ns-recorded", "sc-recorded")
	command, argsFile, namespaceFile := writeDaemonCloseStub(t, profileDir, true)

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if _, err := lease.recoverStaleChromiumSingletons(profileDir); !errors.Is(err, errBrowserProfileBusy) {
		t.Fatalf("reachable daemon socket recovery error = %v, want %v", err, errBrowserProfileBusy)
	}
	if err := reclaimLeakedBrowserProfile(context.Background(), lease, command, profileDir, "ns-current", "sc-current"); err != nil {
		t.Fatalf("reclaim after daemon close = %v, want success", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil || strings.TrimSpace(string(args)) != "close --session sc-recorded --json" {
		t.Fatalf("daemon close arguments = %q err=%v", args, err)
	}
	namespace, err := os.ReadFile(namespaceFile)
	if err != nil || strings.TrimSpace(string(namespace)) != "ns-recorded" {
		t.Fatalf("daemon close namespace = %q err=%v, want recorded owner namespace", namespace, err)
	}
	assertChromiumSingletonsAbsent(t, profileDir)
}

func TestReclaimLeakedBrowserProfileFallsBackToDeterministicIdentity(t *testing.T) {
	profileDir, socketPath := reclaimProfileFixture(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	writeChromiumSingletonSymlinks(t, profileDir, "retired-container-424242", socketPath)
	command, argsFile, namespaceFile := writeDaemonCloseStub(t, profileDir, true)

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if err := reclaimLeakedBrowserProfile(context.Background(), lease, command, profileDir, "ns-current", "sc-current"); err != nil {
		t.Fatalf("reclaim without an owner marker = %v, want fallback identity attempt", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil || strings.TrimSpace(string(args)) != "close --session sc-current --json" {
		t.Fatalf("fallback close arguments = %q err=%v", args, err)
	}
	namespace, err := os.ReadFile(namespaceFile)
	if err != nil || strings.TrimSpace(string(namespace)) != "ns-current" {
		t.Fatalf("fallback close namespace = %q err=%v", namespace, err)
	}
}

func TestReclaimLeakedBrowserProfileStaysBusyWhenDaemonSurvivesClose(t *testing.T) {
	profileDir, socketPath := reclaimProfileFixture(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	writeChromiumSingletonSymlinks(t, profileDir, "retired-container-424242", socketPath)
	command, _, _ := writeDaemonCloseStub(t, profileDir, false)

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if err := reclaimLeakedBrowserProfile(context.Background(), lease, command, profileDir, "ns-current", "sc-current"); !errors.Is(err, errBrowserProfileBusy) {
		t.Fatalf("reclaim with a surviving daemon = %v, want %v", err, errBrowserProfileBusy)
	}
	assertChromiumSingletonsPresent(t, profileDir)
}

func TestBrowserDaemonOwnerMarkerRoundTrip(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "user-data")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok := readBrowserDaemonOwner(profileDir); ok {
		t.Fatal("missing owner marker must read as absent")
	}
	recordBrowserDaemonOwner(profileDir, "ns-a", "sc-a")
	owner, ok := readBrowserDaemonOwner(profileDir)
	if !ok || owner.Namespace != "ns-a" || owner.Session != "sc-a" {
		t.Fatalf("owner marker round trip = %#v ok=%t", owner, ok)
	}
	if err := os.WriteFile(browserDaemonOwnerPath(profileDir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readBrowserDaemonOwner(profileDir); ok {
		t.Fatal("malformed owner marker must read as absent")
	}
}
