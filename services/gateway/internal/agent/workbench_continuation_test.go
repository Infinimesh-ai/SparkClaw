package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type failingWorkbenchBlockRepository struct {
	Repository
	failure error
}

func (r failingWorkbenchBlockRepository) UpdateBrowserLoginBlock(context.Context, app.BrowserLoginBlock, int64) (app.BrowserLoginBlock, error) {
	return app.BrowserLoginBlock{}, r.failure
}

func TestWorkbenchBrowserCleanupDoesNotBypassTemporaryOrPersistenceFailure(t *testing.T) {
	for _, persistenceFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "temporary_authority", true: "terminal_write_failed"}[persistenceFailure], func(t *testing.T) {
			st := store.NewMemoryStore()
			session := storetest.MustCreateSession(t, st, "closed browser")
			run := app.AgentRun{ID: "original", SessionID: session.ID, State: "browser_login_blocked"}
			if _, err := st.SaveRun(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			block := storetest.MustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{SessionID: session.ID, RunID: run.ID, Status: app.BrowserLoginBlockStatusWaiting, SiteOrigin: "https://example.com"})
			runtime := Runtime{store: st}
			failure := errors.New("temporary authority unavailable")
			guardFailure := failure
			if persistenceFailure {
				failure = errors.New("terminal persistence unavailable")
				runtime.store = failingWorkbenchBlockRepository{Repository: st, failure: failure}
				guardFailure = ErrWorkbenchContinuationClosed
			}
			ctx := WithWorkbenchContinuation(t.Context(), func(context.Context, app.AgentRun) (context.Context, error) { return nil, guardFailure })
			_, handled, err := runtime.resumeBrowserLoginBlock(ctx, session.ID, "hello", nil)
			if !handled || !errors.Is(err, failure) {
				t.Fatalf("bypassed failure handled=%v err=%v", handled, err)
			}
			persisted, found := storetest.MustGetBrowserLoginBlock(t, st, block.ID)
			if !found || !app.BrowserHandoffStatusActive(persisted.Status) {
				t.Fatal("cleared block despite uncertain authority or write")
			}
			if !persistenceFailure {
				unchanged, _, err := st.GetRun(t.Context(), run.ID)
				if err != nil || unchanged.State != run.State {
					t.Fatalf("temporary failure changed run: %+v %v", unchanged, err)
				}
			}
		})
	}
}
