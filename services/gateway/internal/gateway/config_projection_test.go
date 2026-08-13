package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type readyProjectionAuthority struct{}

func (readyProjectionAuthority) Ready(context.Context) error { return nil }
func (readyProjectionAuthority) IssuePairingTicket(context.Context, iscppairing.AuthorityRequest) (iscppairing.AuthorityResult, error) {
	return iscppairing.AuthorityResult{}, nil
}

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

func TestPublicMCPConfigOmitsCredentialEnvironmentReferences(t *testing.T) {
	servers := map[string]config.MCPServerConfig{
		"localmind": {
			Transport: "streamable-http", URLEnv: "VERY_PRIVATE_LOCALMIND_URL_ENV", BearerTokenEnv: "VERY_PRIVATE_LOCALMIND_TOKEN_ENV",
			Namespace: "localmind", ExpectedServerName: "localmind-workspace", ProtocolVersion: "2025-06-18",
			AllowMutations: true, ToolAllow: []string{"read_document"},
		},
		"happy-tasks": {
			URL: "https://private-happy.example.test/mcp", TokenEnv: "VERY_PRIVATE_HAPPY_TOKEN_ENV",
			Namespace: "mcp.happy-tasks", ExpectedServerName: "happy-team-tasks",
		},
	}
	raw, err := json.Marshal(publicMCPServersConfig(servers))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "VERY_PRIVATE") || strings.Contains(text, "private-happy.example.test") ||
		strings.Contains(text, "url_env") || strings.Contains(text, "bearer_token_env") || strings.Contains(text, "token_env") {
		t.Fatalf("public MCP config exposed an endpoint or credential reference: %s", text)
	}
	if !strings.Contains(text, `"configured":true`) || !strings.Contains(text, `"allow_mutations":true`) {
		t.Fatalf("public MCP config omitted safe status fields: %s", text)
	}
}

func TestPublicISCPPairingStatusOmitsAuthorityCredentialAndPath(t *testing.T) {
	cfg := config.Default().ISCPPairing
	cfg.Enabled = true
	cfg.DomainID = "domain-public"
	cfg.AuthorityURL = "https://authority.example.test/private/issue"
	cfg.TokenEnv = "VERY_PRIVATE_ISCP_AUTHORITY_TOKEN"
	service := iscppairing.New(store.NewMemoryStore(), iscppairing.Options{
		Enabled: true, DomainID: cfg.DomainID, AuthorityHost: "authority.example.test",
		ExpectedTicketType: cfg.ExpectedTicketType, Authority: readyProjectionAuthority{},
	})
	raw, err := json.Marshal(service.Status(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, secret := range []string{cfg.AuthorityURL, cfg.TokenEnv, "/private/issue", "token_env", "token_file"} {
		if strings.Contains(text, secret) {
			t.Fatalf("public ISCP pairing status exposed private configuration %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, `"ready":true`) || !strings.Contains(text, `"domain_id":"domain-public"`) ||
		!strings.Contains(text, `"authority_host":"authority.example.test"`) {
		t.Fatalf("public ISCP pairing status omitted safe readiness fields: %s", text)
	}
}
