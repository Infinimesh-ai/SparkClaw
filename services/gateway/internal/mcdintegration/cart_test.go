package mcdintegration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeCartRunner struct {
	fakeRunner
	calls     int
	fail      bool
	preflight bool
}

func (f *fakeCartRunner) Cart(_ context.Context, _ Envelope, _ Execution, _ string, _ json.RawMessage, _ string, a *CartAuthority) (json.RawMessage, error) {
	f.calls++
	if a == nil {
		return nil, ErrScope
	}
	if f.fail {
		return nil, ErrUnavailable
	}
	r := CartResult{SchemaVersion: 1, Encoding: "bounded-json-v1", ResultJSON: `{"state":"MATCHED","cart_matches_requested":true}`, WriteClaimed: true, DispatchState: "dispatched"}
	if f.preflight {
		r.ResultJSON = `{}`
		r.WriteClaimed = false
		r.DispatchState = "not_dispatched"
		r.ErrorCode = "cart_operation_requires_review"
	}
	return json.Marshal(r)
}
func TestCartLostResponseCannotReplayAcrossManagerRestart(t *testing.T) {
	_, v, _, e, _ := harness(t)
	e.Operations = []string{"cart_add_simple"}
	runner := &fakeCartRunner{fail: true}
	p := Principal{e.Identity.DeploymentID, e.Identity.OwnerID, e.Identity.ClientID, e.Identity.InstallationID}
	m, _ := NewManager(v, runner, p.DeploymentID)
	if _, err := m.Provision(t.Context(), p, encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	x := execution(e)
	args := json.RawMessage(`{"request":{},"items":[]}`)
	_, first, err := m.Cart(t.Context(), p, x, "cart_add_simple", args, t.TempDir())
	if !errors.Is(err, ErrUnavailable) || first == nil || first.State != "unknown" {
		t.Fatal(first, err)
	}
	m, _ = NewManager(v, runner, p.DeploymentID)
	runner.fail = false
	_, repeated, err := m.Cart(t.Context(), p, x, "cart_add_simple", args, t.TempDir())
	if err != nil || *repeated != *first || runner.calls != 1 {
		t.Fatal("replayed unknown", repeated, err, runner.calls)
	}
	x.ExecutionID = "another-execution"
	if _, _, err = m.Cart(t.Context(), p, x, "cart_add_simple", args, t.TempDir()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatal("parallel execution bypassed member fence")
	}
}
func TestCartProvenPreflightFailureReleasesMemberWithoutReplayingOriginal(t *testing.T) {
	_, v, _, e, _ := harness(t)
	e.Operations = []string{"cart_add_simple"}
	runner := &fakeCartRunner{preflight: true}
	p := Principal{e.Identity.DeploymentID, e.Identity.OwnerID, e.Identity.ClientID, e.Identity.InstallationID}
	m, _ := NewManager(v, runner, p.DeploymentID)
	if _, err := m.Provision(t.Context(), p, encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	x := execution(e)
	args := json.RawMessage(`{"request":{},"items":[]}`)
	_, first, err := m.Cart(t.Context(), p, x, "cart_add_simple", args, t.TempDir())
	if err != nil || first.State != "failed_before_write" {
		t.Fatal(first, err)
	}
	runner.preflight = false
	_, again, err := m.Cart(t.Context(), p, x, "cart_add_simple", args, t.TempDir())
	if err != nil || *again != *first || runner.calls != 1 {
		t.Fatal(again, err)
	}
	x.ExecutionID = "corrected-request"
	_, fresh, err := m.Cart(t.Context(), p, x, "cart_add_simple", args, t.TempDir())
	if err != nil || fresh.State != "confirmed" || runner.calls != 2 {
		t.Fatal(fresh, err)
	}
	if _, err = m.Read(t.Context(), p, x, "cart_add_simple", args); !errors.Is(err, ErrScope) {
		t.Fatal("read route accepted write", err)
	}
}
