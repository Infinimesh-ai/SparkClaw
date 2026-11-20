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

// linkReceipts makes link mutations idempotent. A retried request with the
// same key gets one of three answers: the original outcome, an input
// conflict (the same key with other content), or outcome_unknown when the
// original attempt started and never finished. Bounded; oldest entries go
// first.
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
	Digest string `json:"digest"`
	// Pending marks an attempt that started and has not finished. Found
	// after a restart (or after an uncertain failure) it answers
	// outcome_unknown and the mutation is never started again.
	Pending bool            `json:"pending,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *contract.Error `json:"error,omitempty"`
	At      time.Time       `json:"at"`
}

// linkReceiptOptions selects the recovery rule of one operation.
type linkReceiptOptions struct {
	// Fence persists the attempt before it runs, so an attempt cut off by
	// a restart or an uncertain failure answers outcome_unknown instead of
	// running twice. Operations whose business effect is itself idempotent
	// (creating a conversation by its request id, resolving an approval)
	// run without it and simply run again.
	Fence bool
	// ReplayOnly returns the original outcome and never starts the
	// operation: not_found means it never ran.
	ReplayOnly bool
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

func errLinkOutcomeUnknown() error {
	return link.Errorf(contract.CodeOutcomeUnknown, "the original attempt did not finish; check the current state before trying again")
}

// Do runs fn once per key. digest identifies the request content.
func (r *linkReceipts) Do(key, digest string, opts linkReceiptOptions, fn func() (any, error)) (json.RawMessage, error) {
	r.mu.Lock()
	lock := r.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[key] = lock
	}
	r.mu.Unlock()
	// Attempts of one key are serialized: a concurrent retry waits for the
	// original and then gets its outcome.
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
		if prior.Pending {
			return nil, errLinkOutcomeUnknown()
		}
		if prior.Error != nil {
			return nil, prior.Error
		}
		return prior.Result, nil
	}
	if opts.ReplayOnly {
		return nil, &contract.Error{Code: contract.CodeNotFound, Message: "no attempt with this idempotency key ran", NotSent: true}
	}
	if opts.Fence {
		if err := r.put(key, linkReceipt{Digest: digest, Pending: true, At: time.Now().UTC()}); err != nil {
			return nil, &contract.Error{Code: contract.CodeBackendUnavailable, Message: "the operation could not be recorded", Retryable: true, NotSent: true}
		}
	}
	value, err := fn()
	if err != nil {
		cerr := contract.AsError(err)
		switch {
		case cerr.NotSent || cerr.Code == contract.CodeBusy:
			// It never started: the key is free for a later attempt.
			r.drop(key)
			return nil, err
		case linkDefinitiveError(cerr.Code):
			_ = r.put(key, linkReceipt{Digest: digest, Error: cerr, At: time.Now().UTC()})
			return nil, cerr
		case opts.Fence:
			// Cut off or failed midway: the effect may have happened. The
			// pending entry stays and answers outcome_unknown.
			return nil, err
		default:
			r.drop(key)
			return nil, err
		}
	}
	raw, merr := json.Marshal(value)
	if merr != nil {
		if opts.Fence {
			return nil, errLinkOutcomeUnknown()
		}
		r.drop(key)
		return nil, merr
	}
	_ = r.put(key, linkReceipt{Digest: digest, Result: raw, At: time.Now().UTC()})
	return raw, nil
}

// linkDefinitiveError lists answers that are the operation's outcome and
// are replayed to a retry.
func linkDefinitiveError(code string) bool {
	switch code {
	case contract.CodeInvalidRequest, contract.CodeNotFound, contract.CodeConflict, contract.CodeUnauthorized, contract.CodeRevoked,
		contract.CodeTooLarge, contract.CodeQuotaExceeded, contract.CodeUnsupportedCapability:
		return true
	}
	return false
}

func (r *linkReceipts) put(key string, entry linkReceipt) error {
	r.mu.Lock()
	if _, exists := r.state.Entries[key]; !exists {
		r.state.Order = append(r.state.Order, key)
	}
	r.state.Entries[key] = entry
	for len(r.state.Order) > linkReceiptLimit {
		delete(r.state.Entries, r.state.Order[0])
		r.state.Order = r.state.Order[1:]
	}
	snapshot := r.snapshotLocked()
	r.mu.Unlock()
	return link.WriteJSONAtomic(r.path, snapshot)
}

func (r *linkReceipts) drop(key string) {
	r.mu.Lock()
	if _, exists := r.state.Entries[key]; !exists {
		r.mu.Unlock()
		return
	}
	delete(r.state.Entries, key)
	for i, k := range r.state.Order {
		if k == key {
			r.state.Order = append(r.state.Order[:i:i], r.state.Order[i+1:]...)
			break
		}
	}
	snapshot := r.snapshotLocked()
	r.mu.Unlock()
	_ = link.WriteJSONAtomic(r.path, snapshot)
}

func (r *linkReceipts) snapshotLocked() linkReceiptState {
	snapshot := linkReceiptState{Entries: make(map[string]linkReceipt, len(r.state.Entries)), Order: append([]string(nil), r.state.Order...)}
	for k, v := range r.state.Entries {
		snapshot.Entries[k] = v
	}
	return snapshot
}

// Settle answers a mutation whose attempt is still pending from the
// business state: settle reports the outcome the attempt had, or an error
// when the business state cannot tell yet. It waits for an attempt in
// flight, so it never races the original. A key that never ran is
// not_found; one recorded with other content is a conflict.
func (r *linkReceipts) Settle(key, digest string, settle func() (any, error)) (json.RawMessage, error) {
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
	switch {
	case !ok:
		return nil, &contract.Error{Code: contract.CodeNotFound, Message: "no attempt with this idempotency key ran", NotSent: true}
	case prior.Digest != digest:
		return nil, link.Errorf(contract.CodeConflict, "idempotency key reused with different content")
	case prior.Error != nil:
		return nil, prior.Error
	case !prior.Pending:
		return prior.Result, nil
	}
	value, err := settle()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errLinkOutcomeUnknown()
	}
	if err = r.put(key, linkReceipt{Digest: digest, Result: raw, At: time.Now().UTC()}); err != nil {
		return nil, errLinkOutcomeUnknown()
	}
	return raw, nil
}
