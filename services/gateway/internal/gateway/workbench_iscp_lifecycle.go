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
	endpoint *iscpworkbench.Endpoint
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
}

func (s *Server) StartWorkbenchISCP(ctx context.Context, cfg iscpworkbench.Config, onState func(string)) (*WorkbenchISCP, error) {
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
	endpoint, err := iscpworkbench.NewEndpoint(cfg, handler, onState)
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
