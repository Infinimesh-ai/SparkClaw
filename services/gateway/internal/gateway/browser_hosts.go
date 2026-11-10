package gateway

import (
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

func (s *Server) browserHostBroker() (*browserhost.Broker, error) {
	service, err := s.executionService()
	if err != nil {
		return nil, err
	}
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	if s.browserBroker != nil {
		return s.browserBroker, nil
	}
	broker, err := browserhost.NewBroker(filepath.Join(service.Root(), "browser-control"))
	if err != nil {
		return nil, execution.ErrUnavailable
	}
	broker.SetReverseRouter(&s.browserRoutes)
	broker.EnableClientController(s.cfg)
	s.browserBroker = broker
	ctx := s.executionContext()
	go func() { <-ctx.Done(); _ = broker.Close() }()
	return broker, nil
}

// browserFences projects an interrupted write as unknown without opening a
// writable broker or changing its persisted receipt.
func (s *Server) browserFences(identity browserhost.Identity) ([]browserhost.Fence, error) {
	s.executionMu.Lock()
	broker, root := s.browserBroker, s.executionRoot
	s.executionMu.Unlock()
	if broker != nil {
		return broker.Fences(identity), nil
	}
	if root == "" {
		root = s.cfg.State.Path + ".execution"
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return browserhost.ReadFences(filepath.Join(absolute, "browser-control"), identity)
}
