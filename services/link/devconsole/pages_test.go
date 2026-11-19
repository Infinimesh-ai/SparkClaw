package devconsole_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
)

func newJar() http.CookieJar {
	j, _ := cookiejar.New(nil)
	return j
}

type browser struct {
	t *testing.T
	h *harness
	c *http.Client
}

func (b *browser) get(path string) (int, string) {
	b.t.Helper()
	res, err := b.c.Get(b.h.url + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func (b *browser) post(path string, form url.Values) (int, string, *http.Response) {
	b.t.Helper()
	res, err := b.c.PostForm(b.h.url+path, form)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw), res
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func csrfOf(t *testing.T, page string) string {
	t.Helper()
	m := csrfField.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("page has no CSRF field")
	}
	return m[1]
}

func mustContain(t *testing.T, page string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(page, p) {
			t.Fatalf("page lacks %q", p)
		}
	}
}

func TestPages(t *testing.T) {
	h := newHarness(t)
	b := &browser{t: t, h: h, c: &http.Client{Transport: h.admin.Transport, Jar: newJar()}}

	// Signed out: pages send the browser to sign in.
	st, page := b.get("/domains")
	if st != http.StatusOK || !strings.Contains(page, "登录控制台") {
		t.Fatalf("signed-out /domains: %d", st)
	}
	st, page, _ = b.post("/login", url.Values{"password": {"wrong password"}})
	if st != http.StatusUnauthorized || !strings.Contains(page, "密码不正确") {
		t.Fatalf("wrong password: %d", st)
	}
	st, page, _ = b.post("/login", url.Values{"password": {adminPass}, "next": {"/domains"}})
	if st != http.StatusOK {
		t.Fatalf("login: %d", st)
	}
	mustContain(t, page, "控制台", "域", "退出登录", "还没有域", "创建域", "/assets/console.css")
	csrf := csrfOf(t, page)

	st, page = b.get("/domains?create=1")
	mustContain(t, page, "域是设备与信任关系的隔离边界。当前支持个人域。", "例如：我的工作台")

	// A form without the CSRF token is refused.
	if st, _, _ = b.post("/domains", url.Values{"name": {"x"}}); st != http.StatusForbidden {
		t.Fatalf("form without CSRF: %d", st)
	}
	st, page, res := b.post("/domains", url.Values{"name": {"我的工作台"}, "csrf": {csrf}})
	if st != http.StatusOK {
		t.Fatalf("create domain: %d", st)
	}
	domPath := res.Request.URL.Path
	if !strings.HasPrefix(domPath, "/domains/dom_") {
		t.Fatalf("redirected to %s", domPath)
	}
	mustContain(t, page, "← 全部域", "我的工作台", "概览", "设备", "配对", "审计", "域信息", "后端（domain_host）", relayID)

	st, page = b.get(domPath + "/devices")
	mustContain(t, page, "还没有设备")

	st, page = b.get(domPath + "/pairing?issue=1")
	mustContain(t, page, "签发 SparkClaw 接入凭据", "目标设备类型", "10 分钟", "15 分钟", "30 分钟", `value="sparkclaw" selected`)

	// Issuing shows the credential once: QR code, text and formatted code.
	st, page, _ = b.post(domPath+"/pairing", url.Values{"csrf": {csrf}, "device_type": {"sparkclaw"}, "expiry": {"600"}})
	if st != http.StatusOK {
		t.Fatalf("issue: %d %s", st, page)
	}
	mustContain(t, page, "SparkClaw 接入凭据已签发", "<svg", "此码仅显示这一次，服务端只保存其哈希；关闭后无法再次查看。", "我已保存，关闭", consoleapi.CredentialPrefix)
	codeMatch := regexp.MustCompile(`data-code>(\d{4} \d{4} \d{4})<`).FindStringSubmatch(page)
	textMatch := regexp.MustCompile(`data-credential>(` + regexp.QuoteMeta(consoleapi.CredentialPrefix) + `[A-Za-z0-9_-]+)<`).FindStringSubmatch(page)
	if codeMatch == nil || textMatch == nil {
		t.Fatal("issued page lacks the code or the credential text")
	}
	cred, err := consoleapi.ParseCredential(textMatch[1])
	if err != nil {
		t.Fatal(err)
	}
	if consoleapi.FormatCode(cred.Code) != codeMatch[1] || cred.Type != consoleapi.TypeSparkClaw || cred.TLSPin != h.pin {
		t.Fatalf("credential %+v does not match the shown code %s", cred, codeMatch[1])
	}

	// The list shows it pending; it is never shown again.
	st, page = b.get(domPath + "/pairing")
	mustContain(t, page, "sparkclaw / domain_host", "待激活")
	if strings.Contains(page, cred.Code) || strings.Contains(page, codeMatch[1]) {
		t.Fatal("the code is shown again")
	}

	// The backend joins with it; the device pages show it.
	_, hostKey := h.join(cred, "工作台后端", nil)
	st, page = b.get(domPath + "/devices")
	mustContain(t, page, "工作台后端", "sparkclaw / domain_host", "已信任", "公钥指纹", "最近活动")
	st, page = b.get(domPath + "/pairing")
	mustContain(t, page, "已使用")

	phone, phoneKey := h.join(h.issue(strings.TrimPrefix(domPath, "/domains/"), consoleapi.TypePhone), "我的手机", nil)
	devPath := domPath + "/devices/" + phoneKey.Identity.DeviceID
	st, page = b.get(devPath)
	if st != http.StatusOK {
		t.Fatalf("device page: %d", st)
	}
	mustContain(t, page, "← 设备列表", "设备信息", "公钥指纹", phone.Device.Fingerprint, "配对授权（Trust Grant）", "授权状态", "有效", "对端设备（audience）", hostKey.Identity.DeviceID, consoleapi.Permission, "到期时间", "自动续期授权本身有绝对到期（默认 90 天）", "重新授权自动续期", "危险操作", "撤销设备")

	st, page = b.get(devPath + "?action=revoke")
	mustContain(t, page, "撤销立即生效且不可恢复。")
	st, page, _ = b.post(devPath+"/renewal", url.Values{"csrf": {csrf}})
	mustContain(t, page, "已重新授权自动续期。")
	st, page, _ = b.post(devPath+"/rename", url.Values{"csrf": {csrf}, "name": {"改名后的手机"}})
	mustContain(t, page, "改名后的手机")
	st, page, _ = b.post(devPath+"/revoke", url.Values{"csrf": {csrf}})
	mustContain(t, page, "已撤销", "撤销时间")
	if strings.Contains(page, "危险操作") {
		t.Fatal("revoked device still offers revocation")
	}

	st, page = b.get(domPath + "/audit")
	mustContain(t, page, "时间", "动作", "结果", "操作者", "资源", "access_credential.issue", "device.join", "device.revoke", "允许")

	st, page = b.get("/domains")
	mustContain(t, page, "我的工作台", "活跃", "后端已接入")

	st, _ = b.get("/domains/dom_missing")
	if st != http.StatusNotFound {
		t.Fatalf("unknown domain: %d", st)
	}

	res2, err := b.c.Get(h.url + "/assets/console.css")
	if err != nil {
		t.Fatal(err)
	}
	css, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if !strings.Contains(string(css), "tailwindcss") || res2.Header.Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatal("stylesheet not served")
	}

	// Signing out ends the session.
	b.post("/logout", url.Values{"csrf": {csrf}})
	st, page = b.get("/domains")
	mustContain(t, page, "登录控制台")
}

