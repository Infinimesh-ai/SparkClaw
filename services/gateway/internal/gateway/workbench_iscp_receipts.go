package gateway

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

// Domain receipts are separate from execution content. A persisted intent is an
// uncertainty fence: a process loss after it never starts the mutation twice.
// Completed responses are encrypted because settings may contain private data.
type iscpDomainReceipts struct {
	root         string
	aead         cipher.AEAD
	mu           sync.Mutex
	reserved     int64
	scan         *os.File
	expiries     receiptExpiryHeap
	expiryByKey  map[string]*receiptExpiry
	wake         chan struct{}
	rebuild      bool
	lastPressure time.Time
	// usage caches the directory scan behind hasCapacity. save keeps it exact,
	// retirement invalidates it, and it is rescanned at least every minute.
	usage domainReceiptUsage
}

type domainReceiptUsage struct {
	valid   bool
	at      time.Time
	records int
	bytes   int64
}

const domainReceiptUsageTTL = time.Minute

const domainReceiptMaxBytes = (8 << 20) + (64 << 10)
const domainReceiptTotalBytes = 512 << 20

// Retain original receipts while allowing existing installations that reached
// the former record ceiling to recover. The independent 512 MiB budget remains.
const domainReceiptRecords = 262144

type iscpDomainReceipt struct {
	Version     int             `json:"version"`
	Digest      string          `json:"digest"`
	Operation   string          `json:"operation"`
	Complete    bool            `json:"complete"`
	Status      int             `json:"status,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
	CompletedAt time.Time       `json:"completed_at,omitempty"`
}

// All peer adapters share admission reservations and cleanup serialization.
func (s *Server) domainReceiptJournal(root string) (*iscpDomainReceipts, error) {
	s.domainReceiptsMu.Lock()
	defer s.domainReceiptsMu.Unlock()
	if s.domainReceipts != nil {
		if s.domainReceipts.root != filepath.Join(root, "iscp-domain-receipts") {
			return nil, errors.New("ISCP operation storage root changed")
		}
		return s.domainReceipts, nil
	}
	journal, err := newISCPDomainReceipts(root)
	if err == nil {
		if s.domainReceiptWake == nil {
			s.domainReceiptWake = make(chan struct{}, 1)
		}
		journal.wake = s.domainReceiptWake
		s.domainReceipts = journal
		journal.notifyRetention()
	}
	return journal, err
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
	j := &iscpDomainReceipts{root: root, aead: aead, expiryByKey: map[string]*receiptExpiry{}, wake: make(chan struct{}, 1), rebuild: true}
	if err := j.initializeRetiredCatalog(); err != nil {
		return nil, err
	}
	return j, nil
}
func (j *iscpDomainReceipts) path(scope, operationID string) string {
	return filepath.Join(j.root, execution.Digest([]byte(scope+"\x00"+operationID))+".sealed")
}
func (j *iscpDomainReceipts) load(scope, id string) (iscpDomainReceipt, bool, error) {
	path := j.path(scope, id)
	key := filepath.Base(path)
	if receipt, found, err := j.loadRetired(key); err != nil || found {
		return receipt, found, err
	}
	return j.readReceipt(path, []byte(scope+"\x00"+id))
}

var domainReceiptV2 = []byte("sparkclaw-receipt-v2\x00")

func (j *iscpDomainReceipts) readReceipt(path string, legacyAAD []byte) (iscpDomainReceipt, bool, error) {
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
	aad := legacyAAD
	if bytes.HasPrefix(sealed, domainReceiptV2) {
		sealed = sealed[len(domainReceiptV2):]
		aad = []byte("iscp-domain-receipt-v2\x00" + filepath.Base(path))
	} else if legacyAAD == nil {
		// Old records have no authenticated completion timestamp and their AAD
		// is not recoverable from the filename. Never expire them using mtime.
		return iscpDomainReceipt{}, false, nil
	}
	if len(sealed) <= j.aead.NonceSize() {
		return iscpDomainReceipt{}, false, errors.New("invalid receipt")
	}
	raw, err := j.aead.Open(nil, sealed[:j.aead.NonceSize()], sealed[j.aead.NonceSize():], aad)
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
	if receipt.Complete && receipt.CompletedAt.IsZero() && execution.UUID(id) && domainMutation(receipt.Operation) {
		receipt.CompletedAt = domainNow()
	}
	raw, err := json.Marshal(receipt)
	if err != nil || len(raw) > domainReceiptMaxBytes {
		return errors.New("receipt exceeds limit")
	}
	nonce := make([]byte, j.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	path := j.path(scope, id)
	sealed := append(append([]byte(nil), domainReceiptV2...), j.aead.Seal(nonce, nonce, raw, []byte("iscp-domain-receipt-v2\x00"+filepath.Base(path)))...)
	previous, statErr := os.Lstat(path)
	if err := writeReceiptFile(path, sealed); err != nil {
		j.usage.valid = false
		return err
	}
	switch {
	case statErr == nil && previous.Mode().IsRegular():
		j.usage.bytes += int64(len(sealed)) - previous.Size()
	case errors.Is(statErr, os.ErrNotExist):
		j.usage.records++
		j.usage.bytes += int64(len(sealed))
	default:
		j.usage.valid = false
	}
	j.indexReceipt(filepath.Base(path), receipt)
	return nil
}

func writeReceiptFile(path string, sealed []byte) error {
	root := filepath.Dir(path)
	file, err := os.CreateTemp(root, ".pending-")
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
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncReceiptDir(root)
}

func syncReceiptDir(root string) error {
	dir, err := os.Open(root)
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
	now := domainNow()
	if !j.usage.valid || now.Sub(j.usage.at) >= domainReceiptUsageTTL || now.Before(j.usage.at) {
		records, bytes, ok := j.scanUsage()
		if !ok {
			j.usage.valid = false
			return false
		}
		j.usage = domainReceiptUsage{valid: true, at: now, records: records, bytes: bytes}
	}
	total := j.reserved + reserve + j.usage.bytes
	defer func() {
		if j.usage.records >= domainReceiptRecords*4/5 || total >= domainReceiptTotalBytes*4/5 {
			j.pressureCleanup(now)
		}
	}()
	return j.usage.records < domainReceiptRecords && total <= domainReceiptTotalBytes
}

// scanUsage counts directory entries and their bytes, including retired
// fences; an unsafe entry makes the journal refuse new work.
func (j *iscpDomainReceipts) scanUsage() (int, int64, bool) {
	entries, err := os.ReadDir(j.root)
	if err != nil {
		return 0, 0, false
	}
	var total int64
	for _, entry := range entries {
		if entry.Name() == "retired" {
			size, err := j.retiredBytes()
			if err != nil {
				return 0, 0, false
			}
			total += size
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return 0, 0, false
		}
		total += info.Size()
	}
	return len(entries), total, true
}
