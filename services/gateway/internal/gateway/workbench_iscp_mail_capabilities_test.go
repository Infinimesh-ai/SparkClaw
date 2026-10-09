package gateway

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestISCPMailAttachmentCapabilityRequiresCompleteObjectTransport(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	service, err := emailmanagement.New(repo, &noEmailBrowser{}, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	server.emailManagement = service
	if _, err = repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "iscp-owner", CommandKey: "mail-cap"}, Provider: app.EmailProviderGmail, Address: "owner@example.test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	checked := time.Now().UTC()
	if _, err = repo.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "iscp-owner", Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked}, 0); err != nil {
		t.Fatal(err)
	}
	operations := []string{iscpworkbench.OperationMailDraftsList, iscpworkbench.OperationMailDraftsSave, iscpworkbench.OperationMailDraftsSend, iscpworkbench.OperationMailDraftsReconcile, iscpworkbench.OperationTransferOpen, iscpworkbench.OperationTransferChunk, iscpworkbench.OperationTransferCommit, iscpworkbench.OperationTransferStatus, iscpworkbench.OperationTransferAbort, iscpworkbench.OperationObjectDescribe, iscpworkbench.OperationObjectRelease}
	cfg.QualifiedCapabilities = slices.Clone(operations)
	session := iscpworkbench.SessionInfo{Profile: iscpworkbench.ProfileV2, Scopes: []string{"files.read"}}
	for _, name := range operations {
		spec, ok := iscpworkbench.LookupOperation(name)
		if !ok {
			t.Fatal(name)
		}
		if !slices.Contains(session.Scopes, spec.Scope) {
			session.Scopes = append(session.Scopes, spec.Scope)
		}
	}
	adapter := &iscpDomainAdapter{server: server, config: cfg}
	inspect := func(info iscpworkbench.SessionInfo) iscpDomainCapability {
		t.Helper()
		result := adapter.capabilitiesWithSession(domainTestContext(t), domainTestRequest(iscpworkbench.OperationCapabilitiesGet, nil), info)
		var manifest struct {
			Capabilities []iscpDomainCapability `json:"capabilities"`
		}
		if result.status != 200 || json.Unmarshal(result.body, &manifest) != nil {
			t.Fatalf("capabilities %d %s", result.status, result.body)
		}
		for _, capability := range manifest.Capabilities {
			if capability.ID == "mail_send_attachments" {
				return capability
			}
		}
		t.Fatal("attachment capability absent")
		return iscpDomainCapability{}
	}
	if got := inspect(session); !got.Enabled {
		t.Fatalf("complete dependency set disabled %+v", got)
	}
	for _, operation := range operations {
		adapter.config.QualifiedCapabilities = slices.DeleteFunc(slices.Clone(operations), func(v string) bool { return v == operation })
		if got := inspect(session); got.Enabled || got.Qualified {
			t.Fatalf("missing qualification %s enabled %+v", operation, got)
		}
	}
	adapter.config.QualifiedCapabilities = slices.Clone(operations)
	for _, scope := range session.Scopes {
		limited := session
		limited.Scopes = slices.DeleteFunc(slices.Clone(session.Scopes), func(v string) bool { return v == scope })
		if got := inspect(limited); got.Enabled || got.Permitted {
			t.Fatalf("missing scope %s enabled %+v", scope, got)
		}
	}
}
