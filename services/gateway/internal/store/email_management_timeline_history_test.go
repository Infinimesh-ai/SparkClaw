package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Compare the entire persisted row, including the query sort key and payload,
// rather than only the projected run count or its ID.
func timelineHistoryRows(t *testing.T, repo EmailRepository, owner string) map[string]EmailRecord {
	t.Helper()
	result := map[string]EmailRecord{}
	var rows map[string]EmailRecord
	switch s := repo.(type) {
	case *MemoryStore:
		rows = s.snapshot().EmailRecords
	case *FileStore:
		rows = s.inner.snapshot().EmailRecords
	case *PostgresStore:
		query, err := s.db.Query(t.Context(), `SELECT owner_id,kind,id,parent,related,state,search_text,sort_key,payload FROM email_management_records WHERE owner_id=$1`, owner)
		if err != nil {
			t.Fatal(err)
		}
		defer query.Close()
		rows = map[string]EmailRecord{}
		for query.Next() {
			var row EmailRecord
			if err := query.Scan(&row.Owner, &row.Kind, &row.ID, &row.Parent, &row.Related, &row.State, &row.Search, &row.Sort, &row.Data); err != nil {
				t.Fatal(err)
			}
			rows[emailRecordKey(row.Owner, row.Kind, row.ID)] = row
		}
		if err := query.Err(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unexpected email backend %T", repo)
	}
	for key, row := range rows {
		if row.Owner == owner {
			result[key] = row
		}
	}
	return result
}

func assertTimelineHistoryPreserved(t *testing.T, before, after map[string]EmailRecord) {
	t.Helper()
	for key, row := range before {
		if row.Kind == "sync" || row.Kind == "mail" || row.Kind == "capture" {
			if !reflect.DeepEqual(row, after[key]) {
				t.Errorf("existing %s row %s changed or disappeared", row.Kind, row.ID)
			}
		}
	}
}

func timelineHistoryLease(t *testing.T, f *emailContractFixture) EmailJobLease {
	t.Helper()
	_, err := f.repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover,
		TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
	f.must(err)
	return f.lease(f.claim(app.EmailJobDiscover))
}

func timelineHistoryCommit(t *testing.T, f *emailContractFixture, lease EmailJobLease, upper time.Time) (EmailSyncCommitCommand, EmailSyncCommitResult) {
	t.Helper()
	checkpoint := beginTimelineSync(t, f, upper)
	command := EmailSyncCommitCommand{
		EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration,
		Lease: lease, ObservedAt: time.Now(), ProviderMode: app.EmailProviderModeTimeRange,
		IntervalStart: checkpoint.IntervalStart, IntervalEnd: checkpoint.IntervalEnd, Trigger: "test", Actor: "system", Complete: true,
		Members: []EmailDiscoveryMember{{ProviderMessageID: "same-provider-message", ProviderSelectionID: "same-provider-message", Direction: "inbound", SourceTime: checkpoint.IntervalStart}},
		Captures: []EmailSyncCapture{{ProviderMessageID: "same-provider-message", ReadState: "unknown", Capture: app.EmailCaptureVersion{
			ID: "capture-" + f.box.ID, ManifestPath: "source/manifest.json", ManifestSHA256: strings.Repeat("a", 64),
			OriginalPath: "source/original.eml", OriginalSHA256: strings.Repeat("b", 64), State: app.EmailCaptureComplete,
		}}},
	}
	result, err := f.repo.CommitEmailSync(t.Context(), command)
	f.must(err)
	f.box = result.Mailbox
	return command, result
}

func TestEmailTimelineHistoryIsolatedByCommitAndMailbox(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		f.capture(f.admit("original-mail", time.Now()))
		original := timelineHistoryRows(t, repo, f.owner)
		lease := timelineHistoryLease(t, f)
		first, firstResult := timelineHistoryCommit(t, f, lease, f.box.Boundary.Add(time.Minute))
		before := timelineHistoryRows(t, repo, f.owner)
		assertTimelineHistoryPreserved(t, original, before)
		firstRuns, err := repo.ListEmailSyncRuns(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)

		second, secondResult := timelineHistoryCommit(t, f, lease, first.IntervalEnd.Add(time.Minute))
		after := timelineHistoryRows(t, repo, f.owner)
		assertTimelineHistoryPreserved(t, before, after)
		secondRuns, err := repo.ListEmailSyncRuns(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		if len(secondRuns) != len(firstRuns)+1 {
			t.Errorf("new commit overwrote history: runs before=%d after=%d", len(firstRuns), len(secondRuns))
		}

		// A second provider uses the same owner and native message ID. Its round
		// must not replace either mailbox's previous history or original source.
		f.box, err = repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderOutlook, Address: "other@example.test", Enabled: true})
		f.must(err)
		otherLease := timelineHistoryLease(t, f)
		third, thirdResult := timelineHistoryCommit(t, f, otherLease, f.box.Boundary.Add(time.Minute))
		all := timelineHistoryRows(t, repo, f.owner)
		assertTimelineHistoryPreserved(t, after, all)
		allRuns, err := repo.ListEmailSyncRuns(t.Context(), EmailQuery{OwnerID: f.owner})
		f.must(err)
		if len(allRuns) != len(secondRuns)+1 {
			t.Errorf("mailboxes collided in sync history: first mailbox=%d all=%d", len(secondRuns), len(allRuns))
		}

		// Replay old commits after later rounds and a store restart. The outer
		// command receipt must return the original result without touching rows.
		repo = restartEmailRepeatRepository(t, repo)
		beforeReplay := timelineHistoryRows(t, repo, f.owner)
		for _, prior := range []struct {
			command EmailSyncCommitCommand
			result  EmailSyncCommitResult
		}{{first, firstResult}, {second, secondResult}, {third, thirdResult}} {
			result, err := repo.CommitEmailSync(t.Context(), prior.command)
			f.must(err)
			if !reflect.DeepEqual(result, prior.result) {
				t.Errorf("replay changed its original committed result")
			}
		}
		if !reflect.DeepEqual(beforeReplay, timelineHistoryRows(t, repo, f.owner)) {
			t.Fatal("command replay changed or added persisted rows")
		}
		changed := third
		changed.EmailCommand = first.EmailCommand
		if _, err := repo.CommitEmailSync(t.Context(), changed); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("reusing another mailbox's command identity did not conflict: %v", err)
		}
		if !reflect.DeepEqual(beforeReplay, timelineHistoryRows(t, repo, f.owner)) {
			t.Fatal("conflicting replay mutated persisted rows")
		}
	})
}

