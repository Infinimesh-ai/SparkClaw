package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var s0ProductionConsumerComposites = map[string]string{
	"agent.Runtime":                                 "Session + Conversation + Run + Document + Approval + BrowserState + Memory + Audit + ArtifactMetadata",
	"agent.toolExposureEngine":                      "Run",
	"store.ArchiveToolObservation":                  "ArtifactMetadata",
	"credential.Vault":                              "Credential",
	"gateway.Server":                                "Owner + Client + Session + Conversation + Run + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + Memory + Audit + Evaluation + ArtifactMetadata + Credential",
	"gateway.runHasPendingApproval":                 "Approval",
	"happyapproval.Service":                         "Approval",
	"iscpbridge.GatewayAdapter":                     "Owner + Session + Conversation + Run + Approval + PassiveNotification + Audit",
	"iscppairing.Service":                           "ISCPOnboarding + Audit",
	"mcpaccess.Service":                             "MCP + Run + Approval + Audit",
	"mcpaccess.Provider":                            "MCP + Run + Session + ExternalChat + Audit + ArtifactMetadata",
	"mcpaccess.updateOperationRecord":               "MCP",
	"mcpaccess.rejectPendingApprovals":              "Run + Approval",
	"mcpaccess.finalizeRevokedOperations":           "MCP + Run + Approval + Audit",
	"mcpaccess.runHasApprovedApproval":              "Approval",
	"mcpaccess.runHasPendingApproval":               "Approval",
	"notification.SendWeixinText/Image/File/Typing": "Credential",
	"notification.WeixinAdapter":                    "Connector + Schedule + Credential + Session + ExternalChat + ArtifactMetadata",
	"reminder.Scheduler":                            "Schedule",
	"remindertarget.Resolver":                       "ExternalChat + Connector",
	"telegram.Dispatcher":                           "Owner + Session + Conversation + Run + Approval + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit",
	"telegram.Service":                              "Connector + DeliveryRecord + Audit",
	"telegram.NotificationAdapter":                  "Connector + Schedule + Session + ExternalChat + ArtifactMetadata",
	"toolhub.ToolHub":                               "Session + Run + Approval + Schedule + Connector + ExternalChat + Memory + Audit + ArtifactMetadata",
	"weixin.Dispatcher":                             "Owner + Session + Conversation + Run + Approval + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit",
	"weixin.Syncer":                                 "Session + Connector + Credential + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit",
	"weixin.MediaAdapter":                           "Session + ArtifactMetadata + Audit",
	"cmd/sparkclaw.newStore":                        "Owner + Client + ISCPOnboarding + Credential + Session + Conversation + Run + Document + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + BrowserState + Memory + Audit + Evaluation + ArtifactMetadata",
	"cmd/sparkclaw.newGatewayServices":              "Owner + Client + ISCPOnboarding + Credential + Session + Conversation + Run + Document + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + BrowserState + Memory + Audit + Evaluation + ArtifactMetadata",
	"cmd/sparkclaw.newISCPPairingService":           "ISCPOnboarding + Audit",
	"cmd/sparkclaw.newConnectorAssembly":            "Owner + Credential + Session + Conversation + Run + Approval + Schedule + Connector + ExternalChat + DeliveryRecord + MCP + Audit + ArtifactMetadata",
	"connector.Registry":                            "Connector",
	"messagecontrol.EndpointRegistry":               "Session + Connector + ExternalChat",
	"messagecontrol.mcpEndpointStore":               "MCP",
	"messagecontrol.ScheduleRegistry":               "Schedule + Session + Connector + ExternalChat",
	"messagecontrol.ReceiveLifecycle":               "DeliveryRecord",
	"delivery.PersistentWebDelivery":                "Conversation",
	"delivery.EndpointResourceResolver":             "Session + ArtifactMetadata",
	"delivery.StoreResourceResolver":                "ArtifactMetadata",
	"delivery.ResolveBrowserContent":                "Session + ArtifactMetadata",
	"delivery.RecordExternalDelivery":               "ExternalChat",
	"mcpaccess.auditOperationStore":                 "MCP + Audit",
	"mcpaccess.operationSessionID":                  "MCP",
}

func assertS0ProductionConsumerDocumentation(t *testing.T) {
	t.Helper()
	paths := []string{
		filepath.Join("..", "..", "..", "..", "docs", "store-s0-contract-inventory.md"),
		filepath.Join("..", "..", "..", "..", "zh-cn", "docs", "store-s0-contract-inventory.md"),
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rows := s0ProductionConsumerRows(string(raw))
		if len(rows) != len(s0ProductionConsumerComposites) {
			t.Errorf("%s contains %d production consumer rows, want %d", path, len(rows), len(s0ProductionConsumerComposites))
		}
		for symbol, want := range s0ProductionConsumerComposites {
			if got, ok := rows[symbol]; !ok {
				t.Errorf("%s has no production consumer row for %s", path, symbol)
			} else if got != want {
				t.Errorf("%s consumer %s composite = %q, want %q", path, symbol, got, want)
			}
		}
	}
}

func s0ProductionConsumerRows(document string) map[string]string {
	start := strings.Index(document, "## Production Consumer Matrix")
	if start < 0 {
		start = strings.Index(document, "## 生产消费者矩阵")
	}
	if start < 0 {
		return nil
	}
	section := document[start:]
	if end := strings.Index(section, "\n## Mutation"); end >= 0 {
		section = section[:end]
	}
	rows := map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		columns := strings.Split(line, "|")
		if len(columns) != 5 {
			continue
		}
		code := strings.Split(columns[1], "`")
		if len(code) < 3 {
			continue
		}
		symbol := strings.TrimSpace(code[1])
		if _, tracked := s0ProductionConsumerComposites[symbol]; !tracked {
			continue
		}
		composite := strings.TrimSpace(columns[3])
		if index := strings.IndexAny(composite, ";；"); index >= 0 {
			composite = strings.TrimSpace(composite[:index])
		}
		rows[symbol] = composite
	}
	return rows
}
