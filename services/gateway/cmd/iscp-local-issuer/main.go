package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscplocalissuer"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "local issuer:", err)
		os.Exit(1)
	}
}
func run() error {
	initialize := flag.Bool("init", false, "create a new private local-test issuer")
	directory := flag.String("directory", "", "new absolute private issuer directory")
	subject := flag.String("subject-identity", "", "enrolled SparkX public identity file")
	audience := flag.String("audience-identity", "", "enrolled SparkClaw public identity file")
	relay := flag.String("relay-id", "", "enrolled Relay ID")
	config := flag.String("config", "", "private issuer config")
	authorize := flag.Bool("authorize-renewal", false, "authorize fixed-scope short grant renewal")
	revoke := flag.Bool("revoke-renewal", false, "revoke the existing renewal authorization")
	grantFile := flag.String("grant-file", "", "private current grant file for explicit renewal authorization")
	authorizationHours := flag.Int("authorization-hours", 24, "absolute authorization lifetime, 24 to 8760 hours")
	listen := flag.String("listen", "127.0.0.1:0", "local issuer listen address")
	containerListen := flag.Bool("allow-container-listen", false, "allow local-test container address 0.0.0.0:8080")
	flag.Parse()
	if flag.NArg() != 0 || (*initialize && (*authorize || *revoke)) || (*authorize && *revoke) {
		return errors.New("select exactly one issuer action")
	}
	if *initialize {
		_, err := iscplocalissuer.Initialize(*directory, *subject, *audience, *relay)
		return err
	}
	issuer, err := iscplocalissuer.Load(*config)
	if err != nil {
		return err
	}
	if *authorize {
		return issuer.AuthorizeRenewal(*grantFile, *authorizationHours)
	}
	if *revoke {
		return issuer.RevokeRenewal()
	}
	if err = validateListen(*listen, *containerListen); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: issuer.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 4096}
	go func() {
		<-ctx.Done()
		closing, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(closing)
	}()
	fmt.Printf("{\"issuer_url\":\"http://%s\"}\n", listener.Addr())
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func validateListen(address string, allowContainer bool) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("issuer listen address must be 127.0.0.1:port")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 0 || value > 65535 {
		return errors.New("invalid issuer listen port")
	}
	if host == "127.0.0.1" {
		return nil
	}
	// Load rejects any config other than local-test before this explicit
	// exception is evaluated. Deployment owns container/network publication.
	if allowContainer && address == "0.0.0.0:8080" {
		return nil
	}
	return errors.New("issuer listen address must be 127.0.0.1:port; local containers require the explicit flag and 0.0.0.0:8080")
}
