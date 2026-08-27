package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "iscp-bridge:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: iscp-bridge run -config PATH | enroll [options] | mock [options]")
	}
	switch args[0] {
	case "run":
		return runBridge(args[1:])
	case "enroll":
		return createEnrollmentRequest(args[1:])
	case "enroll-ticket":
		return enrollWithTicket(args[1:])
	case "mock":
		return runMockBridge(args[1:])
	default:
		return errors.New("usage: iscp-bridge run -config PATH | enroll [options] | enroll-ticket [options] | mock [options]")
	}
}

// enrollWithTicket consumes an ISCP v0.2 pairing ticket (v3) from the JingSi
// App and persists the managed enrollment bundle the Bridge runs from.
func enrollWithTicket(args []string) error {
	flags := flag.NewFlagSet("enroll-ticket", flag.ContinueOnError)
	configPath := flags.String("config", "", "Bridge config path (identity/keyring/enrollment locations)")
	payload := flags.String("ticket", "", "enrollment payload from the App (QR/deep-link/copy string)")
	relayURL := flags.String("relay-url", "", "managed relay base URL (e.g. https://iscp.infinimesh.cloud)")
	relayWebSocketURL := flags.String("relay-ws-url", "", "managed relay WebSocket URL")
	trustURL := flags.String("trust-url", "", "trust root base URL (defaults to the relay base URL)")
	displayName := flags.String("display-name", "", "device display name registered with the Cloud")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *payload == "" || *relayURL == "" || *relayWebSocketURL == "" {
		return errors.New("enroll-ticket requires -config, -ticket, -relay-url, and -relay-ws-url")
	}
	config, err := iscpbridge.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	files := config.DeviceFiles()
	_, err = iscpbridge.EnrollWithTicket(context.Background(), iscpbridge.TicketEnrollmentOptions{
		Payload:           *payload,
		RelayBaseURL:      *relayURL,
		RelayWebSocketURL: *relayWebSocketURL,
		TrustBaseURL:      *trustURL,
		DisplayName:       *displayName,
		IdentityDirectory: files.Directory,
		KeyBackend:        config.IdentityKeyBackend,
		KeyringService:    config.IdentityKeyringService,
		Profile:           config.Profile,
	}, config.EnrollmentFile, func(line string) { fmt.Println(line) })
	return err
}

func runMockBridge(args []string) error {
	flags := flag.NewFlagSet("mock", flag.ContinueOnError)
	configPath := flags.String("config", "", "path to Bridge configuration")
	listenAddress := flags.String("listen", "127.0.0.1:18792", "loopback listen address")
	clientTokenFile := flags.String("client-token-file", "", "private token file for the simulated App")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *clientTokenFile == "" {
		return errors.New("-config and -client-token-file are required")
	}
	if err := iscpbridge.ValidateMockListenAddress(*listenAddress); err != nil {
		return err
	}
	config, err := iscpbridge.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if config.Profile != iscpbridge.ProfileLocalLab {
		return errors.New("mock Bridge is available only in the local-lab profile")
	}
	gatewayToken, err := config.LoadGatewayToken()
	if err != nil {
		return err
	}
	gateway, err := iscpbridge.NewGatewayClient(iscpbridge.GatewayClientOptions{
		BaseURL: config.Gateway.BaseURL, UnixSocket: config.Gateway.UnixSocket,
		Token: gatewayToken, Timeout: config.GatewayTimeout(),
	})
	if err != nil {
		return err
	}
	clientToken, err := iscpbridge.LoadPrivateTokenFile(*clientTokenFile)
	if err != nil {
		return err
	}
	handler, err := iscpbridge.NewMockHandler(gateway, clientToken)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("listen for mock Bridge: %w", err)
	}
	defer listener.Close()
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runBridge(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := flags.String("config", "", "path to Bridge configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("-config is required")
	}
	config, err := iscpbridge.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	service, err := iscpbridge.LoadService(config)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err = service.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func createEnrollmentRequest(args []string) error {
	flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
	identityDirectory := flags.String("identity-dir", "", "private directory for the device identity")
	domainID := flags.String("domain", "", "requested ISCP Domain ID")
	deviceID := flags.String("device", "", "device ID")
	hardwareClass := flags.String("hardware", "gb10", "hardware class metadata")
	keyBackend := flags.String("key-backend", iscpbridge.IdentityKeyBackendKeyring, "identity key backend: keyring or file")
	keyringService := flags.String("keyring-service", iscpbridge.DefaultIdentityKeyringService, "system keyring service name")
	proofAudience := flags.String("proof-audience", "", "expected enrollment proof audience")
	proofChallenge := flags.String("proof-challenge", "", "short-lived server enrollment challenge")
	proofNonce := flags.String("proof-nonce", "", "optional unique proof nonce")
	output := flags.String("output", "", "optional enrollment request JSON path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *identityDirectory == "" {
		return errors.New("-identity-dir is required")
	}
	request, files, err := iscpbridge.GenerateEnrollmentRequestWithProof(
		*identityDirectory, *domainID, *deviceID, *hardwareClass, *keyBackend, *keyringService,
		iscpbridge.EnrollmentProofOptions{Audience: *proofAudience, Challenge: *proofChallenge, Nonce: *proofNonce},
		time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return errors.New("encode enrollment request")
	}
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o700); err != nil {
			return fmt.Errorf("create enrollment request directory: %w", err)
		}
		if err := os.WriteFile(*output, append(raw, '\n'), 0o600); err != nil {
			return fmt.Errorf("write enrollment request: %w", err)
		}
	} else {
		_, _ = os.Stdout.Write(append(raw, '\n'))
	}
	_, _ = fmt.Fprintf(os.Stderr, "device identity created: %s\n", files.IdentityFile)
	return nil
}
