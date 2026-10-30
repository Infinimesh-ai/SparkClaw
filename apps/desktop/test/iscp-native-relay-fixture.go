// This disposable fixture is built in the explicitly selected ISCP source
// module. It exercises that repository's real duplex Relay, never a substitute
// implementation in SparkClaw or an existing user's Relay/database.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/config"
	"github.com/Infinimesh-ai/ISCP/pkg/server/postgres"
	relay "github.com/Infinimesh-ai/ISCP/pkg/server/relay"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		// Database credentials and Relay credentials must not reach runner logs.
		fmt.Fprintln(os.Stderr, "isolated duplex Relay fixture:", err)
		os.Exit(1)
	}
}

func run() error {
	root := os.Getenv("SPARKCLAW_STREAM_FIXTURE_ROOT")
	info, err := os.Stat(root)
	if err != nil || !filepath.IsAbs(root) || !strings.HasPrefix(root, "/private/tmp/") || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private disposable root required")
	}
	origin := os.Getenv("ISCP_NATIVE_FIXTURE_BASE_URL")
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("loopback fixture advertisement required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(startup, os.Getenv("SPARKCLAW_STREAM_FIXTURE_DSN"))
	if err != nil {
		return fmt.Errorf("isolated database configuration failed")
	}
	defer db.Close()
	migrations, err := postgres.EmbeddedMigrations()
	if err != nil {
		return fmt.Errorf("load duplex migrations")
	}
	if err = postgres.ApplyMigrations(startup, db, migrations); err != nil {
		return fmt.Errorf("apply duplex migrations")
	}
	if err = relay.PrepareDatabase(startup, db); err != nil {
		return fmt.Errorf("prepare duplex database")
	}
	srv, err := relay.New(relay.Config{SignerFile: filepath.Join(root, "relay-signer.json"), DomainID: "stream-native-domain", RelayID: "stream-native-relay", BaseURL: origin, WebSocketURL: "ws" + strings.TrimPrefix(origin, "http") + "/v2/relay/duplex", ProfileGate: config.DefaultGate(config.ProfileLocalLab), DB: db})
	if err != nil {
		return fmt.Errorf("initialize duplex Relay")
	}
	defer srv.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on isolated loopback")
	}
	server := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 3 * time.Second}
	defer server.Close()
	go func() { <-ctx.Done(); _ = server.Close() }()
	if err = json.NewEncoder(os.Stdout).Encode(map[string]string{"relay_url": "http://" + listener.Addr().String()}); err != nil {
		return fmt.Errorf("publish private fixture listener")
	}
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve isolated Relay")
	}
	return nil
}
