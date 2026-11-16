package mcdintegration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestManagerRestartDiscoveryExpiryAndScopedRevoke(t *testing.T) {
	_, vault, runner, e, now := harness(t)
	makeManager := func() *Manager {
		m, err := NewManager(vault, runner, e.Identity.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		m.now = func() time.Time { return time.UnixMilli(now.Load()) }
		return m
	}
	m := makeManager()
	i := e.Identity
	p := Principal{i.DeploymentID, i.OwnerID, i.ClientID, i.InstallationID}
	raw := encoded(t, e)
	if _, err := m.Provision(t.Context(), p, raw); err != nil {
		t.Fatal(err)
	}
	m = makeManager()
	statuses, err := m.Status(t.Context(), p)
	if err != nil || len(statuses) != 1 || statuses[0].State != "ready" {
		t.Fatalf("restart status: %v %v", statuses, err)
	}
	foreign := p
	foreign.InstallationID = "other"
	if _, err = m.Provision(t.Context(), foreign, raw); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	if statuses, err = m.Status(t.Context(), foreign); err != nil || len(statuses) != 0 {
		t.Fatal("foreign status exposed")
	}
	now.Store(e.SessionNotAfterMS)
	if err = m.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, err := m.connection(p, i, e.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	material, _, found, err := vault.OpenBindingVersion(t.Context(), c.binding+":material", c.kind+":material")
	clear(material)
	if err != nil || found {
		t.Fatal("expired material survived restart sweep")
	}
	if _, err = m.Revoke(t.Context(), p, i, e.ConnectionID, "disconnect"); err != nil {
		t.Fatal(err)
	}
	if statuses, err = makeManager().Status(t.Context(), p); err != nil || statuses[0].State != "revoked" {
		t.Fatal("revocation lost")
	}
}

func TestExecutionBudgetSurvivesFailureAndRestart(t *testing.T) {
	c, v, r, e, now := harness(t)
	e.MaxRequests = 8
	if _, err := c.Provision(t.Context(), encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	r.read = func(context.Context, Envelope, Execution, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("unknown provider outcome")
	}
	x := execution(e)
	x.MaxRequests = 5
	if _, err := c.Read(t.Context(), x, "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	restarted, err := NewConnection(v, r, e.Identity, e.ConnectionID, func() time.Time { return time.UnixMilli(now.Load()) })
	if err != nil {
		t.Fatal(err)
	}
	x.MaxRequests = 4
	if _, err = restarted.Read(t.Context(), x, "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	x.MaxRequests = 3
	x.DeadlineMS++
	if _, err = restarted.Read(t.Context(), x, "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	x.DeadlineMS--
	r.read = nil
	if _, err = restarted.Read(t.Context(), x, "store_get", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestManagerMissingDirectoryKeepsIndependentFence(t *testing.T) {
	_, v, r, e, _ := harness(t)
	m, _ := NewManager(v, r, e.Identity.DeploymentID)
	i := e.Identity
	p := Principal{i.DeploymentID, i.OwnerID, i.ClientID, i.InstallationID}
	if _, err := m.Provision(t.Context(), p, encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	if err := v.DeleteBinding(t.Context(), "mcd-directory:"+i.DeploymentID, "mcd-directory-v1"); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewManager(v, r, i.DeploymentID)
	if _, err := restarted.Status(t.Context(), p); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	e.ConnectionID = "new-id"
	if _, err := restarted.Provision(t.Context(), p, encoded(t, e)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
