package store

import (
	"context"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	EmailIntakeCommitted  = "committed"
	EmailIntakeNotApplied = "not_applied"
)

// Key binds deployment, owner, client, installation and the original operation.
// Digest binds the original immutable request; neither contains current credentials.
type EmailIntakeOperation struct {
	Key             string `json:"key"`
	Digest          string `json:"digest"`
	Provider        string `json:"provider"`
	Enabled         bool   `json:"enabled"`
	ExpectedVersion int64  `json:"expected_version"`
}
type EmailIntakeResolution struct {
	Operation EmailIntakeOperation `json:"operation"`
	State     string               `json:"state"`
	Version   int64                `json:"version"`
	Mailbox   app.EmailMailbox     `json:"mailbox"`
}

func (o EmailIntakeOperation) valid() bool {
	return emailHashValid(o.Key) && !strings.HasPrefix(o.Key, "sha256:") && emailHashValid(o.Digest) && !strings.HasPrefix(o.Digest, "sha256:") && app.KnownEmailProvider(o.Provider) && o.ExpectedVersion >= 0
}
func emailIntakeResult(e *emailEngine, o EmailIntakeOperation) (EmailIntakeResolution, bool) {
	result, found := emailGet[EmailIntakeResolution](e, "counter", "intake_operation:"+o.Key)
	if found && result.Operation != o {
		e.err = errEmailConflict
	}
	if found && result.State != EmailIntakeCommitted && result.State != EmailIntakeNotApplied {
		e.err = errEmailCorrupt
	}
	return result, found
}
func emailSaveIntakeResult(e *emailEngine, result EmailIntakeResolution) {
	id := "intake_operation:" + result.Operation.Key
	emailPut(e, "counter", id, "", "", result.State, "", id, result)
}
func emailProviderVersions(e *emailEngine) (map[string]int64, error) {
	versions := map[string]int64{}
	for _, provider := range app.EmailProviderIDs() {
		binding, _ := emailGet[emailBindingSelection](e, "counter", "binding:"+provider)
		versions[provider] = binding.Version
	}
	return versions, e.err
}

// Resolution is itself idempotent under the original operation key. If no
// positive commit proof exists, the CAS advances even an empty binding: any
// delayed original writer still carrying ExpectedVersion is permanently fenced.
func emailResolveIntake(e *emailEngine, o EmailIntakeOperation) (EmailIntakeResolution, error) {
	if !o.valid() {
		return EmailIntakeResolution{}, errEmailInvalid
	}
	if result, found := emailIntakeResult(e, o); found || e.err != nil {
		return result, e.err
	}
	selection, _ := emailGet[emailBindingSelection](e, "counter", "binding:"+o.Provider)
	if e.err != nil {
		return EmailIntakeResolution{}, e.err
	}
	if selection.Version != o.ExpectedVersion {
		return EmailIntakeResolution{}, errEmailConflict
	}
	selection.Version++
	emailPut(e, "counter", "binding:"+o.Provider, "", "", "", "", "binding:"+o.Provider, selection)
	if selection.MailboxID != "" {
		mailbox, found := emailGet[app.EmailMailbox](e, "mailbox", selection.MailboxID)
		if !found {
			return EmailIntakeResolution{}, errEmailCorrupt
		}
		mailbox.Version = selection.Version
		emailSaveMailbox(e, mailbox)
	} else {
		// Version-only fences must invalidate existing status snapshots as well.
		status, _ := emailGet[app.EmailOwnerStatus](e, "counter", "owner_status")
		status.Revision++
		emailPut(e, "counter", "owner_status", "", "", "", "", "owner_status", status)
	}
	result := EmailIntakeResolution{Operation: o, State: EmailIntakeNotApplied, Version: selection.Version}
	emailSaveIntakeResult(e, result)
	return result, e.err
}
func (s *MemoryStore) GetEmailProviderVersions(ctx context.Context, owner string) (map[string]int64, error) {
	return emailMemoryRun(s, ctx, OperationGetEmailProviderVersions, owner, "", nil, false, emailProviderVersions)
}
func (s *PostgresStore) GetEmailProviderVersions(ctx context.Context, owner string) (map[string]int64, error) {
	return emailPostgresRun(s, ctx, OperationGetEmailProviderVersions, owner, "", nil, false, emailProviderVersions)
}
func (s *FileStore) GetEmailProviderVersions(ctx context.Context, owner string) (map[string]int64, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailProviderVersions, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.GetEmailProviderVersions(ctx, owner)
}
func (s *MemoryStore) ResolveEmailIntake(ctx context.Context, owner string, o EmailIntakeOperation) (EmailIntakeResolution, error) {
	return emailMemoryRun(s, ctx, OperationResolveEmailIntake, owner, "", nil, true, func(e *emailEngine) (EmailIntakeResolution, error) { return emailResolveIntake(e, o) })
}
func (s *PostgresStore) ResolveEmailIntake(ctx context.Context, owner string, o EmailIntakeOperation) (EmailIntakeResolution, error) {
	return emailPostgresRun(s, ctx, OperationResolveEmailIntake, owner, "", nil, true, func(e *emailEngine) (EmailIntakeResolution, error) { return emailResolveIntake(e, o) })
}
func (s *FileStore) ResolveEmailIntake(ctx context.Context, owner string, o EmailIntakeOperation) (EmailIntakeResolution, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationResolveEmailIntake, fileAdmissionCapacity)
	if err != nil {
		return EmailIntakeResolution{}, err
	}
	defer release()
	return emailFileRun(s, ctx, OperationResolveEmailIntake, owner, "", nil, true, func(e *emailEngine) (EmailIntakeResolution, error) { return emailResolveIntake(e, o) })
}
