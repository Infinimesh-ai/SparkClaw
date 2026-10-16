package emailmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const lifecycleMaintenanceInterval = 2 * time.Minute

// maintainLifecycle retries durable-receipt ACKs and confirms the drained
// request epoch: the Gateway outbox is the only allocator of send
// identities, so only it can prove none issued in the epoch is unsubmitted.
func (s *Service) maintainLifecycle(ctx context.Context) {
	if _, ok := s.browser.(LifecycleBrowser); !ok {
		return
	}
	ticker := time.NewTicker(lifecycleMaintenanceInterval)
	defer ticker.Stop()
	for {
		if err := s.lifecycleRound(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("email lifecycle maintenance failed", "code", safeCode(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type lifecycleDraft struct {
	owner    string
	provider string
	draft    store.EmailDraft
}

func (s *Service) lifecycleDrafts(ctx context.Context) ([]lifecycleDraft, error) {
	owners, err := s.repository.ListOwnerProfiles(ctx)
	if err != nil {
		return nil, err
	}
	var drafts []lifecycleDraft
	for _, owner := range owners {
		boxes, err := s.repository.ListEmailMailboxes(ctx, owner.ID)
		if err != nil {
			return nil, err
		}
		providers := map[string]string{}
		for _, box := range boxes {
			providers[box.ID] = box.Provider
		}
		rows, err := s.repository.ListEmailDrafts(ctx, owner.ID, "")
		if err != nil {
			return nil, err
		}
		for _, d := range rows {
			if provider := providers[d.MailboxID]; provider != "" {
				drafts = append(drafts, lifecycleDraft{owner: owner.ID, provider: provider, draft: d})
			}
		}
	}
	return drafts, nil
}

func (s *Service) lifecycleRound(ctx context.Context) error {
	browser := s.browser.(LifecycleBrowser)
	drafts, err := s.lifecycleDrafts(ctx)
	if err != nil {
		return err
	}
	var provider string
	for _, item := range drafts {
		provider = item.provider
		if item.draft.State == "sent" && item.draft.Receipt != nil && item.draft.Receipt.Lifecycle != nil && !item.draft.Receipt.Lifecycle.Acknowledged {
			if _, err := s.acknowledgeDraft(ctx, item.owner, item.draft, item.provider); err != nil && ctx.Err() != nil {
				return err
			}
		}
	}
	if provider == "" {
		return nil
	}
	state, err := browser.OperationEpoch(ctx, provider)
	if err != nil || state.DrainingEpoch == nil {
		return err
	}
	draining := *state.DrainingEpoch
	// Every send identity issued in the draining epoch must be admitted (or
	// definitively retired) before the product confirms the drain.
	var issued []string
	for _, item := range drafts {
		snapshot := item.draft.Snapshot
		if snapshot == nil {
			continue
		}
		generation, epoch, ok := app.OperationEpoch(snapshot.InvocationID)
		if !ok || generation != state.Generation || epoch != draining {
			continue
		}
		if item.draft.State == "sending" || item.draft.State == "unknown" {
			outcome, _, err := browser.LookupSend(ctx, item.provider, snapshot.InvocationID)
			if err != nil {
				return err
			}
			if outcome != "known" && outcome != "retired" {
				return nil
			}
		}
		issued = append(issued, snapshot.InvocationID)
	}
	sort.Strings(issued)
	sum := sha256.Sum256([]byte(strings.Join(issued, "\n")))
	_, err = browser.SealOperationEpoch(ctx, provider, draining, hex.EncodeToString(sum[:]))
	return err
}
