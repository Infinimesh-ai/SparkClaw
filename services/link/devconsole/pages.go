package devconsole

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

//go:embed templates/*.html assets/console.css
var assets embed.FS

func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(assets, "assets/console.css")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b)
}

// --- view models ---------------------------------------------------------------

type domainView struct {
	ID, Name, State      string
	CreatedAt, UpdatedAt time.Time
	Host                 *deviceView
	HostLAN              consoleapi.LAN
	Devices, Trusted     int
	PendingCredentials   int
	Revision             int64
	DisplayName          string
}

type deviceView struct {
	consoleapi.DeviceRecord
	Grant         *trust.Grant
	GrantStatus   string // active, expired, none
	RenewalStatus string // active, expired, ""
	IsHost        bool
}

type credentialView struct {
	ID, Type, Role, Status string
	ExpiresAt, CreatedAt   time.Time
	UsedAt                 *time.Time
	UsedBy                 string
}

type issuedView struct {
	Text, Code, CodeDisplay, Type, Role string
	ExpiresAt                           time.Time
	QR                                  template.HTML
}

type deviceTypeOption struct {
	Value, Label string
	Selected     bool
}

type pageData struct {
	Title, CSRF, Nonce string
	UserID, UserName   string
	Tab                string
	Modal              string
	Error, Notice      string
	Next               string
	Domains            []domainView
	Domain             *domainView
	Devices            []deviceView
	Device             *deviceView
	Credentials        []credentialView
	Issued             *issuedView
	Audit              []auditRec
	AuditMore          bool
	AuditNext          int
	Relay              relay.Endpoint
	ConsoleID          string
	DeviceTypes        []deviceTypeOption
	GrantTTL           string
	Now                time.Time
}

func (s *Server) domainViewLocked(d *domainRec, now time.Time) domainView {
	v := domainView{ID: d.DomainID, Name: d.Name, State: d.State, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, HostLAN: d.HostLAN, Revision: s.st.Revision}
	v.DisplayName = d.Name
	if v.DisplayName == "" {
		v.DisplayName = d.DomainID
	}
	if h := s.hostLocked(d); h != nil {
		hv := s.deviceViewLocked(h, now)
		v.Host = &hv
	}
	for _, dev := range s.st.domainDevices(d.DomainID) {
		v.Devices++
		if dev.State == consoleapi.StateTrusted {
			v.Trusted++
		}
	}
	for _, c := range s.st.Credentials {
		if c.DomainID == d.DomainID && c.status(now) == "pending" {
			v.PendingCredentials++
		}
	}
	return v
}

func (s *Server) deviceViewLocked(dev *deviceRec, now time.Time) deviceView {
	v := deviceView{DeviceRecord: dev.record(), Grant: dev.Grant, GrantStatus: "none", IsHost: dev.Role == consoleapi.RoleDomainHost}
	if g := dev.Grant; g != nil {
		v.GrantStatus = "active"
		if !now.Before(g.ExpiresAt) {
			v.GrantStatus = "expired"
		}
	}
	if dev.Role == consoleapi.RoleOwnerDevice && dev.RenewalExpiresAt != nil {
		v.RenewalStatus = "active"
		if now.After(*dev.RenewalExpiresAt) {
			v.RenewalStatus = "expired"
		}
	}
	return v
}

// --- templates -------------------------------------------------------------------

var badgeTone = map[string]string{
	"active": "bg-ok-100 text-ok-700", "pending": "bg-warn-100 text-warn-700", "revoked": "bg-danger-100 text-danger-700",
	"suspended": "bg-danger-100 text-danger-700", "consumed": "bg-slate-200 text-slate-600", "expired": "bg-slate-200 text-slate-600",
	"allow": "bg-ok-100 text-ok-700", "deny": "bg-danger-100 text-danger-700",
}

var badgeLabel = map[string]string{
	"active": "活跃", "pending": "待激活", "revoked": "已撤销", "suspended": "已暂停",
	"consumed": "已使用", "expired": "已过期", "allow": "允许", "deny": "拒绝",
}

var toneClass = map[string]string{
	"ok": "bg-ok-100 text-ok-700", "warn": "bg-warn-100 text-warn-700", "danger": "bg-danger-100 text-danger-700", "muted": "bg-slate-200 text-slate-600",
}

const badgeBase = "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium "

