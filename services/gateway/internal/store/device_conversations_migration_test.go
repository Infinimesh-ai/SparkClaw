package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration 0024 deletes every conversation that predates device ownership
// with the owner-deletion closure and keeps the MCP binding sessions
// (docs/client-link-layer-design.md 4.4).
func TestPostgresMigrationDeletesLegacyConversations(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	ctx := context.Background()
	migrations, err := loadPostgresMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []postgresMigration
	for _, m := range migrations {
		if m.Filename < "0024_device_conversations.sql" {
			before = append(before, m)
		}
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := runPostgresMigrationsWith(ctx, pool, before, postgresMigrationHooks{}); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO sessions (id,owner_id,title,source,hidden) VALUES ('s-web','owner','web','webchat',false)`,
		`INSERT INTO sessions (id,owner_id,title,source,hidden) VALUES ('s-chat','owner','chat','weixin',true)`,
		`INSERT INTO sessions (id,owner_id,title,source,hidden) VALUES ('s-mcp','owner','mcp','mcp',true)`,
		`INSERT INTO messages (id,session_id,role,content) VALUES ('m-web','s-web','user','hello')`,
		`INSERT INTO messages (id,session_id,role,content) VALUES ('m-mcp','s-mcp','user','kept')`,
		`INSERT INTO weixin_chat_sessions (id,owner_id,binding_id,linked_session_id,status) VALUES ('wx-1','owner','bind','s-chat','active')`,
		`INSERT INTO events (id,happened_at,type,session_id,payload) VALUES ('e-web',now(),'session.created','s-web','{}')`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			pool.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	pool.Close()

	st, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	count := func(query string) int {
		t.Helper()
		var n int
		if err := st.db.QueryRow(ctx, query).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM sessions WHERE id IN ('s-web','s-chat')`); n != 0 {
		t.Fatalf("legacy conversations left: %d", n)
	}
	if n := count(`SELECT count(*) FROM messages WHERE session_id='s-web'`) + count(`SELECT count(*) FROM events WHERE session_id='s-web'`) + count(`SELECT count(*) FROM weixin_chat_sessions`); n != 0 {
		t.Fatalf("legacy conversation records left: %d", n)
	}
	if n := count(`SELECT count(*) FROM sessions WHERE id='s-mcp'`) + count(`SELECT count(*) FROM messages WHERE session_id='s-mcp'`); n != 2 {
		t.Fatalf("MCP binding session not kept: %d", n)
	}
	if n := count(`SELECT count(*) FROM session_tombstones`); n != 0 {
		t.Fatalf("legacy tombstones left: %d", n)
	}

	created, err := st.CreateDeviceSession(ctx, "phone", "owner", "", "dev-phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(ctx, `UPDATE sessions SET origin_device_id='dev-other' WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	header, ok, err := st.SessionHeader(ctx, created.ID)
	if err != nil || !ok || header.OriginDeviceID != "dev-phone" {
		t.Fatalf("ownership changed: %+v %v", header, err)
	}
}
