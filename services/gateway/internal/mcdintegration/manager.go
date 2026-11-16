package mcdintegration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

// Principal is built by authenticated product admission, never by a model or
// request body. A lease remains independent from the connected Mac's liveness.
type Principal struct {
	DeploymentID   string
	OwnerID        string
	ClientID       string
	InstallationID string
}

func (p Principal) matches(i Identity) bool {
	return p.DeploymentID == i.DeploymentID && p.OwnerID == i.OwnerID && p.ClientID == i.ClientID && p.InstallationID == i.InstallationID
}

type connectionRef struct {
	Identity Identity `json:"identity"`
	ID       string   `json:"connection_id"`
}
type directory struct {
	Version     int             `json:"version"`
	Connections []connectionRef `json:"connections"`
}

// Manager owns discovery, one shared cancellation scope per connection and
// expiry sweeps. Its directory contains no credentials or provider payloads.
type Manager struct {
	vault       Vault
	runner      Runner
	deployment  string
	now         func() time.Time
	mu          sync.Mutex
	connections map[string]*Connection
}

func NewManager(v Vault, r Runner, deployment string) (*Manager, error) {
	if v == nil || r == nil || !idPattern.MatchString(deployment) {
		return nil, ErrInvalid
	}
	return &Manager{vault: v, runner: r, deployment: deployment, now: time.Now, connections: map[string]*Connection{}}, nil
}
func (m *Manager) directory(ctx context.Context) (directory, int64, error) {
	key := "mcd-directory:" + m.deployment
	raw, gen, found, err := m.vault.OpenBindingVersion(ctx, key, "mcd-directory-v1")
	defer clear(raw)
	anchor, _, anchored, anchorErr := m.vault.OpenBindingVersion(ctx, key+":anchor", "mcd-directory-anchor-v1")
	defer clear(anchor)
	if err != nil || anchorErr != nil || found != anchored || len(raw) > 256<<10 || found && string(anchor) != digest(json.RawMessage(raw)) {
		return directory{}, 0, ErrUnavailable
	}
	if !found {
		return directory{Version: 1, Connections: []connectionRef{}}, 0, nil
	}
	var d directory
	if appstream.DecodeStrict(raw, &d) != nil || d.Version != 1 || len(d.Connections) > 256 {
		return directory{}, 0, ErrUnavailable
	}
	seen := map[string]bool{}
	for _, ref := range d.Connections {
		if !ref.Identity.valid() || ref.Identity.DeploymentID != m.deployment || !idPattern.MatchString(ref.ID) || seen[ref.ID] {
			return directory{}, 0, ErrUnavailable
		}
		seen[ref.ID] = true
	}
	return d, gen, nil
}
func (m *Manager) register(ctx context.Context, ref connectionRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, gen, err := m.directory(ctx)
	if err != nil {
		return err
	}
	for _, old := range d.Connections {
		if old.ID == ref.ID {
			if old != ref {
				return ErrScope
			}
			return nil
		}
	}
	if len(d.Connections) >= 256 {
		return ErrUnavailable
	}
	d.Connections = append(d.Connections, ref)
	raw, err := json.Marshal(d)
	if err != nil {
		return ErrUnavailable
	}
	defer clear(raw)
	key := "mcd-directory:" + m.deployment
	previous, anchorGen, _, err := m.vault.OpenBindingVersion(ctx, key+":anchor", "mcd-directory-anchor-v1")
	clear(previous)
	if err != nil {
		return ErrUnavailable
	}
	if m.vault.CompareAndReplaceBinding(ctx, key+":anchor", "mcd-directory-anchor-v1", []byte(digest(json.RawMessage(raw))), anchorGen) != nil {
		return ErrConflict
	}
	if m.vault.CompareAndReplaceBinding(ctx, key, "mcd-directory-v1", raw, gen) != nil {
		return ErrConflict
	}
	return nil
}
func (m *Manager) connection(p Principal, i Identity, id string) (*Connection, error) {
	if p.DeploymentID != m.deployment || !p.matches(i) {
		return nil, ErrScope
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := digest(connectionRef{i, id})
	if c := m.connections[key]; c != nil {
		return c, nil
	}
	if len(m.connections) >= 256 {
		return nil, ErrUnavailable
	}
	c, err := NewConnection(m.vault, m.runner, i, id, m.now)
	if err != nil {
		return nil, err
	}
	m.connections[key] = c
	return c, nil
}
func (m *Manager) Provision(ctx context.Context, p Principal, raw []byte) (Receipt, error) {
	e, err := DecodeEnvelope(raw)
	if err != nil {
		return Receipt{}, err
	}
	defer clear(e.Material)
	c, err := m.connection(p, e.Identity, e.ConnectionID)
	if err != nil {
		return Receipt{}, err
	}
	// Register before sending credentials to the runner: a crash cannot leave
	// material that a restarted sweep cannot discover.
	if err = m.register(ctx, connectionRef{e.Identity, e.ConnectionID}); err != nil {
		return Receipt{}, err
	}
	return c.Provision(ctx, raw)
}
func (m *Manager) Revoke(ctx context.Context, p Principal, i Identity, id, request string) (Receipt, error) {
	c, err := m.connection(p, i, id)
	if err != nil {
		return Receipt{}, err
	}
	if err = m.register(ctx, connectionRef{i, id}); err != nil {
		return Receipt{}, err
	}
	return c.Revoke(ctx, request)
}
func (m *Manager) Read(ctx context.Context, p Principal, x Execution, op string, args json.RawMessage) (json.RawMessage, error) {
	d, _, err := m.directory(ctx)
	if err != nil {
		return nil, err
	}
	registered := false
	for _, ref := range d.Connections {
		registered = registered || ref.ID == x.ConnectionID && ref.Identity == x.Identity
	}
	if !registered {
		return nil, ErrUnavailable
	}
	c, err := m.connection(p, x.Identity, x.ConnectionID)
	if err != nil {
		return nil, err
	}
	return c.Read(ctx, x, op, args)
}

// Selection freezes one unambiguous connection for a newly admitted Workflow.
// A later account change never silently rebinds this execution.
func (m *Manager) Selection(ctx context.Context, p Principal, executionID string) (Execution, error) {
	d, _, err := m.directory(ctx)
	if err != nil {
		return Execution{}, err
	}
	var result *Execution
	for _, ref := range d.Connections {
		if !p.matches(ref.Identity) {
			continue
		}
		c, err := m.connection(p, ref.Identity, ref.ID)
		if err != nil {
			return Execution{}, err
		}
		r, _, err := c.load(ctx)
		if err != nil {
			return Execution{}, err
		}
		if r.Closed || !r.MaterialPresent || r.Session == nil {
			continue
		}
		now := m.now().UnixMilli()
		if now < r.ObservedMS || now >= r.Session.SessionNotAfterMS {
			continue
		}
		if result != nil {
			return Execution{}, ErrConflict
		}
		result = &Execution{Identity: ref.Identity, ConnectionID: ref.ID, ExecutionID: executionID,
			CredentialRevision: r.Session.CredentialRevision, GrantRevision: r.Session.GrantRevision,
			DeadlineMS: min(now+900000, r.Session.SessionNotAfterMS), MaxRequests: min(8, r.Session.MaxRequests)}
	}
	if result == nil {
		return Execution{}, ErrUnavailable
	}
	return *result, nil
}

type Status struct {
	ConnectionID       string `json:"connection_id"`
	AccountRef         string `json:"account_ref"`
	State              string `json:"state"`
	CredentialRevision int64  `json:"credential_revision"`
	GrantRevision      int64  `json:"grant_revision"`
	NotAfterMS         int64  `json:"session_not_after_ms"`
}

// Recovery uses only the original connection's identity/control metadata. It
// does not reopen material or grant reads/cart/purchase rights after revocation.
func (m *Manager) Recovery(ctx context.Context, p Principal, id string) (Execution, error) {
	if !idPattern.MatchString(id) {
		return Execution{}, ErrInvalid
	}
	d, _, err := m.directory(ctx)
	if err != nil {
		return Execution{}, err
	}
	var out *Execution
	for _, ref := range d.Connections {
		if !p.matches(ref.Identity) {
			continue
		}
		c, err := m.connection(p, ref.Identity, ref.ID)
		if err != nil {
			return Execution{}, err
		}
		r, _, err := c.load(ctx)
		if err != nil {
			return Execution{}, err
		}
		if r.Session == nil {
			continue
		}
		if out != nil && out.Identity != ref.Identity {
			return Execution{}, ErrConflict
		}
		out = &Execution{Identity: ref.Identity, ConnectionID: ref.ID, ExecutionID: id, CredentialRevision: r.Session.CredentialRevision, GrantRevision: r.Session.GrantRevision, DeadlineMS: m.now().UnixMilli(), MaxRequests: 1}
	}
	if out == nil {
		return Execution{}, ErrUnavailable
	}
	return *out, nil
}

func (m *Manager) Status(ctx context.Context, p Principal) ([]Status, error) {
	if p.DeploymentID != m.deployment {
		return nil, ErrScope
	}
	d, _, err := m.directory(ctx)
	if err != nil {
		return nil, err
	}
	result := []Status{}
	for _, ref := range d.Connections {
		if !p.matches(ref.Identity) {
			continue
		}
		c, err := m.connection(p, ref.Identity, ref.ID)
		if err != nil {
			return nil, err
		}
		r, _, err := c.load(ctx)
		if err != nil {
			return nil, err
		}
		s := Status{ConnectionID: ref.ID, AccountRef: ref.Identity.AccountRef, State: "unavailable"}
		if r.Session != nil {
			s.CredentialRevision = r.Session.CredentialRevision
			s.GrantRevision = r.Session.GrantRevision
			s.NotAfterMS = r.Session.SessionNotAfterMS
			if r.MaterialPresent && m.now().UnixMilli() >= r.ObservedMS && m.now().UnixMilli() < s.NotAfterMS {
				s.State = "ready"
			}
			if m.now().UnixMilli() >= s.NotAfterMS {
				s.State = "expired"
			}
		}
		if r.Closed {
			s.State = "revoked"
		}
		result = append(result, s)
	}
	return result, nil
}
func (m *Manager) Sweep(ctx context.Context) error {
	d, _, err := m.directory(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, ref := range d.Connections {
		i := ref.Identity
		p := Principal{i.DeploymentID, i.OwnerID, i.ClientID, i.InstallationID}
		c, err := m.connection(p, i, ref.ID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err = c.Expire(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		_ = m.Sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
