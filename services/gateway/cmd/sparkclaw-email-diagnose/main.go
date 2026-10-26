// sparkclaw-email-diagnose reads retained evidence; it never starts the Gateway.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emaildiagnostics"
)

type paths []string

func (p *paths) String() string     { return "retained log files" }
func (p *paths) Set(v string) error { *p = append(*p, v); return nil }
func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("sparkclaw-email-diagnose", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o emaildiagnostics.Options
	var logs paths
	var postgresEnv, format string
	fs.StringVar(&o.Owner, "owner", "", "Gateway owner scope (required)")
	fs.StringVar(&o.Draft, "draft", "", "draft reference")
	fs.StringVar(&o.Operation, "operation", "", "operation reference")
	fs.StringVar(&o.IdentityKind, "identity-kind", "", "invocation or request-key")
	fs.StringVar(&o.Job, "job", "", "job reference")
	fs.StringVar(&o.Snapshot, "snapshot", "", "existing Gateway snapshot")
	fs.StringVar(&postgresEnv, "postgres-env", "", "environment variable containing an existing database connection string")
	fs.StringVar(&o.Ledger, "ledger", "", "existing ledger.sqlite")
	fs.StringVar(&o.LedgerPrincipal, "ledger-principal", "", "exact ledger principal")
	fs.StringVar(&o.LedgerOwner, "ledger-owner", "", "exact ledger owner")
	fs.StringVar(&o.IndexDirectory, "index-directory", "", "existing product alias/head directory")
	fs.StringVar(&o.ReleaseManifest, "release-manifest", "", "expected release manifest; not proof of loaded version")
	fs.Var(&logs, "log", "retained log input; repeat up to 32 times")
	fs.StringVar(&format, "format", "text", "text or json")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fs.SetOutput(out)
			fs.PrintDefaults()
			return 0
		}
		fmt.Fprintln(errOut, "invalid diagnostic arguments; use --help")
		return 2
	}
	if fs.NArg() != 0 || format != "text" && format != "json" {
		fmt.Fprintln(errOut, "invalid diagnostic arguments; use --help")
		return 2
	}
	if postgresEnv != "" {
		o.PostgresDSN = os.Getenv(postgresEnv)
		if o.PostgresDSN == "" {
			fmt.Fprintln(errOut, "database connection configuration unavailable")
			return 2
		}
	}
	o.Logs = logs
	r, err := emaildiagnostics.Build(ctx, o)
	if err != nil {
		fmt.Fprintln(errOut, "invalid diagnostic query; use --help")
		return 2
	}
	raw, err := emaildiagnostics.Encode(r, format)
	if err != nil {
		fmt.Fprintln(errOut, "report encoding failed")
		return 1
	}
	if _, err := out.Write(raw); err != nil {
		fmt.Fprintln(errOut, "report output unavailable")
		return 1
	}
	return 0
}
func main() {
	if code, handled := emaildiagnostics.RunLedgerGuard(os.Args[1:]); handled {
		os.Exit(code)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv(workerEnvironment) == "1" {
		os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(supervise(ctx, os.Args[1:]))
}
