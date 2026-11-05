package docindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// sourceSnapshot exists only while one build parses an admitted source. It is
// private, read-only, inside the owning workspace, and never an original cache.
type sourceSnapshot struct {
	Path      string
	original  string
	identity  os.FileInfo
	directory string
	sum       string
	size      int64
}

func snapshotSource(ctx context.Context, sourcePath, indexDir, expectedSHA string, expectedSize int64) (*sourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, before, err := openSource(sourcePath, MaxSourceBytes)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	if before.Size() != expectedSize {
		return nil, errors.New("document source changed before snapshot")
	}
	directory, err := os.MkdirTemp(indexDir, ".source-")
	if err != nil {
		return nil, err
	}
	snapshot := &sourceSnapshot{original: sourcePath, identity: before, directory: directory, sum: expectedSHA, size: expectedSize}
	snapshot.Path = filepath.Join(directory, "source"+filepath.Ext(sourcePath))
	complete := false
	defer func() {
		if !complete {
			_ = snapshot.Close()
		}
	}()
	// The write descriptor may initialize a 0400 file; parsers only receive its
	// name after this descriptor is closed. No parser writes the governed source.
	output, err := os.OpenFile(snapshot.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	size, copyErr := copySource(ctx, io.MultiWriter(output, digest), source, expectedSize)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	copyErr = errors.Join(copyErr, output.Close())
	if copyErr != nil {
		return nil, copyErr
	}
	if size != expectedSize || hex.EncodeToString(digest.Sum(nil)) != expectedSHA {
		return nil, errors.New("document source changed while creating snapshot")
	}
	if err := sameSourceIdentity(sourcePath, before); err != nil {
		return nil, err
	}
	complete = true
	return snapshot, nil
}

func (snapshot *sourceSnapshot) Verify(ctx context.Context) error {
	actual, size, err := fileSHA256Context(ctx, snapshot.Path, MaxSourceBytes)
	if err != nil {
		return fmt.Errorf("verify document parser snapshot: %w", err)
	}
	if actual != snapshot.sum || size != snapshot.size {
		return errors.New("document parser snapshot changed during structure extraction")
	}
	if err := sameSourceIdentity(snapshot.original, snapshot.identity); err != nil {
		return err
	}
	actual, size, err = fileSHA256Context(ctx, snapshot.original, MaxSourceBytes)
	if err != nil {
		return fmt.Errorf("verify document source: %w", err)
	}
	if actual != snapshot.sum || size != snapshot.size {
		return errors.New("document source changed during structure extraction")
	}
	return nil
}
func (snapshot *sourceSnapshot) Close() error { return os.RemoveAll(snapshot.directory) }

func openSource(path string, limit int64) (*os.File, os.FileInfo, error) {
	named, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !named.Mode().IsRegular() || named.Size() > limit {
		return nil, nil, errors.New("document source exceeds regular-file structure admission")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > limit || !os.SameFile(named, opened) {
		_ = file.Close()
		return nil, nil, errors.New("document source identity changed before read")
	}
	return file, opened, nil
}
func sameSourceIdentity(path string, expected os.FileInfo) error {
	named, err := os.Lstat(path)
	if err != nil || !named.Mode().IsRegular() || !os.SameFile(named, expected) {
		return errors.New("document source identity changed during structure extraction")
	}
	return nil
}

// copySource checks cancellation between fixed-size regular-file reads, and
// rejects growth before writing a byte beyond its admitted limit.
func copySource(ctx context.Context, output io.Writer, input io.Reader, limit int64) (int64, error) {
	buffer := make([]byte, 64<<10)
	total := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, readErr := input.Read(buffer)
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if int64(count) > limit-total {
			return total, errors.New("document source exceeds structure byte admission")
		}
		if count > 0 {
			written, err := output.Write(buffer[:count])
			total += int64(written)
			if err != nil {
				return total, err
			}
			if written != count {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}
func fileSHA256Context(ctx context.Context, path string, limit int64) (string, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	file, before, err := openSource(path, limit)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := copySource(ctx, digest, file, limit)
	if err != nil {
		return "", 0, err
	}
	if err := sameSourceIdentity(path, before); err != nil {
		return "", 0, err
	}
	if size != before.Size() {
		return "", 0, fmt.Errorf("document source size changed during hash")
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

// Kept for package fixtures. Production paths provide their own context.
func fileSHA256(path string) (string, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return fileSHA256Context(ctx, path, MaxSourceBytes)
}

// WithSourceSnapshot admits and verifies a fixed source for format work such as
// targeted page rendering. The callback must consume bytes synchronously; the
// temporary copy is removed before this operation returns.
func WithSourceSnapshot(ctx context.Context, target Target, consume func(context.Context, string) error) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	path, err := targetPath(target)
	if err != nil {
		return err
	}
	sum, size, err := fileSHA256Context(ctx, path, MaxSourceBytes)
	if err != nil {
		return err
	}
	if target.SourceSHA256 == "" || sum != target.SourceSHA256 {
		return errors.New("document source version changed before snapshot")
	}
	dir := Dir(target.WorkspaceRoot, sum)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	snapshot, err := snapshotSource(ctx, path, dir, sum, size)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, snapshot.Close()) }()
	if err = consume(ctx, snapshot.Path); err != nil {
		return err
	}
	return snapshot.Verify(ctx)
}
