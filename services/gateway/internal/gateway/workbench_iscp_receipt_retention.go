package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const domainReceiptRetention = 7 * 24 * time.Hour
const domainReceiptSweepInterval = time.Minute
const domainReceiptSweepLimit = 256
const domainReceiptSweepBudget = 250 * time.Millisecond
const domainRetiredShardBytes = 4 << 20

// A compact, encrypted index retains the original input fence indefinitely.
// Result expiry cannot turn an already admitted operation into an absent key.
type iscpRetiredReceipt struct {
	Digest    string `json:"d"`
	Operation string `json:"o"`
}

type iscpRetiredShard struct {
	Version int                           `json:"v"`
	Entries map[string]iscpRetiredReceipt `json:"e"`
}

type iscpReceiptSweep struct {
	Scanned int
	Expired int
}

func (j *iscpDomainReceipts) retiredRoot() string { return filepath.Join(j.root, "retired") }

func (j *iscpDomainReceipts) initializeRetiredCatalog() error {
	path := filepath.Join(j.root, "retired-catalog.sealed")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		// The catalog is also a format marker. An existing catalog with a
		// missing directory is damaged history, never a fresh empty journal.
		if err := os.MkdirAll(j.retiredRoot(), 0700); err != nil {
			return err
		}
		if err := j.checkRetiredRoot(); err != nil {
			return err
		}
		entries, err := os.ReadDir(j.retiredRoot())
		if err != nil || len(entries) != 0 {
			return errors.New("retired receipt catalog missing")
		}
		if err := j.writeRetiredCatalog(map[string]bool{}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := j.checkRetiredRoot(); err != nil {
		return err
	}
	_, err := j.readRetiredCatalog()
	return err
}

func (j *iscpDomainReceipts) readRetiredCatalog() (map[string]bool, error) {
	path := filepath.Join(j.root, "retired-catalog.sealed")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return nil, errors.New("retired receipt catalog unavailable")
	}
	sealed, err := os.ReadFile(path)
	if err != nil || len(sealed) <= j.aead.NonceSize() || len(sealed) > 4096 {
		return nil, errors.New("invalid retired receipt catalog")
	}
	raw, err := j.aead.Open(nil, sealed[:j.aead.NonceSize()], sealed[j.aead.NonceSize():], []byte("iscp-domain-retired-catalog-v1"))
	if err != nil {
		return nil, err
	}
	var catalog map[string]bool
	if json.Unmarshal(raw, &catalog) != nil || catalog == nil || len(catalog) > 256 {
		return nil, errors.New("invalid retired receipt catalog")
	}
	for bucket, present := range catalog {
		decoded, err := hex.DecodeString(bucket)
		if err != nil || len(decoded) != 1 || bucket != strings.ToLower(bucket) || !present {
			return nil, errors.New("invalid retired receipt bucket")
		}
	}
	return catalog, nil
}

func (j *iscpDomainReceipts) writeRetiredCatalog(catalog map[string]bool) error {
	raw, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	nonce := make([]byte, j.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := j.aead.Seal(nonce, nonce, raw, []byte("iscp-domain-retired-catalog-v1"))
	return writeReceiptFile(filepath.Join(j.root, "retired-catalog.sealed"), sealed)
}

func (j *iscpDomainReceipts) checkRetiredRoot() error {
	info, err := os.Lstat(j.retiredRoot())
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("unsafe retired receipt directory")
	}
	return nil
}

func receiptKeyValid(key string) bool {
	if len(key) != 71 || !strings.HasSuffix(key, ".sealed") {
		return false
	}
	_, err := hex.DecodeString(key[:64])
	return err == nil && key == strings.ToLower(key)
}

func (j *iscpDomainReceipts) readRetiredShard(bucket string) (iscpRetiredShard, error) {
	shard := iscpRetiredShard{Version: 1, Entries: map[string]iscpRetiredReceipt{}}
	if err := j.checkRetiredRoot(); err != nil {
		return shard, err
	}
	catalog, err := j.readRetiredCatalog()
	if err != nil {
		return shard, err
	}
	path := filepath.Join(j.retiredRoot(), bucket+".sealed")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if catalog[bucket] {
			return shard, errors.New("retired receipt shard missing")
		}
		return shard, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > domainRetiredShardBytes+64 {
		return shard, errors.New("unsafe retired receipt shard")
	}
	sealed, err := os.ReadFile(path)
	if err != nil || len(sealed) <= j.aead.NonceSize() || len(sealed) > domainRetiredShardBytes+64 {
		return shard, errors.New("invalid retired receipt shard")
	}
	raw, err := j.aead.Open(nil, sealed[:j.aead.NonceSize()], sealed[j.aead.NonceSize():], []byte("iscp-domain-retired-v1\x00"+bucket))
	if err != nil {
		return shard, err
	}
	if json.Unmarshal(raw, &shard) != nil || shard.Version != 1 || shard.Entries == nil {
		return shard, errors.New("invalid retired receipt schema")
	}
	for key, receipt := range shard.Entries {
		if !receiptKeyValid(key) || key[:2] != bucket || receipt.Digest == "" || !domainMutation(receipt.Operation) {
			return shard, errors.New("invalid retired receipt fence")
		}
	}
	return shard, nil
}

