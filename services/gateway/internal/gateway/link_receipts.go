package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// linkReceipts makes link mutations idempotent: a retried request with the
// same key returns the original outcome. Bounded; oldest entries go first.
type linkReceipts struct {
	path  string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	state linkReceiptState
}

type linkReceiptState struct {
	Entries map[string]linkReceipt `json:"entries"`
	Order   []string               `json:"order"`
}

type linkReceipt struct {
	Digest string          `json:"digest"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *contract.Error `json:"error,omitempty"`
	At     time.Time       `json:"at"`
}

const linkReceiptLimit = 4096

func openLinkReceipts(path string) (*linkReceipts, error) {
	r := &linkReceipts{path: path, locks: map[string]*sync.Mutex{}, state: linkReceiptState{Entries: map[string]linkReceipt{}}}
	raw, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(raw, &r.state) != nil || r.state.Entries == nil {
			r.state = linkReceiptState{Entries: map[string]linkReceipt{}}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return r, nil
}

func linkDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Do runs fn once per key. digest identifies the request content; the same
// key with another digest is a conflict. Only results and contract errors
// that are not retryable are remembered.
func (r *linkReceipts) Do(key, digest string, fn func() (any, error)) (json.RawMessage, error) {
	r.mu.Lock()
	lock := r.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[key] = lock
	}
	r.mu.Unlock()
	lock.Lock()
	defer func() {
		lock.Unlock()
		r.mu.Lock()
		delete(r.locks, key)
		r.mu.Unlock()
	}()
	r.mu.Lock()
	prior, ok := r.state.Entries[key]
	r.mu.Unlock()
	if ok {
		if prior.Digest != digest {
			return nil, link.Errorf(contract.CodeConflict, "idempotency key reused with different content")
		}
		if prior.Error != nil {
			return nil, prior.Error
		}
		return prior.Result, nil
	}
	value, err := fn()
	var entry linkReceipt
	if err != nil {
		cerr := contract.AsError(err)
		if cerr.Retryable || cerr.Code == contract.CodeInternal || cerr.Code == contract.CodeBackendUnavailable || cerr.Code == contract.CodeCancelled || cerr.Code == contract.CodeTimeout {
			return nil, err
		}
		entry = linkReceipt{Digest: digest, Error: cerr, At: time.Now().UTC()}
	} else {
		raw, merr := json.Marshal(value)
		if merr != nil {
			return nil, merr
		}
		entry = linkReceipt{Digest: digest, Result: raw, At: time.Now().UTC()}
	}
	r.mu.Lock()
	r.state.Entries[key] = entry
	r.state.Order = append(r.state.Order, key)
	for len(r.state.Order) > linkReceiptLimit {
		delete(r.state.Entries, r.state.Order[0])
		r.state.Order = r.state.Order[1:]
	}
	snapshot := linkReceiptState{Entries: make(map[string]linkReceipt, len(r.state.Entries)), Order: append([]string(nil), r.state.Order...)}
	for k, v := range r.state.Entries {
		snapshot.Entries[k] = v
	}
	r.mu.Unlock()
	_ = link.WriteJSONAtomic(r.path, snapshot)
	if entry.Error != nil {
		return nil, entry.Error
	}
	return entry.Result, nil
}
