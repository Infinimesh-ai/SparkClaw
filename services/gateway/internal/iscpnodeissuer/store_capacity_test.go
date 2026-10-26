package iscpnodeissuer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	control "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpcontrol"
	crypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/jackc/pgx/v5"
)

// Make valid signed completed issuances, not an oversized request or payload.
func largeIssuanceHistory(t *testing.T) (*fixture, control.Request, *State) {
	t.Helper()
	ctx := context.Background()
	f, r := controlFixture(t, NewMemoryStore())
	out, e := f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil {
		t.Fatal(e)
	}
	st, e := f.store.Read(ctx, f.credential.InvitationID)
	if e != nil {
		t.Fatal(e)
	}
	original := st.Control.Records[r.Key.StorageKey()]
	grant := *out.Result.Payload.Body.Grant
	for n := 1; n <= 1300; n++ {
		intent := original.Intent
		intent.OperationID = fmt.Sprintf("operation-history-capacity-%04d", n)
		intent.Preconditions.CredentialGeneration = control.Number(uint64(n))
		key := original.Key
		key.OperationID = intent.OperationID
		digest, _ := control.Digest("ISCP-OPERATION-INTENT-V2\x00", intent)
		gen := control.Number(uint64(n + 1))
		res, e := control.NewResult(control.ResultPayload{Key: key, SemanticDigest: digest, ResultVersion: "1", State: "succeeded", CompletedAt: time.Now().UTC().Truncate(time.Second), CredentialGeneration: gen, Body: control.ResultBody{Grant: &grant}}, f.cfg.Issuer)
		if e != nil {
			t.Fatal(e)
		}
		ack := control.Ack{ResultDigest: res.ResultDigest, Disposition: "installed", CredentialGeneration: gen, RecordedAt: res.Payload.CompletedAt}
		d, e := control.NewDisposition(key, digest, ack, ack.RecordedAt, f.cfg.Issuer)
		if e != nil {
			t.Fatal(e)
		}
		st.Control.Records[key.StorageKey()] = &control.Record{Key: key, Intent: intent, SemanticDigest: digest, State: "succeeded", Version: "2", Generation: gen, Result: &res, Disposition: &d, CreatedAt: ack.RecordedAt}
		st.Control.Parents[key.StorageKey()] = &ControlParent{Grant: &grant, ChildIndex: "0"}
	}
	delete(st.Control.Records, r.Key.StorageKey())
	delete(st.Control.Parents, r.Key.StorageKey())
	st.Control.Generation = "1301"
	raw, _ := json.Marshal(st)
	if len(raw) <= 4<<20 {
		t.Fatalf("history must exceed old 4 MiB ceiling: %d", len(raw))
	}
	return f, r, st
}
func TestIssuerStateSealAcceptsAccumulatedHistoryAboveFourMiB(t *testing.T) {
	_, _, st := largeIssuanceHistory(t)
	s := &PostgresStore{key: bytes.Repeat([]byte{17}, 32)}
	encrypted, e := s.seal(st.Credential.InvitationID, st)
	if e != nil {
		t.Fatalf("existing signed history cannot be persisted: %v", e)
	}
	if len(encrypted) <= 4<<20 {
		t.Fatal("test did not cross original ciphertext ceiling")
	}
}
func TestIssuerStateOpenReadsExistingHistoryAboveFourMiB(t *testing.T) {
	_, _, st := largeIssuanceHistory(t)
	key := bytes.Repeat([]byte{19}, 32)
	raw, _ := json.Marshal(st)
	nonce := bytes.Repeat([]byte{1}, 12)
	encrypted, e := crypto.NewProvider().Seal(key, nonce, raw, []byte("sparkclaw/iscp-node/storage/v1\x00"+st.Credential.InvitationID))
	if e != nil {
		t.Fatal(e)
	}
	s := &PostgresStore{key: key}
	got, e := s.open(st.Credential.InvitationID, append(nonce, encrypted...))
	if e != nil {
		t.Fatalf("valid existing history cannot be opened: %v", e)
	}
	if len(got.Control.Records) != 1300 || got.Control.Generation != "1301" {
		t.Fatal("history changed on read")
	}
	if _, e = s.open(st.Credential.InvitationID, make([]byte, 27)); e == nil {
		t.Fatal("truncated ciphertext accepted")
	}
}
func TestIssuerControlAdmissionPast1024RetainsHistoryAndReplayIdentity(t *testing.T) {
	ctx := context.Background()
	f, r, st := largeIssuanceHistory(t)
	if e := f.store.Update(ctx, st.Credential.InvitationID, func(s *State) error { *s = *st; return nil }); e != nil {
		t.Fatal(e)
	}
	intent := *r.Intent
	intent.OperationID = "operation-history-capacity-successor"
	intent.Preconditions.CredentialGeneration = st.Control.Generation
	r.Key.OperationID = intent.OperationID
	r.Intent = &intent
	r.SemanticDigest, _ = control.Digest("ISCP-OPERATION-INTENT-V2\x00", intent)
	out, e := f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil || out.Result == nil {
		t.Fatalf("count cap blocked authorized next issuance: %v", e)
	}
	again, e := f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil || again.Result == nil || again.Result.ResultDigest != out.Result.ResultDigest {
		t.Fatal("replay issued a different result")
	}
	got, _ := f.store.Read(ctx, st.Credential.InvitationID)
	if len(got.Control.Records) != 1301 || len(got.Control.Parents) != 1301 || got.Control.Generation != "1302" {
		t.Fatal("automatic pruning or duplicate issuance occurred")
	}
}

