package emaildiagnostics

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSourcePostgresReadOnlyParity(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_DIAGNOSTICS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set isolated diagnostic PostgreSQL fixture DSN")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	schema := "diagnostic_source_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	if _, err = conn.Exec(ctx, "CREATE TABLE "+schema+`.email_management_records(owner_id text NOT NULL,kind text NOT NULL,id text NOT NULL,parent text NOT NULL,related text NOT NULL,state text NOT NULL,search_text text NOT NULL,sort_key text NOT NULL,payload jsonb NOT NULL, PRIMARY KEY(owner_id,kind,id))`); err != nil {
		t.Fatal(err)
	}
	for name, columns := range map[string]string{"email_management_parent": `owner_id,kind,parent,sort_key COLLATE "C" DESC,id COLLATE "C" DESC`, "email_management_order": `owner_id,kind,sort_key COLLATE "C" DESC,id COLLATE "C" DESC`} {
		if _, err = conn.Exec(ctx, "CREATE INDEX "+name+" ON "+schema+".email_management_records("+columns+")"); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range sourceFixture() {
		if _, err = conn.Exec(ctx, "INSERT INTO "+schema+`.email_management_records VALUES($1,$2,$3,$4,$5,$6,'private search','sort',$7)`, record.Owner, record.Kind, record.ID, record.Parent, record.Related, record.State, record.Data); err != nil {
			t.Fatal(err)
		}
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// The test connection is a writer solely for fixture setup. The reader's
	// role has SELECT only, and read-only is required at session and tx level.
	role := schema + "_reader"
	if _, err = conn.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD 'isolated-fixture-only'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP OWNED BY "+role)
		_, _ = conn.Exec(context.Background(), "DROP ROLE "+role)
	})
	if _, err = conn.Exec(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "GRANT SELECT ON "+schema+".email_management_records TO "+role); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "ALTER TABLE "+schema+".email_management_records ENABLE ROW LEVEL SECURITY"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "CREATE POLICY readonly_snapshot_guard ON "+schema+`.email_management_records FOR SELECT TO `+role+` USING(current_setting('transaction_read_only')='on' AND current_setting('transaction_isolation')='repeatable read')`); err != nil {
		t.Fatal(err)
	}
	config.User = role
	config.Password = "isolated-fixture-only"
	config.RuntimeParams["search_path"] = schema
	// ConnString preserves the original connection text, so construct a fixture
	// URL from the known local test configuration rather than relying on it.
	readerDSN := "host=" + config.Host + " port=" + jsonNumber(config.Port) + " dbname=" + config.Database + " user=" + role + " password=isolated-fixture-only sslmode=disable search_path=" + schema
	unguarded, err := pgx.Connect(ctx, readerDSN)
	if err != nil {
		t.Fatal(err)
	}
	var hidden int
	if err = unguarded.QueryRow(ctx, "SELECT COUNT(*) FROM email_management_records").Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	_ = unguarded.Close(ctx)
	if hidden != 0 {
		t.Fatal("fixture failed to enforce read-only repeatable-read transaction")
	}
	before := pgFixtureRows(t, conn, schema)
	path := writeSourceFixture(t, sourceFixture(), "")
	for _, q := range []Selection{{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, {Owner: "owner-a", Kind: "operation", ID: "operation-a", IdentityKind: "invocation"}, {Owner: "owner-a", Kind: "operation", ID: "collect-a", IdentityKind: "invocation"}, {Owner: "owner-a", Kind: "job", ID: "job-a"}} {
		got := ReadPostgres(ctx, readerDSN, q, ReadLimits{})
		want := ReadSnapshot(ctx, path, q, ReadLimits{})
		if !reflect.DeepEqual(got.Records, want.Records) || !reflect.DeepEqual(got.Gaps, want.Gaps) {
			t.Fatalf("PG/file mismatch: got %+v want %+v", got, want)
		}
		if got.Bytes == 0 || got.Rows == 0 {
			t.Fatalf("missing counters: %+v", got)
		}
	}
	for _, tc := range []struct {
		limits ReadLimits
		gap    string
	}{{ReadLimits{Bytes: 32}, "source_byte_limit"}, {ReadLimits{RecordBytes: 128}, "source_record_limit"}, {ReadLimits{Rows: 1}, "source_row_limit"}} {
		got := ReadPostgres(ctx, readerDSN, Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, tc.limits)
		if !slices.Contains(got.Gaps, tc.gap) {
			t.Fatalf("bound %+v: %+v", tc.limits, got)
		}
	}
	if after := pgFixtureRows(t, conn, schema); before != after {
		t.Fatal("PostgreSQL source rows changed")
	}
	for _, field := range []string{"provider_message_id", "provider_thread_id"} {
		payload := `{"receipt":{"status":"not_sent","` + field + `":"secret provider id"}}`
		if _, err = conn.Exec(ctx, "INSERT INTO "+schema+`.email_management_records VALUES('owner-a','draft',$1,'','','','','',$2)`, field, payload); err != nil {
			t.Fatal(err)
		}
		got := ReadPostgres(ctx, readerDSN, Selection{Owner: "owner-a", Kind: "draft", ID: field}, ReadLimits{})
		if len(got.Records) != 0 || !slices.Contains(got.Gaps, "source_record_invalid") {
			t.Fatalf("PG conflict not exposed: %+v", got)
		}
	}
	if _, err = conn.Exec(ctx, "DROP INDEX "+schema+".email_management_order"); err != nil {
		t.Fatal(err)
	}
	noIndex := ReadPostgres(ctx, readerDSN, Selection{Owner: "owner-a", Kind: "operation", ID: "operation-a", IdentityKind: "invocation"}, ReadLimits{})
	if !slices.Contains(noIndex.Gaps, "source_schema_unsupported") {
		t.Fatalf("missing bounded-scan index not diagnosed: %+v", noIndex)
	}
	if _, err = conn.Exec(ctx, "DROP TABLE "+schema+".email_management_records"); err != nil {
		t.Fatal(err)
	}
	got := ReadPostgres(ctx, readerDSN, Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, ReadLimits{})
	if !slices.Contains(got.Gaps, "source_schema_unsupported") {
		t.Fatalf("missing schema not diagnosed: %+v", got)
	}
}
func jsonNumber(n uint16) string { b, _ := json.Marshal(n); return string(b) }
func pgFixtureRows(t *testing.T, conn *pgx.Conn, schema string) string {
	t.Helper()
	var out string
	if err := conn.QueryRow(t.Context(), "SELECT jsonb_agg(to_jsonb(t) ORDER BY owner_id,kind,id)::text FROM "+schema+".email_management_records t").Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
