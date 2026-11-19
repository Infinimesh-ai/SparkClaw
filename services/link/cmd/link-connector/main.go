// Command link-connector is the SparkClaw ISCP connector: it joins the
// backend to a console domain and terminates device sessions arriving over
// the ISCP Relay, handing their link frames to the Gateway.
//
//	link-connector join -state DIR -credential TEXT|CODE [-console URL -pin PIN] [-name NAME]
//	link-connector run  -state DIR
//	link-connector status -state DIR
//	link-connector leave -state DIR
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/connector"
	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/iscplink"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: link-connector {join|run|status|leave} -state DIR ...")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	state := fs.String("state", os.Getenv("SPARKCLAW_LINK_STATE_DIR"), "shared link state directory")
	credential := fs.String("credential", "", "SparkClaw access credential text or 12-digit code; '-' reads stdin")
	consoleURL := fs.String("console", "", "console address when only a code is given")
	pin := fs.String("pin", "", "console TLS pin when only a code is given")
	name := fs.String("name", "SparkClaw", "backend name shown on the console")
	syncEvery := fs.Duration("sync", 30*time.Second, "directory sync interval")
	_ = fs.Parse(os.Args[2:])
	if *state == "" {
		fmt.Fprintln(os.Stderr, "-state is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*state, 0o700); err != nil {
		fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if os.Getenv("LINK_DEBUG") == "1" {
		iscplink.Debugf = func(f string, a ...any) { logger.Debug(fmt.Sprintf(f, a...)) }
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch cmd {
	case "join":
		text := *credential
		if text == "-" || text == "" {
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			text = strings.TrimSpace(line)
		}
		cred, err := consoleapi.ParseCredential(text)
		if err != nil {
			fatal(err)
		}
		jctx, jcancel := context.WithTimeout(ctx, time.Minute)
		defer jcancel()
		m, err := connector.Join(jctx, *state, cred, *consoleURL, *pin, *name)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("joined domain %s as %s (%s)\n", m.DomainID, m.Device.DeviceID, m.Device.Role)
	case "run":
		if err := connector.Run(ctx, connector.Options{StateDir: *state, SyncEvery: *syncEvery, Logger: logger}); err != nil && ctx.Err() == nil {
			fatal(err)
		}
	case "status":
		var m consoleapi.Membership
		raw, err := os.ReadFile(filepath.Join(*state, connector.MembershipFile))
		if err != nil {
			fmt.Println("not joined")
			os.Exit(1)
		}
		_ = json.Unmarshal(raw, &m)
		var d consoleapi.Directory
		if raw, err := os.ReadFile(filepath.Join(*state, connector.DirectoryFile)); err == nil {
			_ = json.Unmarshal(raw, &d)
		}
		fmt.Printf("domain %s host %s console %s devices %d synced %s\n", m.DomainID, m.Device.DeviceID, m.ConsoleURL, len(d.Devices), d.SyncedAt.Format(time.RFC3339))
	case "leave":
		if err := connector.Leave(ctx, *state); err != nil {
			fatal(err)
		}
		fmt.Println("left the domain")
	default:
		fmt.Fprintln(os.Stderr, "unknown command", cmd)
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "link-connector:", err)
	os.Exit(1)
}
