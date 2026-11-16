package iscpbridge

import (
	"errors"
	"strings"
	"sync"
)

// IdentityKeyBackendHost keeps the device private key in a store supplied by
// the embedding application (for example Android Keystore or iOS Keychain in
// the mobile library). The key never touches a plain file; only the host can
// unwrap it, exactly like the desktop's system keyring backend.
const IdentityKeyBackendHost = "host"

// HostSecretStore is the embedding application's native secret store. Get must
// return an error when the entry does not exist.
type HostSecretStore interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

var hostSecretStore struct {
	sync.RWMutex
	store HostSecretStore
}

// RegisterHostSecretStore installs the process-wide host secret store. Passing
// nil disables the host backend again.
func RegisterHostSecretStore(store HostSecretStore) {
	hostSecretStore.Lock()
	defer hostSecretStore.Unlock()
	hostSecretStore.store = store
}

func currentHostSecretStore() (HostSecretStore, error) {
	hostSecretStore.RLock()
	defer hostSecretStore.RUnlock()
	if hostSecretStore.store == nil {
		return nil, errors.New("host secret store is not registered")
	}
	return hostSecretStore.store, nil
}

// ValidIdentityKeyBackend reports whether backend names a supported device key
// backend. The host backend is accepted only after a store is registered.
func ValidIdentityKeyBackend(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case IdentityKeyBackendKeyring, IdentityKeyBackendFile:
		return true
	case IdentityKeyBackendHost:
		_, err := currentHostSecretStore()
		return err == nil
	default:
		return false
	}
}

func hostService(service string) string {
	if strings.TrimSpace(service) == "" {
		return DefaultIdentityKeyringService
	}
	return service
}
