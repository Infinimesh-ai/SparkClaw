package browserautomation

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserProfileLeaseRecoversStaleChromiumSingletonsFromRetiredContainer(t *testing.T) {
	profileDir := t.TempDir()
	socketPath := filepath.Join(t.TempDir(), "missing", "SingletonSocket")
	writeChromiumSingletonSymlinks(t, profileDir, "retired-container-424242", socketPath)

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	recovered, err := lease.recoverStaleChromiumSingletons(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("stale Chromium singleton locks were not recovered")
	}
	assertChromiumSingletonsAbsent(t, profileDir)
}

func TestBrowserProfileLeasePreservesLiveSameHostChromiumOwner(t *testing.T) {
	profileDir := t.TempDir()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	writeChromiumSingletonSymlinks(
		t,
		profileDir,
		fmt.Sprintf("%s-%d", hostname, os.Getpid()),
		filepath.Join(t.TempDir(), "missing", "SingletonSocket"),
	)

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if recovered, err := lease.recoverStaleChromiumSingletons(profileDir); recovered || !errors.Is(err, errBrowserProfileBusy) {
		t.Fatalf("live Chromium owner recovery = (%t, %v), want (false, %v)", recovered, err, errBrowserProfileBusy)
	}
	assertChromiumSingletonsPresent(t, profileDir)
}

func TestBrowserProfileLeaseRecoversDeadSameHostChromiumOwner(t *testing.T) {
	deadProcess := exec.Command(os.Args[0], "-test.run=^$")
	if err := deadProcess.Run(); err != nil {
		t.Fatal(err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	profileDir := t.TempDir()
	writeChromiumSingletonSymlinks(
		t,
		profileDir,
		fmt.Sprintf("%s-%d", hostname, deadProcess.Process.Pid),
		filepath.Join(t.TempDir(), "missing", "SingletonSocket"),
	)

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	recovered, err := lease.recoverStaleChromiumSingletons(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("dead same-host Chromium singleton locks were not recovered")
	}
	assertChromiumSingletonsAbsent(t, profileDir)
}

func TestBrowserProfileLeasePreservesReachableChromiumSocket(t *testing.T) {
	profileDir := t.TempDir()
	socketPath := filepath.Join(t.TempDir(), "SingletonSocket")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	writeChromiumSingletonSymlinks(t, profileDir, "other-container-424242", socketPath)

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if recovered, err := lease.recoverStaleChromiumSingletons(profileDir); recovered || !errors.Is(err, errBrowserProfileBusy) {
		t.Fatalf("reachable Chromium socket recovery = (%t, %v), want (false, %v)", recovered, err, errBrowserProfileBusy)
	}
	assertChromiumSingletonsPresent(t, profileDir)
}

func TestBrowserProfileLeaseRefusesUnexpectedChromiumSingletonFile(t *testing.T) {
	profileDir := t.TempDir()
	lockPath := filepath.Join(profileDir, "SingletonLock")
	if err := os.WriteFile(lockPath, []byte("do-not-delete"), 0o600); err != nil {
		t.Fatal(err)
	}

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	recovered, err := lease.recoverStaleChromiumSingletons(profileDir)
	if recovered || err == nil || !strings.Contains(err.Error(), "not a symbolic link") {
		t.Fatalf("unexpected Chromium singleton recovery = (%t, %v)", recovered, err)
	}
	if data, readErr := os.ReadFile(lockPath); readErr != nil || string(data) != "do-not-delete" {
		t.Fatalf("unexpected Chromium singleton file changed: data=%q err=%v", data, readErr)
	}
}

func TestBrowserProfileLeaseRefusesMalformedChromiumLock(t *testing.T) {
	profileDir := t.TempDir()
	writeChromiumSingletonSymlinks(
		t,
		profileDir,
		"retired-container-not-a-pid",
		filepath.Join(t.TempDir(), "missing", "SingletonSocket"),
	)

	lease, err := acquireBrowserProfileLease(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	recovered, err := lease.recoverStaleChromiumSingletons(profileDir)
	if recovered || err == nil || !strings.Contains(err.Error(), "malformed Chromium SingletonLock") {
		t.Fatalf("malformed Chromium lock recovery = (%t, %v)", recovered, err)
	}
	assertChromiumSingletonsPresent(t, profileDir)
}

func writeChromiumSingletonSymlinks(t *testing.T, profileDir, lockTarget, socketTarget string) {
	t.Helper()
	for name, target := range map[string]string{
		"SingletonLock":   lockTarget,
		"SingletonSocket": socketTarget,
		"SingletonCookie": "1234567890",
	} {
		if err := os.Symlink(target, filepath.Join(profileDir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func assertChromiumSingletonsAbsent(t *testing.T, profileDir string) {
	t.Helper()
	for _, name := range chromiumSingletonNames {
		if _, err := os.Lstat(filepath.Join(profileDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still exists after recovery: %v", name, err)
		}
	}
}

func assertChromiumSingletonsPresent(t *testing.T, profileDir string) {
	t.Helper()
	for _, name := range chromiumSingletonNames {
		if _, err := os.Lstat(filepath.Join(profileDir, name)); err != nil {
			t.Fatalf("%s was removed while the owner was live: %v", name, err)
		}
	}
}
