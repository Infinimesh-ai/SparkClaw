package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
)

type linkTestBackend struct {
	server   *Server
	stateDir string
	host     identity.Device
	lan      consoleapi.LAN
}

func freeLinkPort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func writeLinkDirectory(t *testing.T, dir string, devices ...consoleapi.DeviceRecord) {
	t.Helper()
	if err := link.WriteJSONAtomic(filepath.Join(dir, link.DirectoryFile), consoleapi.Directory{DomainID: "dom_test", Revision: time.Now().UnixNano(), Devices: devices, SyncedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// Directory reload keys on mtime+size; make the change visible.
	future := time.Now().Add(time.Duration(len(devices)+1) * time.Second)
	_ = os.Chtimes(filepath.Join(dir, link.DirectoryFile), future, future)
}

func newLinkTestBackend(t *testing.T) *linkTestBackend {
	t.Helper()
	s, _ := credentialServer(t)
	lifecycle, stop := context.WithCancel(context.Background())
	s.BindLifecycleContext(lifecycle)
	// Unix socket paths are limited to ~104 bytes on macOS.
	dir, err := os.MkdirTemp("/tmp", "lk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	host, err := identity.NewDevice(crypto.NewProvider(), "relay-domain", "sparkclaw-host", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := link.WriteJSONAtomic(filepath.Join(dir, link.MembershipFile), consoleapi.Membership{DomainID: "dom_test", Device: consoleapi.DeviceRecord{DeviceID: host.Identity.DeviceID, Type: consoleapi.TypeSparkClaw, Role: consoleapi.RoleDomainHost, Identity: host.Identity, State: consoleapi.StateTrusted}}); err != nil {
		t.Fatal(err)
	}
	writeLinkDirectory(t, dir)
	addr := freeLinkPort(t)
	if err := s.StartLink(LinkOptions{StateDir: dir, LANAddr: addr, LANHosts: []string{"127.0.0.1"}, OwnerID: app.DefaultOwnerID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.CloseLink(ctx)
		stop()
		_ = s.WaitForBackgroundWork(ctx)
		if s.executions != nil {
			s.executions.Close()
		}
	})
	raw, err := os.ReadFile(filepath.Join(dir, link.LANFile))
	if err != nil {
		t.Fatal(err)
	}
	var lan consoleapi.LAN
	if err := json.Unmarshal(raw, &lan); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(addr)
	lan.Endpoints = []string{"wss://127.0.0.1:" + port + contract.LANPath}
	return &linkTestBackend{server: s, stateDir: dir, host: host, lan: lan}
}

func newLinkTestDevice(t *testing.T, b *linkTestBackend, class string, conditions contract.Conditions) (*linkclient.Client, identity.Device, consoleapi.DeviceRecord, chan contract.Frame) {
	t.Helper()
	dev, err := identity.NewDevice(crypto.NewProvider(), "relay-domain", class+"-"+crypto.Base64URL(crypto.RandomBytes(6)), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	rec := consoleapi.DeviceRecord{DeviceID: dev.Identity.DeviceID, Type: class, Role: consoleapi.RoleOwnerDevice, Identity: dev.Identity, State: consoleapi.StateTrusted, Name: class}
	events := make(chan contract.Frame, 256)
	c, err := linkclient.New(linkclient.Options{Device: dev, Host: consoleapi.Host{DeviceID: b.host.Identity.DeviceID, Identity: b.host.Identity, LAN: b.lan}, Describe: contract.Device{Class: class, Name: class, Conditions: conditions}, OnEvent: func(f contract.Frame) {
		select {
		case events <- f:
		default:
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	return c, dev, rec, events
}

func TestLinkEndToEndOverLAN(t *testing.T) {
	b := newLinkTestBackend(t)
	phone, _, rec, events := newLinkTestDevice(t, b, contract.ClassPhone, contract.Conditions{Attachments: true, Screen: contract.ScreenSmall, BrowserSurface: true})
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	phone.Start(ctx)
	defer phone.Close()
	if err := phone.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if phone.Path() != linkclient.PathLAN {
		t.Fatal("expected LAN path")
	}
	if phone.Available(contract.OpRunSubscribe) == false || phone.Available(contract.OpObjectRead) == false {
		t.Fatalf("features %v", phone.Hello().Features)
	}
	if len(phone.Hello().Reverse) != 0 {
		t.Fatal("a phone must not get the browser surface")
	}

	var header contract.SessionHeader
	if err := phone.Request(ctx, contract.OpSessionCreate, contract.SessionCreateParams{Title: "Link", ClientRequestID: "create-1"}, &header); err != nil {
		t.Fatal(err)
	}
	var again contract.SessionHeader
	if err := phone.Request(ctx, contract.OpSessionCreate, contract.SessionCreateParams{Title: "Link", ClientRequestID: "create-1"}, &again); err != nil || again.ID != header.ID {
		t.Fatalf("idempotent create %v %v", again.ID, err)
	}
	var changes contract.SyncChangesResult
	if err := phone.Request(ctx, contract.OpSyncChanges, contract.SyncChangesParams{}, &changes); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range changes.Sessions {
		found = found || h.ID == header.ID
	}
	if !found || changes.Cursor == 0 {
		t.Fatalf("sync %+v", changes)
	}

	// Upload an attachment in chunks and read it back.
	content := bytes.Repeat([]byte("SparkClaw link attachment\n"), 30000)
	sum := sha256.Sum256(content)
	var up contract.ObjectUploadState
	if err := phone.Request(ctx, contract.OpObjectUploadBegin, contract.ObjectUploadBeginParams{UploadKey: "u1", Name: "notes.txt", Size: int64(len(content)), ContentType: "text/plain", SHA256: hex.EncodeToString(sum[:]), SessionID: header.ID}, &up); err != nil {
		t.Fatal(err)
	}
	for off := up.Received; off < int64(len(content)); {
		end := off + int64(up.ChunkSize)
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		if err := phone.Request(ctx, contract.OpObjectUploadChunk, contract.ObjectUploadChunkParams{UploadID: up.UploadID, Offset: off, Data: content[off:end]}, &up); err != nil {
			t.Fatal(err)
		}
		off = up.Received
	}
	if err := phone.Request(ctx, contract.OpObjectUploadCommit, contract.ObjectUploadCommitParams{UploadID: up.UploadID}, &up); err != nil || up.ObjectID == "" {
		t.Fatalf("commit %+v %v", up, err)
	}
	var read []byte
	for off := int64(0); ; {
		var chunk contract.ObjectReadResult
		if err := phone.Request(ctx, contract.OpObjectRead, contract.ObjectReadParams{ObjectID: up.ObjectID, Offset: off}, &chunk); err != nil {
			t.Fatal(err)
		}
		read = append(read, chunk.Data...)
		off += int64(len(chunk.Data))
		if chunk.EOF {
			break
		}
	}
	if !bytes.Equal(read, content) {
		t.Fatal("attachment round trip differs")
	}

	// Subscribe, send, and follow the run to completion.
	var sub contract.RunSubscribeResult
	if err := phone.Request(ctx, contract.OpRunSubscribe, contract.RunSubscribeParams{SessionID: header.ID}, &sub); err != nil {
		t.Fatal(err)
	}
	var sent contract.MessageSendResult
	if err := phone.Request(ctx, contract.OpMessageSend, contract.MessageSendParams{SessionID: header.ID, ClientMessageID: "msg-1", Content: "你好，链接层", Attachments: []contract.AttachmentRef{{ObjectID: up.ObjectID}}}, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.RunID == "" || sent.Message.Role != "user" || len(sent.Message.Attachments) != 1 {
		t.Fatalf("send %+v", sent)
	}
	var final *contract.RunStatus
	deadline := time.After(40 * time.Second)
	for final == nil {
		select {
		case f := <-events:
			if f.Topic != contract.TopicRun {
				continue
			}
			var ev contract.RunEvent
			_ = json.Unmarshal(f.Data, &ev)
			if ev.Kind == "final" && ev.Status != nil && ev.RunID == sent.RunID {
				final = ev.Status
			}
		case <-deadline:
			var st contract.RunStatus
			_ = phone.Request(ctx, contract.OpRunStatus, contract.RunRef{SessionID: header.ID}, &st)
			t.Fatalf("no final run event; status %+v", st)
		}
	}
	if final.State != contract.RunCompleted && final.State != contract.RunApprovalPending && final.State != contract.RunFailed {
		t.Fatalf("final %+v", final)
	}
	var page contract.SessionMessagesResult
	if err := phone.Request(ctx, contract.OpSessionMessages, contract.SessionMessagesParams{SessionID: header.ID}, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) < 1 || page.Messages[0].Seq != 1 || page.Messages[0].ClientMessageID != "msg-1" {
		t.Fatalf("page %+v", page)
	}
	// Retrying the same send replays it.
	var replay contract.MessageSendResult
	if err := phone.Request(ctx, contract.OpMessageSend, contract.MessageSendParams{SessionID: header.ID, ClientMessageID: "msg-1", Content: "你好，链接层", Attachments: []contract.AttachmentRef{{ObjectID: up.ObjectID}}}, &replay); err != nil || !replay.Replayed || replay.RunID != sent.RunID {
		t.Fatalf("replay %+v %v", replay, err)
	}
	// A retry that only learns the outcome: the original admission, and
	// not_found for a message that never arrived.
	var looked contract.MessageSendResult
	if err := phone.Request(ctx, contract.OpMessageSend, contract.MessageSendParams{SessionID: header.ID, ClientMessageID: "msg-1", Content: "你好，链接层", Attachments: []contract.AttachmentRef{{ObjectID: up.ObjectID}}, ReplayOnly: true}, &looked); err != nil || !looked.Replayed || looked.RunID != sent.RunID {
		t.Fatalf("replay_only %+v %v", looked, err)
	}
	if err := phone.Request(ctx, contract.OpMessageSend, contract.MessageSendParams{SessionID: header.ID, ClientMessageID: "msg-never", Content: "never sent", ReplayOnly: true}, nil); contract.AsError(err).Code != contract.CodeNotFound || !contract.AsError(err).NotSent {
		t.Fatalf("replay_only of an unknown message: %v", err)
	}
	if err := phone.Request(ctx, contract.OpMessageSend, contract.MessageSendParams{SessionID: header.ID, ClientMessageID: "msg-1", Content: "other content", ReplayOnly: true}, nil); contract.AsError(err).Code != contract.CodeConflict {
		t.Fatalf("replay_only with other content: %v", err)
	}
	// Unknown operations keep the connection.
	if err := phone.Request(ctx, "nope.op", nil, nil); err == nil {
		t.Fatal("unknown op accepted")
	} else if contract.AsError(err).Code != contract.CodeUnsupportedCapability {
		t.Fatal(err)
	}
	if err := phone.Ping(ctx); err != nil {
		t.Fatal("connection lost after unknown op")
	}

	// Revocation closes the connection and refuses new ones.
	rec.State = consoleapi.StateRevoked
	writeLinkDirectory(t, b.stateDir, rec)
	_ = b.server.link.directory.Refresh()
	waitUntil(t, 10*time.Second, func() bool { return phone.Path() == linkclient.PathNone })
	time.Sleep(1500 * time.Millisecond)
	if phone.Path() != linkclient.PathNone {
		t.Fatal("revoked device reconnected")
	}
}

func TestLinkWatchProjectionAndFeatures(t *testing.T) {
	b := newLinkTestBackend(t)
	watch, _, rec, _ := newLinkTestDevice(t, b, contract.ClassWatch, contract.Conditions{Attachments: true, Screen: contract.ScreenTiny})
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	watch.Start(ctx)
	defer watch.Close()
	if err := watch.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if watch.Available(contract.OpObjectRead) || watch.Available(contract.OpObjectUploadBegin) {
		t.Fatal("a watch shows file names only")
	}
	err := watch.Request(ctx, contract.OpObjectRead, contract.ObjectReadParams{ObjectID: "x"}, nil)
	if contract.AsError(err).Code != contract.CodeUnsupportedCapability {
		t.Fatalf("watch object.read: %v", err)
	}
	if !watch.Available(contract.OpMessageSend) || !watch.Available(contract.OpApprovalResolve) {
		t.Fatal("core operations must be available on a watch")
	}
}

func TestLinkTwoDevicesShareConversations(t *testing.T) {
	b := newLinkTestBackend(t)
	phone, _, prec, _ := newLinkTestDevice(t, b, contract.ClassPhone, contract.Conditions{Screen: contract.ScreenSmall})
	desk, _, drec, devents := newLinkTestDevice(t, b, contract.ClassDesktop, contract.Conditions{Screen: contract.ScreenLarge, Attachments: true})
	writeLinkDirectory(t, b.stateDir, prec, drec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	phone.Start(ctx)
	desk.Start(ctx)
	defer phone.Close()
	defer desk.Close()
	if phone.WaitReady(ctx) != nil || desk.WaitReady(ctx) != nil {
		t.Fatal("not ready")
	}
	var h contract.SessionHeader
	if err := phone.Request(ctx, contract.OpSessionCreate, contract.SessionCreateParams{Title: "Shared", ClientRequestID: "c"}, &h); err != nil {
		t.Fatal(err)
	}
	// The desktop learns of the change through a sync event and the cursor.
	gotSync := false
	deadline := time.After(10 * time.Second)
	for !gotSync {
		select {
		case f := <-devents:
			gotSync = f.Topic == contract.TopicSync
		case <-deadline:
			t.Fatal("no sync event on the second device")
		}
	}
	var changes contract.SyncChangesResult
	if err := desk.Request(ctx, contract.OpSyncChanges, contract.SyncChangesParams{}, &changes); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, s := range changes.Sessions {
		seen = seen || s.ID == h.ID
	}
	if !seen {
		t.Fatal("desktop does not see the phone's conversation")
	}
	var archived contract.SessionHeader
	if err := desk.Request(ctx, contract.OpSessionArchive, contract.SessionArchiveParams{SessionID: h.ID, Archived: true}, &archived); err != nil || !archived.Archived {
		t.Fatal(err)
	}
	var del map[string]any
	if err := phone.Request(ctx, contract.OpSessionDelete, contract.SessionRef{SessionID: h.ID}, &del); err != nil {
		t.Fatal(err)
	}
	var after contract.SyncChangesResult
	if err := desk.Request(ctx, contract.OpSyncChanges, contract.SyncChangesParams{Cursor: changes.Cursor}, &after); err != nil {
		t.Fatal(err)
	}
	deleted := false
	for _, s := range after.Sessions {
		deleted = deleted || (s.ID == h.ID && s.Deleted)
	}
	if !deleted {
		t.Fatalf("no tombstone %+v", after)
	}
}

func waitUntil(t *testing.T, d time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

var _ = sync.Mutex{}

func TestLinkDomainCallBridge(t *testing.T) {
	b := newLinkTestBackend(t)
	desk, _, drec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, contract.Conditions{Screen: contract.ScreenLarge, Attachments: true, BrowserSurface: true})
	phone, _, prec, _ := newLinkTestDevice(t, b, contract.ClassPhone, contract.Conditions{Screen: contract.ScreenSmall})
	writeLinkDirectory(t, b.stateDir, drec, prec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	desk.Start(ctx)
	phone.Start(ctx)
	defer desk.Close()
	defer phone.Close()
	if desk.WaitReady(ctx) != nil || phone.WaitReady(ctx) != nil {
		t.Fatal("not ready")
	}
	var out contract.DomainCallResult
	if err := desk.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: "settings", Method: "settings.owner.get"}, &out); err != nil {
		t.Fatal(err)
	}
	var res linkDomainResult
	if err := json.Unmarshal(out.Result, &res); err != nil || res.Status != 200 || len(res.Body) == 0 {
		t.Fatalf("settings.owner.get %+v %v %s", res, err, out.Result)
	}
	if err := desk.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: "notifications", Method: "notifications.list", Args: json.RawMessage(`{"params":{"limit":"5"}}`)}, &out); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(out.Result, &res)
	if res.Status != 200 {
		t.Fatalf("notifications.list %+v", res)
	}
	for _, m := range []string{"presentation.config", "presentation.ready", "presentation.owner"} {
		if err := desk.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: "presentation", Method: m}, &out); err != nil {
			t.Fatal(m, err)
		}
		_ = json.Unmarshal(out.Result, &res)
		if res.Status != 200 || len(res.Body) < 3 {
			t.Fatalf("%s %+v", m, res)
		}
	}
	// Replaced and console-owned operations are refused with a reason.
	err := desk.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: "execution", Method: "execution.submit"}, nil)
	if contract.AsError(err).Code != contract.CodeUnsupportedCapability {
		t.Fatalf("execution.submit through domain.call: %v", err)
	}
	// A phone has no built-in browser, so MCD selections are closed to it.
	err = phone.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: "mcd", Method: "mcd.session.status"}, nil)
	if contract.AsError(err).Code != contract.CodeUnsupportedCapability {
		t.Fatalf("phone mcd: %v", err)
	}
	var describe contract.DeviceDescribeResult
	if err := desk.Request(ctx, contract.OpDeviceDescribe, nil, &describe); err != nil || len(describe.Domains) < 3 {
		t.Fatalf("describe %+v %v", describe, err)
	}
}

