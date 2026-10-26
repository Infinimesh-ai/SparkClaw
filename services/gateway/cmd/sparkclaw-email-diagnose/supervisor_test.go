package main

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestSupervisorBoundsBlockedWorkerAndInheritedPipes(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := exec.Command("sh", "-c", "sleep 20 & wait")
	start := time.Now()
	if code := superviseCommand(t.Context(), cmd, &out, &errOut, 100*time.Millisecond); code != 0 {
		t.Fatalf("timeout should yield incomplete report: %d %s", code, &errOut)
	}
	if time.Since(start) > 2*time.Second || !bytes.Contains(out.Bytes(), []byte("query_timeout")) {
		t.Fatalf("unbounded worker: %s", &out)
	}
}

type blockedOutput struct{ release chan struct{} }

func (b blockedOutput) Write(p []byte) (int, error) { <-b.release; return len(p), nil }
func TestSupervisorBoundsBlockedOutput(t *testing.T) {
	writer := blockedOutput{make(chan struct{})}
	defer close(writer.release)
	var stderr bytes.Buffer
	start := time.Now()
	if code := superviseCommand(context.Background(), exec.Command("sh", "-c", "printf '{}'"), writer, &stderr, 100*time.Millisecond); code != 1 {
		t.Fatalf("blocked output code=%d", code)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("stdout was not bounded")
	}
}
