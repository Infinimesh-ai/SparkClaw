// Command progress-anchor serves the App-CLI lifecycle 3.0 progress anchor.
// Run it with a state directory outside every runtime backup set; on a
// separate host it detects whole-set rollback of that runtime.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/progressanchor"
)

func main() {
	stateDirectory := flag.String("state-dir", "", "owner-private anchor state directory (absolute)")
	tokenFile := flag.String("token-file", "", "owner-private bearer token file (at least 32 bytes)")
	socketPath := flag.String("socket", "", "Unix socket path to listen on")
	address := flag.String("listen", "", "TCP address to listen on (use only on a private network or behind TLS)")
	flag.Parse()
	if err := run(*stateDirectory, *tokenFile, *socketPath, *address); err != nil {
		slog.Error("progress anchor stopped", "error", err)
		os.Exit(1)
	}
}

func run(stateDirectory, tokenFile, socketPath, address string) error {
	if (socketPath == "") == (address == "") {
		return errors.New("select exactly one of -socket or -listen")
	}
	info, err := os.Stat(tokenFile)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		return errors.New("token file must be owner-private")
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return err
	}
	token := bytes.TrimSpace(raw)
	if len(token) < 32 {
		return errors.New("token must contain at least 32 bytes")
	}
	store, err := progressanchor.Open(stateDirectory)
	if err != nil {
		return err
	}
	var listener net.Listener
	if socketPath != "" {
		_ = os.Remove(socketPath)
		if listener, err = net.Listen("unix", socketPath); err == nil {
			err = os.Chmod(socketPath, 0o600)
		}
	} else {
		listener, err = net.Listen("tcp", address)
	}
	if err != nil {
		return err
	}
	server := &http.Server{Handler: progressanchor.Handler(store, token), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
