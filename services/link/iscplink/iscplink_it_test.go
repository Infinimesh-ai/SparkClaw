package iscplink

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/relay"
	"github.com/Chiiz0/SparkClaw/services/link/relaytest"
)

type mapDirectory struct {
	mu sync.Mutex
	m  map[string]DeviceEntry
}

func (d *mapDirectory) Lookup(id string) (DeviceEntry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.m[id]
	return e, ok
}

func (d *mapDirectory) set(e DeviceEntry) {
	d.mu.Lock()
	d.m[e.Identity.DeviceID] = e
	d.mu.Unlock()
}

func bindDevice(t *testing.T, r *relaytest.Relay, dev identity.Device) relay.Credentials {
	t.Helper()
	proof, err := dev.CreateProof(crypto.NewProvider(), r.RelayID, "bind", crypto.Base64URL(crypto.RandomBytes(16)), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	creds, err := relay.Bind(context.Background(), http.DefaultClient, r.BaseURL, r.AdminToken, dev.Identity, proof)
	if err != nil {
		t.Fatal(err)
	}
	return creds
}

func TestInitiatorResponderOverRealRelay(t *testing.T) {
	r := relaytest.Start(t)
	p := crypto.NewProvider()
	now := time.Now().UTC()
	signer, _ := identity.NewDevice(p, r.DomainID, "console-signer", now)
	host, _ := identity.NewDevice(p, r.DomainID, "sparkclaw-host", now)
	phone, _ := identity.NewDevice(p, r.DomainID, "phone-1", now)
	ep := relay.Endpoint{DomainID: r.DomainID, RelayID: r.RelayID, BaseURL: r.BaseURL, WebSocketURL: r.WSURL}
	sign := func(ttl time.Duration, epoch uint64) trust.Grant {
		g, err := trust.SignGrant(p, signer, trust.Grant{GrantID: "grant-" + crypto.Base64URL(crypto.RandomBytes(6)), SubjectDeviceID: phone.Identity.DeviceID, Audience: host.Identity.DeviceID, ConfirmationThumbprint: phone.Identity.PublicKey.KID, Permissions: []string{GrantPermission}, RelayConstraints: []string{r.RelayID}, NotBefore: time.Now().Add(-time.Second), ExpiresAt: time.Now().Add(ttl), RevocationEpoch: epoch})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	dir := &mapDirectory{m: map[string]DeviceEntry{}}
	dir.set(DeviceEntry{Identity: phone.Identity, Trusted: true, RevocationEpoch: 1, Class: "phone"})

	hostRelay, err := relay.NewClient(ep, host, nil, bindDevice(t, r, host), nil)
	if err != nil {
		t.Fatal(err)
	}
	sessions := make(chan *PeerSession, 8)
	resp, err := NewResponder(ResponderConfig{Relay: hostRelay, Device: host, Signer: func() identity.DeviceIdentity { return signer.Identity }, Directory: dir, OnSession: func(s *PeerSession) { sessions <- s }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go resp.Run(ctx, nil)

	var grantMu sync.Mutex
	grant := sign(time.Hour, 1)
	phoneRelay, err := relay.NewClient(ep, phone, nil, bindDevice(t, r, phone), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 16)
	ini, err := NewInitiator(InitiatorConfig{Relay: phoneRelay, Device: phone, Host: host.Identity, Grant: func(context.Context) (trust.Grant, error) {
		grantMu.Lock()
		defer grantMu.Unlock()
		return grant, nil
	}, OnFrame: func(b []byte) { got <- string(b) }})
	if err != nil {
		t.Fatal(err)
	}
	go ini.Run(ctx)
	wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
	defer wcancel()
	if err := ini.WaitReady(wctx); err != nil {
		t.Fatalf("not ready: %v\n%s", err, r.Log())
	}
	var ps *PeerSession
	select {
	case ps = <-sessions:
	case <-time.After(10 * time.Second):
		t.Fatal("no peer session")
	}
	if ps.DeviceID != "phone-1" {
		t.Fatal(ps.DeviceID)
	}
	// Echo server.
	go func() {
		for f := range ps.Frames() {
			_ = ps.Send(context.Background(), append([]byte("echo:"), f...))
		}
	}()
	for i := 0; i < 5; i++ {
		if err := ini.Send(wctx, []byte{byte('a' + i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		select {
		case s := <-got:
			if s != "echo:"+string(rune('a'+i)) {
				t.Fatalf("frame %d = %q", i, s)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("echo timeout")
		}
	}

	// Revocation drops the session; a new handshake is refused.
	dir.set(DeviceEntry{Identity: phone.Identity, Trusted: false})
	resp.Revoke("phone-1")
	select {
	case <-ps.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session not closed on revoke")
	}
	time.Sleep(2 * time.Second)
	if ini.Ready() {
		t.Fatal("initiator still ready after revocation")
	}
	// Restoring trust lets the device in again with a fresh session.
	dir.set(DeviceEntry{Identity: phone.Identity, Trusted: true, RevocationEpoch: 1})
	wctx2, c2 := context.WithTimeout(ctx, 40*time.Second)
	defer c2()
	if err := ini.WaitReady(wctx2); err != nil {
		t.Fatal("did not reconnect after trust restored")
	}
	select {
	case ps = <-sessions:
	case <-time.After(10 * time.Second):
		t.Fatal("no second session")
	}
	go func() {
		for f := range ps.Frames() {
			_ = ps.Send(context.Background(), append([]byte("echo:"), f...))
		}
	}()
	if err := ini.Send(wctx2, []byte("z")); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "echo:z" {
			t.Fatal(s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("echo after reconnect")
	}

	// A Grant revoked by epoch is refused on the next frame.
	dir.set(DeviceEntry{Identity: phone.Identity, Trusted: true, RevocationEpoch: 2})
	_ = ini.Send(wctx2, []byte("after-epoch"))
	select {
	case <-ps.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("old-epoch grant still accepted")
	}
	grantMu.Lock()
	grant = sign(time.Hour, 2)
	grantMu.Unlock()
	wctx3, c3 := context.WithTimeout(ctx, 40*time.Second)
	defer c3()
	time.Sleep(time.Second)
	if err := ini.WaitReady(wctx3); err != nil {
		t.Fatal("no session with renewed grant")
	}

	// Responder restart: the connector forgets every session; the device's
	// next frame triggers a reset and a fresh handshake.
	for len(sessions) > 0 {
		<-sessions
	}
	resp.closeAll("restart")
	_ = ini.Send(wctx3, []byte("lost"))
	select {
	case ps = <-sessions:
	case <-time.After(40 * time.Second):
		t.Fatal("no session after responder restart")
	}
	go func() {
		for f := range ps.Frames() {
			_ = ps.Send(context.Background(), append([]byte("echo:"), f...))
		}
	}()
	for len(got) > 0 {
		<-got
	}
	wctx4, c4 := context.WithTimeout(ctx, 30*time.Second)
	defer c4()
	if err := ini.Send(wctx4, []byte("y")); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "echo:y" {
			t.Fatal(s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("echo after responder restart")
	}
}
