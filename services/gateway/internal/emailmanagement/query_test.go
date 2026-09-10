package emailmanagement

import (
	"context"
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type emailProjectionChurn struct {
	*store.MemoryStore
	reads int
}

func (r *emailProjectionChurn) GetEmailOwnerStatus(ctx context.Context, owner string) (app.EmailOwnerStatus, error) {
	status, err := r.MemoryStore.GetEmailOwnerStatus(ctx, owner)
	r.reads++
	status.Revision += int64(r.reads)
	return status, err
}
func TestQueryRejectsProjectionAssembledAcrossChangingRevisions(t *testing.T) {
	repo := &emailProjectionChurn{MemoryStore: store.NewMemoryStore()}
	service := &Service{repository: repo}
	result, err := service.QueryConversations(t.Context(), store.EmailQuery{OwnerID: app.DefaultOwnerID, Limit: 30})
	if !errors.Is(err, ErrProjectionBusy) || result.Version != 0 {
		t.Fatalf("mixed projection published: result=%+v err=%v", result, err)
	}
	if repo.reads > 6 {
		t.Fatalf("projection retry was not bounded: %d reads", repo.reads)
	}
}
