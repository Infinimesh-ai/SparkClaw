package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

// Domain receipts are separate from execution content. A persisted intent is an
// uncertainty fence: a process loss after it never starts the mutation twice.
// Completed responses are encrypted because settings may contain private data.
type iscpDomainReceipts struct {
	root     string
	aead     cipher.AEAD
	mu       sync.Mutex
	reserved int64
}

const domainReceiptMaxBytes = (8 << 20) + (64 << 10)
const domainReceiptTotalBytes = 512 << 20
const domainReceiptRecords = 65536

type iscpDomainReceipt struct {
	Version   int             `json:"version"`
	Digest    string          `json:"digest"`
	Operation string          `json:"operation"`
	Complete  bool            `json:"complete"`
	Status    int             `json:"status,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
}

func newISCPDomainReceipts(root string) (*iscpDomainReceipts, error) {
	if root == "" {
		return nil, errors.New("ISCP durable operation storage is unavailable")
	}
	root = filepath.Join(root, "iscp-domain-receipts")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("ISCP operation directory is not private")
	}
	path := filepath.Join(root, "key")
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, err = f.Write(key)
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("ISCP receipt key is not private")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &iscpDomainReceipts{root: root, aead: aead}, nil
}
func (j *iscpDomainReceipts) path(scope, operationID string) string {
	return filepath.Join(j.root, execution.Digest([]byte(scope+"\x00"+operationID))+".sealed")
}
func (j *iscpDomainReceipts) load(scope, id string) (iscpDomainReceipt, bool, error) {
	path := j.path(scope, id)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return iscpDomainReceipt{}, false, nil
	}
	if err != nil {
		return iscpDomainReceipt{}, false, err
	}
	defer f.Close()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return iscpDomainReceipt{}, false, errors.New("unsafe receipt")
	}
	sealed, err := io.ReadAll(io.LimitReader(f, domainReceiptMaxBytes+128))
	if err != nil || len(sealed) <= j.aead.NonceSize() || len(sealed) > domainReceiptMaxBytes+64 {
		return iscpDomainReceipt{}, false, errors.New("invalid receipt")
	}
	raw, err := j.aead.Open(nil, sealed[:j.aead.NonceSize()], sealed[j.aead.NonceSize():], []byte(scope+"\x00"+id))
	if err != nil {
		return iscpDomainReceipt{}, false, err
	}
	var receipt iscpDomainReceipt
	if json.Unmarshal(raw, &receipt) != nil || receipt.Version != 1 {
		return receipt, false, errors.New("invalid receipt schema")
	}
	return receipt, true, nil
}
func (j *iscpDomainReceipts) save(scope, id string, receipt iscpDomainReceipt) error {
	raw, err := json.Marshal(receipt)
	if err != nil || len(raw) > domainReceiptMaxBytes {
		return errors.New("receipt exceeds limit")
	}
	nonce := make([]byte, j.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	sealed := j.aead.Seal(nonce, nonce, raw, []byte(scope+"\x00"+id))
	file, err := os.CreateTemp(j.root, ".pending-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(sealed)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, j.path(scope, id)); err != nil {
		return err
	}
	dir, err := os.Open(j.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Called under mu. Reserve a maximum result before a mutation can have effects.
// Incomplete intents found after restart never execute, so only this process's
// active calls reserve future bytes in addition to durable file sizes.
func (j *iscpDomainReceipts) hasCapacity(reserve int64) bool {
	entries, err := os.ReadDir(j.root)
	if err != nil || len(entries) >= domainReceiptRecords {
		return false
	}
	total := j.reserved + reserve
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		total += info.Size()
		if total > domainReceiptTotalBytes {
			return false
		}
	}
	return true
}
