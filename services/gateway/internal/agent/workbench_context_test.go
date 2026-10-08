package agent

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestHostAndSubmittedConversationUseSameContextSelection(t *testing.T) {
	ctx := t.Context()
	host, err := store.NewFileStore(filepath.Join(t.TempDir(), "workbench.json"))
	if err != nil {
		t.Fatal(err)
	}
	submitted := store.NewMemoryStore()
	now := time.Now().UTC()
	var selections [][]string
	for _, repository := range []Repository{host, submitted} {
		session, err := repository.CreateSessionWithScope(ctx, "equivalent", "owner", "", "webchat", false)
		if err != nil {
			t.Fatal(err)
		}
		other, err := repository.CreateSessionWithScope(ctx, "private other", "other", "", "webchat", false)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 40; i++ {
			content := fmt.Sprintf("message %d", i)
			if i == 38 {
				content = strings.Repeat("中", app.WorkbenchInputBytes)
			}
			for _, target := range []string{session.ID, other.ID} {
				if _, err := repository.AddMessage(ctx, app.Message{ID: app.NewID("msg"), SessionID: target, Role: "assistant", Content: content, CreatedAt: now.Add(time.Duration(i-40) * time.Second)}); err != nil {
					t.Fatal(err)
				}
			}
		}
		history, err := (Runtime{store: repository}).buildInvocationHistory(ctx, app.AgentRun{ID: "current", SessionID: session.ID, StartedAt: now}, "current-message")
		if err != nil {
			t.Fatal(err)
		}
		selected := []string{}
		for _, message := range history.Selected.Messages {
			if message.SessionID != session.ID {
				t.Fatal("another workbench supplied context")
			}
			selected = append(selected, message.Role+":"+message.Content)
		}
		selections = append(selections, selected)
	}
	if len(selections[0]) != app.WorkbenchSelectedMessages || !reflect.DeepEqual(selections[0], selections[1]) {
		t.Fatal("host and submitted conversation selection diverged")
	}
}
