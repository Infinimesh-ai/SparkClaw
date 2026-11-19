package devconsole_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/devconsole"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

const (
	relayDomain = "infinimesh-test-domain"
	relayID     = "relay-test-1"
	relayToken  = "relay-admin-token-for-tests"
	adminPass   = "correct horse battery"
)

// --- fake Relay ---------------------------------------------------------------------

type fakeRelay struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	bound   map[string]int
	revoked []string
	fail    bool
}

func newFakeRelay(t *testing.T) *fakeRelay {
	fr := &fakeRelay{t: t, bound: map[string]int{}}
	provider := crypto.NewProvider()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v2/relay/devices/bind-self", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+relayToken {
			http.Error(w, "bad admin token", http.StatusUnauthorized)
			return
		}
		fr.mu.Lock()
		fail := fr.fail
		fr.mu.Unlock()
		if fail {
			http.Error(w, "relay is down", http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Identity identity.DeviceIdentity `json:"identity"`
			Proof    identity.DeviceProof    `json:"proof"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if body.Identity.DomainID != relayDomain {
			http.Error(w, "wrong domain", http.StatusForbidden)
			return
		}
		if err := identity.VerifyProof(provider, body.Identity, body.Proof, relayID, consoleapi.OpRelayAccess, time.Now(), 5*time.Minute); err != nil {
			http.Error(w, "bad proof: "+err.Error(), http.StatusForbidden)
			return
		}
		fr.mu.Lock()
		fr.bound[body.Identity.DeviceID]++
		n := fr.bound[body.Identity.DeviceID]
		fr.mu.Unlock()
		exp := time.Now().Add(time.Hour).UTC()
		_ = json.NewEncoder(w).Encode(relay.Credentials{
			Access:  relay.Token{Token: fmt.Sprintf("access-%s-%d", body.Identity.DeviceID, n), ExpiresAt: exp},
			Refresh: relay.Token{Token: fmt.Sprintf("refresh-%s-%d", body.Identity.DeviceID, n), ExpiresAt: exp.Add(24 * time.Hour)},
		})
	})
	mux.HandleFunc("POST /v2/relay/devices/revoke-access", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+relayToken {
			http.Error(w, "bad admin token", http.StatusUnauthorized)
			return
		}
		var body struct {
			DeviceID string `json:"device_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fr.mu.Lock()
		fr.revoked = append(fr.revoked, body.DeviceID)
		fr.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	fr.srv = httptest.NewServer(mux)
	t.Cleanup(fr.srv.Close)
	return fr
}

func (fr *fakeRelay) endpoint() relay.Endpoint {
	return relay.Endpoint{DomainID: relayDomain, RelayID: relayID, BaseURL: fr.srv.URL, WebSocketURL: strings.Replace(fr.srv.URL, "http", "ws", 1) + "/v2/relay/duplex"}
}

func (fr *fakeRelay) setFail(v bool) {
	fr.mu.Lock()
	fr.fail = v
	fr.mu.Unlock()
}

func (fr *fakeRelay) bindCount(id string) int {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return fr.bound[id]
}

func (fr *fakeRelay) wasRevoked(id string) bool {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for _, x := range fr.revoked {
		if x == id {
			return true
		}
	}
	return false
}

// --- console harness ----------------------------------------------------------------

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type harness struct {
	t      *testing.T
	dir    string
	clock  *clock
	relay  *fakeRelay
	srv    *devconsole.Server
	url    string
	pin    string
	client *consoleapi.Client
	admin  *http.Client
	token  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), clock: &clock{t: time.Now().UTC()}, relay: newFakeRelay(t)}
	cert, pin, err := devconsole.EnsureCertificate(h.dir, []string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	h.url = "https://" + ts.Listener.Addr().String()
	h.pin = pin
	h.srv, err = devconsole.New(devconsole.Options{
		DataDir:         h.dir,
		PublicURL:       h.url,
		TLSPin:          pin,
		Relay:           h.relay.endpoint(),
		RelayAdminToken: relayToken,
		AdminPassword:   adminPass,
		SecureCookies:   true,
		Now:             h.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h.srv.Handler()
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	if got := relay.CertificatePin(ts.Certificate().Raw); got != pin {
		t.Fatalf("served certificate pin %s, want %s", got, pin)
	}
	h.client = consoleapi.NewClient(h.url, pin)
	h.admin = relay.HTTPClient(relay.PinnedSHA256Config(pin), 10*time.Second)
	h.token, err = devconsole.RotateAPIToken(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) adminDo(method, path string, in, out any) int {
	h.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.url+path, body)
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := h.admin.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
	if res.StatusCode >= 300 && out != nil {
		h.t.Logf("%s %s: HTTP %d %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode
}

func (h *harness) createDomain(name string) string {
	h.t.Helper()
	var out struct {
		Data struct {
			DomainID string `json:"domain_id"`
		} `json:"data"`
	}
	if st := h.adminDo("POST", "/v1/domains", map[string]any{"metadata": map[string]string{"name": name}}, &out); st != http.StatusCreated {
		h.t.Fatalf("create domain: HTTP %d", st)
	}
	return out.Data.DomainID
}

func (h *harness) issue(domainID, deviceType string) consoleapi.Credential {
	h.t.Helper()
	var out struct {
		Data struct {
			Code        string `json:"code"`
			CodeDisplay string `json:"code_display"`
			Credential  string `json:"credential"`
			Status      string `json:"status"`
		} `json:"data"`
	}
	if st := h.adminDo("POST", "/v1/domains/"+domainID+"/access-credentials", map[string]any{"target_device_type": deviceType, "expires_in_seconds": 900}, &out); st != http.StatusCreated {
		h.t.Fatalf("issue credential: HTTP %d", st)
	}
	if out.Data.Status != "pending" || consoleapi.FormatCode(out.Data.Code) != out.Data.CodeDisplay {
		h.t.Fatalf("unexpected issued credential %+v", out.Data)
	}
	cred, err := consoleapi.ParseCredential(out.Data.Credential)
	if err != nil {
		h.t.Fatal(err)
	}
	if cred.Console != h.url || cred.TLSPin != h.pin || cred.Type != deviceType || cred.Code != out.Data.Code {
		h.t.Fatalf("credential does not name this console: %+v", cred)
	}
	return cred
}

func (h *harness) join(cred consoleapi.Credential, name string, lan *consoleapi.LAN) (consoleapi.Membership, identity.Device) {
	h.t.Helper()
	// A device knows the console from the credential alone.
	c := consoleapi.NewClient(cred.Console, cred.TLSPin)
	m, dev, err := c.Join(context.Background(), cred, consoleapi.JoinOptions{Name: name, Platform: "test", LAN: lan})
	if err != nil {
		h.t.Fatalf("join %s: %v", name, err)
	}
	return m, dev
}

func (h *harness) joinErr(cred consoleapi.Credential) error {
	_, _, err := h.client.Join(context.Background(), cred, consoleapi.JoinOptions{Name: "x"})
	return err
}

func (h *harness) verifyGrant(g *trust.Grant, signer identity.DeviceIdentity, dev identity.Device, hostID string) {
	h.t.Helper()
	if g == nil {
		h.t.Fatal("no grant")
	}
	err := trust.VerifyGrant(crypto.NewProvider(), *g, signer, trust.VerifyOptions{
		Audience:               hostID,
		SubjectDeviceID:        dev.Identity.DeviceID,
		ConfirmationThumbprint: dev.Identity.PublicKey.KID,
		Permission:             consoleapi.Permission,
		RelayID:                relayID,
		CurrentRevocationEpoch: 1,
		Now:                    h.clock.Now(),
	})
	if err != nil {
		h.t.Fatalf("grant does not verify: %v", err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !consoleapi.IsCode(err, code) {
		t.Fatalf("want console error %s, got %v", code, err)
	}
}

// --- tests --------------------------------------------------------------------------------

func TestJoinGrantDirectoryRevoke(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	tr, err := h.client.Trust(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tr.ConsoleID != h.srv.ConsoleID() || len(tr.Relays) != 1 || tr.Relays[0].RelayID != relayID || tr.Signer.PublicKey.KID != h.srv.Signer().PublicKey.KID {
		t.Fatalf("unexpected trust %+v", tr)
	}
	dom := h.createDomain("我的工作台")

	// A phone joins before the backend: no host, no Grant.
	early, earlyKey := h.join(h.issue(dom, consoleapi.TypePhone), "早到的手机", nil)
	if early.Host != nil || early.Grant != nil || early.DomainID != dom || early.DomainName != "我的工作台" {
		t.Fatalf("early membership %+v", early)
	}
	if early.Device.Type != consoleapi.TypePhone || early.Device.Role != consoleapi.RoleOwnerDevice || early.Device.State != consoleapi.StateTrusted {
		t.Fatalf("early device %+v", early.Device)
	}
	if early.RelayCredentials == nil || early.RelayCredentials.Access.Token == "" || h.relay.bindCount(earlyKey.Identity.DeviceID) != 1 {
		t.Fatal("phone was not registered at the Relay")
	}
	if early.RenewalExpiresAt == nil || early.RenewalExpiresAt.Sub(h.clock.Now()) < consoleapi.RenewalLimit-time.Minute {
		t.Fatalf("renewal authorization %v", early.RenewalExpiresAt)
	}
	gr, err := h.client.Grant(ctx, earlyKey, early.ConsoleID)
	if err != nil || gr.Grant != nil || gr.Host != nil {
		t.Fatalf("grant before host: %+v %v", gr, err)
	}

	// The backend joins with its LAN information.
	lan := &consoleapi.LAN{Endpoints: []string{"wss://192.168.1.10:18789/link/v1"}, TLSPin: "lan-pin"}
	hostM, hostKey := h.join(h.issue(dom, consoleapi.TypeSparkClaw), "工作台后端", lan)
	hostID := hostKey.Identity.DeviceID
	if hostM.Device.Role != consoleapi.RoleDomainHost || hostM.Grant != nil || hostM.Host == nil || hostM.Host.DeviceID != hostID || hostM.Host.LAN.TLSPin != "lan-pin" {
		t.Fatalf("host membership %+v", hostM)
	}

	// The early phone now gets a Grant whose audience is the host.
	gr, err = h.client.Grant(ctx, earlyKey, early.ConsoleID)
	if err != nil {
		t.Fatal(err)
	}
	if gr.Host == nil || gr.Host.DeviceID != hostID {
		t.Fatalf("grant response host %+v", gr.Host)
	}
	h.verifyGrant(gr.Grant, tr.Signer, earlyKey, hostID)

	// A desktop joining after the backend gets its Grant at once.
	deskCred := h.issue(dom, consoleapi.TypeDesktop)
	desk, deskKey := h.join(deskCred, "桌面端", nil)
	h.verifyGrant(desk.Grant, desk.Signer, deskKey, hostID)
	if desk.Host == nil || desk.Host.Identity.PublicKey.KID != hostKey.Identity.PublicKey.KID {
		t.Fatal("membership does not carry the host key")
	}
	if desk.ConsoleURL != h.url || desk.ConsoleTLSPin != h.pin || len(desk.Relays) != 1 {
		t.Fatalf("membership console fields %+v", desk)
	}

	// Refusals.
	wantCode(t, h.joinErr(h.issue(dom, consoleapi.TypeSparkClaw)), consoleapi.ErrCodeHostExists)
	wantCode(t, h.joinErr(deskCred), consoleapi.ErrCodeCredentialUsed)
	wantCode(t, h.joinErr(consoleapi.Credential{Code: "000000000000", Type: "phone"}), consoleapi.ErrCodeCredentialInvalid)
	expiring := h.issue(dom, consoleapi.TypeWatch)
	h.clock.Advance(16 * time.Minute)
	wantCode(t, h.joinErr(expiring), consoleapi.ErrCodeCredentialExpired)

	// If the Relay refuses, the credential is not consumed.
	retry := h.issue(dom, consoleapi.TypePhone)
	h.relay.setFail(true)
	if err := h.joinErr(retry); err == nil || !consoleapi.IsCode(err, "relay_unavailable") {
		t.Fatalf("join with Relay down: %v", err)
	}
	h.relay.setFail(false)
	phone, phoneKey := h.join(retry, "手机", nil)
	h.verifyGrant(phone.Grant, phone.Signer, phoneKey, hostID)

	// Directory: backend only.
	_, err = h.client.Directory(ctx, phoneKey, phone.ConsoleID, 0)
	wantCode(t, err, consoleapi.ErrCodeNotHost)
	dir, err := h.client.Directory(ctx, hostKey, hostM.ConsoleID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dir.DomainID != dom || len(dir.Devices) != 4 || dir.Revision <= 0 || dir.Signer.PublicKey.KID != tr.Signer.PublicKey.KID {
		t.Fatalf("directory %+v", dir)
	}
	rev0 := dir.Revision

	// Host information.
	newLAN := consoleapi.LAN{Endpoints: []string{"wss://192.168.1.11:18789/link/v1"}, TLSPin: "lan-pin-2"}
	host, err := h.client.PublishHost(ctx, hostKey, hostM.ConsoleID, newLAN, "新后端名")
	if err != nil || host.Name != "新后端名" || host.LAN.TLSPin != "lan-pin-2" {
		t.Fatalf("publish host %+v %v", host, err)
	}
	_, err = h.client.PublishHost(ctx, phoneKey, phone.ConsoleID, newLAN, "")
	wantCode(t, err, consoleapi.ErrCodeNotHost)

	// Relay access.
	ra, err := h.client.RelayAccess(ctx, phoneKey, phone.ConsoleID, relayID)
	if err != nil || ra.Credentials.Access.Token == "" || ra.Relay.RelayID != relayID || h.relay.bindCount(phoneKey.Identity.DeviceID) != 2 {
		t.Fatalf("relay access %+v %v", ra, err)
	}

	// The host may not hold Grants.
	_, err = h.client.Grant(ctx, hostKey, hostM.ConsoleID)
	wantCode(t, err, "not_owner_device")

	// Revoke the phone through the administrator interface.
	if st := h.adminDo("POST", "/v1/domains/"+dom+"/devices/"+phoneKey.Identity.DeviceID+"/revoke", nil, nil); st != http.StatusOK {
		t.Fatalf("revoke: HTTP %d", st)
	}
	_, err = h.client.Grant(ctx, phoneKey, phone.ConsoleID)
	wantCode(t, err, consoleapi.ErrCodeRevoked)
	if !h.relay.wasRevoked(phoneKey.Identity.DeviceID) {
		t.Fatal("phone was not revoked at the Relay")
	}

	// The desktop leaves.
	if err := h.client.Leave(ctx, deskKey, desk.ConsoleID); err != nil {
		t.Fatal(err)
	}
	_, err = h.client.Grant(ctx, deskKey, desk.ConsoleID)
	wantCode(t, err, consoleapi.ErrCodeRevoked)
	if !h.relay.wasRevoked(deskKey.Identity.DeviceID) {
		t.Fatal("desktop was not revoked at the Relay")
	}

	// The backend learns both revocations from the directory.
	dir, err = h.client.Directory(ctx, hostKey, hostM.ConsoleID, rev0)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]consoleapi.DeviceRecord{}
	for _, d := range dir.Devices {
		states[d.DeviceID] = d
	}
	for _, id := range []string{phoneKey.Identity.DeviceID, deskKey.Identity.DeviceID} {
		d := states[id]
		if d.State != consoleapi.StateRevoked || d.RevokedAt == nil || d.RevocationEpoch != 2 || d.DirectoryRevision <= rev0 {
			t.Fatalf("directory entry for %s: %+v", id, d)
		}
	}
	if states[earlyKey.Identity.DeviceID].State != consoleapi.StateTrusted || states[hostID].Name != "新后端名" {
		t.Fatal("directory lost unchanged devices")
	}
	if dir.Revision <= rev0 {
		t.Fatalf("revision did not move: %d", dir.Revision)
	}

	// Unknown devices and replayed or foreign proofs.
	stranger, _ := consoleapi.NewDeviceIdentity(relayDomain, "phone")
	_, err = h.client.Grant(ctx, stranger, tr.ConsoleID)
	wantCode(t, err, consoleapi.ErrCodeUnknownDevice)
	_, err = h.client.Grant(ctx, earlyKey, "another-console")
	wantCode(t, err, consoleapi.ErrCodeProofInvalid)

	// Revoking the backend frees the domain for a new one; the remaining
	// phone then has no Grant until it joins.
	if st := h.adminDo("POST", "/v1/domains/"+dom+"/devices/"+hostID+"/revoke", nil, nil); st != http.StatusOK {
		t.Fatalf("revoke host: HTTP %d", st)
	}
	gr, err = h.client.Grant(ctx, earlyKey, early.ConsoleID)
	if err != nil || gr.Grant != nil || gr.Host != nil {
		t.Fatalf("grant after host revoked: %+v %v", gr, err)
	}
	_, host2 := h.join(h.issue(dom, consoleapi.TypeSparkClaw), "第二个后端", nil)
	gr, err = h.client.Grant(ctx, earlyKey, early.ConsoleID)
	if err != nil {
		t.Fatal(err)
	}
	h.verifyGrant(gr.Grant, tr.Signer, earlyKey, host2.Identity.DeviceID)

	// The state survives a restart of the console.
	again, err := devconsole.New(devconsole.Options{DataDir: h.dir, PublicURL: h.url, TLSPin: h.pin, Relay: h.relay.endpoint(), RelayAdminToken: relayToken, Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	if again.ConsoleID() != h.srv.ConsoleID() || again.Signer().PublicKey.KID != h.srv.Signer().PublicKey.KID {
		t.Fatal("console identity changed across restart")
	}
}

func TestJoinRejectsReplayedProof(t *testing.T) {
	h := newHarness(t)
	dom := h.createDomain("")
	cred := h.issue(dom, consoleapi.TypePhone)
	tr, err := h.client.Trust(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := consoleapi.NewDeviceIdentity(relayDomain, "phone")
	p := crypto.NewProvider()
	proof, _ := dev.CreateProof(p, tr.ConsoleID, consoleapi.JoinChallenge(cred.Code), "fixed-nonce", time.Now())
	wrong, _ := dev.CreateProof(p, tr.ConsoleID, consoleapi.JoinChallenge("111111111111"), "other-nonce", time.Now())
	rp, _ := dev.CreateProof(p, relayID, consoleapi.OpRelayAccess, "relay-nonce", time.Now())
	post := func(req consoleapi.JoinRequest) (int, string) {
		b, _ := json.Marshal(req)
		res, err := h.admin.Post(h.url+"/v1/link/join", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(raw)
	}
	if st, body := post(consoleapi.JoinRequest{Code: cred.Code, Identity: dev.Identity, Proof: wrong, RelayProof: &rp}); st != http.StatusUnauthorized || !strings.Contains(body, consoleapi.ErrCodeProofInvalid) {
		t.Fatalf("proof for another code: %d %s", st, body)
	}
	other, _ := consoleapi.NewDeviceIdentity("some-other-domain", "phone")
	op, _ := other.CreateProof(p, tr.ConsoleID, consoleapi.JoinChallenge(cred.Code), "n1", time.Now())
	if st, body := post(consoleapi.JoinRequest{Code: cred.Code, Identity: other.Identity, Proof: op, RelayProof: &rp}); !strings.Contains(body, consoleapi.ErrCodeProofInvalid) {
		t.Fatalf("identity outside the Relay domain: %d %s", st, body)
	}
	if st, body := post(consoleapi.JoinRequest{Code: cred.Code, Identity: dev.Identity, Proof: proof, RelayProof: &rp}); st != http.StatusOK {
		t.Fatalf("join: %d %s", st, body)
	}
	if st, body := post(consoleapi.JoinRequest{Code: cred.Code, Identity: dev.Identity, Proof: proof, RelayProof: &rp}); !strings.Contains(body, consoleapi.ErrCodeCredentialUsed) {
		t.Fatalf("replayed join: %d %s", st, body)
	}
}

func TestGrantRenewalAndLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dom := h.createDomain("续期")
	_, hostKey := h.join(h.issue(dom, consoleapi.TypeSparkClaw), "后端", nil)
	hostID := hostKey.Identity.DeviceID
	m, key := h.join(h.issue(dom, consoleapi.TypeWatch), "手表", nil)
	g1 := m.Grant
	h.verifyGrant(g1, m.Signer, key, hostID)
	if got := g1.ExpiresAt.Sub(g1.NotBefore); got < consoleapi.GrantTTL || got > consoleapi.GrantTTL+2*time.Second {
		t.Fatalf("grant lifetime %v", got)
	}
	window := consoleapi.RenewBefore(*g1)

	// Outside the renewal window the same Grant comes back.
	h.clock.Advance(consoleapi.GrantTTL - window - time.Hour)
	gr, err := h.client.Grant(ctx, key, m.ConsoleID)
	if err != nil {
		t.Fatal(err)
	}
	if gr.Grant.GrantID != g1.GrantID {
		t.Fatalf("grant renewed too early: %s", gr.Grant.GrantID)
	}

	// Inside it a new one is issued.
	h.clock.Advance(2 * time.Hour)
	gr, err = h.client.Grant(ctx, key, m.ConsoleID)
	if err != nil {
		t.Fatal(err)
	}
	if gr.Grant.GrantID == g1.GrantID || !gr.Grant.ExpiresAt.After(g1.ExpiresAt) {
		t.Fatalf("grant was not renewed: %+v", gr.Grant)
	}
	h.verifyGrant(gr.Grant, m.Signer, key, hostID)
	if gr.Device.GrantID != gr.Grant.GrantID {
		t.Fatal("device record does not name the current grant")
	}
	g2 := gr.Grant
	gr, err = h.client.Grant(ctx, key, m.ConsoleID)
	if err != nil || gr.Grant.GrantID != g2.GrantID {
		t.Fatalf("renewed grant not kept: %v", err)
	}

	// After the renewal limit the device must be authorized again.
	h.clock.Advance(consoleapi.RenewalLimit)
	_, err = h.client.Grant(ctx, key, m.ConsoleID)
	wantCode(t, err, consoleapi.ErrCodeRenewalExpired)

	var out struct {
		Data struct {
			RenewalExpiresAt *time.Time `json:"renewal_expires_at"`
			Renewal          struct {
				Status string `json:"status"`
			} `json:"renewal_authorization"`
		} `json:"data"`
	}
	if st := h.adminDo("POST", "/v1/domains/"+dom+"/devices/"+key.Identity.DeviceID+"/renewal-authorizations", nil, &out); st != http.StatusOK {
		t.Fatalf("renewal authorization: HTTP %d", st)
	}
	if out.Data.Renewal.Status != "active" || out.Data.RenewalExpiresAt == nil || out.Data.RenewalExpiresAt.Sub(h.clock.Now()) < consoleapi.RenewalLimit-time.Minute {
		t.Fatalf("renewal authorization response %+v", out.Data)
	}
	gr, err = h.client.Grant(ctx, key, m.ConsoleID)
	if err != nil {
		t.Fatal(err)
	}
	if gr.Grant.GrantID == g2.GrantID {
		t.Fatal("an expired grant was handed out")
	}
	h.verifyGrant(gr.Grant, m.Signer, key, hostID)
}

func TestAdminAPIAuth(t *testing.T) {
	h := newHarness(t)
	res, err := h.admin.Get(h.url + "/v1/domains")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: %d", res.StatusCode)
	}
	jar := newJar()
	c := &http.Client{Transport: h.admin.Transport, Jar: jar}
	res, err = c.Post(h.url+"/v1/accounts/login", "application/json", strings.NewReader(`{"password":"wrong password"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	res, err = c.Post(h.url+"/v1/accounts/login", "application/json", strings.NewReader(`{"password":"`+adminPass+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&login)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || login.Data.CSRF == "" {
		t.Fatalf("login: %d", res.StatusCode)
	}
	for _, ck := range res.Cookies() {
		if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode {
			t.Fatalf("session cookie flags %+v", ck)
		}
	}
	// A cookie change without the CSRF header is refused.
	res, _ = c.Post(h.url+"/v1/domains", "application/json", strings.NewReader(`{"name":"x"}`))
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("change without CSRF: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("POST", h.url+"/v1/domains", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("X-CSRF-Token", login.Data.CSRF)
	res, _ = c.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("change with CSRF: %d", res.StatusCode)
	}
	var me struct {
		Data map[string]any `json:"data"`
	}
	if st := h.adminDo("GET", "/v1/me", nil, &me); st != http.StatusOK || me.Data["user_id"] != "admin" {
		t.Fatalf("me: %d %+v", st, me)
	}
	// A rotated token replaces the old one.
	old := h.token
	if h.token, err = devconsole.RotateAPIToken(h.dir); err != nil {
		t.Fatal(err)
	}
	h.token, old = old, h.token
	if st := h.adminDo("GET", "/v1/me", nil, nil); st != http.StatusUnauthorized {
		t.Fatalf("old token still works: %d", st)
	}
	h.token = old
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if st := h.adminDo("GET", "/v1/domains", nil, &list); st != http.StatusOK || len(list.Items) != 1 {
		t.Fatalf("list domains: %d %d", st, len(list.Items))
	}
	res, _ = h.admin.Get(h.url + "/healthz")
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("healthz %d", res.StatusCode)
	}
}
