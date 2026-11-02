package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"os"
	"path/filepath"
	"testing"
)

func completionFixture() app.Approval {
	return app.Approval{ID: app.NewID("approval"), Tool: "notify.ask_approval", ArgumentsImmutable: true, Arguments: map[string]any{"draft": "frozen"}, InputForm: &app.ApprovalInputForm{SchemaVersion: 1, Entries: []app.ApprovalInputEntry{{ID: "entry_1", Label: "Medicine", Fields: []app.ApprovalInputField{{Name: "expiry", Label: "Expiry", Type: app.ApprovalInputDateOrNone, Required: true}}}}}}
}
func runApprovalInputContract(t *testing.T, repo ApprovalRepository) app.Approval {
	t.Helper()
	ctx := t.Context()
	pending, err := repo.SaveApproval(ctx, completionFixture())
	if err != nil {
		t.Fatal(err)
	}
	if pending.InputFormDigest == "" {
		t.Fatal("missing draft digest")
	}
	if _, err := repo.ResolveApproval(ctx, pending.ID, app.ApprovalStatusApproved, ""); err == nil {
		t.Fatal("legacy approve bypassed form")
	}
	invalid := app.ApprovalInputSubmission{Digest: pending.InputFormDigest, SchemaVersion: 1, Inputs: app.ApprovalInputValues{"entry_1": {"expiry": "2027-02-31"}}}
	if _, err := repo.ResolveApprovalWithInputs(ctx, pending.ID, app.ApprovalStatusApproved, "", invalid); err == nil {
		t.Fatal("invalid date approved")
	}
	still, _, _ := repo.GetApproval(ctx, pending.ID)
	if still.Status != app.ApprovalStatusPending || still.InputsDigest != "" {
		t.Fatal("invalid input changed state")
	}
	input := invalid
	input.Inputs = app.ApprovalInputValues{"entry_1": {"expiry": "2027-05-31"}}
	approved, err := repo.ResolveApprovalWithInputs(ctx, pending.ID, app.ApprovalStatusApproved, "", input)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != app.ApprovalStatusApproved || approved.InputsDigest == "" || approved.InputValues["entry_1"]["expiry"] != "2027-05-31" {
		t.Fatal(approved)
	}
	if _, err := repo.ResolveApprovalWithInputs(ctx, pending.ID, app.ApprovalStatusApproved, "", input); err != nil {
		t.Fatal("same completion did not reconcile", err)
	}
	input.Inputs["entry_1"]["expiry"] = "no_expiry"
	if _, err := repo.ResolveApprovalWithInputs(ctx, pending.ID, app.ApprovalStatusApproved, "", input); err == nil {
		t.Fatal("different completion replay accepted")
	}
	stored, _, _ := repo.GetApproval(ctx, pending.ID)
	if stored.InputValues["entry_1"]["expiry"] != "2027-05-31" {
		t.Fatal("caller mutated stored consent")
	}
	return approved
}
func TestApprovalInputsAtomicMemoryAndFile(t *testing.T) {
	t.Run("memory", func(t *testing.T) { runApprovalInputContract(t, NewMemoryStore()) })
	t.Run("file reopen", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "state.json")
		repo, err := NewFileStore(file)
		if err != nil {
			t.Fatal(err)
		}
		approved := runApprovalInputContract(t, repo)
		reopened, err := NewFileStore(file)
		if err != nil {
			t.Fatal(err)
		}
		stored, found, err := reopened.GetApproval(t.Context(), approved.ID)
		if err != nil || !found || stored.InputsDigest != approved.InputsDigest || stored.InputValues["entry_1"]["expiry"] != "2027-05-31" {
			t.Fatal(stored, err)
		}
	})
}
func TestPostgresApprovalInputsAtomicContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	repo, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	runApprovalInputContract(t, repo)
}