func (j *iscpDomainReceipts) loadRetired(key string) (iscpDomainReceipt, bool, error) {
	shard, err := j.readRetiredShard(key[:2])
	if err != nil {
		return iscpDomainReceipt{}, false, err
	}
	receipt, found := shard.Entries[key]
	// Reuse the existing unavailable-outcome response. Neither old nor new
	// clients may interpret an expired result as a failed/unexecuted operation.
	return iscpDomainReceipt{Version: 1, Digest: receipt.Digest, Operation: receipt.Operation}, found, nil
}

func (j *iscpDomainReceipts) retireReceipt(key string, receipt iscpDomainReceipt) error {
	shard, err := j.readRetiredShard(key[:2])
	if err != nil {
		return err
	}
	fence := iscpRetiredReceipt{Digest: receipt.Digest, Operation: receipt.Operation}
	if previous, found := shard.Entries[key]; found && previous != fence {
		return errors.New("retired receipt conflict")
	}
	shard.Entries[key] = fence
	raw, err := json.Marshal(shard)
	if err != nil || len(raw) > domainRetiredShardBytes {
		return errors.New("retired receipt shard exceeds limit")
	}
	nonce := make([]byte, j.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	sealed := j.aead.Seal(nonce, nonce, raw, []byte("iscp-domain-retired-v1\x00"+key[:2]))
	catalog, err := j.readRetiredCatalog()
	if err != nil {
		return err
	}
	// Reclamation must not grow the durable byte budget, including the first
	// catalog entry for a bucket. Temporary atomic-write files can require
	// extra filesystem headroom; a failed write leaves the result intact.
	oldResult, err := os.Stat(filepath.Join(j.root, key))
	if err != nil {
		return err
	}
	var oldShardBytes int64
	if info, err := os.Stat(filepath.Join(j.retiredRoot(), key[:2]+".sealed")); err == nil {
		oldShardBytes = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	catalogGrowth := int64(0)
	if !catalog[key[:2]] {
		catalogGrowth = 11
	}
	if int64(len(sealed))+catalogGrowth > oldShardBytes+oldResult.Size() {
		return errors.New("retired receipt would grow storage")
	}
	if err = writeReceiptFile(filepath.Join(j.retiredRoot(), key[:2]+".sealed"), sealed); err != nil {
		return err
	}
	if !catalog[key[:2]] {
		catalog[key[:2]] = true
		if err := j.writeRetiredCatalog(catalog); err != nil {
			return err
		}
	}
	// Commit+fsync the fence BEFORE removing the result. A crash at any point
	// leaves the original result, the durable fence, or both. Lookup prefers
	// the fence; retrying cleanup after a crash is idempotent.
	if err = os.Remove(filepath.Join(j.root, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncReceiptDir(j.root)
}

func (j *iscpDomainReceipts) retiredBytes() (int64, error) {
	if err := j.checkRetiredRoot(); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(j.retiredRoot())
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return 0, errors.New("unsafe retired receipt entry")
		}
		total += info.Size()
	}
	return total, nil
}

// sweep is a bounded directory walk, not a full listing on every tick. Only
// authenticated completion times in the new format qualify. Intent fences,
// legacy ciphertext, subscription state and speech checkpoints are retained.
func (j *iscpDomainReceipts) sweep(ctx context.Context, now time.Time) (iscpReceiptSweep, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var result iscpReceiptSweep
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if j.scan == nil {
		var err error
		j.scan, err = os.Open(j.root)
		if err != nil {
			return result, err
		}
	}
	deadline := time.Now().Add(domainReceiptSweepBudget)
	for result.Scanned < domainReceiptSweepLimit && time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		names, err := j.scan.Readdirnames(1)
		if err != nil {
			j.closeScanLocked()
			if errors.Is(err, io.EOF) {
				return result, nil
			}
			return result, err
		}
		result.Scanned++
		key := names[0]
		if !receiptKeyValid(key) {
			continue
		}
		receipt, found, err := j.readReceipt(filepath.Join(j.root, key), nil)
		if err != nil {
			return result, err
		}
		if !found || !receipt.Complete || receipt.CompletedAt.IsZero() || receipt.CompletedAt.Add(domainReceiptRetention).After(now) || receipt.Digest == "" || !domainMutation(receipt.Operation) {
			continue
		}
		if err = j.retireReceipt(key, receipt); err != nil {
			return result, err
		}
		result.Expired++
	}
	return result, nil
}

func (j *iscpDomainReceipts) closeScanLocked() {
	if j.scan != nil {
		_ = j.scan.Close()
		j.scan = nil
	}
}