func TestLinkReverseCallsReachTheDesktopBrowser(t *testing.T) {
	b := newLinkTestBackend(t)
	dev, _ := identity.NewDevice(crypto.NewProvider(), "relay-domain", "desktop-rev", time.Now().UTC())
	rec := consoleapi.DeviceRecord{DeviceID: dev.Identity.DeviceID, Type: contract.ClassDesktop, Role: consoleapi.RoleOwnerDevice, Identity: dev.Identity, State: consoleapi.StateTrusted}
	writeLinkDirectory(t, b.stateDir, rec)
	served := make(chan string, 4)
	desk, err := linkclient.New(linkclient.Options{Device: dev, Host: consoleapi.Host{DeviceID: b.host.Identity.DeviceID, Identity: b.host.Identity, LAN: b.lan}, Describe: contract.Device{Class: contract.ClassDesktop, Conditions: contract.Conditions{BrowserSurface: true, Attachments: true, Screen: contract.ScreenLarge}},
		Reverse: []string{"browser.controller.status", "mcd.orders.query"},
		OnReverse: func(_ context.Context, op string, params json.RawMessage) (any, error) {
			var req linkReverseRequest
			_ = json.Unmarshal(params, &req)
			served <- op + " " + string(req.Body)
			return map[string]any{"status": 200, "body": map[string]any{"state": "available", "op": op}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	desk.Start(ctx)
	defer desk.Close()
	if err := desk.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if got := desk.Hello().Reverse; len(got) != 2 {
		t.Fatalf("accepted reverse %v", got)
	}
	var peer linkPeer
	waitUntil(t, 5*time.Second, func() bool {
		var e error
		peer, e = b.server.browserRoutes.peerFor(app.DefaultOwnerID, linkClientID(dev.Identity.DeviceID), linkInstallationID(dev.Identity.DeviceID))
		return e == nil
	})
	resp, err := peer.Command(ctx, "browser.controller.status", json.RawMessage(`{"probe":1}`))
	if err != nil || resp.Status != 200 || !strings.Contains(string(resp.Body), `"available"`) {
		t.Fatalf("reverse call %+v %v", resp, err)
	}
	if got := <-served; got != `browser.controller.status {"probe":1}` {
		t.Fatal(got)
	}
	// An operation the device did not declare is never sent.
	if _, err := peer.Command(ctx, "mcd.checkout.commit", json.RawMessage(`{}`)); !errors.Is(err, errLinkPeerUnavailable) {
		t.Fatalf("undeclared reverse op: %v", err)
	}
}
