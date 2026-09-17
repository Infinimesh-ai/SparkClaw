package emailautomation

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// This TTL originally covered the old twenty-minute polling interval. Normal
// intake now waits one minute after collection; the proof lifetime stays bounded
// at thirty minutes. Only a freshly verified complete timeline read can renew
// it. Admission/cache hits themselves never extend this lifetime.
const intakeProbeTTL = 30 * time.Minute

// The production runner obtains this from the local browser-control credential
// status without a browser operation. Runners without that capability continue
// to probe, rather than reusing a proof whose credential binding is unknown.
type intakeCredentialReader interface {
	credentialGeneration(context.Context, uint64) (int64, error)
}

type intakeProbeKey struct {
	ownerID    string
	providerID string
}

type intakeProbeEntry struct {
	settingVersion int64
	account        string
	cachedAt       time.Time
	proof          ProbeResult
}

// The provider gate covers the complete admission, so same-provider lanes share
// one fresh proof. The cache mutex only protects map access, never external I/O.
func (c *Controller) intakeProbe(ctx context.Context, key intakeProbeKey, provider Provider, setting app.EmailProviderSetting) (ProbeResult, bool, error) {
	now := c.now()
	c.intakeProbesMu.Lock()
	for candidate, entry := range c.intakeProbes {
		if age := now.Sub(entry.cachedAt); age < 0 || age >= intakeProbeTTL {
			delete(c.intakeProbes, candidate)
		}
	}
	cached, exists := c.intakeProbes[key]
	c.intakeProbesMu.Unlock()
	if exists {
		reader, canCheck := c.runner.(intakeCredentialReader)
		if canCheck && cached.settingVersion == setting.Version && cached.account == setting.Account && cached.proof.Revision == provider.Probe.Revision {
			generation, err := reader.credentialGeneration(ctx, 0)
			if err != nil {
				c.forgetIntakeProbe(key)
				return ProbeResult{}, false, err
			}
			if uint64(generation) == cached.proof.Generation {
				return cached.proof, true, nil
			}
		}
		c.forgetIntakeProbe(key)
	}
	proof, err := c.probe(ctx, provider, app.NewID("email_intake_admit"))
	return proof, false, err
}

func (c *Controller) rememberIntakeProbe(key intakeProbeKey, setting app.EmailProviderSetting, proof ProbeResult) {
	if _, canCheck := c.runner.(intakeCredentialReader); !canCheck {
		return
	}
	c.intakeProbesMu.Lock()
	defer c.intakeProbesMu.Unlock()
	if c.intakeProbes == nil {
		c.intakeProbes = make(map[intakeProbeKey]intakeProbeEntry)
	}
	c.intakeProbes[key] = intakeProbeEntry{settingVersion: setting.Version, account: setting.Account, cachedAt: c.now(), proof: proof}
}

func (c *Controller) forgetIntakeProbe(key intakeProbeKey) {
	c.intakeProbesMu.Lock()
	defer c.intakeProbesMu.Unlock()
	delete(c.intakeProbes, key)
}

// renewIntakeProbe records fresh read-path health, not a new login probe. The
// original CheckedAt is intentionally retained. Journal replay can return a
// valid old result without visiting the browser, so its observation must not
// keep this cache alive. Browser timestamps have millisecond precision.
func (c *Controller) renewIntakeProbe(ownerID string, request ReadRequest, output app.EmailPageResult, started time.Time) {
	now := c.now()
	if request.Discovery == nil || request.Discovery.ProviderMode != app.EmailProviderModeTimeRange ||
		len(output.Failures) != 0 || (output.Status != "empty" && output.Status != "collected") ||
		!output.Discovery.Coverage.ScanComplete || !output.Discovery.Coverage.BoundaryQualified ||
		output.Discovery.ObservedAt.Before(started.Truncate(time.Millisecond)) || output.Discovery.ObservedAt.After(now) || now.Before(started) {
		return
	}
	for _, capture := range output.Captures {
		if capture.Result.Status != "collected" {
			return
		}
	}
	key := intakeProbeKey{ownerID: ownerID, providerID: request.Provider}
	c.intakeProbesMu.Lock()
	defer c.intakeProbesMu.Unlock()
	entry, exists := c.intakeProbes[key]
	if !exists || entry.settingVersion != request.SettingVersion || entry.account != request.Account ||
		entry.proof.Generation != request.BrowserCredentialGeneration || entry.proof.Revision != request.ProbeRevision ||
		started.Before(entry.cachedAt) || now.Sub(entry.cachedAt) >= intakeProbeTTL {
		return
	}
	entry.cachedAt = now
	c.intakeProbes[key] = entry
}
