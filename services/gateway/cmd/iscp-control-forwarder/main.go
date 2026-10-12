package main

import (
	"context"
	"flag"
	"fmt"
	ctl "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodecontrol"
	n "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type config struct {
	SchemaVersion  int    `json:"schema_version"`
	Bind           string `json:"bind"`
	IssuerRoute    string `json:"issuer_route"`
	PeerConfigFile string `json:"peer_config_file"`
	RelaySocket    string `json:"relay_socket"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "ISCP control forwarder startup failed")
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "", "private configuration")
	flag.Parse()
	var cfg config
	if e := ctl.ReadPrivate(*path, &cfg); e != nil {
		return e
	}
	host, _, e := net.SplitHostPort(cfg.Bind)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || cfg.SchemaVersion != 1 || strings.ContainsAny(cfg.IssuerRoute, "/\\") {
		return n.ErrInvalid
	}
	adapter, e := ctl.NewUnixRelay(cfg.RelaySocket)
	if e != nil {
		return e
	}
	peer, e := ctl.LoadPeer(cfg.PeerConfigFile, nil)
	if e != nil {
		return e
	}
	forwarder, e := ctl.NewForwarder(cfg.IssuerRoute, peer, adapter)
	if e != nil {
		return e
	}
	peer.SetHandler(forwarder.PrivateHandler)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go peer.Run(ctx)
	listener, e := net.Listen("tcp", cfg.Bind)
	if e != nil {
		return e
	}
	server := &http.Server{Handler: forwarder.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = server.Shutdown(shutdown)
	}()
	e = server.Serve(listener)
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
