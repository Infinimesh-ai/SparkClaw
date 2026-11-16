package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeenroll"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
)

type nodeConnectionDescriptor = iscpnodeenroll.Descriptor

// enrollNode consumes exactly one private stdin line. It persists the native
// identity before network mutation and writes a pending independent profile;
// the caller verifies native business readiness before selecting that profile.
func enrollNode(ctx context.Context, input io.Reader, output io.Writer, directory, backend, keyringService, installation string) error {
	if backend != iscpbridge.IdentityKeyBackendKeyring && backend != iscpbridge.IdentityKeyBackendFile {
		return errors.New("enrollment requires private absolute directory, supported key backend and installation UUID")
	}
	scanner := bufio.NewScanner(io.LimitReader(input, iscpnodeissuer.MaxCredentialBytes*2))
	scanner.Buffer(make([]byte, 4096), iscpnodeissuer.MaxCredentialBytes*2)
	if !scanner.Scan() {
		return errors.New("missing enrollment credential")
	}
	encoded := scanner.Text()
	if scanner.Scan() || scanner.Err() != nil {
		return errors.New("enrollment accepts exactly one credential line")
	}
	result, err := iscpnodeenroll.Enroll(ctx, encoded, iscpnodeenroll.Options{Directory: directory, KeyBackend: backend, KeyringService: keyringService, InstallationID: installation, DeviceIDPrefix: "sparkx", HardwareClass: "desktop"})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		IPCVersion  int                      `json:"ipc_version"`
		Type        string                   `json:"type"`
		ProfilePath string                   `json:"profile_path"`
		Descriptor  nodeConnectionDescriptor `json:"descriptor"`
	}{1, "enrollment", result.ProfilePath, result.Descriptor})
}

func newHelperUUID() string { return iscpnodeenroll.NewUUID() }
