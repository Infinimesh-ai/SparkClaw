package emailautomation

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const intakeProbeTTL = 60 * time.Second

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
