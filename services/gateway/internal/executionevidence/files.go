package executionevidence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func scopeKey(scope Scope) (string, error) {
	for _, id := range []string{scope.OwnerID, scope.SessionID, scope.RunID} {
		if !validID(id) {
			return "", ErrScope
		}
	}
	return digest([]byte(scope.OwnerID + "\x00" + scope.SessionID + "\x00" + scope.RunID)), nil
}
func validID(id string) bool {
	if id == "" || len(id) > 256 || id == "." || id == ".." {
		return false
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("_.:-", ch)) {
			return false
		}
	}
	return true
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("execution evidence random source unavailable")
	}
	return hex.EncodeToString(raw[:])
}

// rootLock serializes short storage mutations across recorder instances and
// processes. Export releases this lock after freezing its manifest and events.
func (r *Recorder) rootLock(ctx context.Context, create bool) (*os.Root, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if r == nil || r.root == "" {
		return nil, nil, ErrUnbound
	}
	if create {
		if err := os.MkdirAll(r.root, 0700); err != nil {
			return nil, nil, err
		}
	}
	info, err := os.Lstat(r.root)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, ErrIntegrity
	}
	root, err := os.OpenRoot(r.root)
	if err != nil {
		return nil, nil, err
	}
	flags := os.O_RDWR | syscall.O_NOFOLLOW
	if create {
		flags |= os.O_CREATE
	}
	lock, err := root.OpenFile(".lock", flags, 0600)
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			lock.Close()
			root.Close()
			return nil, nil, err
		}
		select {
		case <-ctx.Done():
			lock.Close()
			root.Close()
			return nil, nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	return root, func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); _ = lock.Close(); _ = root.Close() }, nil
}

func safePath(root *os.Root, name string, allowMissing bool) error {
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return ErrIntegrity
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if errors.Is(err, fs.ErrNotExist) && allowMissing {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrIntegrity
		}
		if i < len(parts)-1 && !info.IsDir() {
			return ErrIntegrity
		}
	}
	return nil
}
func safeOpen(root *os.Root, name string) (*os.File, error) {
	if err := safePath(root, name, false); err != nil {
		return nil, err
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrIntegrity
	}
	return file, nil
}
func safeMkdir(root *os.Root, name string) error {
	if err := safePath(root, name, true); err != nil {
		return err
	}
	return root.MkdirAll(name, 0700)
}
func readBounded(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := safeOpen(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, ErrLimit
	}
	return raw, nil
}
func atomicWrite(root *os.Root, name string, raw []byte) error {
	if err := safePath(root, name, true); err != nil {
		return err
	}
	tmp := filepath.ToSlash(filepath.Dir(name)) + "/.tmp-" + randomID()
	file, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = root.Rename(tmp, name); err != nil {
		return err
	}
	dir, err := root.Open(filepath.ToSlash(filepath.Dir(name)))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func atomicJSON(root *os.Root, name string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(root, name, append(raw, '\n'))
}
func storageBytes(ctx context.Context, root *os.Root) (int64, error) {
	var total int64
	entries := 0
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 200000 {
			return ErrLimit
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrIntegrity
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrIntegrity
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// quota tracks conservative reservations. A crash between reservation and
// publication consumes space rather than undercounting it. No automatic history
// deletion or quota reset is performed. Only the first use reconciles the tree.
type quota struct {
	Used int64 `json:"reserved_bytes"`
}

func (r *Recorder) quota(root *os.Root, ctx context.Context) (quota, error) {
	raw, err := readBounded(root, ".quota.json", 4096)
	if errors.Is(err, fs.ErrNotExist) {
		used, scanErr := storageBytes(ctx, root)
		if scanErr != nil {
			return quota{}, scanErr
		}
		q := quota{Used: used}
		return q, atomicJSON(root, ".quota.json", q)
	}
	if err != nil {
		return quota{}, err
	}
	var q quota
	if json.Unmarshal(raw, &q) != nil || q.Used < 0 {
		return quota{}, ErrIntegrity
	}
	return q, nil
}
func (r *Recorder) remainingSpace(ctx context.Context, root *os.Root, m Manifest) (int64, error) {
	var used int64 = m.EventBytes
	for _, p := range m.Payloads {
		used += p.Bytes
	}
	q, err := r.quota(root, ctx)
	if err != nil {
		return 0, err
	}
	return min(r.opts.MaxRunBytes-used, r.opts.MaxTotalBytes-q.Used-4096), nil
}
func (r *Recorder) checkSpace(ctx context.Context, root *os.Root, m Manifest, additional int64) error {
	if additional < 0 {
		return ErrLimit
	}
	remaining, err := r.remainingSpace(ctx, root, m)
	if err != nil {
		return err
	}
	if additional > remaining {
		return fmt.Errorf("%w: retained bytes", ErrLimit)
	}
	q, err := r.quota(root, ctx)
	if err != nil {
		return err
	}
	// Include a conservative allowance for metadata growth and atomic replacement.
	q.Used += additional + 4096
	return atomicJSON(root, ".quota.json", q)
}

// releaseReservation returns only the unused portion of this locked operation's
// reservation. A crash or failed refund leaves a conservative overcount.
func (r *Recorder) releaseReservation(root *os.Root, ctx context.Context, unused int64) error {
	if unused <= 0 {
		return nil
	}
	q, err := r.quota(root, ctx)
	if err != nil {
		return err
	}
	if unused > q.Used {
		return ErrIntegrity
	}
	q.Used -= unused
	return atomicJSON(root, ".quota.json", q)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
