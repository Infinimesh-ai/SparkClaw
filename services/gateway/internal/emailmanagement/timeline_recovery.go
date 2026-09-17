package emailmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"reflect"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
)

// Reconcile only the exact journal named by the durable in-flight interval.
// No date-tree walk, historical list or provider call participates in recovery.
func (s *Service) recoverTimelineBatch(ctx context.Context, owner string, request app.EmailReadRequest) (app.EmailPageResult, bool, error) {
	var result app.EmailPageResult
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return result, false, err
	}
	defer root.Close()
	digest := sha256.Sum256([]byte(request.Provider + "\x00" + request.InvocationID))
	journal := path.Join("email", ownerScope(owner), "batches", hex.EncodeToString(digest[:])+".json")
	file, err := root.Open(journal)
	if errors.Is(err, os.ErrNotExist) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	file.Close()
	if readErr != nil {
		return result, true, readErr
	}
	var batch struct {
		SchemaVersion int    `json:"schema_version"`
		Provider      string `json:"provider"`
		InvocationID  string `json:"invocation_id"`
		Entries       []struct {
			Target  app.EmailCaptureTarget `json:"target"`
			Result  app.EmailReadResult    `json:"result"`
			Staging string                 `json:"staging"`
		} `json:"entries"`
		Result app.EmailPageResult `json:"result"`
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &batch) != nil || batch.SchemaVersion != 1 || batch.Provider != request.Provider || batch.InvocationID != request.InvocationID || len(batch.Entries) > 100 {
		return result, true, errors.New("email_batch_journal_invalid")
	}
	result = batch.Result
	if result.Provider != request.Provider || !strings.EqualFold(result.AccountAddress, request.Discovery.AccountAddress) || !result.DiscoveryOptions.IntervalStart.Equal(request.Discovery.IntervalStart) || !result.DiscoveryOptions.IntervalEnd.Equal(request.Discovery.IntervalEnd) {
		return result, true, errors.New("email_batch_journal_invalid")
	}
	// Check the whole receipt before moving any directory. A journal entry must
	// correspond to an outcome that will be committed to Store. Reused originals
	// have outcomes but need no staging entry.
	if len(result.Captures) > 100 || len(result.Failures) > 100 || len(result.Discovery.Candidates) > 100 {
		return result, true, errors.New("email_batch_journal_invalid")
	}
	outcomes := make(map[string]app.EmailPageCapture, len(result.Captures))
	for _, capture := range result.Captures {
		if _, exists := outcomes[capture.Target.ProviderMessageID]; exists || capture.Target.ProviderMessageID == "" || !strings.EqualFold(capture.Target.AccountAddress, request.Discovery.AccountAddress) {
			return result, true, errors.New("email_batch_journal_invalid")
		}
		outcomes[capture.Target.ProviderMessageID] = capture
	}
	seen := make(map[string]bool, len(batch.Entries))
	for _, entry := range batch.Entries {
		outcome, exists := outcomes[entry.Target.ProviderMessageID]
		if entry.Target.ProviderMessageID == "" || seen[entry.Target.ProviderMessageID] || !strings.EqualFold(entry.Target.AccountAddress, request.Discovery.AccountAddress) ||
			!exists || !reflect.DeepEqual(entry.Target, outcome.Target) || !reflect.DeepEqual(entry.Result, outcome.Result) {
			return result, true, errors.New("email_batch_journal_invalid")
		}
		seen[entry.Target.ProviderMessageID] = true
	}
	for _, entry := range batch.Entries {
		if err := ctx.Err(); err != nil {
			return result, true, err
		}
		ref := entry.Result.Capture
		if ref == nil {
			return result, true, errors.New("email_batch_journal_invalid")
		}
		mailboxID, mailID, _, ok := captureDirScope(ref.ManifestPath, owner)
		if !ok || mailboxID != ref.MailboxID || mailID != ref.MailID || path.Base(path.Dir(ref.ManifestPath)) != ref.CaptureID {
			return result, true, errors.New("email_batch_journal_invalid")
		}
		invocation := emailautomation.PageCaptureInvocationID(request.InvocationID, request.Provider, entry.Target)
		stageHash := sha256.Sum256([]byte(request.Provider + "\x00" + invocation))
		staging := path.Join("email", ownerScope(owner), "staging", mailboxID, mailID, "attempt_"+hex.EncodeToString(stageHash[:]))
		if entry.Staging != staging {
			return result, true, errors.New("email_batch_journal_invalid")
		}
		if _, err := root.Stat(ref.ManifestPath); errors.Is(err, os.ErrNotExist) {
			// Parent directories were created before journal publication. If they
			// were lost during power failure, the exact target is retried below.
			_ = root.Rename(staging, path.Dir(ref.ManifestPath))
		}
	}
	// Validate recovered sources once. A missing/corrupt source becomes an exact
	// operational failure; valid siblings are committed without redownloading.
	valid := make([]app.EmailPageCapture, 0, len(result.Captures))
	for _, captured := range result.Captures {
		if captured.Result.Capture == nil {
			return result, true, errors.New("email_batch_journal_invalid")
		}
		ref := captured.Result.Capture
		version := app.EmailCaptureVersion{ID: ref.CaptureID, ManifestPath: ref.ManifestPath, ManifestSHA256: ref.ManifestSHA256}
		_, files, verifyErr := loadManifest(ctx, s.opts.WorkspaceRoot, owner, version)
		if verifyErr == nil {
			for _, source := range files {
				checked, err := openVerifiedFile(ctx, root, source, maxSourceBytes)
				if err != nil {
					verifyErr = err
					break
				}
				checked.Close()
			}
		}
		if verifyErr != nil {
			result.Failures = append(result.Failures, app.EmailPageFailure{Target: captured.Target, ErrorCode: "email_source_missing", Scope: app.EmailSyncFailureLocalOperational})
			continue
		}
		valid = append(valid, captured)
	}
	result.Captures = valid
	if len(result.Failures) > 0 {
		result.Status = "partial"
	}
	return result, true, nil
}
