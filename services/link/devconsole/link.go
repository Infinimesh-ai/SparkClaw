package devconsole

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func (s *Server) trust() consoleapi.Trust {
	return consoleapi.Trust{ConsoleID: s.consoleID, Name: s.opts.Name, Signer: s.signer.Identity, Relays: []relay.Endpoint{s.endpoint}}
}

func (s *Server) handleTrust(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.trust())
}

// joinCheckLocked validates a join against the current state. It returns
// the credential and domain when they are known (for auditing).
func (s *Server) joinCheckLocked(digest string, id identity.DeviceIdentity, now time.Time) (*credentialRec, *domainRec, *apiError) {
	cred := s.st.credentialByDigest(digest)
	if cred == nil {
		return nil, nil, errf(http.StatusNotFound, consoleapi.ErrCodeCredentialInvalid, "access code is not known")
	}
	d := s.st.domain(cred.DomainID)
	if d == nil {
		return cred, nil, errf(http.StatusNotFound, consoleapi.ErrCodeCredentialInvalid, "access code names no domain")
	}
	switch cred.status(now) {
	case "consumed":
		return cred, d, errf(http.StatusConflict, consoleapi.ErrCodeCredentialUsed, "access credential was already used")
	case "expired":
		return cred, d, errf(http.StatusGone, consoleapi.ErrCodeCredentialExpired, "access credential has expired")
	}
	if d.State != DomainActive {
		return cred, d, errf(http.StatusForbidden, codeDomainSuspended, "domain is suspended")
	}
	if s.st.device(id.DeviceID) != nil {
		return cred, d, errf(http.StatusConflict, codeDeviceExists, "device is already registered; a device joins with a new key")
	}
	if cred.Type == consoleapi.TypeSparkClaw && s.hostLocked(d) != nil {
		return cred, d, errf(http.StatusConflict, consoleapi.ErrCodeHostExists, "the domain already has a backend")
	}
	return cred, d, nil
}

func (s *Server) checkIdentity(id identity.DeviceIdentity) *apiError {
	if id.Type != identity.TypeDeviceIdentity || id.PublicKey.KTY != "Ed25519" {
		return errf(http.StatusBadRequest, consoleapi.ErrCodeProofInvalid, "unsupported device identity")
	}
	if id.DomainID != s.endpoint.DomainID {
		return errf(http.StatusBadRequest, consoleapi.ErrCodeProofInvalid, "device identity is not in the Relay domain %s", s.endpoint.DomainID)
	}
	if !deviceIDPattern.MatchString(id.DeviceID) {
		return errf(http.StatusBadRequest, consoleapi.ErrCodeProofInvalid, "device id is malformed")
	}
	kid, err := identity.Thumbprint(id)
	if err != nil || kid != id.PublicKey.KID {
		return errf(http.StatusBadRequest, consoleapi.ErrCodeProofInvalid, "device key id does not match its public key")
	}
	return nil
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req consoleapi.JoinRequest
	if e := decodeJSON(r, &req); e != nil {
		writeError(w, e)
		return
	}
	code, err := consoleapi.NormalizeCode(req.Code)
	if err != nil {
		writeError(w, errf(http.StatusBadRequest, consoleapi.ErrCodeCredentialInvalid, "%v", err))
		return
	}
	digest := consoleapi.CodeDigest(code)

	s.joinMu.Lock()
	defer s.joinMu.Unlock()

	deny := func(domainID string, e *apiError) {
		if domainID != "" {
			s.mu.Lock()
			s.auditLocked(domainID, "device.join", resultDeny, actorDevice, req.Identity.DeviceID, "device", req.Identity.DeviceID, e.Code+": "+e.Message)
			_ = s.saveLocked()
			s.mu.Unlock()
		}
		writeError(w, e)
	}

	s.mu.Lock()
	_, d, e := s.joinCheckLocked(digest, req.Identity, s.now())
	s.mu.Unlock()
	domainID := ""
	if d != nil {
		domainID = d.DomainID
	}
	if e != nil {
		deny(domainID, e)
		return
	}
	if e := s.checkIdentity(req.Identity); e != nil {
		deny(domainID, e)
		return
	}
	if e := s.verifyProof(req.Identity, req.Proof, consoleapi.JoinChallenge(code), joinProofTTL); e != nil {
		deny(domainID, e)
		return
	}
	if req.RelayProof == nil {
		deny(domainID, errf(http.StatusBadRequest, codeInvalidRequest, "relay_proof is required"))
		return
	}

	// Register at the Relay before consuming the credential: if the Relay
	// refuses, the device can try again with the same code.
	creds, err := s.relayBind(r.Context(), req.Identity, *req.RelayProof)
	if err != nil {
		s.logger.Printf("dev-console: relay bind %s: %v", req.Identity.DeviceID, err)
		deny(domainID, errf(http.StatusBadGateway, codeRelayUnavailable, "the Relay did not register the device: %v", err))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	cred, d, e := s.joinCheckLocked(digest, req.Identity, now)
	if e != nil {
		writeError(w, e)
		return
	}
	usedAt := now
	cred.UsedAt = &usedAt
	cred.UsedByDevice = req.Identity.DeviceID

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = cred.Type + " " + shortID(req.Identity.DeviceID)
	}
	kid, _ := identity.Thumbprint(req.Identity)
	dev := &deviceRec{DeviceRecord: consoleapi.DeviceRecord{
		DeviceID:        req.Identity.DeviceID,
		DomainID:        d.DomainID,
		Name:            truncate(name, 80),
		Type:            cred.Type,
		Role:            consoleapi.RoleFor(cred.Type),
		Identity:        req.Identity,
		Fingerprint:     kid,
		State:           consoleapi.StateTrusted,
		Platform:        truncate(strings.TrimSpace(req.Platform), 80),
		CreatedAt:       now,
		UpdatedAt:       now,
		LastSeenAt:      &usedAt,
		RevocationEpoch: 1,
	}}
	if dev.Role == consoleapi.RoleOwnerDevice {
		exp := now.Add(consoleapi.RenewalLimit)
		dev.RenewalExpiresAt = &exp
	}
	s.st.Devices = append(s.st.Devices, dev)
	s.touchLocked(dev, now)
	if dev.Role == consoleapi.RoleDomainHost {
		d.HostDeviceID = dev.DeviceID
		if req.LAN != nil {
			d.HostLAN = *req.LAN
		}
		d.UpdatedAt = now
	}
	s.auditLocked(d.DomainID, "device.join", resultAllow, actorDevice, dev.DeviceID, "device", dev.DeviceID, "access_credential "+cred.CredentialID)
	if host := s.hostLocked(d); host != nil && dev.Role == consoleapi.RoleOwnerDevice {
		if g, err := s.signGrantLocked(dev, host, now); err != nil {
			s.logger.Printf("dev-console: sign grant: %v", err)
		} else {
			s.auditLocked(d.DomainID, "grant.issue", resultAllow, actorDevice, dev.DeviceID, "grant", g.GrantID, "")
		}
	}
	if err := s.saveLocked(); err != nil {
		writeError(w, errf(http.StatusInternalServerError, codeInternal, "state could not be saved"))
		return
	}
	writeJSON(w, http.StatusOK, consoleapi.Membership{
		ConsoleURL:       s.opts.PublicURL,
		ConsoleTLSPin:    s.opts.TLSPin,
		ConsoleID:        s.consoleID,
		Signer:           s.signer.Identity,
		DomainID:         d.DomainID,
		DomainName:       d.Name,
		Device:           dev.record(),
		Host:             s.hostViewLocked(d),
		Grant:            dev.Grant,
		RenewalExpiresAt: dev.RenewalExpiresAt,
		Relays:           []relay.Endpoint{s.endpoint},
		RelayCredentials: &creds,
	})
}

