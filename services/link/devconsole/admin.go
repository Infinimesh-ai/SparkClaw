package devconsole

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
)

const (
	sessionCookie = "sparkclaw_console_session"
	sessionTTL    = 12 * time.Hour
)

type session struct {
	token   string
	csrf    string
	userID  string
	expires time.Time
}

// adminCtx is the authenticated administrator of one request.
type adminCtx struct {
	UserID      string
	DisplayName string
	CSRF        string
	Bearer      bool
}

// --- sessions --------------------------------------------------------------

func (s *Server) newSession(userID string) *session {
	sess := &session{token: crypto.Base64URL(crypto.RandomBytes(32)), csrf: crypto.Base64URL(crypto.RandomBytes(24)), userID: userID, expires: time.Now().Add(sessionTTL)}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	now := time.Now()
	for k, v := range s.sessions {
		if now.After(v.expires) {
			delete(s.sessions, k)
		}
	}
	s.sessions[sess.token] = sess
	return sess
}

func (s *Server) sessionFor(r *http.Request) *session {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	sess := s.sessions[c.Value]
	if sess == nil || time.Now().After(sess.expires) {
		delete(s.sessions, c.Value)
		return nil
	}
	return sess
}

func (s *Server) dropSession(r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessMu.Lock()
		delete(s.sessions, c.Value)
		s.sessMu.Unlock()
	}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sess *session) {
	c := &http.Cookie{Name: sessionCookie, Path: "/", HttpOnly: true, Secure: s.opts.SecureCookies, SameSite: http.SameSiteStrictMode}
	if sess == nil {
		c.MaxAge = -1
	} else {
		c.Value = sess.token
		c.MaxAge = int(sessionTTL / time.Second)
	}
	http.SetCookie(w, c)
}

// loginAllowed limits password guessing: at most five failures in five
// minutes.
func (s *Server) loginAllowed() bool {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	cut := time.Now().Add(-5 * time.Minute)
	kept := s.loginFails[:0]
	for _, t := range s.loginFails {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.loginFails = kept
	return len(kept) < 5
}

func (s *Server) loginFailed() {
	s.sessMu.Lock()
	s.loginFails = append(s.loginFails, time.Now())
	s.sessMu.Unlock()
}

// checkLogin verifies the password and opens a session.
func (s *Server) checkLogin(password string) (*session, *account, *apiError) {
	if !s.loginAllowed() {
		return nil, nil, errf(http.StatusTooManyRequests, codeTooManyRequests, "too many failed sign-ins; try again in a few minutes")
	}
	acct, err := loadAccount(s.opts.DataDir)
	if err != nil || acct == nil || !acct.checkPassword(password) {
		s.loginFailed()
		return nil, nil, errf(http.StatusUnauthorized, codeUnauthorized, "password is not correct")
	}
	return s.newSession(acct.UserID), acct, nil
}

// authenticate resolves the administrator from the session cookie or an
// API bearer token.
func (s *Server) authenticate(r *http.Request) (*adminCtx, bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		acct, err := loadAccount(s.opts.DataDir)
		if err != nil || !acct.checkAPIToken(strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))) {
			return nil, false
		}
		return &adminCtx{UserID: acct.UserID, DisplayName: acct.DisplayName, Bearer: true}, true
	}
	sess := s.sessionFor(r)
	if sess == nil {
		return nil, false
	}
	name := "管理员"
	if acct, err := loadAccount(s.opts.DataDir); err == nil && acct != nil && acct.DisplayName != "" {
		name = acct.DisplayName
	}
	return &adminCtx{UserID: sess.userID, DisplayName: name, CSRF: sess.csrf}, true
}

