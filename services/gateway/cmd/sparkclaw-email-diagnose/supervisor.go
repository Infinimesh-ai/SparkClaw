package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

const workerEnvironment = "SPARKCLAW_EMAIL_DIAGNOSTICS_WORKER"

type outputBuffer struct {
	bytes.Buffer
	limit int
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("output limit")
	}
	return b.Buffer.Write(p)
}

// A worker owns all source reads, parsing, aggregation and its Node descendants.
// The supervisor buffers bounded output and can terminate the entire process
// group even when a filesystem operation or native SQLite call cannot cancel.
func superviseCommand(parent context.Context, cmd *exec.Cmd, out, errOut io.Writer, limit time.Duration) int {
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	stdout := &outputBuffer{limit: 1 << 20}
	stderr := &outputBuffer{limit: 4096}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 100 * time.Millisecond
	if !ownProcessGroup(cmd) {
		return writeResult(ctx, out, []byte("{\"schema_version\":1,\"read_only\":true,\"evidence\":\"incomplete\",\"missing\":[\"process_guard_unavailable\"]}\n"))
	}
	if err := cmd.Start(); err != nil {
		return 1
	}
	stopped := false
	stopGroup := func() {
		if !stopped {
			stopProcessGroup(cmd)
			stopped = true
		}
	}
	defer stopGroup()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		stopGroup()
		// Kill closes inherited pipes as well; WaitDelay bounds pipe cleanup.
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
		}
		if parent.Err() != nil {
			return 1
		}
		// The work deadline expired. A separate short output deadline prevents a
		// blocked consumer from keeping the supervisor alive while reporting it.
		outputCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer stop()
		return writeResult(outputCtx, out, []byte("{\"schema_version\":1,\"read_only\":true,\"evidence\":\"incomplete\",\"missing\":[\"query_timeout\"]}\n"))
	}
	stopGroup()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 2 {
			writeResult(ctx, errOut, []byte("invalid diagnostic query; use --help\n"))
			return 2
		}
		writeResult(ctx, errOut, []byte("diagnostic worker failed\n"))
		return 1
	}
	return writeResult(ctx, out, stdout.Bytes())
}
func writeResult(ctx context.Context, out io.Writer, raw []byte) int {
	done := make(chan error, 1)
	go func() {
		n, err := out.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return 1
		}
		return 0
	case <-ctx.Done():
		return 1
	}
}
func supervise(ctx context.Context, args []string) int {
	executable, err := os.Executable()
	if err != nil {
		return 1
	}
	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(), workerEnvironment+"=1")
	return superviseCommand(ctx, cmd, os.Stdout, os.Stderr, 10*time.Second)
}
