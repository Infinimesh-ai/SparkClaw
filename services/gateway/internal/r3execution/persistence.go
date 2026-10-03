package r3execution

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type control struct {
	Version       int               `json:"schema_version"`
	Installations map[string]string `json:"installations"`
	Fences        map[string]Fence  `json:"fences"`
}
type bundle struct {
	Payload string            `json:"payload"`
	Files   map[string][]byte `json:"files"`
}

func privateDirectory(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("R3 root must be absolute")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	stat, owned := info.Sys().(*syscall.Stat_t)
	if !owned || stat.Uid != uint32(os.Getuid()) {
		return errors.New("R3 root must be owned by the service user")
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("R3 root must be a private directory")
	}
	return nil
}
func readPrivate(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, owned := info.Sys().(*syscall.Stat_t)
	if !owned || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("R3 file ownership mismatch")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("invalid R3 private file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}
func atomicFile(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Service) saveLocked() error {
	raw, err := json.Marshal(s.control)
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(s.root, "control.json"), raw)
}
func (s *Service) contentPath(key string) string {
	return filepath.Join(s.root, "content", Digest([]byte(key))+".sealed")
}
func (s *Service) seal(key string, value bundle) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nonce, nonce, raw, []byte(key))
	return atomicFile(s.contentPath(key), sealed)
}
func (s *Service) unseal(key string) (bundle, error) {
	var value bundle
	raw, err := readPrivate(s.contentPath(key), TaskBytes)
	if err != nil {
		return value, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return value, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return value, err
	}
	if len(raw) < gcm.NonceSize() {
		return value, errors.New("invalid sealed R3 output")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(key))
	if err != nil {
		return value, err
	}
	err = json.Unmarshal(plain, &value)
	return value, err
}
