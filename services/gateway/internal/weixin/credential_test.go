package weixin

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const weixinTestCredentialKind = "openclaw-weixin-bot-token"

func newWeixinTestVault(t *testing.T, repository store.CredentialRepository) credential.CredentialVault {
	t.Helper()
	vault := credential.New(repository, credential.Options{Key: strings.Repeat("w", 32)})
	if err := vault.Ready(); err != nil {
		t.Fatal(err)
	}
	return vault
}

func sealWeixinTestCredential(t *testing.T, vault credential.CredentialVault, bindingID, token string) string {
	t.Helper()
	ref, err := vault.Seal(t.Context(), bindingID, weixinTestCredentialKind, []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
