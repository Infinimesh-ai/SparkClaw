//go:build darwin || linux

// An isolated acceptance harness, not a public service or a product capability.
// Its caller supplies authenticated private stdin. Production ISCP admission
// must be implemented and qualified before mounting mcdintegration in Gateway.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"golang.org/x/sys/unix"
)

type request struct {
	Operation    string                   `json:"operation"`
	Identity     mcdintegration.Identity  `json:"identity"`
	ConnectionID string                   `json:"connection_id"`
	Envelope     json.RawMessage          `json:"envelope,omitempty"`
	RequestID    string                   `json:"request_id,omitempty"`
	Execution    mcdintegration.Execution `json:"execution,omitempty"`
	Tool         string                   `json:"tool,omitempty"`
	Arguments    json.RawMessage          `json:"arguments,omitempty"`
}

func run() (any, error) {
	root := flag.String("root", "", "dedicated private acceptance directory")
	python := flag.String("python", "", "absolute, verified helper Python")
	initialize := flag.Bool("initialize", false, "initialize an empty acceptance directory once")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 95*time.Second)
	defer cancel()
	if !filepath.IsAbs(*root) || !filepath.IsAbs(*python) {
		return nil, mcdintegration.ErrInvalid
	}
	if err := os.MkdirAll(*root, 0700); err != nil {
		return nil, mcdintegration.ErrUnavailable
	}
	physical, err := filepath.EvalSymlinks(*root)
	if err != nil || physical != filepath.Clean(*root) {
		return nil, mcdintegration.ErrUnavailable
	}
	stat, err := os.Stat(*root)
	if err != nil || !stat.IsDir() || stat.Mode().Perm()&0077 != 0 {
		return nil, mcdintegration.ErrUnavailable
	}
	var owner unix.Stat_t
	if unix.Stat(*root, &owner) != nil || owner.Uid != uint32(os.Getuid()) {
		return nil, mcdintegration.ErrUnavailable
	}
	entries, err := os.ReadDir(*root)
	if err != nil || *initialize && len(entries) != 0 {
		return nil, mcdintegration.ErrUnavailable
	}
	// FileStore has a single process owner. Hold a nonblocking OS lifetime lock
	// before opening its snapshot, including while the owned helper performs I/O.
	fd, err := unix.Open(filepath.Join(*root, "process.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, mcdintegration.ErrUnavailable
	}
	defer unix.Close(fd)
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return nil, mcdintegration.ErrUnavailable
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	state, key := filepath.Join(*root, "vault.json"), filepath.Join(*root, "vault.key")
	for _, name := range []string{state, key} {
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) && *initialize {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, mcdintegration.ErrUnavailable
		}
	}
	repository, err := store.NewFileStore(state)
	if err != nil {
		return nil, mcdintegration.ErrUnavailable
	}
	vault := credential.New(repository, credential.Options{KeyFile: key, AutoCreate: *initialize})
	if vault.Ready() != nil || vault.ValidateExisting(ctx) != nil {
		return nil, mcdintegration.ErrUnavailable
	}
	if *initialize {
		if vault.CompareAndReplaceBinding(ctx, "mcd-probe-anchor", "mcd-probe-v1", []byte("initialized"), 0) != nil {
			return nil, mcdintegration.ErrUnavailable
		}
	} else {
		anchor, found, err := vault.OpenBinding(ctx, "mcd-probe-anchor", "mcd-probe-v1")
		valid := err == nil && found && string(anchor) == "initialized"
		clear(anchor)
		if !valid {
			return nil, mcdintegration.ErrUnavailable
		}
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10+1))
	defer clear(raw)
	var input request
	if err != nil || len(raw) > 64<<10 || appstream.DecodeStrict(raw, &input) != nil {
		return nil, mcdintegration.ErrInvalid
	}
	defer clear(input.Envelope)
	conn, err := mcdintegration.NewConnection(vault, mcdintegration.Helper{Python: *python}, input.Identity, input.ConnectionID, nil)
	if err != nil {
		return nil, err
	}
	switch input.Operation {
	case "provision":
		return conn.Provision(ctx, input.Envelope)
	case "read":
		return conn.Read(ctx, input.Execution, input.Tool, input.Arguments)
	case "revoke":
		return conn.Revoke(ctx, input.RequestID)
	case "expire":
		return map[string]bool{"checked": true}, conn.Expire(ctx)
	default:
		return nil, mcdintegration.ErrInvalid
	}
}

func main() {
	// Bound even an input pipe that never reaches EOF; the caller also owns
	// the subprocess and imposes its original request deadline.
	deadline := time.AfterFunc(100*time.Second, func() { os.Exit(1) })
	defer deadline.Stop()
	result, err := run()
	response := map[string]any{"ok": err == nil}
	if err == nil {
		response["result"] = result
	} else {
		// No raw library/provider exceptions, stderr, request bodies or traces.
		code := "mcd_session_unavailable"
		for _, known := range []error{mcdintegration.ErrInvalid, mcdintegration.ErrConflict, mcdintegration.ErrClosed, mcdintegration.ErrExpired, mcdintegration.ErrScope} {
			if errors.Is(err, known) {
				code = known.Error()
				break
			}
		}
		response["error"] = code
	}
	if json.NewEncoder(os.Stdout).Encode(response) != nil || err != nil {
		os.Exit(1)
	}
}