// signedDevice authenticates a signed request for op. On success it holds
// s.mu; the caller must unlock it.
func (s *Server) signedDevice(w http.ResponseWriter, r *http.Request, op string) (consoleapi.SignedRequest, *deviceRec, *domainRec, bool) {
	var req consoleapi.SignedRequest
	if e := decodeJSON(r, &req); e != nil {
		writeError(w, e)
		return req, nil, nil, false
	}
	s.mu.Lock()
	dev := s.st.device(req.DeviceID)
	if dev == nil {
		s.mu.Unlock()
		writeError(w, errf(http.StatusNotFound, consoleapi.ErrCodeUnknownDevice, "device is not registered"))
		return req, nil, nil, false
	}
	id := dev.Identity
	s.mu.Unlock()
	if e := s.verifyProof(id, req.Proof, op, signedProofTTL); e != nil {
		writeError(w, e)
		return req, nil, nil, false
	}
	s.mu.Lock()
	dev = s.st.device(req.DeviceID)
	if dev == nil {
		s.mu.Unlock()
		writeError(w, errf(http.StatusNotFound, consoleapi.ErrCodeUnknownDevice, "device is not registered"))
		return req, nil, nil, false
	}
	if dev.State != consoleapi.StateTrusted {
		s.mu.Unlock()
		writeError(w, errf(http.StatusForbidden, consoleapi.ErrCodeRevoked, "device was revoked; it must join the domain again"))
		return req, nil, nil, false
	}
	d := s.st.domain(dev.DomainID)
	if d == nil || d.State != DomainActive {
		s.mu.Unlock()
		writeError(w, errf(http.StatusForbidden, codeDomainSuspended, "domain is suspended"))
		return req, nil, nil, false
	}
	seen := s.now()
	dev.LastSeenAt = &seen
	return req, dev, d, true
}

