// Package progressanchor is the independent monotonic progress anchor used by
// App-CLI lifecycle 3.0 to detect whole-set rollback of a runtime state
// directory. It stores one constant-size record per owner and never request
// bodies. Only dispatch releases advance it.
package progressanchor

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Ring is the number of recent releases retained for precise rollback
// classification; older history is never needed.
const Ring = 256

var (
	ownerPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// ErrConflict reports a CAS that does not extend the current state.
	ErrConflict = errors.New("anchor conflict")
)

type Release struct {
	Seq         int64  `json:"seq"`
	OperationID string `json:"operation_id"`
	Attempt     int64  `json:"attempt"`
}

type State struct {
	Generation int64     `json:"generation"`
	Seq        int64     `json:"seq"`
	Digest     string    `json:"digest"`
	Ring       []Release `json:"ring"`
	TimeMS     int64     `json:"time_ms,omitempty"`
}

type Advance struct {
	Owner       string `json:"owner"`
	Generation  int64  `json:"generation"`
	PreviousSeq int64  `json:"previous_seq"`
	NextSeq     int64  `json:"next_seq"`
	Digest      string `json:"digest"`
	OperationID string `json:"operation_id"`
	Attempt     int64  `json:"attempt"`
}

type Reseed struct {
	Owner      string `json:"owner"`
	Generation int64  `json:"generation"`
	Seq        int64  `json:"seq"`
	Digest     string `json:"digest"`
}

// Store persists each owner's record with write-temp, fsync and rename. Its
// directory must not be restored together with any runtime state backup.
type Store struct {
	directory string
	mu        sync.Mutex
	now       func() time.Time
}

func Open(directory string) (*Store, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("anchor directory must be absolute")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("anchor directory must be an owner-private directory")
	}
	return &Store{directory: directory, now: time.Now}, nil
}

func (s *Store) file(owner string) string { return filepath.Join(s.directory, owner+".json") }

func (s *Store) load(owner string) (*State, error) {
	raw, err := os.ReadFile(s.file(owner))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil || state.Generation <= 0 || state.Seq < 0 || !digestPattern.MatchString(state.Digest) {
		return nil, errors.New("anchor record is corrupt")
	}
	return &state, nil
}

func (s *Store) save(owner string, state State) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temporary := s.file(owner) + ".tmp"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temporary, s.file(owner)); err != nil {
		return err
	}
	directory, err := os.Open(s.directory)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *Store) Read(owner string) (*State, error) {
	if !ownerPattern.MatchString(owner) {
		return nil, fmt.Errorf("owner is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load(owner)
	if state != nil {
		state.TimeMS = s.now().UnixMilli()
	}
	return state, err
}

// Advance is a compare-and-advance by exactly one release. Repeating the
// same CAS succeeds idempotently; any other mismatch is a conflict.
func (s *Store) Advance(request Advance) (State, error) {
	if !ownerPattern.MatchString(request.Owner) || request.Generation <= 0 || request.PreviousSeq < 0 ||
		request.NextSeq != request.PreviousSeq+1 || !digestPattern.MatchString(request.Digest) ||
		request.OperationID == "" || len(request.OperationID) > 128 || request.Attempt <= 0 {
		return State{}, fmt.Errorf("advance is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load(request.Owner)
	if err != nil {
		return State{}, err
	}
	if current == nil {
		current = &State{Generation: request.Generation, Digest: zeroDigest}
	}
	if current.Generation == request.Generation && current.Seq == request.NextSeq && current.Digest == request.Digest {
		return *current, nil
	}
	if current.Generation != request.Generation || current.Seq != request.PreviousSeq {
		return State{}, ErrConflict
	}
	ring := append(current.Ring, Release{Seq: request.NextSeq, OperationID: request.OperationID, Attempt: request.Attempt})
	if len(ring) > Ring {
		ring = ring[len(ring)-Ring:]
	}
	next := State{Generation: request.Generation, Seq: request.NextSeq, Digest: request.Digest, Ring: ring}
	return next, s.save(request.Owner, next)
}

// Reseed starts a new anchor generation after the runtime fenced forward.
// Generations only increase, so an old runtime cannot reseed over a new one.
func (s *Store) Reseed(request Reseed) (State, error) {
	if !ownerPattern.MatchString(request.Owner) || request.Generation <= 0 || request.Seq < 0 || !digestPattern.MatchString(request.Digest) {
		return State{}, fmt.Errorf("reseed is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load(request.Owner)
	if err != nil {
		return State{}, err
	}
	if current != nil && request.Generation <= current.Generation {
		return State{}, ErrConflict
	}
	next := State{Generation: request.Generation, Seq: request.Seq, Digest: request.Digest, Ring: []Release{}}
	return next, s.save(request.Owner, next)
}

var zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"

// Handler serves POST /v1/anchors/{read,advance,reseed} with bearer auth.
func Handler(store *Store, token []byte) http.Handler {
	mux := http.NewServeMux()
	authorized := func(r *http.Request) bool {
		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		return len(token) >= 32 && len(header) > len(prefix) && header[:len(prefix)] == prefix &&
			subtle.ConstantTimeCompare([]byte(header[len(prefix):]), token) == 1
	}
	decode := func(w http.ResponseWriter, r *http.Request, out any) bool {
		if !authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return false
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(out); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return false
		}
		return true
	}
	respond := func(w http.ResponseWriter, value any, err error) {
		switch {
		case errors.Is(err, ErrConflict):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict"})
		case err != nil:
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		default:
			writeJSON(w, http.StatusOK, value)
		}
	}
	mux.HandleFunc("POST /v1/anchors/read", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Owner string `json:"owner"`
		}
		if !decode(w, r, &input) {
			return
		}
		state, err := store.Read(input.Owner)
		respond(w, map[string]any{"state": state}, err)
	})
	mux.HandleFunc("POST /v1/anchors/advance", func(w http.ResponseWriter, r *http.Request) {
		var input Advance
		if !decode(w, r, &input) {
			return
		}
		state, err := store.Advance(input)
		respond(w, map[string]any{"seq": state.Seq, "digest": state.Digest}, err)
	})
	mux.HandleFunc("POST /v1/anchors/reseed", func(w http.ResponseWriter, r *http.Request) {
		var input Reseed
		if !decode(w, r, &input) {
			return
		}
		state, err := store.Reseed(input)
		respond(w, map[string]any{"seq": state.Seq, "digest": state.Digest}, err)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
