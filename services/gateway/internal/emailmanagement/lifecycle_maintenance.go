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

func (s *Service) lifecycleDrafts(ctx context.Context) ([]lifecycleDraft, string, error) {
	owners, err := s.repository.ListOwnerProfiles(ctx)
	if err != nil {
		return nil, "", err
	}
	var drafts []lifecycleDraft
	// Epoch control is owner-level in the runtime; any bound provider reaches
	// it, so a deployment without drafts still confirms its drained epochs.
	var control string
	for _, owner := range owners {
		boxes, err := s.repository.ListEmailMailboxes(ctx, owner.ID)
		if err != nil {
			return nil, "", err
		}
		providers := map[string]string{}
		for _, box := range boxes {
			providers[box.ID] = box.Provider
			if control == "" && box.Active && box.Provider != "" {
				control = box.Provider
			}
		}
		rows, err := s.repository.ListEmailDrafts(ctx, owner.ID, "")
		if err != nil {
			return nil, "", err
		}
		for _, d := range rows {
			if provider := providers[d.MailboxID]; provider != "" {
				drafts = append(drafts, lifecycleDraft{owner: owner.ID, provider: provider, draft: d})
			}
		}
	}
	return drafts, control, nil
}

func (s *Service) lifecycleRound(ctx context.Context) error {
	browser := s.browser.(LifecycleBrowser)
	drafts, provider, err := s.lifecycleDrafts(ctx)
	if err != nil {
		return err
	}
	for _, item := range drafts {
		if pendingTerminalACK(item.draft, item.provider) {
			if _, err := s.acknowledgeDraft(ctx, item.owner, item.draft, item.provider); err != nil {
				recordSendDiagnostic(item.owner, item.draft, "ack", "", item.draft.State, err)
				if ctx.Err() != nil {
					return err
				}
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

// A failed draft is eligible only when the persisted receipt still proves a
// rejection of this exact send. Unknown and pre-dispatch failures stay fenced.
func pendingTerminalACK(d store.EmailDraft, provider string) bool {
	if d.Receipt == nil || d.Receipt.Lifecycle == nil || d.Receipt.Lifecycle.Acknowledged {
		return false
	}
	if d.State == "sent" {
		return true
	}
	return d.State == "failed" && d.Snapshot != nil &&
		d.Receipt.Status == "not_sent" && d.Receipt.Provider == provider &&
		d.Receipt.NotSent.ValidFor(d.Snapshot.InvocationID, provider) &&
		d.Receipt.NotSent.RejectionErrorCode(provider) != "" &&
		d.Receipt.Lifecycle.OperationID == d.Snapshot.InvocationID &&
		d.Receipt.Lifecycle.TaskID == d.Receipt.NotSent.TaskID
}