func (s *Server) handleGrant(w http.ResponseWriter, r *http.Request) {
	_, dev, d, ok := s.signedDevice(w, r, consoleapi.OpGrant)
	if !ok {
		return
	}
	defer s.mu.Unlock()
	if dev.Role != consoleapi.RoleOwnerDevice {
		_ = s.saveLocked()
		writeError(w, errf(http.StatusForbidden, codeNotOwnerDevice, "only owner devices hold Grants; the backend is their audience"))
		return
	}
	now := s.now()
	if dev.RenewalExpiresAt == nil || now.After(*dev.RenewalExpiresAt) {
		_ = s.saveLocked()
		writeError(w, errf(http.StatusForbidden, consoleapi.ErrCodeRenewalExpired, "automatic renewal has reached its limit; authorize it again on the console"))
		return
	}
	host := s.hostLocked(d)
	var grant = dev.Grant
	switch {
	case host == nil:
		grant = nil
	case grantUsable(dev, host, now):
	default:
		g, err := s.signGrantLocked(dev, host, now)
		if err != nil {
			writeError(w, errf(http.StatusInternalServerError, codeInternal, "grant could not be signed"))
			return
		}
		grant = g
		s.auditLocked(d.DomainID, "grant.renew", resultAllow, actorDevice, dev.DeviceID, "grant", g.GrantID, "")
	}
	if err := s.saveLocked(); err != nil {
		writeError(w, errf(http.StatusInternalServerError, codeInternal, "state could not be saved"))
		return
	}
	writeJSON(w, http.StatusOK, consoleapi.GrantResponse{Device: dev.record(), Host: s.hostViewLocked(d), Grant: grant, RenewalExpiresAt: dev.RenewalExpiresAt})
}

func (s *Server) handleRelayAccess(w http.ResponseWriter, r *http.Request) {
	req, dev, _, ok := s.signedDevice(w, r, consoleapi.OpRelayAccess)
	if !ok {
		return
	}
	id := dev.Identity
	_ = s.saveLocked()
	s.mu.Unlock()
	if req.RelayProof == nil {
		writeError(w, errf(http.StatusBadRequest, codeInvalidRequest, "relay_proof is required"))
		return
	}
	creds, err := s.relayBind(r.Context(), id, *req.RelayProof)
	if err != nil {
		s.logger.Printf("dev-console: relay bind %s: %v", id.DeviceID, err)
		writeError(w, errf(http.StatusBadGateway, codeRelayUnavailable, "the Relay did not register the device: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, consoleapi.RelayAccessResponse{Relay: s.endpoint, Credentials: creds})
}

func (s *Server) handleDirectory(w http.ResponseWriter, r *http.Request) {
	_, dev, d, ok := s.signedDevice(w, r, consoleapi.OpDirectory)
	if !ok {
		return
	}
	defer s.mu.Unlock()
	_ = s.saveLocked()
	if dev.Role != consoleapi.RoleDomainHost {
		writeError(w, errf(http.StatusForbidden, consoleapi.ErrCodeNotHost, "only the domain's backend reads the directory"))
		return
	}
	list := s.st.domainDevices(d.DomainID)
	sortDevices(list)
	out := consoleapi.Directory{DomainID: d.DomainID, Revision: s.st.Revision, Devices: make([]consoleapi.DeviceRecord, 0, len(list)), Signer: s.signer.Identity, SyncedAt: s.now()}
	for _, x := range list {
		out.Devices = append(out.Devices, x.record())
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	req, dev, d, ok := s.signedDevice(w, r, consoleapi.OpHost)
	if !ok {
		return
	}
	defer s.mu.Unlock()
	if dev.Role != consoleapi.RoleDomainHost || d.HostDeviceID != dev.DeviceID {
		_ = s.saveLocked()
		writeError(w, errf(http.StatusForbidden, consoleapi.ErrCodeNotHost, "only the domain's backend publishes host information"))
		return
	}
	now := s.now()
	if req.LAN != nil {
		d.HostLAN = *req.LAN
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		dev.Name = truncate(name, 80)
	}
	d.UpdatedAt = now
	s.touchLocked(dev, now)
	s.auditLocked(d.DomainID, "host.update", resultAllow, actorDevice, dev.DeviceID, "device", dev.DeviceID, "")
	if err := s.saveLocked(); err != nil {
		writeError(w, errf(http.StatusInternalServerError, codeInternal, "state could not be saved"))
		return
	}
	writeJSON(w, http.StatusOK, s.hostViewLocked(d))
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	_, dev, d, ok := s.signedDevice(w, r, consoleapi.OpLeave)
	if !ok {
		return
	}
	domainID, deviceID := d.DomainID, dev.DeviceID
	s.mu.Unlock()
	if _, e := s.revoke(domainID, deviceID, "device.leave", actorDevice, deviceID); e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func shortID(id string) string {
	if i := strings.LastIndexAny(id, "-_"); i >= 0 && i+1 < len(id) {
		id = id[i+1:]
	}
	if len(id) > 6 {
		id = id[:6]
	}
	return id
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
