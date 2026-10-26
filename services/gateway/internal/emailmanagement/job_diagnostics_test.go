package emailmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type diagnosticJobRepository struct {
	*store.MemoryStore
	renewErrors          map[string]error
	dispatchError        error
	block                bool
	mu                   sync.Mutex
	finished             []store.EmailJobFinish
	completeAfterRenewal chan struct{}
}

func (r *diagnosticJobRepository) ReconcileEmailCommand(ctx context.Context, owner, key string) (store.EmailCommandReceipt, bool, error) {
	if r.completeAfterRenewal != nil {
		select {
		case <-r.completeAfterRenewal:
		case <-ctx.Done():
			return store.EmailCommandReceipt{}, false, ctx.Err()
		}
		return store.EmailCommandReceipt{}, true, nil
	}
	return r.MemoryStore.ReconcileEmailCommand(ctx, owner, key)
}

func (r *diagnosticJobRepository) GetEmailMail(ctx context.Context, _, _ string) (app.EmailMail, bool, error) {
	if r.block {
		<-ctx.Done()
		return app.EmailMail{}, false, ctx.Err()
	}
	return app.EmailMail{}, false, r.dispatchError
}
func (r *diagnosticJobRepository) RenewEmailJob(ctx context.Context, c store.EmailJobRenew) (app.EmailJob, error) {
	if r.completeAfterRenewal != nil {
		close(r.completeAfterRenewal)
		<-ctx.Done()
		return app.EmailJob{}, &store.StoreError{Code: store.StoreErrorCanceled, Err: ctx.Err()}
	}
	return app.EmailJob{}, r.renewErrors[c.JobID]
}

func TestJobDiagnosticNormalCompletionDoesNotInventCancellationFailure(t *testing.T) {
	output := captureJobDiagnostics(t)
	repo := &diagnosticJobRepository{MemoryStore: store.NewMemoryStore(), completeAfterRenewal: make(chan struct{})}
	s := &Service{repository: repo, now: time.Now, opts: Options{LeaseDuration: 30 * time.Millisecond, JobTimeout: time.Second}}
	s.execute(t.Context(), app.EmailJob{ID: strings.Repeat("d", 64), OwnerID: "owner", Kind: app.EmailJobParse, Attempt: 2})
	if len(jobDiagnosticRows(t, output.String())) != 0 {
		t.Fatalf("cleanup was logged as a business failure: %s", output.String())
	}
	if len(repo.finished) != 1 || repo.finished[0].ErrorCode != "" || !repo.finished[0].RetryAt.IsZero() {
		t.Fatalf("completion changed: %+v", repo.finished)
	}
}
func (r *diagnosticJobRepository) FinishEmailJob(_ context.Context, c store.EmailJobFinish) (app.EmailJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, c)
	return app.EmailJob{}, nil
}

