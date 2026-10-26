package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestEmailListFailureAttributionSurvivesBackendRestart(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		const invocation = "email_changes_0123456789abcdef_r2"
		commitTimelineSync(t, f, checkpoint, func(c *EmailSyncCommitCommand) {
			c.Complete = false
			c.ErrorCode = "email_provider_unavailable"
			c.FailureScope = app.EmailSyncFailureProviderOperational
			c.InvocationID = invocation
		})
		before := timelineHistoryRows(t, repo, f.owner)
		repo = restartEmailRepeatRepository(t, repo)
		after := timelineHistoryRows(t, repo, f.owner)
		found := 0
		for key, row := range after {
			if row.Kind != "sync_failure" {
				continue
			}
			found++
			var failure app.EmailSyncFailure
			if err := json.Unmarshal(row.Data, &failure); err != nil {
				t.Fatal(err)
			}
			if len(failure.Attempts) != 1 || failure.Attempts[0].InvocationID != invocation || failure.Attempts[0].Outcome != "failed" {
				t.Fatalf("lost failure identity: %+v", failure)
			}
			var original app.EmailSyncFailure
			if err := json.Unmarshal(before[key].Data, &original); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(failure, original) {
				t.Fatalf("restart changed failure evidence: before=%+v after=%+v", original, failure)
			}
		}
		if found != 1 {
			t.Fatalf("failure rows=%d", found)
		}
	})
}
