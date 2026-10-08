package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

func TestWorkbenchInputAdmissionHasNoDesktopSpecificByteLimit(t *testing.T) {
	for _, size := range []int{16<<10 + 1, app.WorkbenchInputBytes, app.WorkbenchInputBytes + 1} {
		content := strings.Repeat("x", size)
		hostErr := validateWorkbenchInput(webMessageInput{Content: content})
		e := execution.Envelope{SchemaVersion: 1, OwnerID: "owner", ClientID: "client", DeploymentID: "deployment",
			InstallationID: "11111111-1111-4111-8111-111111111111", ConversationID: "22222222-2222-4222-8222-222222222222",
			TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444",
			Messages: []execution.Message{{Role: "user", Content: content}}}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		_, remoteErr := execution.Decode(raw, execution.Digest(raw))
		if (hostErr != nil) != (remoteErr != nil) || (hostErr != nil) != (size > app.WorkbenchInputBytes) {
			t.Fatalf("%d bytes: host=%v remote=%v", size, hostErr, remoteErr)
		}
	}
}
