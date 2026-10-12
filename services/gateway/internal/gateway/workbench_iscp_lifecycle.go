package gateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// WorkbenchISCP owns the responder receive connection and its Client revocation
// registration. It is closed before Gateway storage on process shutdown.
type WorkbenchISCP struct {
	endpoint interface {
		Run(context.Context) error
		Close() error
	}
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (s *Server) StartWorkbenchISCP(ctx context.Context, cfg iscpworkbench.Config, onState func(string)) (*WorkbenchISCP, error) {
	if cfg.Mode == "node-managed" {
		return s.startNodeWorkbenchISCP(ctx, cfg, onState)
	}
	if !s.cfg.Gateway.WorkbenchISCPLocalTest {
		return nil, errors.New("ISCP local test transport requires explicit Gateway opt-in")
	}
	handler, err := s.NewWorkbenchISCPHandler(cfg)
	if err != nil {
		return nil, err
	}
	connected, release, err := s.clientConnectionContext(ctx, cfg.Binding.ClientID)
	if err != nil {
		return nil, errors.New("ISCP responder Client is unavailable")
	}
	connected, cancel := context.WithCancel(connected)
	endpoint, err := iscpworkbench.NewEndpoint(cfg, handler, func(state string) {
		if state == "authorization_revoked" {
			s.cancelClientConnections(cfg.Binding.ClientID)
		}
		if onState != nil {
			onState(state)
		}
	})
	if err != nil {
		cancel()
		release()
		return nil, err
	}
	service := &WorkbenchISCP{endpoint: endpoint, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(service.done)
		defer release()
		defer endpoint.Close()
		if err := endpoint.Run(connected); err != nil && connected.Err() == nil {
			// Transport diagnostics stay redacted; an authorization bundle or peer
			// business body must never appear in Gateway logs.
			slog.Warn("ISCP workbench responder stopped")
		}
	}()
	return service, nil
}

func (s *WorkbenchISCP) Close() error {
	var err error
	s.once.Do(func() {
		s.cancel()
		err = s.endpoint.Close()
		<-s.done
	})
	return err
}

// startNodeWorkbenchISCP retains the Gateway Client authority and every existing
// business handler. A shared responder transport routes to isolated peer adapters.
func (s *Server) startNodeWorkbenchISCP(ctx context.Context, cfg iscpworkbench.Config, onState func(string)) (*WorkbenchISCP, error) {
	if !s.cfg.Gateway.WorkbenchISCPNodeManaged || s.cfg.Gateway.WorkbenchISCPLocalTest || cfg.Mode != "node-managed" || cfg.SchemaVersion != 2 {
		return nil, errors.New("node-managed responder requires explicit Gateway opt-in")
	}
	ctx, cancel := context.WithCancel(ctx)
	endpoint, err := iscpworkbench.NewNodeResponder(cfg, s.NewWorkbenchISCPHandler, func(callCtx context.Context, p iscpworkbench.ManagedPeer) error {
		if p.Binding.DeploymentID != s.cfg.Gateway.DeploymentID {
			return errors.New("node peer deployment binding differs from Gateway")
		}
		_, err := s.workbenchISCPPrincipal(callCtx, p.Binding)
		return err
	}, onState)
	if err != nil {
		cancel()
		return nil, err
	}
	service := &WorkbenchISCP{endpoint: endpoint, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(service.done)
		defer endpoint.Close()
		if err := endpoint.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("ISCP node workbench responder stopped")
		}
	}()
	return service, nil
}
