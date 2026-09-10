package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestEmailManagementPostgresBytewiseCursorOrdering(t *testing.T) {
	s, err := NewPostgresStore(t.Context(), newPostgresMigrationTestSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	const owner = "sort-owner"
	// Equal prefixes deliberately mix case and punctuation so a locale-aware
	// sort cannot accidentally pass by using only the usual hexadecimal IDs.
	_, err = emailPostgresRun(s, t.Context(), OperationRequestEmailJob, owner, "seed", nil, true, func(e *emailEngine) (struct{}, error) {
		for _, id := range []string{"a", "~", "Z", "9"} {
			emailPut(e, "job", id, "mailbox", "discover", app.EmailJobQueued, "", "same/"+id, app.EmailJob{ID: id})
		}
		return struct{}{}, e.err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, asc := range []bool{true, false} {
		query := emailRowsQuery{Kind: "job", Asc: asc, Limit: 2}
		var ids []string
		for {
			jobs, err := emailPostgresRun(s, t.Context(), OperationListEmailJobs, owner, "", nil, false, func(e *emailEngine) ([]app.EmailJob, error) { return emailList[app.EmailJob](e, query), e.err })
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) == 0 {
				break
			}
			for _, job := range jobs {
				ids = append(ids, job.ID)
			}
			query.After = "same/" + jobs[len(jobs)-1].ID
		}
		expected := []string{"~", "a", "Z", "9"}
		if asc {
			expected = []string{"9", "Z", "a", "~"}
		}
		if !reflect.DeepEqual(ids, expected) {
			t.Fatalf("ascending=%v paginated order=%v want %v", asc, ids, expected)
		}
	}
	jobs, err := emailPostgresRun(s, t.Context(), OperationListEmailJobs, owner, "", nil, false, func(e *emailEngine) ([]app.EmailJob, error) {
		return emailList[app.EmailJob](e, emailRowsQuery{Kind: "job", Due: "same/~", Asc: true, Limit: 10}), e.err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 {
		t.Fatalf("inclusive due boundary omitted %d jobs", 4-len(jobs))
	}
	// The bytewise range/order remains index-supported after upgrading a
	// database created with any locale.
	tx, err := s.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), "SET LOCAL enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(t.Context(), `EXPLAIN SELECT id FROM email_management_records WHERE owner_id=$1 AND kind='job' AND sort_key COLLATE "C"<=$2 ORDER BY sort_key COLLATE "C" DESC,id COLLATE "C" DESC LIMIT 2`, owner, "same/~")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "email_management_order") || strings.Contains(plan, "Sort") {
		t.Fatalf("bytewise range/order lost index support:\n%s", plan)
	}
}
