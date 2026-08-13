package iscppairing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/provisioning"
)

func TestHTTPAuthorityAndServiceIssueCopyOnceTicket(t *testing.T) {
	now := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	var received AuthorityRequest
	authorityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer authority-secret" || r.Header.Get("Idempotency-Key") == "" {
			t.Fatalf("unexpected authority request: method=%s headers=%v", r.Method, r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authority_ref": "authority-ref-1",
			"ticket": provisioning.PairingTicket{
				Type: provisioning.TypePairingTicket, TicketID: "iscp-ticket-1", DomainID: "domain-a",
				RelayID: "relay-a", TrustRootID: "root-a", MaxUses: 1, IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute),
				Signature: identity.Signature{Alg: "Ed25519", KID: "root-key-a", Value: "signed-ticket-value"},
			},
		})
	}))
	defer authorityServer.Close()
	t.Setenv("ISCP_AUTHORITY_TEST_TOKEN", "authority-secret")
	authority, err := NewHTTPAuthority(HTTPAuthorityOptions{
		Endpoint: authorityServer.URL, TokenEnv: "ISCP_AUTHORITY_TEST_TOKEN", Timeout: time.Second, ResponseMaxBytes: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemoryStore()
	service := New(st, Options{
		Enabled: true, DomainID: "domain-a", AuthorityHost: "authority.test",
		ExpectedTicketType: provisioning.TypePairingTicket, DefaultTTL: 10 * time.Minute, Authority: authority,
	})
	issued, err := service.Start(t.Context(), app.DefaultOwnerID, "owner-actor", StartRequest{DisplayName: "LocalMind gateway"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if received.Type != AuthorityRequestType || received.DomainID != "domain-a" || received.MaxUses != 1 || received.RequestRef != issued.Onboarding.ID {
		t.Fatalf("invalid authority request: %#v", received)
	}
	if issued.Ticket.Signature.Value != "signed-ticket-value" || issued.Onboarding.TicketID != issued.Ticket.TicketID {
		t.Fatalf("invalid issued pairing: %#v", issued)
	}
	snapshot, _ := json.Marshal(st.ListISCPOnboardings(app.DefaultOwnerID))
	if strings.Contains(string(snapshot), "signed-ticket-value") || strings.Contains(string(snapshot), `"signature"`) {
		t.Fatalf("persisted onboarding leaked the Pairing Ticket: %s", snapshot)
	}
	audits := st.ListAudit("")
	auditJSON, _ := json.Marshal(audits)
	if strings.Contains(string(auditJSON), "signed-ticket-value") {
		t.Fatalf("audit leaked the Pairing Ticket: %s", auditJSON)
	}
}

func TestPairingServiceRejectsAuthorityContractViolations(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name   string
		mutate func(*provisioning.PairingTicket)
	}{
		{name: "domain", mutate: func(ticket *provisioning.PairingTicket) { ticket.DomainID = "other" }},
		{name: "reusable", mutate: func(ticket *provisioning.PairingTicket) { ticket.MaxUses = 2 }},
		{name: "unsigned", mutate: func(ticket *provisioning.PairingTicket) { ticket.Signature = identity.Signature{} }},
		{name: "long lived", mutate: func(ticket *provisioning.PairingTicket) { ticket.ExpiresAt = ticket.IssuedAt.Add(time.Hour) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ticket := validTicket(now)
			test.mutate(&ticket)
			service := New(store.NewMemoryStore(), Options{Enabled: true, DomainID: "domain-a", Authority: staticAuthority{result: AuthorityResult{AuthorityRef: "ref", Ticket: ticket}}})
			if _, err := service.Start(context.Background(), "owner", "owner", StartRequest{DisplayName: "gateway"}, now); err == nil {
				t.Fatal("invalid authority ticket was accepted")
			}
		})
	}
}

func TestHTTPAuthorityRejectsUnboundedAndErrorResponses(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{name: "oversized", body: strings.Repeat("x", 2048), status: http.StatusOK},
		{name: "server error", body: `{"secret":"must not surface"}`, status: http.StatusUnauthorized},
		{name: "unknown field", body: `{"authority_ref":"ref","ticket":{},"extra":true}`, status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			t.Setenv("ISCP_AUTHORITY_ERROR_TOKEN", "token")
			authority, err := NewHTTPAuthority(HTTPAuthorityOptions{Endpoint: server.URL, TokenEnv: "ISCP_AUTHORITY_ERROR_TOKEN", Timeout: time.Second, ResponseMaxBytes: 1024})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := authority.IssuePairingTicket(t.Context(), AuthorityRequest{RequestRef: "request"}); err == nil || strings.Contains(err.Error(), "must not surface") {
				t.Fatalf("unsafe authority response was accepted or leaked: %v", err)
			}
		})
	}
}

func TestHTTPAuthorityTokenFileIsRegularPrivateAndBounded(t *testing.T) {
	newAuthority := func(t *testing.T, path string) *HTTPAuthority {
		t.Helper()
		authority, err := NewHTTPAuthority(HTTPAuthorityOptions{
			Endpoint: "https://authority.test/pairing", TokenFile: path, Timeout: time.Second, ResponseMaxBytes: 1024,
		})
		if err != nil {
			t.Fatal(err)
		}
		return authority
	}

	t.Run("private regular file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(path, []byte(" authority-secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if token, err := newAuthority(t, path).token(); err != nil || token != "authority-secret" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		if _, err := newAuthority(t, t.TempDir()).token(); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("directory token source error = %v", err)
		}
	})

	t.Run("symbolic link", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		link := filepath.Join(dir, "token")
		if err := os.WriteFile(target, []byte("authority-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := newAuthority(t, link).token(); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("symbolic-link token source error = %v", err)
		}
	})

	t.Run("group readable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(path, []byte("authority-secret"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := newAuthority(t, path).token(); err == nil || !strings.Contains(err.Error(), "group or others") {
			t.Fatalf("permissive token source error = %v", err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(path, []byte(strings.Repeat("x", int(maxAuthorityTokenFileBytes+1))), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := newAuthority(t, path).token(); err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("oversized token source error = %v", err)
		}
	})
}

type staticAuthority struct{ result AuthorityResult }

func (a staticAuthority) Ready(context.Context) error { return nil }
func (a staticAuthority) IssuePairingTicket(context.Context, AuthorityRequest) (AuthorityResult, error) {
	return a.result, nil
}

func validTicket(now time.Time) provisioning.PairingTicket {
	return provisioning.PairingTicket{
		Type: provisioning.TypePairingTicket, TicketID: "ticket", DomainID: "domain-a", RelayID: "relay", TrustRootID: "root", MaxUses: 1,
		IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute), Signature: identity.Signature{Alg: "Ed25519", KID: "key", Value: "signature"},
	}
}
