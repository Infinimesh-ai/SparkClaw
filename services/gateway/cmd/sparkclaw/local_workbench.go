package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const maxDesktopClientProvisioningBytes = 64 << 10

type desktopClientProvisioning struct {
	SchemaVersion int    `json:"schema_version"`
	DeploymentID  string `json:"deployment_id"`
	ClientID      string `json:"client_id"`
	OwnerID       string `json:"owner_id"`
	ActorID       string `json:"actor_id,omitempty"`
	ClientName    string `json:"client_name"`
	Token         string `json:"token"`
}

type desktopClientRepository interface {
	store.OwnerRepository
	store.ClientRepository
}

func registerProvisionedDesktopClient(ctx context.Context, cfg config.Config, repository desktopClientRepository) error {
	path := strings.TrimSpace(cfg.Gateway.DesktopClientFile)
	if path == "" {
		return nil
	}
	if strings.TrimSpace(cfg.Gateway.DeploymentID) == "" {
		return errors.New("desktop Client provisioning requires SPARKCLAW_DEPLOYMENT_ID")
	}
	provisioning, err := loadDesktopClientProvisioning(path)
	if err != nil {
		return err
	}
	if provisioning.DeploymentID != cfg.Gateway.DeploymentID {
		return errors.New("desktop Client deployment identity does not match the Gateway")
	}
	profile, found, err := repository.GetOwnerProfileByID(ctx, provisioning.OwnerID)
	if err != nil {
		return fmt.Errorf("verify desktop Client Owner: %w", err)
	}
	if !found || profile.ID != provisioning.OwnerID {
		return errors.New("desktop Client Owner does not exist")
	}
	actorID := provisioning.ActorID
	if actorID == "" {
		actorID = provisioning.OwnerID
	}
	candidate := app.Client{
		ID: provisioning.ClientID, OwnerID: provisioning.OwnerID, ActorID: actorID,
		Name: provisioning.ClientName, TokenHash: desktopTokenHash(provisioning.Token),
	}
	saved, err := repository.RegisterClient(ctx, candidate)
	if err == nil {
		if saved.RevokedAt != nil {
			return errors.New("desktop Client is revoked")
		}
		return nil
	}
	if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
		persisted, found, readErr := repository.GetClient(ctx, candidate.ID)
		if readErr == nil && found && persisted.RevokedAt == nil &&
			persisted.OwnerID == candidate.OwnerID && persisted.ActorID == candidate.ActorID &&
			persisted.Name == candidate.Name && persisted.TokenHash == candidate.TokenHash {
			return nil
		}
	}
	if store.StoreErrorCodeOf(err) == store.StoreErrorConflict {
		return errors.New("desktop Client identity conflicts with persisted state or was revoked")
	}
	return fmt.Errorf("register desktop Client: %w", err)
}

func loadDesktopClientProvisioning(path string) (desktopClientProvisioning, error) {
	if !filepath.IsAbs(path) {
		return desktopClientProvisioning{}, errors.New("desktop Client provisioning path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return desktopClientProvisioning{}, fmt.Errorf("inspect desktop Client provisioning file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return desktopClientProvisioning{}, errors.New("desktop Client provisioning path must be a regular file")
	}
	if info.Mode().Perm() != 0o600 || !localFileOwnedByCurrentUser(info) {
		return desktopClientProvisioning{}, errors.New("desktop Client provisioning file must not be accessible by group or others")
	}
	if err := checkPrivateLocalDirectory(filepath.Dir(path)); err != nil {
		return desktopClientProvisioning{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return desktopClientProvisioning{}, fmt.Errorf("open desktop Client provisioning file: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return desktopClientProvisioning{}, errors.New("desktop Client provisioning file changed while opening")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxDesktopClientProvisioningBytes+1))
	decoder.DisallowUnknownFields()
	var provisioning desktopClientProvisioning
	if err := decoder.Decode(&provisioning); err != nil {
		return desktopClientProvisioning{}, errors.New("decode desktop Client provisioning file")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return desktopClientProvisioning{}, errors.New("desktop Client provisioning file must contain one JSON object")
	}
	provisioning.DeploymentID = strings.TrimSpace(provisioning.DeploymentID)
	provisioning.ClientID = strings.TrimSpace(provisioning.ClientID)
	provisioning.OwnerID = strings.TrimSpace(provisioning.OwnerID)
	provisioning.ActorID = strings.TrimSpace(provisioning.ActorID)
	provisioning.ClientName = strings.TrimSpace(provisioning.ClientName)
	provisioning.Token = strings.TrimSpace(provisioning.Token)
	if provisioning.SchemaVersion != 1 || provisioning.DeploymentID == "" || provisioning.ClientID == "" ||
		provisioning.OwnerID == "" || provisioning.ClientName == "" || len(provisioning.Token) < 32 || len(provisioning.Token) > 512 {
		return desktopClientProvisioning{}, errors.New("desktop Client provisioning file is incomplete")
	}
	return provisioning, nil
}

func desktopTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func checkPrivateLocalDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !localFileOwnedByCurrentUser(info) {
		return errors.New("local management directory must be private and owned by the deployment user")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != filepath.Clean(directory) {
		return errors.New("local management directory must not contain symbolic links")
	}
	return nil
}
