package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type attributionRepository struct {
	Repository
	commits []store.EmailSyncCommitCommand
}

func (r *attributionRepository) CommitEmailSync(ctx context.Context, c store.EmailSyncCommitCommand) (store.EmailSyncCommitResult, error) {
	r.commits = append(r.commits, c)
	return r.Repository.CommitEmailSync(ctx, c)
}

type attributionBrowser struct {
	*splitRangeFixture
	failAt             int
	admissionFailure   bool
	admissionFailureAt int
	badContract        bool
}

func (b *attributionBrowser) AdmitIntake(ctx context.Context, owner, provider string) (app.EmailAdmissionBinding, error) {
	if b.admissionFailure || b.admissionFailureAt > 0 && len(b.calls)+1 == b.admissionFailureAt {
		return app.EmailAdmissionBinding{}, errors.New("email_account_unverified")
	}
	return b.intakeFixture.AdmitIntake(ctx, owner, provider)
}
func (b *attributionBrowser) CollectPageForOwner(ctx context.Context, owner string, request app.EmailReadRequest) (app.EmailPageResult, error) {
	if len(b.calls)+1 == b.failAt {
		b.calls = append(b.calls, request)
		if b.badContract {
			return app.EmailPageResult{AccountAddress: "other@example.test"}, nil
		}
		return app.EmailPageResult{}, errors.New("email_source_missing")
	}
	return b.splitRangeFixture.CollectPageForOwner(ctx, owner, request)
}
func TestIntakeFailureKeepsOnlyActualFailedInvocation(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		for _, scenario := range []string{"before_admission", "collection", "split", "split_admission", "split_contract"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				output := captureJobDiagnostics(t)
				var base Repository = store.NewMemoryStore()
				if backend == "file" {
					file, err := store.NewFileStore(filepath.Join(t.TempDir(), "state.json"))
					if err != nil {
						t.Fatal(err)
					}
					base = file
				}
				repo := &attributionRepository{Repository: base}
				s, intake, _ := newFixtureService(t, repo)
				b := &attributionBrowser{splitRangeFixture: &splitRangeFixture{intakeFixture: intake}, failAt: 1}
				s.browser = b
				now := time.Now().UTC().Truncate(time.Microsecond)
				s.now = func() time.Time { return now }
				box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "before_admission" {
					b.admissionFailure = true
				}
				if strings.HasPrefix(scenario, "split") {
					b.failAt = 4
					if scenario == "split_admission" {
						b.admissionFailureAt = 4
					}
					if scenario == "split_contract" {
						b.badContract = true
					}
				}
				if err := s.plan(t.Context()); err != nil {
					t.Fatal(err)
				}
				now = now.Add(time.Second)
				if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
					t.Fatal(worked, err)
				}
				if strings.HasPrefix(scenario, "split") {
					now = now.Add(time.Minute)
					if _, err := s.Sync(t.Context(), "email-owner", box.ID); err != nil {
						t.Fatal(err)
					}
					if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
						t.Fatal(worked, err)
					}
				}
				var failed *store.EmailSyncCommitCommand
				for i := range repo.commits {
					if repo.commits[i].ErrorCode != "" {
						failed = &repo.commits[i]
					}
				}
				if failed == nil {
					t.Fatalf("no failure persisted: %+v", repo.commits)
				}
				want := ""
				if scenario != "before_admission" && scenario != "split_admission" {
					want = b.calls[len(b.calls)-1].InvocationID
					if want == "" {
						t.Fatal("fixture did not invoke")
					}
				}
				wantStage := map[string]string{"before_admission": "admission", "collection": "collect", "split": "split_collect", "split_admission": "split_admission", "split_contract": "split_page_contract"}[scenario]
				var stages []string
				for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
					var row map[string]any
					if json.Unmarshal([]byte(line), &row) == nil && row["msg"] == "email collection failed" {
						stages = append(stages, row["stage"].(string))
					}
				}
				if len(stages) != 1 || stages[0] != wantStage {
					t.Fatalf("stages=%v want=%s", stages, wantStage)
				}
				if failed.InvocationID != want {
					t.Fatalf("failure invocation=%q want actual=%q", failed.InvocationID, want)
				}
				if scenario == "before_admission" && len(b.calls) != 0 {
					t.Fatal("admission failure created an invocation")
				}
				if (scenario == "split" || scenario == "split_contract") && (len(b.calls) != 4 || failed.InvocationID == b.calls[1].InvocationID || failed.InvocationID == b.calls[2].InvocationID) {
					t.Fatal("split failure attributed to a successful query")
				}
			})
		}
	}
}
