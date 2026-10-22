package emailautomation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Send admission reuses a recent full login check instead of probing the
// provider page again before every send (about 2.7 s on work2). Only a probe
// that this controller ran and persisted as ready (Admit or Check) seeds it,
// and only for the configured window. The send script still proves the exact
// account on the page before any effect, and the send itself re-checks the
// setting version and browser credential generation.
//
// A cached proof is dropped, so the next admission probes again, when any of
// these change or are observed:
//   - the window elapses (measured from the probe, never extended by reuse);
//   - the provider setting (version, account, enabled, state, error code);
//   - the probe script revision;
//   - the browser credential generation (Controller/browser reconnect);
//   - the provider's mailbox observer state: a new observer registration or
//     watch epoch, a resync for a replaced document or registration, a watch
//     state change, a mailbox error, and never while it reports login_required;
//   - a failed probe (Check, Admit or intake) or a send that did not end sent.
const (
	DefaultSendAdmissionReuse = 120 * time.Second
	MaxSendAdmissionReuse     = 10 * time.Minute
)

type sendAdmissionEntry struct {
	settingVersion int64
	account        string
	cachedAt       time.Time
	proof          ProbeResult
	signals        string
}

type mailboxSignalReader interface {
	ListEmailMailboxes(context.Context, string) ([]app.EmailMailbox, error)
}

// WithSendAdmissionReuse enables reuse of a recent login proof for sends.
// Zero (the NewController default) always probes.
func (c *Controller) WithSendAdmissionReuse(window time.Duration) *Controller {
	if window < 0 {
		window = 0
	}
	if window > MaxSendAdmissionReuse {
		window = MaxSendAdmissionReuse
	}
	c.sendAdmissionsMu.Lock()
	defer c.sendAdmissionsMu.Unlock()
	c.sendAdmissionWindow = window
	c.sendAdmissions = nil
	return c
}

// reuseSendAdmission runs under the provider gate. It returns false whenever a
// full probe is required; it never returns an error so that a failed signal
// read simply falls back to probing.
func (c *Controller) reuseSendAdmission(ctx context.Context, key intakeProbeKey, provider Provider) (AdmissionResult, bool) {
	c.sendAdmissionsMu.Lock()
	window := c.sendAdmissionWindow
	entry, exists := c.sendAdmissions[key]
	c.sendAdmissionsMu.Unlock()
	if window <= 0 || !exists {
		return AdmissionResult{}, false
	}
	reader, canCheck := c.runner.(intakeCredentialReader)
	age := c.now().Sub(entry.cachedAt)
	setting, found, err := c.store.GetEmailProviderSetting(ctx, key.ownerID, key.providerID)
	if !canCheck || age < 0 || age >= window || err != nil || !found || !setting.Enabled ||
		setting.State != app.EmailStateReady || setting.ErrorCode != "" || setting.Version != entry.settingVersion ||
		setting.Account != entry.account || entry.proof.Revision != provider.Probe.Revision {
		c.forgetSendAdmission(key)
		return AdmissionResult{}, false
	}
	generation, err := reader.credentialGeneration(ctx, 0)
	if err != nil || uint64(generation) != entry.proof.Generation {
		c.forgetSendAdmission(key)
		return AdmissionResult{}, false
	}
	signals, usable := c.mailboxSignals(ctx, key)
	if !usable || signals != entry.signals {
		c.forgetSendAdmission(key)
		return AdmissionResult{}, false
	}
	return AdmissionResult{
		Provider: provider.ID, Account: app.EmailAccountDefault, AccountHint: setting.AccountHint,
		SettingVersion: setting.Version, BrowserCredentialGeneration: entry.proof.Generation,
		ProbeRevision: provider.Probe.Revision, SendScriptRevision: provider.Send.Revision, ReadScriptRevision: provider.Read.Revision,
		ValidatedAt: entry.proof.CheckedAt,
	}, true
}

// rememberSendAdmission records a fresh persisted-ready probe. The signal
// snapshot is taken after the probe, so a reload during it forces a new probe.
func (c *Controller) rememberSendAdmission(ctx context.Context, key intakeProbeKey, setting app.EmailProviderSetting, proof ProbeResult) {
	c.sendAdmissionsMu.Lock()
	enabled := c.sendAdmissionWindow > 0
	c.sendAdmissionsMu.Unlock()
	if _, canCheck := c.runner.(intakeCredentialReader); !enabled || !canCheck || setting.State != app.EmailStateReady {
		c.forgetSendAdmission(key)
		return
	}
	signals, usable := c.mailboxSignals(ctx, key)
	if !usable {
		c.forgetSendAdmission(key)
		return
	}
	c.sendAdmissionsMu.Lock()
	defer c.sendAdmissionsMu.Unlock()
	if c.sendAdmissions == nil {
		c.sendAdmissions = make(map[intakeProbeKey]sendAdmissionEntry)
	}
	c.sendAdmissions[key] = sendAdmissionEntry{settingVersion: setting.Version, account: setting.Account,
		cachedAt: c.now(), proof: proof, signals: signals}
}

func (c *Controller) forgetSendAdmission(key intakeProbeKey) {
	c.sendAdmissionsMu.Lock()
	defer c.sendAdmissionsMu.Unlock()
	delete(c.sendAdmissions, key)
}

// Observer resync reasons that mean the provider document was (re)loaded.
var pageReloadReasons = map[string]bool{
	"registration": true, "document_replaced": true, "sequence_gap": true, "buffer_overflow": true, "observer_degraded": true,
}

// mailboxSignals fingerprints the provider's active mailboxes as maintained by
// the mail observer and intake. usable is false while any of them reports a
// lost login or the state cannot be read. Stores without mailboxes have none.
func (c *Controller) mailboxSignals(ctx context.Context, key intakeProbeKey) (string, bool) {
	reader, ok := c.store.(mailboxSignalReader)
	if !ok {
		return "", true
	}
	mailboxes, err := reader.ListEmailMailboxes(ctx, key.ownerID)
	if err != nil {
		return "", false
	}
	var parts []string
	for _, box := range mailboxes {
		if !box.Active || box.Provider != key.providerID {
			continue
		}
		if box.WatchState == app.EmailStateLoginRequired || box.ErrorCode != "" {
			return "", false
		}
		reload := ""
		if pageReloadReasons[box.LastNotificationReason] {
			reload = box.LastNotificationReason + "@" + box.LastNotificationAt.UTC().Format(time.RFC3339Nano)
		}
		parts = append(parts, fmt.Sprintf("%s|%d|%s|%d|%s|%s|%s", box.ID, box.BindingGeneration, box.ObserverEpoch,
			box.ObserverCredentialGeneration, box.WatchEpoch, box.WatchState, reload))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n"), true
}