func TestIssuerPostgresRemovesLegacyHistoryCeiling(t *testing.T) {
	dsn := os.Getenv("SC_ISCP_NODE_TEST_DSN")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	ctx := context.Background()
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	database := "issuer_capacity_" + fmt.Sprintf("%x", crypto.RandomBytes(8))
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)")
	cfg.Database = database
	isolatedURL, e := url.Parse(dsn)
	if e != nil || (isolatedURL.Scheme != "postgres" && isolatedURL.Scheme != "postgresql") {
		t.Fatal("test DSN must be a PostgreSQL URL")
	}
	isolatedURL.Path = "/" + database
	isolatedDSN := isolatedURL.String()
	db, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close(ctx)
	_, e = db.Exec(ctx, `CREATE TABLE sparkclaw_iscp_issuer_state (id text PRIMARY KEY,ciphertext bytea NOT NULL,updated_at timestamptz NOT NULL DEFAULT now(),CONSTRAINT bounded_id CHECK(length(id)<=200),CONSTRAINT bounded_ciphertext CHECK(octet_length(ciphertext)<=4194304))`)
	if e != nil {
		t.Fatal(e)
	}
	f, _, st := largeIssuanceHistory(t)
	key := crypto.RandomBytes(32)
	store, e := NewPostgresStore(ctx, isolatedDSN, key)
	if e != nil {
		t.Fatal(e)
	}
	var actualDatabase string
	if e = store.pool.QueryRow(ctx, "SELECT current_database()").Scan(&actualDatabase); e != nil || actualDatabase != database {
		t.Fatalf("migration used the wrong database: got %q, want %q: %v", actualDatabase, database, e)
	}
	var ceilings int
	if e = store.pool.QueryRow(ctx, "SELECT count(*) FROM pg_constraint WHERE conrelid='sparkclaw_iscp_issuer_state'::regclass AND conname='bounded_ciphertext'").Scan(&ceilings); e != nil || ceilings != 0 {
		t.Fatalf("legacy constraint remains: %v", e)
	}
	if e = store.Create(ctx, f.credential.InvitationID, st); e != nil {
		t.Fatalf("existing database still rejects signed history: %v", e)
	}
	store.Close()
	// Startup migration is idempotent; all bytes and generations survive restart.
	store, e = NewPostgresStore(ctx, isolatedDSN, key)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	got, e := store.Read(ctx, f.credential.InvitationID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(st)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		t.Fatal("history changed across PostgreSQL restart")
	}
	if e = store.ActivateControlWriter(ctx, "1", crypto.Base64URL(crypto.RandomBytes(32))); e != nil {
		t.Fatal(e)
	}
	if e = store.Update(ctx, f.credential.InvitationID, func(*State) error { return nil }); e != ErrWriterFenced {
		t.Fatalf("unconfigured writer bypassed fence: %v", e)
	}
	if e = store.ConfigureControlWriter(ctx, "1"); e != nil {
		t.Fatal(e)
	}
	sentinel := fmt.Errorf("rollback sentinel")
	if e = store.Update(ctx, f.credential.InvitationID, func(s *State) error { s.Control.Generation = "9999"; return sentinel }); e != sentinel {
		t.Fatal(e)
	}
	got, e = store.Read(ctx, f.credential.InvitationID)
	if e != nil || got.Control.Generation != st.Control.Generation {
		t.Fatal("failed update was persisted")
	}
}
