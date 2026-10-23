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
	children []*WorkbenchISCP
	endpoint interface {
		Run(context.Context) error
		Close() error
	}
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	// unroute stops the browser broker from addressing this transport's peers.
	unroute func()
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
	service := &WorkbenchISCP{endpoint: endpoint, cancel: cancel, done: make(chan struct{}), unroute: s.browserRoutes.add(endpointBrowserRoute(endpoint, cfg.ExpectedInstallationID))}
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
		if len(s.children) != 0 {
			for _, child := range s.children {
				if childErr := child.Close(); err == nil {
					err = childErr
				}
			}
			return
		}
		if s.unroute != nil {
			s.unroute()
		}
		s.cancel()
		err = s.endpoint.Close()
		<-s.done
	})
	return err
}

// startNodeWorkbenchISCP retains the Gateway Client authority and every existing
// business handler. A shared responder transport routes to isolated peer adapters.
func (s *Server) startNodeWorkbenchISCP(ctx context.Context, cfg iscpworkbench.Config, onState func(string)) (*WorkbenchISCP, error) {
	if !s.cfg.Gateway.WorkbenchISCPNodeManaged || cfg.Mode != "node-managed" || cfg.SchemaVersion != 2 {
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
	service := &WorkbenchISCP{endpoint: endpoint, cancel: cancel, done: make(chan struct{}), unroute: s.browserRoutes.add(nodeBrowserRoute(endpoint))}
	go func() {
		defer close(service.done)
		defer endpoint.Close()
		if err := endpoint.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("ISCP node workbench responder stopped")
		}
	}()
	return service, nil
}

// JoinWorkbenchISCP keeps independently keyed old/new responder transports alive
// during an audited migration and closes both before Gateway storage shutdown.
func JoinWorkbenchISCP(services ...*WorkbenchISCP) *WorkbenchISCP {
	children := make([]*WorkbenchISCP, 0, len(services))
	for _, service := range services {
		if service != nil {
			children = append(children, service)
		}
	}
	if len(children) == 0 {
		return nil
	}
	if len(children) == 1 {
		return children[0]
	}
	return &WorkbenchISCP{children: children}
}
