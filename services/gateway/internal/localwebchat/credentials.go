package localwebchat

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type identity struct {
	SchemaVersion int    `json:"schema_version"`
	DeploymentID  string `json:"deployment_id"`
	ClientID      string `json:"client_id"`
	OwnerID       string `json:"owner_id"`
	ActorID       string `json:"actor_id"`
}

type credential struct {
	identity
	ClientName string `json:"client_name"`
	Token      string `json:"token"`
}

func privateDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ownedByCurrentUser(info) {
		return errors.New("local WebChat directory must be private and owned by the deployment user")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != filepath.Clean(directory) {
		return errors.New("local WebChat directory must not contain symbolic links")
	}
	return nil
}

func readCredential(runtimeDir string) (credential, error) {
	var result credential
	if err := privateDirectory(runtimeDir); err != nil {
		return result, err
	}
	filename := filepath.Join(runtimeDir, "local-webchat.json")
	info, err := os.Lstat(filename)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !ownedByCurrentUser(info) || info.Size() > 64<<10 {
		return result, errors.New("local WebChat credential must be a private regular file owned by the deployment user")
	}
	file, err := os.Open(filename)
	if err != nil {
		return result, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return result, errors.New("local WebChat credential changed while opening")
	}
	decoder := json.NewDecoder(io.LimitReader(file, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return credential{}, errors.New("invalid local WebChat credential")
	}
	if result.SchemaVersion != 1 || strings.TrimSpace(result.DeploymentID) == "" || !strings.HasPrefix(result.ClientID, "local_webchat_") || strings.TrimSpace(result.OwnerID) == "" || strings.TrimSpace(result.ClientName) == "" || len(result.Token) < 32 || len(result.Token) > 512 || strings.ContainsAny(result.Token, "\r\n\t ") {
		return credential{}, errors.New("incomplete local WebChat credential")
	}
	if result.ActorID == "" {
		result.ActorID = result.OwnerID
	}
	return result, nil
}

func privateSocket(runtimeDir string) (string, error) {
	directory := filepath.Join(runtimeDir, "local-webchat")
	if err := privateDirectory(directory); err != nil {
		return "", err
	}
	socket := filepath.Join(directory, "workbench.sock")
	info, err := os.Lstat(socket)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 || !ownedByCurrentUser(info) {
		return "", errors.New("local WebChat upstream socket must be private and owned by the deployment user")
	}
	return socket, nil
}
