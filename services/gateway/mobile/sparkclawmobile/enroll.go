package sparkclawmobile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeenroll"
)

// SecretStore is implemented by the app with its native secret storage
// (Android Keystore-wrapped preferences, iOS Keychain). Get must return an
// error when the entry is missing. Values are opaque base64url strings.
type SecretStore interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

// DefaultKeyService is the secret-store service name used for the device key.
const DefaultKeyService = "ai.infinimesh.sparkclaw.iscp"

// SetSecretStore registers the app's native secret store. It must be called
// before Enroll or Open.
func SetSecretStore(store SecretStore) {
	if store == nil {
		iscpbridge.RegisterHostSecretStore(nil)
		return
	}
	iscpbridge.RegisterHostSecretStore(store)
}

// NewInstallationID returns a fresh installation UUID. Persist it with the
// enrollment directory and reuse it for every retry of the same enrollment.
func NewInstallationID() string { return iscpnodeenroll.NewUUID() }

// Enroll redeems one connection credential (canonical or compact QR form)
// through the node issuer. directory must be an absolute app-private path.
// Retrying with the same credential, directory and installationID resumes the
// original operation instead of creating a second device. The returned JSON is
// {"profile_path","helper_config","descriptor":{...}}; pass helper_config to
// Open.
func Enroll(credential, directory, installationID string, timeoutMillis int64) (string, error) {
	canonical, err := NormalizeCredential(credential)
	if err != nil {
		return "", err
	}
	if timeoutMillis <= 0 || timeoutMillis > 180000 {
		timeoutMillis = 90000
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMillis)*time.Millisecond)
	defer cancel()
	result, err := iscpnodeenroll.Enroll(ctx, canonical, iscpnodeenroll.Options{
		Directory:      directory,
		KeyBackend:     iscpbridge.IdentityKeyBackendHost,
		KeyringService: DefaultKeyService,
		InstallationID: strings.ToLower(strings.TrimSpace(installationID)),
		DeviceIDPrefix: "phone",
		HardwareClass:  "phone",
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", errors.New("enrollment timed out; retry with the same credential")
		}
		return "", err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
