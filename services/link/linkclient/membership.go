package linkclient

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

// GrantKeeper holds a device's Grant and renews it through the console
// adapter before expiry. A short console outage only matters once the
// Grant actually expires (design 3.11).
type GrantKeeper struct {
	console   *consoleapi.Client
	device    identity.Device
	consoleID string
	OnUpdate  func(consoleapi.GrantResponse)

	mu    sync.Mutex
	grant *trust.Grant
	host  *consoleapi.Host
}

// NewGrantKeeper starts from the membership's Grant.
func NewGrantKeeper(m consoleapi.Membership, dev identity.Device) *GrantKeeper {
	return &GrantKeeper{console: consoleapi.NewClient(m.ConsoleURL, m.ConsoleTLSPin), device: dev, consoleID: m.ConsoleID, grant: m.Grant, host: m.Host}
}

// ErrNoGrant means the console has not issued a Grant (the backend has not
// joined the domain yet).
var ErrNoGrant = &contract.Error{Code: contract.CodeBackendUnavailable, Message: "the backend has not joined this domain yet", Retryable: true}

// Grant returns a valid Grant, renewing when it nears expiry.
func (k *GrantKeeper) Grant(ctx context.Context) (trust.Grant, error) {
	k.mu.Lock()
	cur := k.grant
	k.mu.Unlock()
	now := time.Now()
	if cur != nil && now.Before(cur.ExpiresAt) && cur.ExpiresAt.Sub(now) > consoleapi.RenewBefore(*cur) {
		return *cur, nil
	}
	resp, err := k.console.Grant(ctx, k.device, k.consoleID)
	if err != nil {
		if cur != nil && now.Before(cur.ExpiresAt.Add(-time.Minute)) && !consoleapi.IsCode(err, consoleapi.ErrCodeRevoked) {
			return *cur, nil
		}
		if consoleapi.IsCode(err, consoleapi.ErrCodeRevoked) {
			return trust.Grant{}, &contract.Error{Code: contract.CodeRevoked, Message: "this device was revoked on the console"}
		}
		if consoleapi.IsCode(err, consoleapi.ErrCodeRenewalExpired) {
			return trust.Grant{}, &contract.Error{Code: contract.CodeUnauthorized, Message: "automatic renewal expired; authorize the device again on the console"}
		}
		return trust.Grant{}, err
	}
	if resp.Grant == nil {
		return trust.Grant{}, ErrNoGrant
	}
	k.mu.Lock()
	k.grant = resp.Grant
	if resp.Host != nil {
		k.host = resp.Host
	}
	k.mu.Unlock()
	if k.OnUpdate != nil {
		k.OnUpdate(resp)
	}
	return *resp.Grant, nil
}

// Host returns the latest known backend.
func (k *GrantKeeper) Host() *consoleapi.Host {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.host
}

// RefreshHost asks the console for the backend (after it joined later).
func (k *GrantKeeper) RefreshHost(ctx context.Context) (*consoleapi.Host, error) {
	resp, err := k.console.Grant(ctx, k.device, k.consoleID)
	if err != nil {
		return nil, err
	}
	k.mu.Lock()
	if resp.Grant != nil {
		k.grant = resp.Grant
	}
	if resp.Host != nil {
		k.host = resp.Host
	}
	h := k.host
	k.mu.Unlock()
	if k.OnUpdate != nil {
		k.OnUpdate(resp)
	}
	if h == nil {
		return nil, ErrNoGrant
	}
	return h, nil
}

// FromMembership builds client options for a joined device.
func FromMembership(m consoleapi.Membership, dev identity.Device, keeper *GrantKeeper, store relay.CredentialStore, describe contract.Device) (Options, error) {
	host := keeper.Host()
	if host == nil {
		return Options{}, errors.New("the backend has not joined this domain yet")
	}
	opts := Options{Device: dev, Host: *host, Grant: keeper.Grant, Describe: describe}
	if len(m.Relays) > 0 {
		ep := m.Relays[0]
		opts.Relay = &ep
		opts.RelayStore = store
		if m.RelayCredentials != nil {
			opts.RelayCreds = *m.RelayCredentials
		}
		opts.RelayRebind = consoleapi.NewClient(m.ConsoleURL, m.ConsoleTLSPin).RelayRebinder(dev, m.ConsoleID, ep.RelayID)
	}
	return opts, nil
}
