package sandbox

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestHTTPRunnerRoundTrip(t *testing.T) {
	fake := fakeRunner{result: Result{
		Status:  "completed",
		Backend: "fake",
		Network: "none",
		Stdout:  "ok\n",
	}}
	server := httptest.NewServer(Handler(fake))
	defer server.Close()

	runner := NewHTTPRunner(server.URL)
	result, err := runner.Run(t.Context(), Request{
		Command:       "echo ok",
		WorkspaceRoot: t.TempDir(),
		TimeoutMS:     1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "ok\n" || result.Backend != "fake" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestHTTPRunnerReturnsRemoteErrorWithResult(t *testing.T) {
	fake := fakeRunner{err: context.DeadlineExceeded, result: Result{
		Status:  "timed_out",
		Backend: "fake",
		Network: "none",
		Stdout:  "partial",
	}}
	server := httptest.NewServer(Handler(fake))
	defer server.Close()

	runner := NewHTTPRunner(server.URL)
	result, err := runner.Run(t.Context(), Request{
		Command:       "sleep 10",
		WorkspaceRoot: t.TempDir(),
		TimeoutMS:     1000,
	})
	if err == nil {
		t.Fatal("expected remote error")
	}
	if result.Status != "timed_out" || result.Stdout != "partial" {
		t.Fatalf("remote result was not preserved: %#v", result)
	}
}

type fakeRunner struct {
	result Result
	err    error
}

func (r fakeRunner) Run(context.Context, Request) (Result, error) {
	return r.result, r.err
}
