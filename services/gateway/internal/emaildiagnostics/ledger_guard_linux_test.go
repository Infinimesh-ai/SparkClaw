package emaildiagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func init() {
	guardTestWorker = func() {
		if len(os.Args) != 3 {
			return
		}
		switch os.Args[1] {
		case "--test-handle-filter":
			runtime.LockOSThread()
			if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
				os.Exit(1)
			}
			if err := denyLedgerEscape(); err != nil {
				os.Exit(1)
			}
			for _, call := range []uintptr{unix.SYS_NAME_TO_HANDLE_AT, unix.SYS_OPEN_BY_HANDLE_AT} {
				_, _, errno := unix.Syscall6(call, ^uintptr(0), 0, 0, 0, 0, 0)
				if errno != unix.EPERM {
					fmt.Fprintf(os.Stderr, "handle syscall %d was not denied: %v\n", call, errno)
					os.Exit(1)
				}
			}
			os.Exit(0)
		case "--test-occupied-pid-namespace":
			child := exec.Command("/bin/sleep", "30")
			if err := child.Start(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			code, _ := RunLedgerGuard([]string{linuxLedgerGuardArgument, os.Args[2]})
			_ = child.Process.Kill()
			_ = child.Wait()
			os.Exit(code)
		case "--test-launch-ledger":
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd, err := ledgerCommand(ctx, os.Args[2])
			if err == nil {
				cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
				cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
				err = cmd.Run()
			}
			if err != nil {
				fmt.Println(`{"schema_version":1,"tasks":[],"gaps":["ledger_write_guard_unavailable"],"rows_read":0,"bytes_read":0}`)
			}
			os.Exit(0)
		case "--test-probe-ledger-guard":
			if err := linuxLedgerGuard(os.Args[2], ledgerReaderSource+linuxWriteProbe); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
}

func TestLinuxLedgerGuardReadsUserOwnedPrivateSource(t *testing.T) {
	requireLinuxGuardQualification(t)
	testEmbeddedLedgerReader(t, true)
}

func TestLinuxLedgerGuardDeniesHandleEscapes(t *testing.T) {
	requireLinuxGuardQualification(t)
	self, _ := os.Executable()
	cmd := exec.CommandContext(t.Context(), self, "--test-handle-filter", "unused")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
}

func TestLinuxLedgerGuardRejectsExistingPIDNamespaceBeforeSourceOpen(t *testing.T) {
	requireLinuxGuardQualification(t)
	file := filepath.Join(t.TempDir(), "ledger.sqlite")
	if err := os.WriteFile(file, []byte("retained evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	watch, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(watch)
	if _, err := unix.InotifyAddWatch(watch, file, unix.IN_OPEN|unix.IN_ACCESS|unix.IN_MODIFY|unix.IN_ATTRIB); err != nil {
		t.Fatal(err)
	}
	cmd, err := ledgerCommand(t.Context(), "node")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Args[1] = "--test-occupied-pid-namespace"
	input, _ := json.Marshal(map[string]any{"path": file, "owner": "owner", "principal": "principal", "owner_scope": digestString("owner"), "operations": []ledgerOperation{{RequestKey: "fixture"}}})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var result ledgerResult
	if json.Unmarshal(output, &result) != nil || !reflect.DeepEqual(result.Gaps, []string{"ledger_write_guard_unavailable"}) {
		t.Fatalf("existing PID namespace was accepted: %s", output)
	}
	var events [4096]byte
	if n, err := unix.Read(watch, events[:]); n > 0 || err != unix.EAGAIN {
		t.Fatalf("source accessed in existing PID namespace: %d %v", n, err)
	}
}

const linuxWriteProbe = `
const probe = JSON.parse(fs.readFileSync(0, 'utf8')), denied = [];
for (const file of probe.files) {
  for (const action of [()=>fs.writeFileSync(file, 'changed'), ()=>fs.appendFileSync(file, 'changed'),
    ()=>fs.chmodSync(file, 0o666), ()=>fs.unlinkSync(file), ()=>fs.renameSync(file, file+'.renamed')]) {
    try {action(); denied.push('allowed');} catch(error) {denied.push(error.code);}
  }
}
for (const target of [probe.root+'/new-file', probe.root+'/index/new-alias']) {
  try {fs.writeFileSync(target,'changed');denied.push('allowed');} catch(error) {denied.push(error.code);}
}
// A host PID must not be reachable through procfs magic links. The unguarded
// parent keeps this source open, so the old procfs would expose a writable fd.
let hostFDReachable = false;
try {fs.statSync('/proc/'+probe.pid+'/fd/'+probe.fd); hostFDReachable = true;} catch {}
const capabilities = Object.fromEntries(['CapEff','CapPrm','CapBnd','CapInh','CapAmb'].map(key =>
  [key,new RegExp('^'+key+':\\s+([0-9a-f]+)$','mu').exec(kernelText('/proc/self/status'))?.[1]]));
const readable = probe.files.every(file => fs.readFileSync(file,'utf8') === 'retained evidence');
process.stdout.write(JSON.stringify({denied, hostFDReachable, readable, capabilities, guarded:await guardedProcess()}));
`

func requireLinuxGuardQualification(t *testing.T) {
	t.Helper()
	if os.Getenv("SPARKCLAW_TEST_LINUX_LEDGER_GUARD") != "1" {
		t.Skip("requires an isolated privileged Linux qualification container")
	}
}

func TestLinuxLedgerGuardKernelDeniesWritesAndHostProcEscape(t *testing.T) {
	testLinuxLedgerGuardWrites(t, false)
}

func TestLinuxLedgerGuardUserOwnedSourcesRemainWriteDenied(t *testing.T) {
	testLinuxLedgerGuardWrites(t, true)
}

func testLinuxLedgerGuardWrites(t *testing.T, userOwned bool) {
	requireLinuxGuardQualification(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "index"), 0700); err != nil {
		t.Fatal(err)
	}
	files := []string{}
	for _, name := range []string{"ledger.sqlite", "ledger.sqlite-wal", "ledger.sqlite-shm", "authority.json", "index/alias.json", "index/head.json"} {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte("retained evidence"), 0600); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	if userOwned {
		for _, file := range append([]string{dir, filepath.Join(dir, "index")}, files...) {
			if err := os.Chown(file, 1000, 1000); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Keep a host fd open to make the procfs escape test meaningful.
	fd, err := os.OpenFile(files[0], os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	before := linuxEvidenceSnapshot(t, dir)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := ledgerCommand(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Args[1] = "--test-probe-ledger-guard"
	input, _ := json.Marshal(map[string]any{"root": dir, "files": files, "pid": os.Getpid(), "fd": fd.Fd()})
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native guard probe: %v %s", err, output)
	}
	var result struct {
		Denied          []string          `json:"denied"`
		HostFDReachable bool              `json:"hostFDReachable"`
		Guarded         bool              `json:"guarded"`
		Readable        bool              `json:"readable"`
		Capabilities    map[string]string `json:"capabilities"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err, string(output))
	}
	if len(result.Denied) != 32 || result.HostFDReachable || !result.Guarded || !result.Readable {
		t.Fatalf("guard not proved: %s", output)
	}
	for _, key := range []string{"CapEff", "CapPrm", "CapBnd", "CapInh", "CapAmb"} {
		expected := uint32(0)
		if key != "CapInh" && key != "CapAmb" {
			expected = uint32(1) << unix.CAP_DAC_READ_SEARCH
		}
		if result.Capabilities[key] != fmt.Sprintf("%016x", expected) || expected&(uint32(1)<<unix.CAP_DAC_OVERRIDE) != 0 {
			t.Fatalf("unexpected %s mask: %s", key, output)
		}
	}
	for _, code := range result.Denied {
		if code != "EROFS" && !(userOwned && (code == "EACCES" || code == "EPERM")) {
			t.Fatalf("source write was not denied by read-only mounts: %s", output)
		}
	}
	if after := linuxEvidenceSnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("source bytes, modes, times or inventory changed")
	}
	// A child remount must never make the parent mount read-only.
	if err := os.WriteFile(filepath.Join(dir, "parent-still-writable"), []byte("fixture"), 0600); err != nil {
		t.Fatalf("mount isolation leaked to parent: %v", err)
	}
	t.Logf("32 write/chmod/unlink/rename/create attempts denied (user-owned=%t); exact read/search capability only; host proc fd inaccessible; parent mount unchanged", userOwned)
}

func linuxEvidenceSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		var content []byte
		if info.Mode().IsRegular() {
			content, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		stat := info.Sys().(*syscall.Stat_t)
		result[rel] = fmt.Sprintf("%d/%d/%d/%d/%d/%x", stat.Uid, stat.Gid, info.Mode(), info.Size(), info.ModTime().UnixNano(), content)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLinuxLedgerGuardUnavailableNeverOpensSourceOrStartsNode(t *testing.T) {
	dir, err := os.MkdirTemp("", "ledger-unprivileged-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(dir, "ledger.sqlite")
	marker := filepath.Join(dir, "node-started")
	fakeNode := filepath.Join(dir, "node")
	if err := os.WriteFile(ledger, []byte("retained evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fakeNode, []byte("#!/bin/sh\nprintf started > '"+marker+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	watch, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(watch)
	if _, err = unix.InotifyAddWatch(watch, ledger, unix.IN_OPEN|unix.IN_ACCESS|unix.IN_MODIFY|unix.IN_ATTRIB); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	// Copy the test helper outside Go's private build directory so the
	// unprivileged run fails at namespace setup, not executable traversal.
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "test-helper")
	if err := os.WriteFile(helper, raw, 0755); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{linuxLedgerGuardArgument, "--test-launch-ledger"} {
		cmd := exec.CommandContext(t.Context(), helper, mode, fakeNode)
		if os.Geteuid() == 0 {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
		}
		cmd.Stdin = bytes.NewBufferString(`{"path":"` + ledger + `"}`)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(output))
		}
		var result ledgerResult
		if json.Unmarshal(output, &result) != nil || !reflect.DeepEqual(result.Gaps, []string{"ledger_write_guard_unavailable"}) || result.RowsRead != 0 || result.BytesRead != 0 {
			t.Fatalf("unexpected unavailable result: %s", output)
		}
	}
	var events [4096]byte
	if n, err := unix.Read(watch, events[:]); n > 0 || err != unix.EAGAIN {
		t.Fatalf("source was accessed before a guard existed: %d %v", n, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Node was started before a guard existed")
	}
}
