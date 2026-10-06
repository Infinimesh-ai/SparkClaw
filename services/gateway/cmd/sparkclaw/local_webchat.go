package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
)

func startLocalWebChat(ctx context.Context, cfg config.Config, repository desktopClientRepository, server *gateway.Server) (*http.Server, error) {
	if !cfg.Gateway.LocalWebChatEnabled {
		return nil, nil
	}
	credential, err := loadDesktopClientProvisioning(cfg.Gateway.LocalWebChatFile)
	if err != nil {
		return nil, err
	}
	if credential.DeploymentID != cfg.Gateway.DeploymentID || !strings.HasPrefix(credential.ClientID, "local_webchat_") {
		return nil, errors.New("local WebChat identity does not match this deployment")
	}
	profile, found, err := repository.GetOwnerProfileByID(ctx, credential.OwnerID)
	if err != nil || !found || profile.ID != credential.OwnerID {
		return nil, errors.New("local WebChat Owner is unavailable")
	}
	listener, err := listenPrivateLocalSocket(filepath.Join(filepath.Dir(cfg.Gateway.LocalWebChatFile), "local-webchat"), "workbench.sock")
	if err != nil {
		return nil, err
	}
	actorID := credential.ActorID
	if actorID == "" {
		actorID = credential.OwnerID
	}
	httpServer := &http.Server{
		Handler:           server.LocalWebChatHandler(credential.OwnerID, actorID, credential.ClientID, desktopTokenHash(credential.Token)),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		// Streaming handlers own finite operation deadlines; no blanket write
		// timeout may truncate SSE or uploaded documents.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() { _ = httpServer.Serve(listener) }()
	return httpServer, nil
}