func captureJobDiagnostics(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

func jobDiagnosticRows(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["msg"] == "email job failed" {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestJobDiagnosticRenewalFirstCauseAndConcurrentIdentity(t *testing.T) {
	output := captureJobDiagnostics(t)
	now := time.Now()
	repo := &diagnosticJobRepository{MemoryStore: store.NewMemoryStore(), block: true, renewErrors: map[string]error{
		strings.Repeat("a", 64): &store.StoreError{Code: store.StoreErrorUnavailable, Err: errors.New("owner@example.test secret payload")},
		strings.Repeat("b", 64): &store.StoreError{Code: store.StoreErrorConflict, Err: errors.New("https://private.example credential")},
	}}
	s := &Service{repository: repo, now: func() time.Time { return now }, opts: Options{LeaseDuration: 30 * time.Millisecond, JobTimeout: time.Second}}
	var work sync.WaitGroup
	for _, id := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		work.Add(1)
		go func() {
			defer work.Done()
			s.execute(t.Context(), app.EmailJob{ID: id, OwnerID: "private-owner@example.test", Kind: app.EmailJobParse, Attempt: 2, Generation: 3})
		}()
	}
	work.Wait()
	rows := jobDiagnosticRows(t, output.String())
	if len(rows) != 2 {
		t.Fatalf("want two independently attributed failures; got %s", output.String())
	}
	want := map[string]string{strings.Repeat("a", 64): "store_unavailable", strings.Repeat("b", 64): "store_conflict"}
	for _, row := range rows {
		id, _ := row["job_id"].(string)
		if row["cause_code"] != want[id] || row["stage"] != "lease_renewal" || row["cause_source"] != "store" || row["public_code"] != "canceled" || row["attempt"] != float64(2) || row["generation"] != float64(3) {
			t.Fatalf("first renewal failure was lost or misattributed: %#v", row)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("missing identities: %v", want)
	}
	for _, finish := range repo.finished {
		if finish.ErrorCode != "canceled" || !finish.RetryAt.Equal(now.Add(40*time.Second)) {
			t.Fatalf("diagnostics changed scheduling: %+v", finish)
		}
	}
	for _, private := range []string{"@", "secret", "payload", "https://", "credential"} {
		if strings.Contains(output.String(), private) {
			t.Fatalf("diagnostic leaked %q", private)
		}
	}
}

func TestJobDiagnosticDistinguishesContextAndInternalCauseWithoutPublicChanges(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		err                                error
		cancel, deadline                   bool
		cause, source, stage, public, kind string
	}{
		{name: "cancellation", cancel: true, cause: "canceled", source: "context", stage: "cancellation", public: "store_canceled"},
		{name: "deadline", deadline: true, cause: "timeout", source: "context", stage: "deadline", public: "timeout"},
		{name: "source", err: errors.New("email_source_integrity"), cause: "email_source_integrity", source: "source", stage: "parse", public: "email_processing_failed"},
		{name: "email_batch_journal_invalid", err: errors.New("email_batch_journal_invalid"), cause: "email_batch_journal_invalid", source: "source", stage: "parse", public: "email_processing_failed"},
		{name: "email_capture_identity", err: errors.New("email_capture_identity"), cause: "email_capture_identity", source: "source", stage: "parse", public: "email_processing_failed"},
		{name: "email_command_receipt_invalid", err: errors.New("email_command_receipt_invalid"), cause: "email_command_receipt_invalid", source: "source", stage: "parse", public: "email_processing_failed"},
		{name: "email_output_path_invalid", err: errors.New("email_output_path_invalid"), cause: "email_output_path_invalid", source: "source", stage: "parse", public: "email_processing_failed"},
		{name: "model", err: errors.New("email_model_output_limit"), cause: "email_model_output_limit", source: "model", stage: "analysis", public: "email_processing_failed", kind: app.EmailJobClassification},
		{name: "private", err: errors.New("owner@example.test /secret/file body subject bearer-token"), cause: "unclassified", source: "internal", stage: "parse", public: "email_processing_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureJobDiagnostics(t)
			now := time.Now()
			repo := &diagnosticJobRepository{MemoryStore: store.NewMemoryStore(), block: tc.cancel || tc.deadline, dispatchError: tc.err}
			s := &Service{repository: repo, now: func() time.Time { return now }, opts: Options{LeaseDuration: time.Minute, JobTimeout: time.Second}}
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			if tc.deadline {
				s.opts.JobTimeout = time.Millisecond
			}
			kind := tc.kind
			if kind == "" {
				kind = app.EmailJobParse
			}
			s.execute(parent, app.EmailJob{ID: strings.Repeat("c", 64), OwnerID: "owner", Kind: kind, Attempt: 2, Generation: 3})
			rows := jobDiagnosticRows(t, output.String())
			if len(rows) != 1 {
				t.Fatalf("missing failure diagnostic: %s", output.String())
			}
			row := rows[0]
			if row["cause_code"] != tc.cause || row["cause_source"] != tc.source || row["stage"] != tc.stage || row["public_code"] != tc.public || row["operation"] != kind {
				t.Fatalf("diagnostic=%#v", row)
			}
			if len(repo.finished) != 1 || repo.finished[0].ErrorCode != tc.public || !repo.finished[0].RetryAt.Equal(now.Add(40*time.Second)) {
				t.Fatalf("finish semantics changed: %+v", repo.finished)
			}
			for _, private := range []string{"@", "/secret", "body subject", "bearer-token"} {
				if strings.Contains(output.String(), private) {
					t.Fatalf("private value logged: %s", output.String())
				}
			}
		})
	}
}

func TestJobDiagnosticRejectsUntrustedTypedCodesAndIdentity(t *testing.T) {
	for _, failure := range []error{
		&store.StoreError{Code: "private-store-token", Err: errors.New("private detail")},
		&emailautomation.Error{Code: "private-provider-token", Message: "private detail"},
	} {
		output := captureJobDiagnostics(t)
		repo := &diagnosticJobRepository{MemoryStore: store.NewMemoryStore(), dispatchError: failure}
		s := &Service{repository: repo, now: time.Now, opts: Options{LeaseDuration: time.Minute, JobTimeout: time.Second}}
		s.execute(t.Context(), app.EmailJob{ID: "private-filename.json", OwnerID: "owner", Kind: app.EmailJobParse})
		rows := jobDiagnosticRows(t, output.String())
		if len(rows) != 1 || rows[0]["cause_code"] != "unclassified" || rows[0]["public_code"] != "unclassified" || rows[0]["job_id"] != "" || strings.Contains(output.String(), "private") {
			t.Fatalf("untrusted diagnostic values escaped: %s", output.String())
		}
		// Rejecting the diagnostic code must not rewrite the business result.
		if len(repo.finished) != 1 || repo.finished[0].ErrorCode != safeCode(failure) {
			t.Fatalf("business error changed: %+v", repo.finished)
		}
	}
}
