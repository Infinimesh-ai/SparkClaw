package progressanchor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func privateDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func call(t *testing.T, handler http.Handler, token, action string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/v1/anchors/"+action, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var out map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &out)
	return recorder.Code, out
}

func TestAnchorAdvancesByOneReleaseIdempotentlyAndPersists(t *testing.T) {
	directory := privateDir(t)
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 40)
	handler := Handler(store, []byte(token))
	if code, _ := call(t, handler, "wrong", "read", map[string]string{"owner": "owner"}); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read: %d", code)
	}
	if code, out := call(t, handler, token, "read", map[string]string{"owner": "owner"}); code != 200 || out["state"] != nil {
		t.Fatalf("fresh read: %d %v", code, out)
	}
	digest := func(c string) string { return strings.Repeat(c, 64) }
	step := Advance{Owner: "owner", Generation: 1, PreviousSeq: 0, NextSeq: 1, Digest: digest("a"), OperationID: "op3.1.1." + strings.Repeat("0", 32), Attempt: 1}
	for range 2 {
		if code, out := call(t, handler, token, "advance", step); code != 200 || out["seq"] != float64(1) {
			t.Fatalf("advance: %d %v", code, out)
		}
	}
	conflicting := step
	conflicting.Digest = digest("b")
	if code, _ := call(t, handler, token, "advance", conflicting); code != http.StatusConflict {
		t.Fatalf("same sequence with another digest: %d", code)
	}
	skipped := Advance{Owner: "owner", Generation: 1, PreviousSeq: 2, NextSeq: 3, Digest: digest("c"), OperationID: "x", Attempt: 1}
	if code, _ := call(t, handler, token, "advance", skipped); code != http.StatusConflict {
		t.Fatalf("skipped sequence: %d", code)
	}
	reopened, _ := Open(directory)
	state, err := reopened.Read("owner")
	if err != nil || state.Seq != 1 || state.Digest != digest("a") || len(state.Ring) != 1 || state.TimeMS == 0 {
		t.Fatalf("persisted state %+v %v", state, err)
	}
	if code, _ := call(t, handler, token, "reseed", Reseed{Owner: "owner", Generation: 1, Seq: 0, Digest: digest("0")}); code != http.StatusConflict {
		t.Fatalf("reseed must raise the generation: %d", code)
	}
	if code, _ := call(t, handler, token, "reseed", Reseed{Owner: "owner", Generation: 2, Seq: 5, Digest: digest("e")}); code != 200 {
		t.Fatalf("reseed: %d", code)
	}
	if state, _ := reopened.Read("owner"); state.Generation != 2 || state.Seq != 5 || len(state.Ring) != 0 {
		t.Fatalf("reseeded state %+v", state)
	}
}

func TestAnchorRingIsBounded(t *testing.T) {
	store, err := Open(privateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for seq := int64(1); seq <= Ring+10; seq++ {
		if _, err := store.Advance(Advance{Owner: "o", Generation: 1, PreviousSeq: seq - 1, NextSeq: seq, Digest: strings.Repeat("f", 64), OperationID: "op", Attempt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := store.Read("o")
	if len(state.Ring) != Ring || state.Ring[0].Seq != 11 {
		t.Fatalf("ring %d first %d", len(state.Ring), state.Ring[0].Seq)
	}
}