var typeLabels = map[string]string{
	consoleapi.TypeSparkClaw: "后端",
	consoleapi.TypeDesktop:   "桌面端",
	consoleapi.TypePhone:     "手机",
	consoleapi.TypeWatch:     "手表",
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"badge": func(value string) template.HTML {
			tone, ok := badgeTone[value]
			if !ok {
				tone = "bg-slate-200 text-slate-600"
			}
			label, ok := badgeLabel[value]
			if !ok {
				label = value
			}
			return template.HTML(`<span class="` + badgeBase + tone + `">` + template.HTMLEscapeString(label) + `</span>`)
		},
		"tone": func(label, tone string) template.HTML {
			c, ok := toneClass[tone]
			if !ok {
				c = toneClass["muted"]
			}
			return template.HTML(`<span class="` + badgeBase + c + `">` + template.HTMLEscapeString(label) + `</span>`)
		},
		"idPrefix": func(id string) string {
			if i := strings.Index(id, "_"); i > 0 {
				return id[:i+1]
			}
			return ""
		},
		"idRest": func(id string) string {
			if i := strings.Index(id, "_"); i > 0 {
				return id[i+1:]
			}
			return id
		},
		"time":      s.formatTime,
		"typeLabel": func(t string) string { return typeLabels[t] },
		"short": func(v string, n int) string {
			if len(v) <= n {
				return v
			}
			return v[:n] + "…"
		},
		"join": strings.Join,
		"list": func(v ...string) []string { return v },
		"tabPath": func(t string) string {
			if t == "overview" {
				return ""
			}
			return "/" + t
		},
		"tabLabel": func(t string) string {
			return map[string]string{"overview": "概览", "devices": "设备", "pairing": "配对", "audit": "审计"}[t]
		},
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				k, _ := kv[i].(string)
				m[k] = kv[i+1]
			}
			return m
		},
		"code": consoleapi.FormatCode,
	}
}

func (s *Server) formatTime(v any) string {
	var t time.Time
	switch x := v.(type) {
	case time.Time:
		t = x
	case *time.Time:
		if x == nil {
			return "—"
		}
		t = *x
	default:
		return "—"
	}
	if t.IsZero() {
		return "—"
	}
	return t.In(s.opts.Location).Format("2006/01/02 15:04:05")
}

