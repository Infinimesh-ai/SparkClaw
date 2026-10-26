package emaildiagnostics

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const linuxLedgerGuardArgument = "--internal-linux-ledger-reader"
const linuxLedgerReadCapability = uint32(1) << unix.CAP_DAC_READ_SEARCH

func ledgerCommand(ctx context.Context, node string) (*exec.Cmd, error) {
	node, err := exec.LookPath(node)
	if err != nil {
		return nil, err
	}
	node, err = filepath.Abs(node)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, self, linuxLedgerGuardArgument, node)
	// A fresh PID namespace and procfs prevent /proc/<host-pid>/root magic
	// links from escaping the read-only mount view. No user namespace or
	// privilege escalation is attempted: unavailable capabilities fail closed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNS | unix.CLONE_NEWPID | unix.CLONE_NEWNET | unix.CLONE_NEWIPC}
	return cmd, nil
}

func ledgerCommandFailure() string { return "ledger_write_guard_unavailable" }

// RunLedgerGuard is called before normal CLI handling. It never opens a ledger
// or accepts reader code from arguments. Namespace setup must succeed before
// the embedded reader and its dynamic loader start.
func RunLedgerGuard(args []string) (int, bool) {
	if len(args) == 0 || args[0] != linuxLedgerGuardArgument {
		return 0, false
	}
	if len(args) == 2 && filepath.IsAbs(args[1]) {
		_ = linuxLedgerGuard(args[1], ledgerReaderSource+"\nawait runCLI();")
	}
	_, _ = fmt.Fprintln(os.Stdout, `{"schema_version":1,"tasks":[],"gaps":["ledger_write_guard_unavailable"],"rows_read":0,"bytes_read":0}`)
	return 0, true
}

func linuxLedgerGuard(node, source string) error {
	runtime.LockOSThread()
	// Do not unlock: after a failed setup this thread must only exit. The
	// caller immediately exits the private worker, never resumes report work.
	if os.Getpid() != 1 {
		return unix.EPERM
	}
	// Even a direct invocation as an existing container's PID 1 must first
	// get its own mount namespace; no remount can leak into its caller.
	if err := unix.Unshare(unix.CLONE_NEWNS | unix.CLONE_NEWNET | unix.CLONE_NEWIPC); err != nil {
		return err
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return err
	}
	if err := unix.Mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, ""); err != nil {
		return err
	}
	if err := requireEmptyPIDNamespace(); err != nil {
		return err
	}
	if err := unix.MountSetattr(unix.AT_FDCWD, "/", unix.AT_RECURSIVE, &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY | unix.MOUNT_ATTR_NODEV | unix.MOUNT_ATTR_NOSUID}); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	for capability := uintptr(0); capability < 64; capability++ {
		if capability == unix.CAP_DAC_READ_SEARCH {
			continue
		}
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, capability, 0, 0, 0); err != nil && err != unix.EINVAL {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return err
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	// The operator may be root while evidence belongs to an application user
	// with 0700 directories and 0600 files. Retain only read/search DAC bypass,
	// never CAP_DAC_OVERRIDE, which also bypasses write permissions.
	data := [2]unix.CapUserData{{Effective: linuxLedgerReadCapability, Permitted: linuxLedgerReadCapability}}
	if err := unix.Capset(&header, &data[0]); err != nil {
		return err
	}
	if err := denyLedgerEscape(); err != nil {
		return err
	}
	// Derive the Node verification mask from the same kernel constant.
	source = fmt.Sprintf("const LINUX_LEDGER_READ_CAPABILITY = %dn;\n", linuxLedgerReadCapability) + source
	return unix.Exec(node, []string{node, "--no-warnings", "--input-type=module", "--eval", source, "--", "--guarded-reader"}, []string{"PATH=" + os.Getenv("PATH")})
}

// A caller may invoke the private flag as PID 1 of an existing container.
// Reject its old children: their proc root/fd links could point at writable
// mounts. Threads are under /proc/1/task, not separate top-level proc entries.
func requireEmptyPIDNamespace() error {
	dir, err := os.Open("/proc")
	if err != nil {
		return err
	}
	defer dir.Close()
	for seen := 0; seen < 4096; seen += 64 {
		names, err := dir.Readdirnames(64)
		for _, name := range names {
			if pid, parseErr := strconv.Atoi(name); parseErr == nil && pid != 1 {
				return unix.EPERM
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return unix.E2BIG
}

func denyLedgerEscape() error {
	var architecture uint32
	switch runtime.GOARCH {
	case "amd64":
		architecture = unix.AUDIT_ARCH_X86_64
	case "arm64":
		architecture = unix.AUDIT_ARCH_AARCH64
	default:
		return unix.ENOTSUP
	}
	filters := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4}, // seccomp_data.arch
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: architecture, Jt: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}, // syscall number
		// Reject the x32 ABI as well as unexpected syscall number ranges.
		{Code: unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K, K: 0x40000000, Jf: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
	}
	// Read-only mounts do not stop UNIX sockets from commanding another
	// process. Nor should a reader regain mount privileges in a user namespace.
	for _, number := range []uint32{unix.SYS_SOCKET, unix.SYS_SOCKETPAIR, unix.SYS_IO_URING_SETUP,
		// CAP_DAC_READ_SEARCH must not open inodes via handles that bypass the
		// guarded mount tree. Readers have no need to obtain such handles either.
		unix.SYS_NAME_TO_HANDLE_AT, unix.SYS_OPEN_BY_HANDLE_AT,
		unix.SYS_UNSHARE, unix.SYS_SETNS, unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_MOUNT_SETATTR,
		unix.SYS_FSOPEN, unix.SYS_FSCONFIG, unix.SYS_FSMOUNT, unix.SYS_MOVE_MOUNT, unix.SYS_PIVOT_ROOT} {
		filters = append(filters,
			unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: number, Jf: 1},
			unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)})
	}
	filters = append(filters, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0)
	runtime.KeepAlive(filters)
	return err
}
