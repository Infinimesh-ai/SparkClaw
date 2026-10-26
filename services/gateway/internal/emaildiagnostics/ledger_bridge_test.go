package emaildiagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedLedgerReaderUsesGatewayOwnerDigestWithoutMutatingUnknown(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("production ledger reads require the qualified macOS write guard")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "ledger.sqlite")
	owner := "gateway-owner"
	key := "op3.1.4." + strings.Repeat("a", 32)
	payload, _ := json.Marshal(map[string]string{"file": file, "scope": digestString(owner), "key": key, "marker": filepath.Join(dir, "authority.json")})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	script := `import fs from 'node:fs';import {DatabaseSync} from 'node:sqlite';
const p=JSON.parse(process.argv[1]),db=new DatabaseSync(p.file);
db.exec("CREATE TABLE metadata(key TEXT PRIMARY KEY,value INTEGER NOT NULL);CREATE TABLE tasks(id TEXT PRIMARY KEY,principal TEXT NOT NULL,owner TEXT NOT NULL,request_key TEXT NOT NULL,app TEXT NOT NULL,command TEXT NOT NULL,request TEXT NOT NULL,status TEXT NOT NULL,reason TEXT,effect INTEGER NOT NULL,attempt INTEGER NOT NULL,dispatch TEXT,terminal_revision INTEGER NOT NULL,result_digest TEXT,ack TEXT,sequence INTEGER NOT NULL,UNIQUE(principal,owner,request_key));CREATE TABLE events(task TEXT NOT NULL,sequence INTEGER NOT NULL,type TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(task,sequence));");
for(const [k,v] of Object.entries({schema_version:2,epoch:4,commit_sequence:3,generation:1,open_epoch:4,sealed_through:2,authorization_generation:1}))db.prepare('INSERT INTO metadata VALUES(?,?)').run(k,v);
db.prepare('INSERT INTO tasks VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)').run('11111111-1111-4111-8111-111111111111','product-owner','ledger-owner',p.key,'mail-gmail','collect_page',JSON.stringify({app:'mail-gmail',command:'collect_page',request_key:p.key,arguments:{owner_scope:p.scope,body:'private-body',recipient:'private@example.test'}}),'uncertain','EMAIL_NETWORK_READ_FAILED',1,1,'released',1,'a'.repeat(64),null,0);
db.close();fs.chmodSync(p.file,0o600);fs.writeFileSync(p.marker,JSON.stringify({schema_version:2,epoch:4,commit_sequence:3}),{mode:0o600});`
	cmd := exec.CommandContext(ctx, node, "--no-warnings", "--input-type=module", "--eval", script, string(payload))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated SQLite fixture: %v %s", err, out)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, queryOwner := range []string{owner, "different-owner", owner} {
		r, err := Build(ctx, Options{Owner: queryOwner, Operation: key, IdentityKind: "invocation", Ledger: file, LedgerOwner: "ledger-owner", LedgerPrincipal: "product-owner", Node: node})
		if err != nil {
			t.Fatal(err)
		}
		if queryOwner == owner {
			if len(r.Attempts) != 1 || r.Attempts[0].Execution != "uncertain" || r.Attempts[0].Effect != "unknown" {
				t.Fatalf("embedded bridge did not join exact owner scope: %+v", r)
			}
		} else if r.Attempts[0].Execution != "incomplete" {
			t.Fatal("other owner joined ledger task")
		}
		raw, _ := Encode(r, "json")
		if bytes.Contains(raw, []byte("private@example.test")) || bytes.Contains(raw, []byte("private-body")) {
			t.Fatal("private ledger request escaped projection")
		}
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("unknown ledger changed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatal("report created source sidecars")
	}
}
