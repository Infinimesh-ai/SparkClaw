package iscpworkbench

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeclient"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

type grantLifecycle interface {
	Current(context.Context) (trust.Grant, error)
	Renew(context.Context, trust.Grant) (trust.Grant, error)
	HasPendingRenewal() bool
	CommitRenewal() error
	AcceptGrant(trust.Grant) error
	CheckAuthorization(context.Context) error
	AuthorizationPolicy(context.Context) (iscpauth.Policy, error)
	DeleteAuthorization(context.Context, string, uint64) (iscpauth.DeletionReceipt, error)
	AuthorizationDeletionReceipt(context.Context, string, uint64) (iscpauth.DeletionReceipt, error)
	UsesStandingAuthorization() bool
}

func newNodeLifecycle(cfg Config, m material) (*iscpnodeclient.Client, error) {
	if cfg.Role != RoleInitiator || cfg.IssuerControlFile == "" || cfg.Binding == nil {
		return nil, errors.New("node-managed lifecycle requires its original subject and binding")
	}
	var stored iscpnodeclient.StoredConfig
	if err := readJSONFile(cfg.IssuerControlFile, &stored, true); err != nil {
		return nil, errors.New("load node issuer control identity")
	}
	if stored.Origin != m.enrollment.RelayBaseURL || stored.TLSCertificatePEM != m.enrollment.TLSCertificatePEM || stored.RelayID != m.enrollment.RelayID || stored.Binding.DeploymentID != cfg.Binding.DeploymentID || stored.Binding.OwnerID != cfg.Binding.OwnerID || stored.Binding.ClientID != cfg.Binding.ClientID || stored.Revision != m.grant.RevocationEpoch || stored.IssuerIdentity.DeviceID != m.issuer.DeviceID || stored.IssuerIdentity.PublicKey.Public != m.issuer.PublicKey.Public || stored.RelaySignerIdentity.PublicKey.Public != m.enrollment.RelaySignerIdentity.PublicKey.Public || stored.ResponderIdentity.DeviceID != m.peer.DeviceID || stored.ResponderIdentity.PublicKey.Public != m.peer.PublicKey.Public {
		return nil, errors.New("node lifecycle pins or original binding differ")
	}
	return iscpnodeclient.NewStored(stored, m.device, filepath.Join(cfg.IdentityDirectory, "node-control"), m.grant)
}

func recoverNodeRuntime(ctx context.Context, path string, cfg Config, m material) (Config, error) {
	if cfg.Role != RoleInitiator {
		return Config{}, errors.New("node service-peer recovery requires its operator adapter")
	}
	client, err := newNodeLifecycle(cfg, m)
	if err != nil {
		return Config{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := client.RecoverTransport(ctx)
	if err != nil {
		return Config{}, err
	}
	if result.Grant == nil || result.RelayCredentials == nil {
		return Config{}, errors.New("node recovery did not return bound Grant and transport credentials")
	}
	m.grant = *result.Grant
	if err = verifyGrant(cfg, m, time.Now().UTC()); err != nil {
		return Config{}, err
	}
	// Authenticate a fresh descriptor independently of the issuer result; the
	// Relay signer remains pinned and supplies the current validity window.
	discovery, err := iscpbridge.DiscoverNodeRelay(ctx, m.enrollment)
	if err != nil {
		return Config{}, err
	}
	updated := m.enrollment
	updated.Access = result.RelayCredentials.Access
	updated.Refresh = result.RelayCredentials.Refresh
	updated.IssuedAt = time.Now().UTC()
	updated.ExpiresAt = minTime(discovery.ExpiresAt, updated.Refresh.ExpiresAt)
	if err = updated.ValidateCredentials(time.Now().UTC()); err != nil {
		return Config{}, err
	}
	if err = saveCurrentGrant(cfg.GrantFile, m.grant); err != nil {
		return Config{}, err
	}
	if err = iscpbridge.SaveEnrollment(cfg.EnrollmentFile, updated); err != nil {
		return Config{}, err
	}
	if err = client.AcceptGrant(m.grant); err != nil {
		return Config{}, err
	}
	if err = client.CommitRenewal(); err != nil {
		return Config{}, err
	}
	return LoadConfig(path)
}
func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
