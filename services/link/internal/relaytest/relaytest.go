// Package relaytest runs a real ISCP public Relay (built from a local ISCP
// checkout) against an isolated PostgreSQL database for integration tests.
// Tests using it skip unless LINK_RELAY_IT=1 and SC_RELAY_TEST_DSN are set.
package relaytest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Relay is a running test Relay.
type Relay struct {
	BaseURL    string
	WSURL      string
	DomainID   string
	RelayID    string
	AdminToken string

	t       *testing.T
	env     []string
	bin     string
	cmd     *exec.Cmd
	logPath string
}

var buildOnce sync.Once
var buildDir string
var buildErr error

func build(t *testing.T) (string, string) {
	buildOnce.Do(func() {
		src := os.Getenv("ISCP_SRC")
		if src == "" {
			home, _ := os.UserHomeDir()
			src = filepath.Join(home, "Desktop", "ISCP")
		}
		buildDir, buildErr = os.MkdirTemp("", "linkrelay-bin-")
		if buildErr != nil {
			return
		}
		for _, c := range []string{"iscp-public-relay", "iscp-relay-maintenance"} {
			cmd := exec.Command("go", "build", "-o", filepath.Join(buildDir, c), "./cmd/"+c)
			cmd.Dir = src
			cmd.Env = append(os.Environ(), "GOWORK=off")
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %v: %s", c, err, out)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return filepath.Join(buildDir, "iscp-public-relay"), filepath.Join(buildDir, "iscp-relay-maintenance")
}

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Start runs a fresh Relay with its own database and activated duplex
// authority.
func Start(t *testing.T) *Relay {
	t.Helper()
	if os.Getenv("LINK_RELAY_IT") != "1" {
		t.Skip("set LINK_RELAY_IT=1 and SC_RELAY_TEST_DSN to run against a real Relay")
	}
	dsn := os.Getenv("SC_RELAY_TEST_DSN")
	if dsn == "" {
		t.Skip("SC_RELAY_TEST_DSN required")
	}
	relayBin, maintBin := build(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "linkrelay_" + random(6)
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	})
	u, _ := url.Parse(dsn)
	u.Path = "/" + name
	dbURL := u.String()
	port := freePort(t)
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o700)
	r := &Relay{
		BaseURL:    fmt.Sprintf("http://127.0.0.1:%d", port),
		WSURL:      fmt.Sprintf("ws://127.0.0.1:%d/v2/relay/duplex", port),
		DomainID:   "link-it-" + random(4),
		RelayID:    "relay-it-" + random(4),
		AdminToken: "admin-" + random(16),
		t:          t,
		bin:        relayBin,
		logPath:    filepath.Join(dir, "relay.log"),
	}
	r.env = append(os.Environ(),
		"ISCP_DATABASE_URL="+dbURL,
		"ISCP_SIGNER_FILE="+filepath.Join(dir, "signer.json"),
		"ISCP_DOMAIN_ID="+r.DomainID,
		"ISCP_RELAY_ID="+r.RelayID,
		"ISCP_RELAY_BASE_URL="+r.BaseURL,
		"ISCP_RELAY_WS_URL="+r.WSURL,
		"ISCP_ADMIN_TOKEN="+r.AdminToken,
		"ISCP_ALLOWED_ORIGINS="+r.BaseURL,
		fmt.Sprintf("ISCP_RELAY_ADDR=127.0.0.1:%d", port),
	)
	// First start applies migrations; then activate the duplex authority.
	r.start()
	r.Stop()
	cmd := exec.Command(maintBin, "bootstrap-empty", "--relay-id", r.RelayID, "--domain-id", r.DomainID, "--history-id", strings.Repeat("A", 43), "--evidence-sha256", strings.Repeat("B", 43), "--confirm-never-served")
	cmd.Env = r.env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v %s", err, out)
	}
	r.start()
	t.Cleanup(r.Stop)
	return r
}

func (r *Relay) start() {
	f, err := os.OpenFile(r.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	cmd := exec.Command(r.bin)
	cmd.Env = r.env
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.cmd = cmd
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(r.BaseURL + "/healthz")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	raw, _ := os.ReadFile(r.logPath)
	r.t.Fatalf("relay did not start: %s", raw)
}

// Stop stops the Relay process.
func (r *Relay) Stop() {
	if r.cmd == nil || r.cmd.Process == nil {
		return
	}
	_ = r.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = r.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = r.cmd.Process.Kill()
		<-done
	}
	r.cmd = nil
}

// Restart restarts the Relay process on the same database.
func (r *Relay) Restart() {
	r.Stop()
	r.start()
}

// Log returns the Relay log.
func (r *Relay) Log() string {
	raw, _ := os.ReadFile(r.logPath)
	return string(raw)
}
