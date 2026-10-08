package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestHostWorkbenchDraftOwnerScopeRevisionAndWelcome(t *testing.T) {
	st := store.NewMemoryStore()
	session, err := st.CreateSessionWithScope(t.Context(), "Draft", "owner-a", "", "webchat", false)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: st}
	request := func(method, owner, sessionID, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/workbench/draft", strings.NewReader(body))
		r.SetPathValue("id", sessionID)
		r = r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, requestPrincipal{OwnerID: owner, ActorID: owner, Authenticated: true}))
		w := httptest.NewRecorder()
		if method == http.MethodGet {
			server.getWorkbenchDraft(w, r)
		} else {
			server.saveWorkbenchDraft(w, r)
		}
		return w
	}
	w := request(http.MethodPut, "owner-a", session.ID, `{"content":" draft\n ","attachment_ids":["local-file"],"revision":0}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save=%d %s", w.Code, w.Body.String())
	}
	var saved app.WorkbenchDraft
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Content != " draft\n " || saved.Revision != 1 {
		t.Fatalf("saved=%#v", saved)
	}
	if w := request(http.MethodGet, "owner-b", session.ID, ""); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "local-file") {
		t.Fatalf("scope=%d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodPut, "owner-a", session.ID, `{"content":"","attachment_ids":[],"revision":1}`); w.Code != http.StatusOK {
		t.Fatalf("clear=%d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodPut, "owner-a", session.ID, `{"content":"stale draft","revision":1}`); w.Code != http.StatusConflict {
		t.Fatalf("stale=%d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, "owner-a", session.ID, ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "stale draft") || !strings.Contains(w.Body.String(), `"revision":2`) {
		t.Fatalf("reload=%d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodPut, "owner-a", "", `{"content":"welcome","revision":0}`); w.Code != http.StatusOK {
		t.Fatalf("welcome=%d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, "owner-b", "", ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "welcome") {
		t.Fatalf("welcome isolation=%d %s", w.Code, w.Body.String())
	}
}
