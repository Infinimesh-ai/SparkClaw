// iscp-public-relay keeps the pinned reference transport and adds a local-only
// managed transaction socket. TLS is terminated by the existing TEST1 ingress.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscprelayadapter"
	ref "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscprelayreference"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/config"
	"github.com/Infinimesh-ai/ISCP/pkg/server/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if e := run(); e != nil {
		slog.Error("public Relay stopped", "reason", "startup_or_runtime_failed")
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dsn := os.Getenv("ISCP_DATABASE_URL")
	if dsn == "" {
		return errors.New("persistent database required")
	}
	startup, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	pool, e := pgxpool.New(startup, dsn)
	if e != nil {
		return e
	}
	defer pool.Close()
	migrations, e := postgres.EmbeddedMigrations()
	if e != nil {
		return e
	}
	if e = postgres.ApplyMigrations(startup, pool, migrations); e != nil {
		return e
	}
	limit, e := strconv.Atoi(env("ISCP_REQUESTS_PER_MINUTE", "1200"))
	if e != nil || limit < 120 || limit > 6000 {
		return errors.New("invalid capacity")
	}
	cfg := ref.Config{SignerFile: os.Getenv("ISCP_SIGNER_FILE"), DomainID: env("ISCP_DOMAIN_ID", "infinimesh-public"), RelayID: env("ISCP_RELAY_ID", "relay-210-16-177-239"), BaseURL: env("ISCP_RELAY_BASE_URL", "https://210.16.177.239"), WebSocketURL: env("ISCP_RELAY_WS_URL", "wss://210.16.177.239/v2/relay/connect"), ProfileGate: config.DefaultGate(config.ProfileProduction), DB: pool, AdminToken: os.Getenv("ISCP_ADMIN_TOKEN"), AllowedOrigins: strings.Split(os.Getenv("ISCP_ALLOWED_ORIGINS"), ","), RequestsPerMinute: limit}
	var managed *iscprelayadapter.Adapter
	if p := os.Getenv("ISCP_NODE_AUTHORITY_FILE"); p != "" {
		var nc iscprelayadapter.Config
		b, e := os.ReadFile(p)
		if e != nil || iscpnodeissuer.StrictDecode(b, &nc) != nil {
			return errors.New("invalid node authority file")
		}
		if nc.DomainID != cfg.DomainID || nc.RelayID != cfg.RelayID {
			return errors.New("authority scope mismatch")
		}
		managed, e = iscprelayadapter.New(startup, pool, nc)
		if e != nil {
			return e
		}
		cfg.Managed = managed
		cfg.TicketIssuer = &nc.InvitationSigner
	}
	relay, e := ref.New(cfg)
	if e != nil {
		return e
	}
	if managed != nil {
		managed.Invalidate = relay.InvalidateDevice
		managed.MarkRevoked = relay.MarkRevoked
	}
	public := server(env("ISCP_RELAY_ADDR", "127.0.0.1:55117"), relay.Handler())
	errs := make(chan error, 2)
	go func() { errs <- public.ListenAndServe() }()
	var private *http.Server
	var listener net.Listener
	socket := os.Getenv("ISCP_MANAGED_SOCKET")
	if managed != nil {
		if !filepath.IsAbs(socket) {
			return errors.New("private managed socket required")
		}
		info, e := os.Lstat(socket)
		if e == nil {
			if info.Mode()&os.ModeSocket == 0 {
				return errors.New("unsafe existing socket")
			}
			if c, e := net.DialTimeout("unix", socket, time.Second); e == nil {
				c.Close()
				return errors.New("managed socket already in use")
			}
			if e = os.Remove(socket); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		listener, e = net.Listen("unix", socket)
		if e != nil {
			return e
		}
		defer os.Remove(socket)
		defer listener.Close()
		if e = os.Chmod(socket, 0o660); e != nil {
			return e
		}
		private = server("", managed.Handler())
		go func() { errs <- private.Serve(listener) }()
	}
	slog.Info("public Relay ready", "managed", managed != nil, "profile", "production")
	select {
	case <-ctx.Done():
	case e = <-errs:
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
	}
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = public.Shutdown(shutdown)
	if private != nil {
		_ = private.Shutdown(shutdown)
	}
	return nil
}
func server(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
