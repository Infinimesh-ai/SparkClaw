package browserautomation

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const chromiumSingletonProbeTimeout = 250 * time.Millisecond

var chromiumSingletonNames = []string{
	"SingletonLock",
	"SingletonSocket",
	"SingletonCookie",
}

type chromiumSingletonArtifact struct {
	path   string
	target string
}

// recoverStaleChromiumSingletons must run while this lease exclusively owns profileDir.
func (l *browserProfileLease) recoverStaleChromiumSingletons(profileDir string) (bool, error) {
	if l == nil {
		return false, errors.New("recover Chromium profile locks without a profile lease")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return false, errors.New("recover Chromium profile locks with a released profile lease")
	}
	if filepath.Clean(profileDir) != l.profileDir {
		return false, errors.New("recover Chromium profile locks for a different profile")
	}

	artifacts, err := inspectChromiumSingletonArtifacts(profileDir)
	if err != nil || len(artifacts) == 0 {
		return false, err
	}
	if socket, ok := artifacts["SingletonSocket"]; ok {
		active, socketErr := chromiumSingletonSocketActive(profileDir, socket.target)
		if socketErr != nil {
			return false, socketErr
		}
		if active {
			return false, errBrowserProfileBusy
		}
	}
	if lock, ok := artifacts["SingletonLock"]; ok {
		hostname, pid, parseErr := parseChromiumSingletonLock(lock.target)
		if parseErr != nil {
			return false, parseErr
		}
		currentHostname, hostnameErr := os.Hostname()
		if hostnameErr != nil {
			return false, fmt.Errorf("read current hostname for Chromium profile lock: %w", hostnameErr)
		}
		if hostname == currentHostname {
			active, processErr := chromiumSingletonProcessAlive(pid)
			if processErr != nil {
				return false, processErr
			}
			if active {
				return false, errBrowserProfileBusy
			}
		}
	}

	for _, name := range chromiumSingletonNames {
		artifact, ok := artifacts[name]
		if !ok {
			continue
		}
		if err := os.Remove(artifact.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("remove stale Chromium %s: %w", name, err)
		}
	}
	return true, nil
}

func inspectChromiumSingletonArtifacts(profileDir string) (map[string]chromiumSingletonArtifact, error) {
	artifacts := make(map[string]chromiumSingletonArtifact, len(chromiumSingletonNames))
	for _, name := range chromiumSingletonNames {
		path := filepath.Join(profileDir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect Chromium %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("refuse to recover Chromium %s because it is not a symbolic link", name)
		}
		target, err := os.Readlink(path)
		if err != nil {
			return nil, fmt.Errorf("read Chromium %s: %w", name, err)
		}
		if strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("refuse to recover Chromium %s with an empty target", name)
		}
		artifacts[name] = chromiumSingletonArtifact{path: path, target: target}
	}
	return artifacts, nil
}

func chromiumSingletonSocketActive(profileDir, target string) (bool, error) {
	socketPath := target
	if !filepath.IsAbs(socketPath) {
		socketPath = filepath.Join(profileDir, socketPath)
	}
	info, err := os.Stat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect Chromium SingletonSocket target: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return false, errors.New("refuse to recover Chromium SingletonSocket whose target is not a Unix socket")
	}
	conn, err := net.DialTimeout("unix", socketPath, chromiumSingletonProbeTimeout)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("probe Chromium SingletonSocket owner: %w", err)
}

func parseChromiumSingletonLock(target string) (string, int, error) {
	separator := strings.LastIndexByte(target, '-')
	if separator <= 0 || separator == len(target)-1 {
		return "", 0, fmt.Errorf("refuse to recover malformed Chromium SingletonLock target %q", target)
	}
	pid, err := strconv.Atoi(target[separator+1:])
	if err != nil || pid <= 0 {
		return "", 0, fmt.Errorf("refuse to recover malformed Chromium SingletonLock target %q", target)
	}
	return target[:separator], pid, nil
}

func chromiumSingletonProcessAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, fmt.Errorf("probe Chromium SingletonLock process %d: %w", pid, err)
	}
}
