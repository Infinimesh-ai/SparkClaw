package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
)

func TestPublicAdapterConfigDoesNotExposeDocumentOCRDestination(t *testing.T) {
	cfg := config.Default().Adapters
	cfg.DocumentOCR.Enabled = true
	cfg.DocumentOCR.Provider = "openai-http"
	cfg.DocumentOCR.BaseURL = "https://private-ocr.example.test/v1"
	cfg.DocumentOCR.AllowedHosts = []string{"private-ocr.example.test"}
	cfg.DocumentOCR.Model = "ATH-MaaS/OvisOCR2"
	raw, err := json.Marshal(publicAdapterConfig(cfg, documentocr.RuntimeReadiness{
		ConfiguredEnabled: true, AdapterReady: false, RuntimeStatus: "degraded", ReasonCode: "constructor_failed",
		Provider: cfg.DocumentOCR.Provider, Model: cfg.DocumentOCR.Model,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, cfg.DocumentOCR.BaseURL) || strings.Contains(text, cfg.DocumentOCR.AllowedHosts[0]) || strings.Contains(text, "base_url") || strings.Contains(text, "allowed_hosts") {
		t.Fatalf("public config exposed the document OCR destination: %s", text)
	}
	if !strings.Contains(text, `"model":"ATH-MaaS/OvisOCR2"`) || !strings.Contains(text, `"configured_enabled":true`) ||
		!strings.Contains(text, `"adapter_ready":false`) || !strings.Contains(text, `"runtime_status":"degraded"`) || !strings.Contains(text, `"reason_code":"constructor_failed"`) {
		t.Fatalf("public config omitted document OCR identity: %s", text)
	}
}
