package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// LinkOptions configures the link entry (docs/client-link-layer-design.md).
type LinkOptions struct {
	// StateDir is shared with the ISCP connector: membership, directory,
	// LAN publication and the connector socket.
	StateDir string
	// LANAddr enables the LAN connector, for example ":18792".
	LANAddr string
	// LANHosts are the names/addresses devices use on the LAN.
	LANHosts []string
	// OwnerID is the single owner this backend serves.
	OwnerID string
}

type linkRuntime struct {
	opts      LinkOptions
	service   *link.Service
	directory *link.Directory
	lan       *link.LANServer
	socket    *link.SocketServer
	runs      *linkRunHub
	receipts  *linkReceipts
	objects   *linkObjects
	stop      chan struct{}
	stopOnce  sync.Once
	clientsMu sync.Mutex
	clients   map[string]bool
}

// LinkService exposes the link service (tests and wiring).
func (s *Server) LinkService() *link.Service {
	if s.link == nil {
		return nil
	}
	return s.link.service
}

// StartLink brings up the link entry, the connector socket and, when
// configured, the LAN connector.
func (s *Server) StartLink(opts LinkOptions) error {
	if strings.TrimSpace(opts.StateDir) == "" {
		return errors.New("link state directory is required")
	}
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return err
	}
	if opts.OwnerID == "" {
		opts.OwnerID = app.DefaultOwnerID
	}
	rt := &linkRuntime{opts: opts, service: link.NewService(), directory: link.NewDirectory(opts.StateDir), stop: make(chan struct{}), clients: map[string]bool{}}
	rt.service.Backend = "sparkclaw"
	rt.runs = newLinkRunHub()
	receipts, err := openLinkReceipts(filepath.Join(opts.StateDir, "receipts.json"))
	if err != nil {
		return err
	}
	rt.receipts = receipts
	objects, err := openLinkObjects(filepath.Join(opts.StateDir, "objects"))
	if err != nil {
		return err
	}
	rt.objects = objects
	s.link = rt
	s.registerLinkHandlers(rt.service)
	rt.directory.OnRevoke(func(deviceID string) { s.revokeLinkDevice(deviceID) })
	_ = rt.directory.Refresh()
	go rt.directory.Watch(rt.stop, 3*time.Second)
	socket, err := link.NewSocketServer(filepath.Join(opts.StateDir, link.SocketFile), rt.service, s.authorizeLinkDevice)
	if err != nil {
		return err
	}
	rt.socket = socket
	if opts.LANAddr != "" {
		lan, err := link.NewLANServer(link.LANConfig{Addr: opts.LANAddr, Hosts: opts.LANHosts, StateDir: opts.StateDir, Directory: rt.directory, Service: rt.service, Authorize: s.authorizeLinkDevice})
		if err != nil {
			socket.Close()
			return err
		}
		if err := lan.Start(); err != nil {
			socket.Close()
			return err
		}
		rt.lan = lan
	}
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		s.watchLinkChanges(rt)
	}()
	slog.Info("link entry ready", "state_dir", opts.StateDir, "lan", opts.LANAddr)
	return nil
}

// CloseLink stops the link entry.
func (s *Server) CloseLink(ctx context.Context) {
	rt := s.link
	if rt == nil {
		return
	}
	rt.stopOnce.Do(func() { close(rt.stop) })
	if rt.lan != nil {
		_ = rt.lan.Shutdown(ctx)
	}
	if rt.socket != nil {
		_ = rt.socket.Close()
	}
}

func linkClientID(deviceID string) string { return "link-" + deviceID }

// authorizeLinkDevice checks the directory on every connection and keeps a
// Gateway client row per device so the existing revocation fences
// (execution leases, browser hosts, client connections) apply.
func (s *Server) authorizeLinkDevice(ctx context.Context, deviceID, transport string) (link.Principal, error) {
	rt := s.link
	rec, ok := rt.directory.Lookup(deviceID)
	if !ok {
		return link.Principal{}, link.ErrDeviceNotTrusted
	}
	if !contract.ValidClass(rec.Type) {
		return link.Principal{}, link.ErrDeviceNotTrusted
	}
	clientID := linkClientID(deviceID)
	if err := s.ensureLinkClient(ctx, rt, clientID); err != nil {
		return link.Principal{}, err
	}
	return link.Principal{
		Principal: contract.Principal{OwnerID: rt.opts.OwnerID, DeviceID: deviceID, Class: rec.Type, Name: rec.Name},
		ClientID:  clientID,
		Transport: transport,
	}, nil
}

