package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
)

func startLocalCredentialManagement(ctx context.Context, cfg config.Config, repository desktopClientRepository, server *gateway.Server) (*http.Server, error) {
	if cfg.Gateway.LocalManagementFile == "" {
		return nil, nil
	}
	credential, err := loadDesktopClientProvisioning(cfg.Gateway.LocalManagementFile)
	if err != nil {
		return nil, err
	}
	if credential.DeploymentID != cfg.Gateway.DeploymentID || !strings.HasPrefix(credential.ClientID, "local_management_") {
		return nil, errors.New("local management identity does not match this deployment")
	}
	profile, found, err := repository.GetOwnerProfileByID(ctx, credential.OwnerID)
	if err != nil || !found || profile.ID != credential.OwnerID {
		return nil, errors.New("local management Owner is unavailable")
	}
	directory := filepath.Join(filepath.Dir(cfg.Gateway.LocalManagementFile), "management")
	listener, err := listenPrivateLocalSocket(directory, "credentials.sock")
	if err != nil {
		return nil, err
	}
	actorID := credential.ActorID
	if actorID == "" {
		actorID = credential.OwnerID
	}
	httpServer := &http.Server{
		Handler:           server.LocalManagementHandler(credential.OwnerID, actorID, credential.ClientID, desktopTokenHash(credential.Token)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       30 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() { _ = httpServer.Serve(listener) }()
	return httpServer, nil
}

func listenPrivateLocalSocket(directory, name string) (net.Listener, error) {
	if err := checkPrivateLocalDirectory(directory); err != nil {
		return nil, err
	}
	socket := filepath.Join(directory, name)
	if existing, err := os.Lstat(socket); err == nil {
		if existing.Mode()&os.ModeSocket == 0 || existing.Mode().Perm() != 0o600 || !localFileOwnedByCurrentUser(existing) {
			return nil, errors.New("local management socket is not controlled by the deployment user")
		}
		// Never replace a live listener, including another Gateway process.
		if connection, err := net.DialTimeout("unix", socket, 200*time.Millisecond); err == nil {
			connection.Close()
			return nil, errors.New("private local listener is already running")
		}
		if err := os.Remove(socket); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listen on private local socket: %w", err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