func TestEmailTimelinePreservesLegacyEmptyKeyHistory(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		legacy := app.EmailSyncRun{ID: emailID(f.owner, ""), MailboxID: f.box.ID, Trigger: "recent_inbound", Coverage: "partial", Discovered: 17, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		seed := func(e *emailEngine) (bool, error) {
			emailPut(e, "sync", legacy.ID, legacy.MailboxID, "", "", "", emailOrder(legacy.CreatedAt, legacy.ID), legacy)
			return true, e.err
		}
		var err error
		switch s := repo.(type) {
		case *MemoryStore:
			_, err = emailMemoryRun(s, t.Context(), OperationCommitEmailSync, f.owner, "", nil, true, seed)
		case *FileStore:
			_, err = emailFileRun(s, t.Context(), OperationCommitEmailSync, f.owner, "", nil, true, seed)
		case *PostgresStore:
			_, err = emailPostgresRun(s, t.Context(), OperationCommitEmailSync, f.owner, "", nil, true, seed)
		}
		f.must(err)
		before := timelineHistoryRows(t, repo, f.owner)
		timelineHistoryCommit(t, f, timelineHistoryLease(t, f), f.box.Boundary.Add(time.Minute))
		assertTimelineHistoryPreserved(t, before, timelineHistoryRows(t, repo, f.owner))
		runs, err := repo.ListEmailSyncRuns(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		if len(runs) != 2 {
			t.Fatalf("legacy row was replaced or migrated: got %d runs, want legacy + new", len(runs))
		}
	})
}
