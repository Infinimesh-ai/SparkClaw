package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	flag.Parse()
	if *initialize {
		_, err := iscplocalissuer.Initialize(*directory, *subject, *audience, *relay)
		return err
	}
	issuer, err := iscplocalissuer.Load(*config)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
