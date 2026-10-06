package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/localwebchat"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, enabled, err := localwebchat.LoadEnvironment(os.Getenv)
	if err != nil {
		return err
	}
	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != "--check" {
			return fmt.Errorf("usage: local-webchat [--check]")
		}
		if !enabled {
			return nil
		}
		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer client.CloseIdleConnections()
		response, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)) + "/readyz")
		if err != nil {
			return fmt.Errorf("local WebChat is not ready")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("local WebChat is not ready")
		}
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !enabled {
		<-ctx.Done()
		return nil
	}
	server, err := localwebchat.New(cfg)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(closeCtx)
	}()
	if err := server.Start(ctx); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "local WebChat listening on %v\n", server.Addresses())
	<-ctx.Done()
	return nil
}
