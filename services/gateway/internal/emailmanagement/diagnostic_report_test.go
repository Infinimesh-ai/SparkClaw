package emailmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// This runs the actual standalone command against a reopened product fixture.
// No diagnostic entry has a Service, Browser, Store constructor or mutation API.
func TestReadOnlyReportPreservesRejectedACKAndUnknownAfterRestart(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	repo, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	bind := func(owner, provider string) app.EmailMailbox {
		box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: owner, CommandKey: "bind"}, Provider: provider, Address: "private@example.test", Boundary: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return box
	}
	box := bind("owner-a", app.EmailProviderQQMail)
	browser := &rejectingComposeBrowser{lifecycleComposeBrowser: &lifecycleComposeBrowser{composeFixture: &composeFixture{intakeFixture: &intakeFixture{}}, state: browsercontrol.EpochState{Generation: 1, OpenEpoch: 4}, ackError: errors.New("ACK unavailable")}, code: -21101, reason: "PROVIDER_PARAMETER_REJECTED"}
	s := &Service{repository: repo, browser: browser}
	d, err := s.SaveDraft(t.Context(), "owner-a", store.EmailDraft{ID: "same-private-draft", MailboxID: box.ID, To: []string{"recipient@example.test"}, Subject: "private-subject", Body: "private-body"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.SendDraft(t.Context(), "owner-a", d.ID, d.Version, "rejected-key")
	if err != nil || d.State != "failed" || d.Receipt.Lifecycle.Acknowledged {
		t.Fatalf("rejection fixture: %s %v", d.State, err)
	}
	otherBox := bind("owner-b", app.EmailProviderGmail)
	unknownBrowser := &composeFixture{intakeFixture: &intakeFixture{}, address: "private@example.test", resultError: errors.New("transport lost")}
	other := &Service{repository: repo, browser: unknownBrowser}
	u, err := other.SaveDraft(t.Context(), "owner-b", store.EmailDraft{ID: d.ID, MailboxID: otherBox.ID, To: []string{"other@example.test"}, Subject: "other-secret", Body: "other-body"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	u, err = other.SendDraft(t.Context(), "owner-b", u.ID, u.Version, "unknown-key")
	if err != nil || u.State != "unknown" {
		t.Fatalf("unknown fixture: %s %v", u.State, err)
	}
	reopened, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "diagnose")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/sparkclaw-email-diagnose")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("read-only report command unavailable: %v\n%s", err, out)
	}
	for range 2 {
		for _, tc := range []struct{ owner, state, ack, recovery string }{{"owner-a", "failed", "pending", "existing_maintenance_ack_eligible"}, {"owner-b", "unknown", "incomplete", "inspect_without_resend"}} {
			cmd := exec.CommandContext(ctx, binary, "--owner", tc.owner, "--draft", d.ID, "--snapshot", state, "--format", "json")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("report failed: %v", err)
			}
			var report struct {
				Attempts []struct {
					Business struct {
						State string `json:"state"`
					} `json:"business"`
					ACK      string `json:"ack"`
					Recovery string `json:"recovery"`
				} `json:"attempts"`
			}
			if err := json.Unmarshal(out, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Attempts) != 1 || report.Attempts[0].Business.State != tc.state || report.Attempts[0].ACK != tc.ack || report.Attempts[0].Recovery != tc.recovery {
				t.Fatalf("wrong owner/result: %s", out)
			}
			for _, private := range []string{"@example.test", "private-subject", "private-body", "other-secret", "other-body", d.ID, "rejected-key", "unknown-key", state} {
				if bytes.Contains(out, []byte(private)) {
					t.Fatalf("report disclosed %q", private)
				}
			}
		}
	}
	after, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || info.Mode() != afterInfo.Mode() || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("diagnosis changed source snapshot")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("diagnosis created source files: %v %v", entries, err)
	}
	if browser.sends != 1 || len(browser.acks) != 0 || unknownBrowser.sends != 1 {
		t.Fatal("diagnosis executed business actions")
	}
	other.repository = reopened
	if _, err := other.SendDraft(t.Context(), "owner-b", u.ID, u.Version, "unknown-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.SendDraft(t.Context(), "owner-b", u.ID, u.Version, "new-key"); err == nil {
		t.Fatal("diagnosis removed unknown fence")
	}
	if unknownBrowser.sends != 1 {
		t.Fatal("unknown resent")
	}
	rows, err := reopened.ListEmailDrafts(t.Context(), "owner-a", d.ID)
	if err != nil || len(rows) != 1 || rows[0].Receipt.Lifecycle.Acknowledged || !strings.EqualFold(rows[0].State, "failed") {
		t.Fatal("diagnosis acknowledged durable rejection")
	}
}
