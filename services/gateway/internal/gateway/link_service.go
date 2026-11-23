package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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
	domainObj *linkDomainObjects
	speech    *linkSpeech
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
	domainObjects, err := openLinkDomainObjects(filepath.Join(opts.StateDir, "objects", "domain"))
	if err != nil {
		return err
	}
	rt.domainObj = domainObjects
	if rt.speech, err = newLinkSpeech(filepath.Join(opts.StateDir, "speech")); err != nil {
		return err
	}
	s.link = rt
	s.registerLinkHandlers(rt.service)
	// Desktops with a built-in browser serve the controller and McDonald's
	// reverse operations over their link connection.
	unroute := s.browserRoutes.add(s.linkPeerRoute)
	go func() { <-rt.stop; unroute() }()
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
		rt.service.LAN = lan.Info
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
		registered, err := s.store.RegisterClient(ctx, app.Client{ID: clientID, OwnerID: rt.opts.OwnerID, ActorID: rt.opts.OwnerID, Name: "link:" + strings.TrimPrefix(clientID, "link-"), TokenHash: hex.EncodeToString(sum[:])})
		if err != nil && store.StoreErrorCodeOf(err) != store.StoreErrorConflict {
			return err
		}
		if err == nil && registered.RevokedAt != nil {
			return link.ErrDeviceNotTrusted
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
	if existing, found, err := s.store.GetClient(ctx, clientID); err == nil && found && existing.RevokedAt == nil {
		if revoked, err := s.store.RevokeClient(ctx, clientID); err != nil || revoked.RevokedAt == nil {
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
	lastApprovals := map[string]string{}
	// The starting cursor is taken before any device connects, so a change
	// made right after connecting is still announced.
	startCtx, cancelStart := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStart()
	for cursor := int64(0); ; {
		page, err := s.store.SessionChanges(startCtx, rt.opts.OwnerID, store.AllDevices, cursor, 500)
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
	// Business-domain invalidations follow the Gateway event log from its
	// tail at startup.
	lastEvent := ""
	if window, err := s.store.ReadEventWindow(startCtx, "", store.EventWindowLimit, true); err == nil {
		lastEvent = window.Cursor
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
		lastEvent = s.broadcastLinkDomainEvents(rt, lastEvent)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// Each device hears only of its own conversations (design 4.4).
		changes, err := s.store.SessionChanges(ctx, rt.opts.OwnerID, store.AllDevices, maxInt64(lastCursor, 0), 500)
		if err == nil && len(changes) > 0 {
			devices := map[string]bool{}
			head := lastCursor
			for len(changes) > 0 {
				for _, c := range changes {
					if c.Session.OriginDeviceID != "" {
						devices[c.Session.OriginDeviceID] = true
					}
					head = c.Session.Revision
				}
				if len(changes) < 200 {
					break
				}
				more, err := s.store.SessionChanges(ctx, rt.opts.OwnerID, store.AllDevices, head, 500)
				if err != nil {
					break
				}
				changes = more
			}
			if lastCursor >= 0 && head > lastCursor {
				for device := range devices {
					rt.service.BroadcastDevice(rt.opts.OwnerID, device, contract.TopicSync, contract.SyncEvent{Cursor: head})
				}
			}
			if head > lastCursor {
				lastCursor = head
			}
		} else if err == nil && lastCursor < 0 {
			lastCursor = 0
		}
		approvals, err := s.store.ListApprovals(ctx, app.ApprovalStatusPending)
		if err == nil {
			current := s.linkPendingApprovalsByDevice(ctx, approvals)
			for device, ids := range current {
				if lastApprovals[device] != ids.key {
					rt.service.BroadcastDevice(rt.opts.OwnerID, device, contract.TopicApproval, map[string]int{"pending": ids.count})
				}
			}
			for device := range lastApprovals {
				if _, ok := current[device]; !ok {
					rt.service.BroadcastDevice(rt.opts.OwnerID, device, contract.TopicApproval, map[string]int{"pending": 0})
				}
			}
			next := make(map[string]string, len(current))
			for device, ids := range current {
				next[device] = ids.key
			}
			lastApprovals = next
		}
		cancel()
	}
}

type linkDeviceApprovals struct {
	key   string
	count int
}

// linkPendingApprovalsByDevice groups pending approvals by the device that
// owns their conversation. Approvals of conversations without a device are
// decided in the host WebChat or their channel and reach no device.
func (s *Server) linkPendingApprovalsByDevice(ctx context.Context, approvals []app.Approval) map[string]linkDeviceApprovals {
	origins := map[string]string{}
	out := map[string]linkDeviceApprovals{}
	for _, a := range approvals {
		sessionID := strings.TrimSpace(a.SessionID)
		if sessionID == "" && strings.TrimSpace(a.RunID) != "" {
			if run, found, err := s.store.GetRun(ctx, a.RunID); err == nil && found {
				sessionID = run.SessionID
			}
		}
		if sessionID == "" {
			continue
		}
		device, seen := origins[sessionID]
		if !seen {
			if header, found, err := s.store.SessionHeader(ctx, sessionID); err == nil && found {
				device = header.OriginDeviceID
			}
			origins[sessionID] = device
		}
		if device == "" {
			continue
		}
		entry := out[device]
		if entry.key != "" {
			entry.key += ","
		}
		entry.key += a.ID
		entry.count++
		out[device] = entry
	}
	return out
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// linkEventDomains maps Gateway event categories to the business domains
// a device reads again.
var linkEventDomains = map[string]string{"email": "mail", "notifications": "notifications", "medicine": "medicine", "settings": "settings"}

// broadcastLinkDomainEvents announces which business domains changed since
// cursor and returns the new cursor. A lost cursor announces every domain.
func (s *Server) broadcastLinkDomainEvents(rt *linkRuntime, cursor string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window, err := s.store.ReadEventWindow(ctx, cursor, store.EventWindowLimit, false)
	if err != nil {
		return cursor
	}
	changed := map[string]bool{}
	if cursor != "" && !window.CursorFound {
		for _, d := range linkEventDomains {
			changed[d] = true
		}
		if tail, err := s.store.ReadEventWindow(ctx, "", store.EventWindowLimit, true); err == nil {
			window.Cursor = tail.Cursor
		}
	} else {
		for _, event := range window.Events {
			if d, ok := linkEventDomains[workbenchEventCategory(event.Type)]; ok && workbenchEventOwner(event) == rt.opts.OwnerID {
				changed[d] = true
			}
		}
	}
	if len(changed) > 0 {
		domains := make([]string, 0, len(changed))
		for d := range changed {
			domains = append(domains, d)
		}
		sort.Strings(domains)
		rt.service.Broadcast(rt.opts.OwnerID, contract.TopicDomain, contract.DomainEvent{Domains: domains})
	}
	if window.Cursor != "" {
		return window.Cursor
	}
	return cursor
}
