// Command dev-console runs the SparkClaw self-hosted development console
// (docs/self-hosted-console-design.md). Development only: it binds devices,
// pairs them with the backend and issues credentials; it never relays or
// stores messages.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Chiiz0/SparkClaw/services/link/devconsole"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dev-console:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath    = flag.String("config", "", "JSON configuration file; flags override its fields")
		listen        = flag.String("listen", "", "listen address (default :18450)")
		publicURL     = flag.String("public-url", "", "https address devices use, e.g. https://210.16.177.239:18450")
		sans          = flag.String("san", "", "comma-separated IPs and DNS names for the self-signed certificate (the public URL host is always added)")
		dataDir       = flag.String("data-dir", "", "data directory (state, account, signer key, TLS certificate)")
		name          = flag.String("name", "", "console name shown to devices")
		useTLS        = flag.Bool("tls", true, "serve HTTPS with the self-signed certificate (-tls=false: plain HTTP, loopback testing only)")
		timeZone      = flag.String("time-zone", "", "time zone of the pages (default Asia/Shanghai)")
		adminPassword = flag.String("admin-password", "", "administrator password; only initializes the account when none exists (prefer -admin-password-file or DEV_CONSOLE_ADMIN_PASSWORD)")
		passwordFile  = flag.String("admin-password-file", "", "file holding the administrator password")
		setPassword   = flag.Bool("set-password", false, "set the administrator password (from -admin-password, -admin-password-file, DEV_CONSOLE_ADMIN_PASSWORD or stdin) and exit")
		printAPIToken = flag.Bool("print-api-token", false, "create a new administrator API token, print it and exit (replaces the previous token)")
		printPin      = flag.Bool("print-pin", false, "print the console TLS pin (creating the certificate if needed) and exit")
		relayDomain   = flag.String("relay-domain", "", "Relay ISCP domain id")
		relayID       = flag.String("relay-id", "", "Relay id")
		relayBase     = flag.String("relay-base-url", "", "Relay HTTPS base URL")
		relayWS       = flag.String("relay-ws-url", "", "Relay WebSocket URL")
		relayCert     = flag.String("relay-cert", "", "Relay TLS certificate PEM file (pinned)")
		relaySigner   = flag.String("relay-signer", "", "Relay signer identity JSON file (optional)")
		relayToken    = flag.String("relay-admin-token-file", "", "Relay admin token file")
	)
	flag.Parse()

	var cfg devconsole.Config
	if *configPath != "" {
		c, err := devconsole.LoadConfig(*configPath)
		if err != nil {
			return err
		}
		cfg = c
	}
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	override := func(flagName string, dst *string, v string) {
		if set[flagName] {
			*dst = v
		}
	}
	override("listen", &cfg.Listen, *listen)
	override("public-url", &cfg.PublicURL, *publicURL)
	override("data-dir", &cfg.DataDir, *dataDir)
	override("name", &cfg.Name, *name)
	override("time-zone", &cfg.TimeZone, *timeZone)
	override("admin-password-file", &cfg.AdminPasswordFile, *passwordFile)
	override("relay-domain", &cfg.Relay.DomainID, *relayDomain)
	override("relay-id", &cfg.Relay.RelayID, *relayID)
	override("relay-base-url", &cfg.Relay.BaseURL, *relayBase)
	override("relay-ws-url", &cfg.Relay.WebSocketURL, *relayWS)
	override("relay-cert", &cfg.Relay.CertificatePEMFile, *relayCert)
	override("relay-signer", &cfg.Relay.SignerIdentityFile, *relaySigner)
	override("relay-admin-token-file", &cfg.Relay.AdminTokenFile, *relayToken)
	if set["san"] {
		cfg.SANs = splitList(*sans)
	}
	if set["tls"] {
		v := *useTLS
		cfg.TLS = &v
	}
	if cfg.Listen == "" {
		cfg.Listen = ":18450"
	}
	if cfg.DataDir == "" {
		return errors.New("-data-dir is required")
	}
	if err := devconsole.EnsureDataDir(cfg.DataDir); err != nil {
		return err
	}

	password, err := readPassword(*adminPassword, cfg.AdminPasswordFile)
	if err != nil {
		return err
	}

	switch {
	case *printPin:
		_, pin, err := devconsole.EnsureCertificate(cfg.DataDir, cfg.CertificateHosts())
		if err != nil {
			return err
		}
		fmt.Println(pin)
		return nil
	case *setPassword:
		if password == "" {
			fmt.Fprint(os.Stderr, "new administrator password: ")
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil && line == "" {
				return err
			}
			password = strings.TrimRight(line, "\r\n")
		}
		if err := devconsole.SetPassword(cfg.DataDir, password); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "administrator password updated")
		return nil
	case *printAPIToken:
		token, err := devconsole.RotateAPIToken(cfg.DataDir)
		if err != nil {
			return err
		}
		fmt.Println(token)
		return nil
	}

	if err := cfg.Validate(); err != nil {
		return err
	}
	if !cfg.TLSEnabled() && !loopback(cfg.Listen) {
		return errors.New("-tls=false serves plain HTTP and is allowed only on a loopback listen address")
	}
	ep, adminToken, err := cfg.RelayEndpoint()
	if err != nil {
		return err
	}
	var loc *time.Location
	if cfg.TimeZone != "" {
		if loc, err = time.LoadLocation(cfg.TimeZone); err != nil {
			return fmt.Errorf("time zone: %w", err)
		}
	}

	var (
		cert tls.Certificate
		pin  string
	)
	if cfg.TLSEnabled() {
		cert, pin, err = devconsole.EnsureCertificate(cfg.DataDir, cfg.CertificateHosts())
		if err != nil {
			return err
		}
		log.Printf("dev-console: TLS pin %s (SANs %s)", pin, strings.Join(devconsole.CertificateHosts(cert), ", "))
	} else {
		log.Printf("dev-console: TLS is off; devices only accept https consoles, so this mode is for local testing")
	}

	srv, err := devconsole.New(devconsole.Options{
		DataDir:         cfg.DataDir,
		PublicURL:       cfg.PublicURL,
		TLSPin:          pin,
		Name:            cfg.Name,
		Relay:           ep,
		RelayAdminToken: adminToken,
		AdminPassword:   password,
		SecureCookies:   cfg.TLSEnabled(),
		Location:        loc,
		Logger:          log.Default(),
	})
	if err != nil {
		return err
	}
	log.Printf("dev-console: console_id %s, signer %s, relay %s (%s)", srv.ConsoleID(), srv.Signer().DeviceID, ep.RelayID, ep.BaseURL)

	hs := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          log.Default(),
	}
	if cfg.TLSEnabled() {
		hs.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		log.Printf("dev-console: listening on %s, public URL %s", cfg.Listen, cfg.PublicURL)
		if cfg.TLSEnabled() {
			errc <- hs.ListenAndServeTLS("", "")
		} else {
			errc <- hs.ListenAndServe()
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(shutdown)
}

func readPassword(flagValue, file string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("admin password file: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return os.Getenv("DEV_CONSOLE_ADMIN_PASSWORD"), nil
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
