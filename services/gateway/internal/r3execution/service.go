package r3execution

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Executor func(context.Context, Envelope, map[string][]byte) (Output, error)
type staged struct {
	created time.Time
	files   map[string][]byte
}
type Service struct {
	lock      *os.File
	closeOnce sync.Once
	closed    bool
	mu        sync.Mutex
	root      string
	key       []byte
	control   control
	execute   Executor
	approvals map[string]map[string]*approvalWait
	active    map[string]context.CancelFunc
	inputs    map[string]*staged
	now       func() time.Time
	start     sync.Once
	wg        sync.WaitGroup
}

func keyFor(owner, client, request string) string { return owner + "\x00" + client + "\x00" + request }
func New(root string, execute Executor) (*Service, error) {
	if err := privateDirectory(root); err != nil {
		return nil, err
	}
	if err := privateDirectory(filepath.Join(root, "content")); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(root, "process.lock")
	if _, err := os.Lstat(lockPath); err == nil {
		if _, err = readPrivate(lockPath, 0); err != nil {
			return nil, err
		}
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, ErrUnavailable
	}
	success := false
	defer func() {
		if !success {
			lock.Close()
		}
	}()
	s := &Service{root: root, lock: lock, execute: execute, control: control{Version: 2, Installations: map[string]string{}, Fences: map[string]Fence{}, WorkbenchFences: map[string]workbenchFence{}}, approvals: map[string]map[string]*approvalWait{}, active: map[string]context.CancelFunc{}, inputs: map[string]*staged{}, now: func() time.Time { return time.Now().UTC() }}
	keyPath := filepath.Join(root, "spool.key")
	key, err := readPrivate(keyPath, 32)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		_, err = rand.Read(key)
		if err == nil {
			err = atomicFile(keyPath, key)
		}
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("R3 spool key unavailable")
	}
	s.key = key
	raw, err := readPrivate(filepath.Join(root, "control.json"), 64<<20)
	if err == nil {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&s.control); err != nil || !json.Valid(raw) || s.control.Version != 2 || s.control.Fences == nil || s.control.Installations == nil || s.control.WorkbenchFences == nil || len(s.control.Fences)+len(s.control.WorkbenchFences) > MaxFences {
			return nil, errors.New("invalid R3 control ledger")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for key, f := range s.control.Fences {
		if key != keyFor(f.OwnerID, f.ClientID, f.RequestID) || !UUID(f.RequestID) || !UUID(f.InstallationID) || !digestPattern.MatchString(f.InputDigest) || f.CreatedAt.IsZero() || f.Deadline.IsZero() {
			return nil, errors.New("invalid R3 durable fence")
		}
		switch f.State {
		case "accepted", "running":
			f.State = "unknown"
			s.control.Fences[key] = f
		case "completed", "delivered", "delivery_expired", "failed", "canceled", "unknown":
		default:
			return nil, errors.New("invalid R3 fence state")
		}
	}
	if err = s.validateWorkbenchFences(); err != nil {
		return nil, err
	}
	if err = s.Sweep(); err != nil {
		return nil, err
	}
	success = true
	return s, nil
}
func (s *Service) Root() string { return s.root }
func (s *Service) Start(ctx context.Context) {
	s.start.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					s.mu.Lock()
					for _, cancel := range s.active {
						cancel()
					}
					s.mu.Unlock()
					s.Close()
					return
				case <-ticker.C:
					_ = s.Sweep()
				}
			}
		}()
	})
}
func (s *Service) Wait() { s.wg.Wait() }
func (s *Service) Bind(owner, client, installation string) error {
	if !identityPattern.MatchString(owner) || !identityPattern.MatchString(client) || !UUID(installation) {
		return ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := keyFor(owner, client, "")
	if prior := s.control.Installations[key]; prior != "" {
		if prior != installation {
			return ErrConflict
		}
		return nil
	}
	if len(s.control.Installations) >= MaxFences {
		return ErrCapacity
	}
	s.control.Installations[key] = installation
	if err := s.saveLocked(); err != nil {
		delete(s.control.Installations, key)
		return ErrUnavailable
	}
	return nil
}
func (s *Service) Installation(owner, client, installation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !UUID(installation) || s.control.Installations[keyFor(owner, client, "")] != installation {
		return ErrConflict
	}
	return nil
}
func (s *Service) reservationLocked(owner string) int {
	count := 0
	for _, f := range s.control.Fences {
		if f.OwnerID == owner && (f.State == "accepted" || f.State == "running" || f.State == "completed") {
			count++
		}
	}
	for key := range s.inputs {
		if strings.HasPrefix(key, owner+"\x00") {
			count++
		}
	}
	return count * TaskBytes
}
func (s *Service) Upload(owner, client, installation, request, file, digest string, raw []byte) error {
	if !UUID(request) || !UUID(file) || len(raw) > ResultBytes || Digest(raw) != digest {
		return ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !UUID(installation) || s.control.Installations[keyFor(owner, client, "")] != installation {
		return ErrConflict
	}
	if s.closed {
		return ErrUnavailable
	}
	key := keyFor(owner, client, request)
	if _, found := s.control.Fences[key]; found {
		return ErrConflict
	}
	st := s.inputs[key]
	if st == nil {
		if s.reservationLocked(owner)+TaskBytes > OwnerBytes || len(s.inputs) >= 32 {
			return ErrCapacity
		}
		st = &staged{created: s.now(), files: map[string][]byte{}}
		s.inputs[key] = st
	}
	if !st.created.Add(ExecutionBudget).After(s.now()) {
		delete(s.inputs, key)
		return ErrExpired
	}
	if prior, ok := st.files[file]; ok {
		if Digest(prior) != digest {
			return ErrConflict
		}
		return nil
	}
	size := len(raw)
	for _, data := range st.files {
		size += len(data)
	}
	if size > TaskBytes {
		return ErrCapacity
	}
	st.files[file] = append([]byte(nil), raw...)
	return nil
}
func (s *Service) Submit(ctx context.Context, e Envelope, digest string) (Status, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Status{}, ErrUnavailable
	}
	key := keyFor(e.OwnerID, e.ClientID, e.RequestID)
	if s.control.Installations[keyFor(e.OwnerID, e.ClientID, "")] != e.InstallationID {
		s.mu.Unlock()
		return Status{}, ErrConflict
	}
	if f, found := s.control.Fences[key]; found {
		s.mu.Unlock()
		if f.InputDigest != digest || f.InstallationID != e.InstallationID {
			return Status{}, ErrConflict
		}
		return s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	}
	var files map[string][]byte
	st := s.inputs[key]
	if len(e.InputFiles) > 0 {
		if st == nil || !st.created.Add(ExecutionBudget).After(s.now()) {
			s.mu.Unlock()
			return Status{}, ErrExpired
		}
		files = map[string][]byte{}
		size := ContextBytes
		names := map[string]bool{}
		for _, manifest := range e.InputFiles {
			data, ok := st.files[manifest.ID]
			if !ok || len(data) != manifest.Size || Digest(data) != manifest.SHA256 || !validName(manifest.Name) || !UUID(manifest.ID) {
				s.mu.Unlock()
				return Status{}, ErrConflict
			}
			if _, exists := files[manifest.ID]; exists || names[manifest.Name] {
				s.mu.Unlock()
				return Status{}, ErrConflict
			}
			names[manifest.Name] = true
			size += len(data)
			files[manifest.ID] = data
		}
		if size > TaskBytes || len(files) != len(st.files) {
			s.mu.Unlock()
			return Status{}, ErrCapacity
		}
	}
	reservation := TaskBytes
	if st != nil {
		reservation = 0
	}
	if len(s.control.Fences)+len(s.control.WorkbenchFences) >= MaxFences || s.reservationLocked(e.OwnerID)+reservation > OwnerBytes || len(s.active) >= 8 {
		s.mu.Unlock()
		return Status{}, ErrCapacity
	}
	now := s.now()
	f := Fence{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID, RequestID: e.RequestID, InputDigest: digest, State: "accepted", CreatedAt: now, Deadline: now.Add(ExecutionBudget)}
	s.control.Fences[key] = f
	if err := s.saveLocked(); err != nil {
		delete(s.control.Fences, key)
		s.mu.Unlock()
		return Status{}, ErrUnavailable
	}
	delete(s.inputs, key)
	run, cancel := context.WithDeadline(ctx, f.Deadline)
	s.active[key] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go s.run(run, key, e, files)
	return statusFor(f), nil
}
func (s *Service) run(ctx context.Context, key string, e Envelope, files map[string][]byte) {
	defer s.wg.Done()
	s.mu.Lock()
	f := s.control.Fences[key]
	if f.State != "accepted" {
		if cancel := s.active[key]; cancel != nil {
			cancel()
			delete(s.active, key)
		}
		s.mu.Unlock()
		return
	}
	f.State = "running"
	s.control.Fences[key] = f
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		s.finish(key, Output{}, ErrUnavailable)
		return
	}
	s.mu.Unlock()
	var out Output
	var err error
	func() {
		defer func() {
			if recover() != nil {
				err = ErrUnavailable
			}
		}()
		out, err = s.execute(ctx, e, files)
	}()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	s.finish(key, out, err)
}
func (s *Service) finish(key string, out Output, executionErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.approvals, key)
	if cancel := s.active[key]; cancel != nil {
		cancel()
		delete(s.active, key)
	}
	f := s.control.Fences[key]
	if f.State != "running" {
		return
	}
	if executionErr != nil {
		f.State = "failed"
		if errors.Is(executionErr, context.Canceled) || errors.Is(executionErr, context.DeadlineExceeded) || errors.Is(executionErr, ErrUnavailable) {
			f.State = "unknown"
		}
		s.control.Fences[key] = f
		_ = s.saveLocked()
		return
	}
	payload := Payload{Content: out.Content, Files: []File{}}
	content := bundle{Files: map[string][]byte{}}
	names := make([]string, 0, len(out.Files))
	for name := range out.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	size := len(out.Content)
	for _, name := range names {
		raw := out.Files[name]
		size += len(raw)
		if !validName(name) || size > ResultBytes {
			f.State = "failed"
			s.control.Fences[key] = f
			_ = s.saveLocked()
			return
		}
		id := newUUID()
		payload.Files = append(payload.Files, File{ID: id, Name: name, Size: len(raw), SHA256: Digest(raw)})
		content.Files[id] = raw
	}
	raw, err := json.Marshal(payload)
	if err != nil || size > ResultBytes || len(raw) > ResultBytes || len(raw)+size-len(out.Content) > ResultBytes {
		f.State = "failed"
		s.control.Fences[key] = f
		_ = s.saveLocked()
		return
	}
	content.Payload = string(raw)
	testEnvelope, _ := json.Marshal(Status{SchemaVersion: 1, RequestID: f.RequestID, InputDigest: f.InputDigest, State: "completed", Result: &Result{Sequence: 1, Digest: Digest(raw), Payload: content.Payload}})
	if len(testEnvelope) > ResultBytes-4096 {
		f.State = "failed"
		s.control.Fences[key] = f
		_ = s.saveLocked()
		return
	}
	if err = s.seal(key, content); err != nil {
		f.State = "unknown"
		s.control.Fences[key] = f
		_ = s.saveLocked()
		return
	}
	now := s.now()
	expiry := now.Add(ResultRetention)
	f.State = "completed"
	f.GeneratedAt = &now
	f.ExpiresAt = &expiry
	f.ResultDigest = Digest(raw)
	s.control.Fences[key] = f
	if err = s.saveLocked(); err != nil {
		f.State = "unknown"
		s.control.Fences[key] = f
		_ = os.Remove(s.contentPath(key))
		_ = s.saveLocked()
	}
}
func statusFor(f Fence) Status {
	status := Status{SchemaVersion: 1, RequestID: f.RequestID, InputDigest: f.InputDigest, State: f.State, ExpiresAt: f.ExpiresAt}
	if f.State == "accepted" || f.State == "running" {
		status.ExecutionExpiresAt = &f.Deadline
	}
	return status
}
func (s *Service) Lookup(owner, client, request string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := keyFor(owner, client, request)
	f, ok := s.control.Fences[key]
	if !ok {
		return Status{}, ErrNotFound
	}
	status := statusFor(f)
	if f.State == "accepted" || f.State == "running" {
		if !f.Deadline.After(s.now()) {
			status.State = "unknown"
		} else {
			status.PendingApprovals = s.pendingLocked(key)
		}
	}
	if f.State == "completed" {
		if f.ExpiresAt == nil || !f.ExpiresAt.After(s.now()) {
			status.State = "delivery_expired"
			return status, nil
		}
		content, err := s.unseal(key)
		if err != nil {
			status.State = "delivery_expired"
			return status, nil
		}
		if Digest([]byte(content.Payload)) != f.ResultDigest {
			return Status{}, ErrUnavailable
		}
		status.Result = &Result{Sequence: 1, Digest: f.ResultDigest, Payload: content.Payload}
	}
	return status, nil
}
func (s *Service) File(owner, client, request, file string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := keyFor(owner, client, request)
	f, ok := s.control.Fences[key]
	if !ok {
		return nil, ErrNotFound
	}
	if f.State != "completed" || f.ExpiresAt == nil || !f.ExpiresAt.After(s.now()) {
		return nil, ErrExpired
	}
	content, err := s.unseal(key)
	if err != nil {
		return nil, ErrExpired
	}
	raw, ok := content.Files[file]
	if !ok {
		return nil, ErrNotFound
	}
	return raw, nil
}
func (s *Service) Ack(owner, client, request string, sequence int, digest string, durable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := keyFor(owner, client, request)
	f, ok := s.control.Fences[key]
	if !ok {
		return ErrNotFound
	}
	if sequence != 1 || !durable || digest != f.ResultDigest || f.ResultDigest == "" {
		return ErrConflict
	}
	if f.State == "delivered" {
		return removeIfPresent(s.contentPath(key))
	}
	if f.State != "completed" || f.ExpiresAt == nil || !f.ExpiresAt.After(s.now()) {
		return ErrExpired
	}
	prior := f
	f.State = "delivered"
	s.control.Fences[key] = f
	if err := s.saveLocked(); err != nil {
		s.control.Fences[key] = prior
		return ErrUnavailable
	}
	return removeIfPresent(s.contentPath(key))
}
func (s *Service) Cancel(owner, client, request string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := keyFor(owner, client, request)
	f, ok := s.control.Fences[key]
	if !ok {
		return ErrNotFound
	}
	if f.State == "accepted" || f.State == "running" {
		f.State = "unknown"
		s.control.Fences[key] = f
		delete(s.approvals, key)
		if cancel := s.active[key]; cancel != nil {
			cancel()
		}
		return s.saveLocked()
	}
	return nil
}
func (s *Service) Sweep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	keep := map[string]bool{}
	for key, f := range s.control.Fences {
		if f.State == "completed" && f.ExpiresAt != nil && f.ExpiresAt.After(now) {
			keep[filepath.Base(s.contentPath(key))] = true
		} else if f.State == "completed" {
			f.State = "delivery_expired"
			s.control.Fences[key] = f
		}
	}
	for key, st := range s.inputs {
		if !st.created.Add(ExecutionBudget).After(now) {
			delete(s.inputs, key)
		}
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "content"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !keep[entry.Name()] {
			if err = removeIfPresent(filepath.Join(s.root, "content", entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 240 && !strings.ContainsAny(name, "/\\\x00\r\n")
}
func newUUID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	raw[6] = (raw[6] & 15) | 64
	raw[8] = (raw[8] & 63) | 128
	const hex = "0123456789abcdef"
	text := make([]byte, 0, 36)
	for i, b := range raw {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			text = append(text, '-')
		}
		text = append(text, hex[b>>4], hex[b&15])
	}
	return string(text)
}

func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.approvals = map[string]map[string]*approvalWait{}
		for _, cancel := range s.active {
			cancel()
		}
		s.mu.Unlock()
		s.wg.Wait()
		if s.lock != nil {
			_ = s.lock.Close()
		}
	})
}