func (s *Server) ensureLinkClient(ctx context.Context, rt *linkRuntime, clientID string) error {
	rt.clientsMu.Lock()
	known := rt.clients[clientID]
	rt.clientsMu.Unlock()
	if known {
		return nil
	}
	existing, found, err := s.store.GetClient(ctx, clientID)
	if err != nil {
		return err
	}
	if found && existing.RevokedAt != nil {
		return link.ErrDeviceNotTrusted
	}
	if !found {
		sum := sha256.Sum256([]byte("sparkclaw-link-device:" + clientID))
		_, err = s.store.RegisterClient(ctx, app.Client{ID: clientID, OwnerID: rt.opts.OwnerID, ActorID: rt.opts.OwnerID, Name: "link:" + strings.TrimPrefix(clientID, "link-"), TokenHash: hex.EncodeToString(sum[:])})
		if err != nil && store.StoreErrorCodeOf(err) != store.StoreErrorConflict {
			return err
		}
	}
	rt.clientsMu.Lock()
	rt.clients[clientID] = true
	rt.clientsMu.Unlock()
	return nil
}

func (s *Server) revokeLinkDevice(deviceID string) {
	rt := s.link
	if rt == nil {
		return
	}
	clientID := linkClientID(deviceID)
	rt.service.Revoke(deviceID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, found, err := s.store.GetClient(ctx, clientID); err == nil && found {
		if _, err := s.store.RevokeClient(ctx, clientID); err != nil {
			slog.Warn("link device revocation not persisted", "device", deviceID, "code", store.StoreErrorCodeOf(err))
		}
	}
	s.cancelClientConnections(clientID)
	rt.clientsMu.Lock()
	delete(rt.clients, clientID)
	rt.clientsMu.Unlock()
}

// watchLinkChanges pushes sync and approval invalidations to duplex
// connections. Events are only an accelerator; the cursor stays the truth.
func (s *Server) watchLinkChanges(rt *linkRuntime) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	var lastCursor int64 = -1
	var lastApprovals string
	// The starting cursor is taken before any device connects, so a change
	// made right after connecting is still announced.
	for cursor := int64(0); ; {
		page, err := s.store.SessionChanges(context.Background(), rt.opts.OwnerID, cursor, 500)
		if err != nil {
			break
		}
		lastCursor = cursor
		if len(page) == 0 {
			break
		}
		cursor = page[len(page)-1].Session.Revision
		lastCursor = cursor
		if len(page) < 200 {
			break
		}
	}
	for {
		select {
		case <-rt.stop:
			return
		case <-s.executionContext().Done():
			return
		case <-t.C:
		}
		if len(rt.service.Connections("")) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		changes, err := s.store.SessionChanges(ctx, rt.opts.OwnerID, maxInt64(lastCursor, 0), 500)
		if err == nil && len(changes) > 0 {
			head := changes[len(changes)-1].Session.Revision
			for len(changes) >= 200 {
				more, err := s.store.SessionChanges(ctx, rt.opts.OwnerID, head, 500)
				if err != nil || len(more) == 0 {
					break
				}
				changes = more
				head = more[len(more)-1].Session.Revision
			}
			if lastCursor >= 0 && head > lastCursor {
				rt.service.Broadcast(rt.opts.OwnerID, contract.TopicSync, contract.SyncEvent{Cursor: head})
			}
			if head > lastCursor {
				lastCursor = head
			}
		} else if err == nil && lastCursor < 0 {
			lastCursor = 0
		}
		approvals, err := s.store.ListApprovals(ctx, app.ApprovalStatusPending)
		cancel()
		if err == nil {
			ids := make([]string, 0, len(approvals))
			for _, a := range approvals {
				ids = append(ids, a.ID)
			}
			key := strings.Join(ids, ",")
			if key != lastApprovals {
				if lastApprovals != "" || key != "" {
					rt.service.Broadcast(rt.opts.OwnerID, contract.TopicApproval, map[string]int{"pending": len(ids)})
				}
				lastApprovals = key
			}
		}
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