// TestTemplateClassesExistInCSS checks that every utility class the pages
// use exists in the official stylesheet (only those render).
func TestTemplateClassesExistInCSS(t *testing.T) {
	css, err := os.ReadFile("assets/console.css")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("templates/*.html")
	src := []string{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		src = append(src, string(b))
	}
	goSrc, _ := os.ReadFile("pages.go")
	qrSrc, _ := os.ReadFile("qr.go")
	actions := regexp.MustCompile(`\{\{.*?\}\}`)
	classAttr := regexp.MustCompile(`class="([^"]*)"`)
	goStrings := regexp.MustCompile(`"((?:bg|text)-[a-z0-9-]+ (?:bg|text)-[a-z0-9-]+)"`)
	seen := map[string]bool{}
	var classes []string
	add := func(list string) {
		for _, c := range strings.Fields(list) {
			if !seen[c] {
				seen[c] = true
				classes = append(classes, c)
			}
		}
	}
	for _, s := range src {
		for _, m := range classAttr.FindAllStringSubmatch(actions.ReplaceAllString(s, " "), -1) {
			add(m[1])
		}
	}
	for _, m := range classAttr.FindAllStringSubmatch(string(qrSrc), -1) {
		add(m[1])
	}
	for _, m := range goStrings.FindAllStringSubmatch(string(goSrc), -1) {
		add(m[1])
	}
	for _, c := range []string{"max-w-xl", "max-w-md", "border-signal-600", "font-medium", "text-ink-900", "inline-flex", "items-center", "rounded-full", "px-2", "py-0.5", "text-xs"} {
		add(c)
	}
	for _, c := range classes {
		var esc strings.Builder
		for _, r := range c {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r > 127 {
				esc.WriteRune(r)
			} else {
				esc.WriteByte('\\')
				esc.WriteRune(r)
			}
		}
		if !strings.Contains(string(css), "."+esc.String()) {
			t.Errorf("class %q is not in the stylesheet", c)
		}
	}
	if len(classes) < 50 {
		t.Fatalf("found only %d classes; the scan is broken", len(classes))
	}
}
