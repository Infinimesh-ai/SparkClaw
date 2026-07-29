package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestBrowserPersistenceRedactsProviderQueryFromNestedOutputAndText(t *testing.T) {
	target := app.BrowserTargetDescriptor{
		TargetKind:    app.BrowserTargetRegisteredDestination,
		DestinationID: "qq_mail", QueryProvenance: app.BrowserQueryProviderVolatile,
		CanonicalURL: "https://mail.qq.com/",
	}
	value := map[string]any{
		"url": "https://wx.mail.qq.com/home/index?sid=secret#/list/1/1",
		"pages": []any{map[string]any{
			"final_url": "https://wx.mail.qq.com/home/index?sid=secret#/list/1/1",
			"title":     "QQ邮箱",
		}},
		"text": "page_2 https://wx.mail.qq.com/home/index?sid=secret#/list/1/1 QQ邮箱 收件箱",
	}
	redacted := browserPersistenceValue(target, "", value)
	raw, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sid=") || strings.Contains(string(raw), "secret") {
		t.Fatalf("provider query survived browser persistence projection: %s", raw)
	}
	for _, want := range []string{
		"https://wx.mail.qq.com/home/index#/list/1/1",
		"QQ邮箱",
		"收件箱",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("browser persistence projection lost %q: %s", want, raw)
		}
	}
}

func TestBrowserPersistenceKeepsOnlyFrozenOwnerQuery(t *testing.T) {
	target := app.BrowserTargetDescriptor{
		TargetKind:      app.BrowserTargetExplicitURL,
		QueryProvenance: app.BrowserQueryOwnerSupplied,
		CanonicalURL:    "https://example.com/report?owner_filter=active",
	}
	got := browserSafePersistenceURL(target, "https://example.com/report?owner_filter=active&sid=provider#summary")
	if got != "https://example.com/report?owner_filter=active#summary" {
		t.Fatalf("owner query persistence projection = %q", got)
	}
}