func csrfOK(want, got string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// api wraps an administrator JSON handler. Cookie-authenticated changes
// need the X-CSRF-Token header (GET /v1/me returns it).
func (s *Server) api(h func(http.ResponseWriter, *http.Request, *adminCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.authenticate(r)
		if !ok {
			writeError(w, errf(http.StatusUnauthorized, codeUnauthorized, "sign in first"))
			return
		}
		if !a.Bearer && r.Method != http.MethodGet && !csrfOK(a.CSRF, r.Header.Get("X-CSRF-Token")) {
			writeError(w, errf(http.StatusForbidden, codeCSRF, "missing or wrong X-CSRF-Token"))
			return
		}
		h(w, r, a)
	}
}

// --- JSON shapes (mirroring the official console) ----------------------------

type domainJSON struct {
	DomainID     string            `json:"domain_id"`
	DomainType   string            `json:"domain_type"`
	Status       string            `json:"status"`
	Metadata     map[string]string `json:"metadata"`
	Name         string            `json:"name"`
	HostDeviceID string            `json:"host_device_id,omitempty"`
	Revision     int64             `json:"directory_revision"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type authorizationJSON struct {
	Status      string    `json:"status"`
	GrantID     string    `json:"grant_id"`
	Audience    string    `json:"audience"`
	Permissions []string  `json:"permissions"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type renewalJSON struct {
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type deviceJSON struct {
	consoleapi.DeviceRecord
	DisplayName   string             `json:"display_name"`
	DeviceType    string             `json:"device_type"`
	DeviceRole    string             `json:"device_role"`
	TrustState    string             `json:"trust_state"`
	RelayState    string             `json:"relay_state"`
	Authorization *authorizationJSON `json:"authorization,omitempty"`
	Renewal       *renewalJSON       `json:"renewal_authorization,omitempty"`
}

type credentialJSON struct {
	CredentialID     string     `json:"credential_id"`
	DomainID         string     `json:"domain_id"`
	TargetDeviceType string     `json:"target_device_type"`
	TargetDeviceRole string     `json:"target_device_role"`
	Status           string     `json:"status"`
	UsedCount        int        `json:"used_count"`
	MaxUses          int        `json:"max_uses"`
	UsedAt           *time.Time `json:"used_at,omitempty"`
	UsedByDevice     string     `json:"used_by_device,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at"`
	CreatedAt        time.Time  `json:"created_at"`
	// Shown once, in the issue response only.
	Code        string `json:"code,omitempty"`
	CodeDisplay string `json:"code_display,omitempty"`
	Credential  string `json:"credential,omitempty"`
}

func (s *Server) domainJSONLocked(d *domainRec) domainJSON {
	out := domainJSON{DomainID: d.DomainID, DomainType: "personal", Status: d.State, Metadata: map[string]string{}, Name: d.Name, Revision: s.st.Revision, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
	if d.Name != "" {
		out.Metadata["name"] = d.Name
	}
	if h := s.hostLocked(d); h != nil {
		out.HostDeviceID = h.DeviceID
	}
	return out
}

func (s *Server) deviceJSONLocked(dev *deviceRec, now time.Time) deviceJSON {
	out := deviceJSON{DeviceRecord: dev.record(), DisplayName: dev.Name, DeviceType: dev.Type, DeviceRole: dev.Role, TrustState: dev.State, RelayState: "active"}
	if dev.State == consoleapi.StateRevoked {
		out.RelayState = "revoked"
	}
	if g := dev.Grant; g != nil {
		st := "active"
		if !now.Before(g.ExpiresAt) {
			st = "expired"
		}
		out.Authorization = &authorizationJSON{Status: st, GrantID: g.GrantID, Audience: g.Audience, Permissions: g.Permissions, ExpiresAt: g.ExpiresAt}
	}
	if dev.Role == consoleapi.RoleOwnerDevice && dev.RenewalExpiresAt != nil {
		st := "active"
		if now.After(*dev.RenewalExpiresAt) {
			st = "expired"
		}
		if dev.State == consoleapi.StateRevoked {
			st = "revoked"
		}
		out.Renewal = &renewalJSON{Status: st, ExpiresAt: dev.RenewalExpiresAt}
	}
	return out
}

func credentialJSONOf(c *credentialRec, now time.Time) credentialJSON {
	out := credentialJSON{CredentialID: c.CredentialID, DomainID: c.DomainID, TargetDeviceType: c.Type, TargetDeviceRole: c.Role, Status: c.status(now), MaxUses: 1, UsedAt: c.UsedAt, UsedByDevice: c.UsedByDevice, ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt}
	if c.UsedAt != nil {
		out.UsedCount = 1
	}
	return out
}

// --- operations shared by the JSON interface and the pages -------------------

func (s *Server) createDomain(name, actor string) (*domainRec, *apiError) {
	name = truncate(strings.TrimSpace(name), 80)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	d := &domainRec{DomainID: "dom_" + randomID(10), Name: name, State: DomainActive, OwnerUserID: actor, CreatedAt: now, UpdatedAt: now}
	s.st.Domains = append(s.st.Domains, d)
	s.auditLocked(d.DomainID, "domain.create", resultAllow, actorAccount, actor, "domain", d.DomainID, "")
	if err := s.saveLocked(); err != nil {
		return nil, errf(http.StatusInternalServerError, codeInternal, "state could not be saved")
	}
	return d, nil
}

func (s *Server) updateDomain(domainID string, name *string, status *string, actor string) *apiError {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.st.domain(domainID)
	if d == nil {
		return errf(http.StatusNotFound, codeNotFound, "domain not found")
	}
	if status != nil && *status != DomainActive && *status != DomainSuspended {
		return errf(http.StatusBadRequest, codeInvalidRequest, "status must be active or suspended")
	}
	if name != nil {
		d.Name = truncate(strings.TrimSpace(*name), 80)
	}
	reason := ""
	if status != nil && *status != d.State {
		d.State = *status
		reason = "status " + *status
	}
	d.UpdatedAt = s.now()
	s.auditLocked(d.DomainID, "domain.update", resultAllow, actorAccount, actor, "domain", d.DomainID, reason)
	if err := s.saveLocked(); err != nil {
		return errf(http.StatusInternalServerError, codeInternal, "state could not be saved")
	}
	return nil
}

func (s *Server) renameDevice(domainID, deviceID, name, actor string) *apiError {
	name = truncate(strings.TrimSpace(name), 80)
	if name == "" {
		return errf(http.StatusBadRequest, codeInvalidRequest, "name is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := s.st.device(deviceID)
	if dev == nil || dev.DomainID != domainID {
		return errf(http.StatusNotFound, codeNotFound, "device not found")
	}
	dev.Name = name
	s.touchLocked(dev, s.now())
	s.auditLocked(domainID, "device.rename", resultAllow, actorAccount, actor, "device", deviceID, "")
	if err := s.saveLocked(); err != nil {
		return errf(http.StatusInternalServerError, codeInternal, "state could not be saved")
	}
	return nil
}

// issuedCredential is a credential as shown once after issuing.
type issuedCredential struct {
	rec  credentialRec
	code string
	text string
}

// Credential lifetime bounds.
const (
	defaultCredentialTTL = 15 * time.Minute
	minCredentialTTL     = time.Minute
	maxCredentialTTL     = 24 * time.Hour
)

func (s *Server) issueCredential(domainID, deviceType string, ttl time.Duration, actor string) (*issuedCredential, *apiError) {
	if !consoleapi.ValidDeviceType(deviceType) {
		return nil, errf(http.StatusBadRequest, codeInvalidRequest, "target_device_type must be sparkclaw, desktop, phone or watch")
	}
	if ttl == 0 {
		ttl = defaultCredentialTTL
	}
	if ttl < minCredentialTTL || ttl > maxCredentialTTL {
		return nil, errf(http.StatusBadRequest, codeInvalidRequest, "expiry must be between 1 minute and 24 hours")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.st.domain(domainID)
	if d == nil {
		return nil, errf(http.StatusNotFound, codeNotFound, "domain not found")
	}
	if d.State != DomainActive {
		return nil, errf(http.StatusForbidden, codeDomainSuspended, "domain is suspended")
	}
	now := s.now()
	code := consoleapi.NewCode()
	for s.st.credentialByDigest(consoleapi.CodeDigest(code)) != nil {
		code = consoleapi.NewCode()
	}
	rec := &credentialRec{CredentialID: "cred_" + randomID(10), DomainID: domainID, CodeDigest: consoleapi.CodeDigest(code), Type: deviceType, Role: consoleapi.RoleFor(deviceType), CreatedAt: now, ExpiresAt: now.Add(ttl).Truncate(time.Second), CreatedBy: actor}
	s.st.Credentials = append(s.st.Credentials, rec)
	s.auditLocked(domainID, "access_credential.issue", resultAllow, actorAccount, actor, "access_credential", rec.CredentialID, deviceType)
	if err := s.saveLocked(); err != nil {
		return nil, errf(http.StatusInternalServerError, codeInternal, "state could not be saved")
	}
	text := consoleapi.Credential{Console: s.opts.PublicURL, TLSPin: s.opts.TLSPin, Code: code, Type: deviceType, ExpiresAt: rec.ExpiresAt.UTC().Format(time.RFC3339)}.Encode()
	return &issuedCredential{rec: *rec, code: code, text: text}, nil
}

// --- JSON handlers -------------------------------------------------------------

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if e := decodeJSON(r, &req); e != nil {
		writeError(w, e)
		return
	}
	sess, acct, e := s.checkLogin(req.Password)
	if e != nil {
		writeError(w, e)
		return
	}
	s.setSessionCookie(w, sess)
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"user_id": acct.UserID, "display_name": acct.DisplayName, "csrf_token": sess.csrf, "expires_at": sess.expires.UTC()}})
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessionFor(r); sess != nil && !csrfOK(sess.csrf, r.Header.Get("X-CSRF-Token")) {
		writeError(w, errf(http.StatusForbidden, codeCSRF, "missing or wrong X-CSRF-Token"))
		return
	}
	s.dropSession(r)
	s.setSessionCookie(w, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	data := map[string]any{"user_id": a.UserID, "display_name": a.DisplayName, "console_id": s.consoleID}
	if a.CSRF != "" {
		data["csrf_token"] = a.CSRF
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *Server) apiListDomains(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]domainJSON, 0, len(s.st.Domains))
	for _, d := range s.st.Domains {
		items = append(items, s.domainJSONLocked(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type domainPatch struct {
	Name     *string           `json:"name"`
	Metadata map[string]string `json:"metadata"`
	Status   *string           `json:"status"`
}

func (p domainPatch) name() *string {
	if p.Name != nil {
		return p.Name
	}
	if n, ok := p.Metadata["name"]; ok {
		return &n
	}
	return nil
}

func (s *Server) apiCreateDomain(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	var req domainPatch
	if r.ContentLength != 0 {
		if e := decodeJSON(r, &req); e != nil {
			writeError(w, e)
			return
		}
	}
	name := ""
	if n := req.name(); n != nil {
		name = *n
	}
	d, e := s.createDomain(name, a.UserID)
	if e != nil {
		writeError(w, e)
		return
	}
	s.mu.Lock()
	out := s.domainJSONLocked(d)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": out})
}

func (s *Server) apiGetDomain(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.st.domain(r.PathValue("d"))
	if d == nil {
		writeError(w, errf(http.StatusNotFound, codeNotFound, "domain not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.domainJSONLocked(d)})
}

func (s *Server) apiPatchDomain(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	var req domainPatch
	if e := decodeJSON(r, &req); e != nil {
		writeError(w, e)
		return
	}
	if e := s.updateDomain(r.PathValue("d"), req.name(), req.Status, a.UserID); e != nil {
		writeError(w, e)
		return
	}
	s.apiGetDomain(w, r, a)
}

func (s *Server) apiListDevices(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.st.domain(r.PathValue("d"))
	if d == nil {
		writeError(w, errf(http.StatusNotFound, codeNotFound, "domain not found"))
		return
	}
	list := s.st.domainDevices(d.DomainID)
	sortDevices(list)
	now := s.now()
	items := make([]deviceJSON, 0, len(list))
	for _, x := range list {
		items = append(items, s.deviceJSONLocked(x, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "directory_revision": s.st.Revision})
}

func (s *Server) writeDevice(w http.ResponseWriter, domainID, deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := s.st.device(deviceID)
	if dev == nil || dev.DomainID != domainID {
		writeError(w, errf(http.StatusNotFound, codeNotFound, "device not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.deviceJSONLocked(dev, s.now())})
}

func (s *Server) apiGetDevice(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	s.writeDevice(w, r.PathValue("d"), r.PathValue("dev"))
}

type devicePatch struct {
	DisplayName *string `json:"display_name"`
	Name        *string `json:"name"`
}

func (s *Server) apiPatchDevice(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	var req devicePatch
	if e := decodeJSON(r, &req); e != nil {
		writeError(w, e)
		return
	}
	name := req.DisplayName
	if name == nil {
		name = req.Name
	}
	if name == nil {
		writeError(w, errf(http.StatusBadRequest, codeInvalidRequest, "display_name is required"))
		return
	}
	if e := s.renameDevice(r.PathValue("d"), r.PathValue("dev"), *name, a.UserID); e != nil {
		writeError(w, e)
		return
	}
	s.writeDevice(w, r.PathValue("d"), r.PathValue("dev"))
}

func (s *Server) apiRevokeDevice(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	if _, e := s.revoke(r.PathValue("d"), r.PathValue("dev"), "device.revoke", actorAccount, a.UserID); e != nil {
		writeError(w, e)
		return
	}
	s.writeDevice(w, r.PathValue("d"), r.PathValue("dev"))
}

func (s *Server) apiRenewalAuthorization(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	if _, e := s.reauthorizeRenewal(r.PathValue("d"), r.PathValue("dev"), a.UserID); e != nil {
		writeError(w, e)
		return
	}
	s.writeDevice(w, r.PathValue("d"), r.PathValue("dev"))
}

type issueRequest struct {
	TargetDeviceType string `json:"target_device_type"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

func (s *Server) apiIssueCredential(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	var req issueRequest
	if e := decodeJSON(r, &req); e != nil {
		writeError(w, e)
		return
	}
	issued, e := s.issueCredential(r.PathValue("d"), req.TargetDeviceType, time.Duration(req.ExpiresInSeconds)*time.Second, a.UserID)
	if e != nil {
		writeError(w, e)
		return
	}
	out := credentialJSONOf(&issued.rec, s.now())
	out.Code, out.CodeDisplay, out.Credential = issued.code, consoleapi.FormatCode(issued.code), issued.text
	writeJSON(w, http.StatusCreated, map[string]any{"data": out})
}

func (s *Server) apiListCredentials(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.st.domain(r.PathValue("d"))
	if d == nil {
		writeError(w, errf(http.StatusNotFound, codeNotFound, "domain not found"))
		return
	}
	now := s.now()
	items := []credentialJSON{}
	for i := len(s.st.Credentials) - 1; i >= 0; i-- {
		if c := s.st.Credentials[i]; c.DomainID == d.DomainID {
			items = append(items, credentialJSONOf(c, now))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// auditLimit reads ?limit= (default 100, at most 1000).
func auditLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return 100
	}
	if n > 1000 {
		n = 1000
	}
	return n
}

// domainAuditLocked returns the newest entries of a domain, newest first.
func (s *Server) domainAuditLocked(domainID string, limit int) ([]auditRec, bool) {
	var out []auditRec
	for i := len(s.st.Audit) - 1; i >= 0; i-- {
		if a := s.st.Audit[i]; a.DomainID == domainID {
			if len(out) == limit {
				return out, true
			}
			out = append(out, *a)
		}
	}
	return out, false
}

func (s *Server) apiAudit(w http.ResponseWriter, r *http.Request, a *adminCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.st.domain(r.PathValue("d"))
	if d == nil {
		writeError(w, errf(http.StatusNotFound, codeNotFound, "domain not found"))
		return
	}
	items, more := s.domainAuditLocked(d.DomainID, auditLimit(r))
	if items == nil {
		items = []auditRec{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "has_more": more})
}
