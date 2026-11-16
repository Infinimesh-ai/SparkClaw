package mcdintegration

import "context"

// RequestsRemaining reports how many provider requests this execution may still
// reserve on its connection. It is read-only: it neither reserves nor refunds,
// so callers can plan optional reads without weakening the aggregate cap.
func (m *Manager) RequestsRemaining(ctx context.Context, p Principal, x Execution) (int, error) {
	c, err := m.connection(p, x.Identity, x.ConnectionID)
	if err != nil {
		return 0, err
	}
	r, _, err := c.load(ctx)
	if err != nil {
		return 0, err
	}
	if r.Session != nil {
		defer clear(r.Session.Material)
	}
	if r.Closed || r.Session == nil || !r.MaterialPresent {
		return 0, ErrUnavailable
	}
	now := c.now().UnixMilli()
	reserved := 0
	if b, ok := r.Executions[x.ExecutionID]; ok && now < b.DeadlineMS {
		if b.DeadlineMS != x.DeadlineMS || b.CredentialRevision != x.CredentialRevision {
			return 0, ErrScope
		}
		reserved = b.Reserved
	}
	return max(r.Session.MaxRequests-reserved, 0), nil
}
