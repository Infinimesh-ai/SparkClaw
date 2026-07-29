package browserautomation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var errBrowserProfileBusy = errors.New("browser_profile_busy")

type browserProfileLease struct {
	mu   sync.Mutex
	file *os.File
}

func acquireBrowserProfileLease(profileDir string) (*browserProfileLease, error) {
	lockPath := filepath.Join(filepath.Dir(profileDir), ".sparkclaw-profile.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open browser profile lease: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errBrowserProfileBusy
		}
		return nil, fmt.Errorf("acquire browser profile lease: %w", err)
	}
	return &browserProfileLease{file: file}, nil
}

func (l *browserProfileLease) release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
