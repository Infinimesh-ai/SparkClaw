package localwebchat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type Server struct {
	cfg         Config
	assets      *os.Root
	transport   *http.Transport
	client      *http.Client
	http        *http.Server
	mu          sync.Mutex
	listeners   []net.Listener
	connections map[*ownedConnection]struct{}
	port        int
	started     bool
	closed      bool
	cancel      context.CancelFunc
	closeOnce   sync.Once
	closeErr    error
}

type connectionContextKey struct{}

// New opens only the public asset root. Credential/socket availability is checked
// on every private request so Gateway may start after the host ingress.
func New(cfg Config) (*Server, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(cfg.AssetsDir)
	if err != nil || resolved != cfg.AssetsDir {
		return nil, errors.New("local WebChat asset root must exist and contain no symbolic links")
	}
	assets, err := os.OpenRoot(cfg.AssetsDir)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, assets: assets, port: cfg.Port, connections: make(map[*ownedConnection]struct{})}
	s.transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			socket, err := privateSocket(cfg.RuntimeDir)
			if err != nil {
				return nil, err
			}
			return (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "unix", socket)
		},
		MaxIdleConns: 32, MaxIdleConnsPerHost: 32, MaxConnsPerHost: 128,
		IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second, MaxResponseHeaderBytes: 64 << 10,
	}
	s.client = &http.Client{Transport: s.transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return s, nil
}

// Start binds IPv4 loopback and IPv6 loopback where available. It never binds a
// wildcard address. The caller owns Close, including after a failed Start.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return errors.New("local WebChat server cannot be started again")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	ipv4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port)))
	if err != nil {
		return err
	}
	s.port = ipv4.Addr().(*net.TCPAddr).Port
	listeners := []net.Listener{ipv4}
	ipv6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(s.port)))
	if err == nil {
		listeners = append(listeners, ipv6)
	} else if !errors.Is(err, syscall.EAFNOSUPPORT) && !errors.Is(err, syscall.EADDRNOTAVAIL) && !errors.Is(err, syscall.EPROTONOSUPPORT) {
		_ = ipv4.Close()
		return fmt.Errorf("listen on IPv6 loopback: %w", err)
	}
	baseContext, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.http = &http.Server{
		Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10,
		BaseContext: func(net.Listener) context.Context { return baseContext },
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, connectionContextKey{}, conn)
		},
	}
	s.started = true
	s.listeners = listeners
	for _, listener := range listeners {
		go func(listener net.Listener) { _ = s.http.Serve(&ownedListener{Listener: listener, server: s}) }(listener)
	}
	return nil
}

func (s *Server) Addresses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	addresses := make([]string, 0, len(s.listeners))
	for _, listener := range s.listeners {
		addresses = append(addresses, listener.Addr().String())
	}
	return addresses
}

// Close also owns upgraded connections, which net/http Shutdown does not close.
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		server, cancel := s.http, s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if server != nil {
			s.closeErr = server.Shutdown(ctx)
		}
		s.mu.Lock()
		connections := make([]*ownedConnection, 0, len(s.connections))
		for conn := range s.connections {
			connections = append(connections, conn)
		}
		s.mu.Unlock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		s.transport.CloseIdleConnections()
		_ = s.assets.Close()
	})
	return s.closeErr
}

type ownedListener struct {
	net.Listener
	server *Server
}
type ownedConnection struct {
	net.Conn
	server *Server
	once   sync.Once
}

func (l *ownedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	owned := &ownedConnection{Conn: conn, server: l.server}
	l.server.mu.Lock()
	if l.server.closed {
		l.server.mu.Unlock()
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	l.server.connections[owned] = struct{}{}
	l.server.mu.Unlock()
	return owned, nil
}

func (c *ownedConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.server.mu.Lock(); delete(c.server.connections, c); c.server.mu.Unlock() })
	return err
}
