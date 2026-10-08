package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

func (e *Endpoint) grantMaterial() material {
	e.grantMu.RLock()
	defer e.grantMu.RUnlock()
	return e.material
}

// Renewal changes authorization, not the execution identity or the active
// encrypted session. In-flight original requests keep their durable fence;
// subsequent handshakes use the new Grant ID.
func (e *Endpoint) refreshAuthorization(ctx context.Context) error {
	if e.lifecycle == nil {
		return nil
	}
	e.renewalMu.Lock()
	defer e.renewalMu.Unlock()
	m := e.grantMaterial()
	due := e.config.Role == RoleInitiator && (e.lifecycle.HasPendingRenewal() || time.Until(m.grant.ExpiresAt) <= iscpbridge.GrantRenewalWindow(m.grant))
	var grant trust.Grant
	var err error
	if due {
		grant, err = e.lifecycle.Renew(ctx, m.grant)
	} else {
		grant, err = e.lifecycle.Current(ctx)
	}
	if err != nil {
		return err
	}
	m.grant = grant
	if err = verifyGrant(e.config, m, time.Now().UTC()); err != nil {
		return err
	}
	if grant.Signature.Value != e.grantMaterial().grant.Signature.Value {
		if err = saveCurrentGrant(e.config.GrantFile, grant); err != nil {
			return err
		}
	}
	e.grantMu.Lock()
	e.material.grant = grant
	e.grantMu.Unlock()
	if due {
		return e.lifecycle.CommitRenewal()
	}
	return e.lifecycle.AcceptGrant(grant)
}

func (e *Endpoint) runGrantLifecycle(ctx context.Context) {
	interval := time.Duration(e.config.GrantRenewal.PollIntervalSeconds) * time.Second
	if interval == 0 {
		interval = 10 * time.Second
	}
	// Short test grants need a check inside their eligibility window too.
	if lead := iscpbridge.GrantRenewalWindow(e.grantMaterial().grant) / 2; lead < interval {
		interval = max(time.Second, lead)
	}
	delay := interval
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		err := e.refreshAuthorization(ctx)
		if err == nil {
			delay = interval
			continue
		}
		if ctx.Err() != nil {
			return
		}
		delay = min(max(interval, delay*2), 30*time.Second)
		var retry interface{ RetryAfter() time.Duration }
		if errors.As(err, &retry) && retry.RetryAfter() > delay {
			delay = retry.RetryAfter()
		}
	}
}

func saveCurrentGrant(path string, grant trust.Grant) error {
	if !filepath.IsAbs(path) {
		return errors.New("current grant path must be absolute")
	}
	info, err := os.Lstat(filepath.Dir(path))
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("current grant directory must be private")
	}
	if info, err = os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("current grant must be a private regular file")
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		return errors.New("encode current grant")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".grant-next-")
	if err != nil {
		return errors.New("create current grant file")
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(append(raw, '\n')); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		return errors.New("persist current grant")
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errors.New("open current grant directory")
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return errors.New("sync current grant directory")
	}
	return nil
}