func (s *Server) parseTemplates() error {
	base, err := template.New("base").Funcs(s.funcs()).ParseFS(assets, "templates/layout.html")
	if err != nil {
		return err
	}
	s.pages = map[string]*template.Template{}
	for _, name := range []string{"domains", "overview", "devices", "device", "pairing", "audit", "message"} {
		t, err := template.Must(base.Clone()).ParseFS(assets, "templates/"+name+".html")
		if err != nil {
			return err
		}
		s.pages[name] = t
	}
	s.login, err = template.Must(base.Clone()).ParseFS(assets, "templates/login.html")
	return err
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data *pageData) {
	data.Nonce = crypto.Base64URL(crypto.RandomBytes(16))
	t, entry := s.pages[name], "layout"
	if name == "login" {
		t, entry = s.login, "login"
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, entry, data); err != nil {
		s.logger.Printf("dev-console: render %s: %v", name, err)
		http.Error(w, "页面渲染失败", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self' data:; script-src 'nonce-"+data.Nonce+"'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// page wraps a signed-in page.
func (s *Server) page(h func(http.ResponseWriter, *http.Request, *adminCtx, *pageData)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.authenticate(r)
		if !ok || a.Bearer {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		h(w, r, a, &pageData{CSRF: a.CSRF, UserID: a.UserID, UserName: a.DisplayName, ConsoleID: s.consoleID, Relay: s.endpoint, Now: s.now()})
	}
}

// form wraps a signed-in form submission (CSRF-checked).
func (s *Server) form(h func(http.ResponseWriter, *http.Request, *adminCtx, *pageData)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.authenticate(r)
		if !ok || a.Bearer {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := r.ParseForm(); err != nil || !csrfOK(a.CSRF, r.PostForm.Get("csrf")) {
			s.message(w, http.StatusForbidden, &pageData{CSRF: a.CSRF, UserID: a.UserID, UserName: a.DisplayName}, "请求已失效", "页面已过期，请返回后重试。")
			return
		}
		h(w, r, a, &pageData{CSRF: a.CSRF, UserID: a.UserID, UserName: a.DisplayName, ConsoleID: s.consoleID, Relay: s.endpoint, Now: s.now()})
	}
}

func (s *Server) message(w http.ResponseWriter, status int, data *pageData, title, text string) {
	data.Title, data.Error = title, text
	s.render(w, status, "message", data)
}

func (s *Server) notFound(w http.ResponseWriter, data *pageData) {
	s.message(w, http.StatusNotFound, data, "页面不存在", "资源不存在或无权访问")
}

// safeNext keeps redirects on this console.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/domains"
	}
	return next
}

// --- sign-in -----------------------------------------------------------------------

func (s *Server) pageLogin(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.authenticate(r); ok && !a.Bearer {
		http.Redirect(w, r, "/domains", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login", &pageData{Title: "登录控制台", Next: r.URL.Query().Get("next")})
}

func (s *Server) pageLoginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	_ = r.ParseForm()
	next := r.PostForm.Get("next")
	sess, _, e := s.checkLogin(r.PostForm.Get("password"))
	if e != nil {
		msg := "密码不正确"
		if e.Status == http.StatusTooManyRequests {
			msg = "请求过于频繁，请稍后再试"
		}
		s.render(w, e.Status, "login", &pageData{Title: "登录控制台", Error: msg, Next: next})
		return
	}
	s.setSessionCookie(w, sess)
	http.Redirect(w, r, safeNext(next), http.StatusSeeOther)
}

func (s *Server) pageLogout(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if sess := s.sessionFor(r); sess != nil && csrfOK(sess.csrf, r.PostForm.Get("csrf")) {
		s.dropSession(r)
	}
	s.setSessionCookie(w, nil)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- domains -----------------------------------------------------------------------

func (s *Server) pageDomains(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	s.mu.Lock()
	for _, d := range s.st.Domains {
		p.Domains = append(p.Domains, s.domainViewLocked(d, p.Now))
	}
	s.mu.Unlock()
	p.Title = "域"
	if r.URL.Query().Get("create") == "1" {
		p.Modal = "create"
	}
	s.render(w, http.StatusOK, "domains", p)
}

func (s *Server) formCreateDomain(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	d, e := s.createDomain(r.PostForm.Get("name"), a.UserID)
	if e != nil {
		s.message(w, e.Status, p, "创建失败", e.Message)
		return
	}
	http.Redirect(w, r, "/domains/"+d.DomainID, http.StatusSeeOther)
}

// loadDomain fills p.Domain; it reports false (and answers) when unknown.
func (s *Server) loadDomain(w http.ResponseWriter, r *http.Request, p *pageData, tab string) bool {
	s.mu.Lock()
	d := s.st.domain(r.PathValue("d"))
	if d != nil {
		v := s.domainViewLocked(d, p.Now)
		p.Domain = &v
	}
	s.mu.Unlock()
	if d == nil {
		s.notFound(w, p)
		return false
	}
	p.Tab = tab
	p.Title = p.Domain.DisplayName
	return true
}

func (s *Server) pageOverview(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if !s.loadDomain(w, r, p, "overview") {
		return
	}
	if r.URL.Query().Get("edit") == "1" {
		p.Modal = "rename"
	}
	s.render(w, http.StatusOK, "overview", p)
}

func (s *Server) formRenameDomain(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	name := r.PostForm.Get("name")
	if e := s.updateDomain(r.PathValue("d"), &name, nil, a.UserID); e != nil {
		s.message(w, e.Status, p, "保存失败", e.Message)
		return
	}
	http.Redirect(w, r, "/domains/"+r.PathValue("d"), http.StatusSeeOther)
}

// --- devices -----------------------------------------------------------------------

func (s *Server) pageDevices(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if !s.loadDomain(w, r, p, "devices") {
		return
	}
	s.mu.Lock()
	list := s.st.domainDevices(p.Domain.ID)
	sortDevices(list)
	for _, dev := range list {
		p.Devices = append(p.Devices, s.deviceViewLocked(dev, p.Now))
	}
	s.mu.Unlock()
	s.render(w, http.StatusOK, "devices", p)
}

func (s *Server) pageDevice(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if !s.loadDomain(w, r, p, "devices") {
		return
	}
	s.mu.Lock()
	dev := s.st.device(r.PathValue("dev"))
	if dev != nil && dev.DomainID == p.Domain.ID {
		v := s.deviceViewLocked(dev, p.Now)
		p.Device = &v
	}
	s.mu.Unlock()
	if p.Device == nil {
		s.notFound(w, p)
		return
	}
	p.Title = p.Device.Name
	p.GrantTTL = durationLabel(consoleapi.GrantTTL)
	switch r.URL.Query().Get("action") {
	case "rename":
		p.Modal = "rename"
	case "revoke":
		p.Modal = "revoke"
	}
	if r.URL.Query().Get("renewed") == "1" {
		p.Notice = "已重新授权自动续期。"
	}
	s.render(w, http.StatusOK, "device", p)
}

func (s *Server) devicePath(r *http.Request) string {
	return "/domains/" + url.PathEscape(r.PathValue("d")) + "/devices/" + url.PathEscape(r.PathValue("dev"))
}

func (s *Server) formRenameDevice(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if e := s.renameDevice(r.PathValue("d"), r.PathValue("dev"), r.PostForm.Get("name"), a.UserID); e != nil {
		s.message(w, e.Status, p, "保存失败", e.Message)
		return
	}
	http.Redirect(w, r, s.devicePath(r), http.StatusSeeOther)
}

func (s *Server) formRevokeDevice(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if _, e := s.revoke(r.PathValue("d"), r.PathValue("dev"), "device.revoke", actorAccount, a.UserID); e != nil {
		s.message(w, e.Status, p, "撤销失败", e.Message)
		return
	}
	http.Redirect(w, r, s.devicePath(r), http.StatusSeeOther)
}

func (s *Server) formRenewal(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if _, e := s.reauthorizeRenewal(r.PathValue("d"), r.PathValue("dev"), a.UserID); e != nil {
		s.message(w, e.Status, p, "授权失败", e.Message)
		return
	}
	http.Redirect(w, r, s.devicePath(r)+"?renewed=1", http.StatusSeeOther)
}

// --- pairing -----------------------------------------------------------------------

func (s *Server) loadCredentials(p *pageData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.st.Credentials) - 1; i >= 0; i-- {
		c := s.st.Credentials[i]
		if c.DomainID != p.Domain.ID {
			continue
		}
		p.Credentials = append(p.Credentials, credentialView{ID: c.CredentialID, Type: c.Type, Role: c.Role, Status: c.status(p.Now), ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt, UsedAt: c.UsedAt, UsedBy: c.UsedByDevice})
	}
}

func deviceTypeOptions(selected string) []deviceTypeOption {
	var out []deviceTypeOption
	for _, t := range []string{consoleapi.TypeDesktop, consoleapi.TypePhone, consoleapi.TypeWatch, consoleapi.TypeSparkClaw} {
		out = append(out, deviceTypeOption{Value: t, Label: typeLabels[t] + "（" + t + "）", Selected: t == selected})
	}
	return out
}

func (s *Server) pagePairing(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if !s.loadDomain(w, r, p, "pairing") {
		return
	}
	s.loadCredentials(p)
	if r.URL.Query().Get("issue") == "1" {
		p.Modal = "issue"
		t := r.URL.Query().Get("type")
		if !consoleapi.ValidDeviceType(t) {
			t = consoleapi.TypePhone
			if p.Domain.Host == nil {
				t = consoleapi.TypeSparkClaw
			}
		}
		p.DeviceTypes = deviceTypeOptions(t)
	}
	s.render(w, http.StatusOK, "pairing", p)
}

func (s *Server) formIssueCredential(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if !s.loadDomain(w, r, p, "pairing") {
		return
	}
	secs, _ := strconv.Atoi(r.PostForm.Get("expiry"))
	switch secs {
	case 600, 900, 1800:
	default:
		secs = 900
	}
	deviceType := r.PostForm.Get("device_type")
	issued, e := s.issueCredential(p.Domain.ID, deviceType, time.Duration(secs)*time.Second, a.UserID)
	if e != nil {
		s.loadCredentials(p)
		p.Modal, p.Error, p.DeviceTypes = "issue", e.Message, deviceTypeOptions(deviceType)
		s.render(w, e.Status, "pairing", p)
		return
	}
	qr, err := qrSVG(issued.text, 240)
	if err != nil {
		s.logger.Printf("dev-console: qr: %v", err)
	}
	p.Issued = &issuedView{Text: issued.text, Code: issued.code, CodeDisplay: consoleapi.FormatCode(issued.code), Type: issued.rec.Type, Role: issued.rec.Role, ExpiresAt: issued.rec.ExpiresAt, QR: qr}
	p.Modal = "issued"
	s.loadCredentials(p)
	s.mu.Lock()
	if d := s.st.domain(p.Domain.ID); d != nil {
		v := s.domainViewLocked(d, s.now())
		p.Domain = &v
	}
	s.mu.Unlock()
	s.render(w, http.StatusOK, "pairing", p)
}

// --- audit -------------------------------------------------------------------------

func (s *Server) pageAudit(w http.ResponseWriter, r *http.Request, a *adminCtx, p *pageData) {
	if !s.loadDomain(w, r, p, "audit") {
		return
	}
	limit := auditLimit(r)
	s.mu.Lock()
	p.Audit, p.AuditMore = s.domainAuditLocked(p.Domain.ID, limit)
	s.mu.Unlock()
	p.AuditNext = limit + 100
	s.render(w, http.StatusOK, "audit", p)
}

func durationLabel(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return strconv.Itoa(int(d/(24*time.Hour))) + " 天"
	}
	if d%time.Hour == 0 {
		return strconv.Itoa(int(d/time.Hour)) + " 小时"
	}
	return d.String()
}
