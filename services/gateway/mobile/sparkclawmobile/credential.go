// Package sparkclawmobile is the gomobile surface that lets Android and iOS
// apps join SparkClaw through the work2 node issuer, exactly like SparkX
// desktop: redeem one connection credential, then keep a workbench endpoint
// whose Grant renews every 15 minutes and recovers after expiry without a new
// invitation.
//
// Every exported identifier uses gomobile-compatible types only.
package sparkclawmobile

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
)

const (
	// CredentialPrefix is the canonical connection credential prefix.
	CredentialPrefix = "sparkclaw-iscp-connect-v1."
	// CompactCredentialPrefix marks the QR form: raw-deflate of the canonical
	// JSON, base64url without padding. It is a lossless transport encoding; the
	// signature is still verified over the canonical credential.
	CompactCredentialPrefix = "sparkclaw-iscp-connect-v1z."
)

// NormalizeCredential accepts pasted or scanned text in either form and
// returns the canonical credential after full format and signature checks.
// Expiry is not enforced here so a pending enrollment can resume.
func NormalizeCredential(text string) (string, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, CompactCredentialPrefix) {
		compressed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(text, CompactCredentialPrefix))
		if err != nil || len(compressed) > iscpnodeissuer.MaxCredentialBytes {
			return "", errors.New("invalid compact connection credential")
		}
		reader := flate.NewReader(bytes.NewReader(compressed))
		raw, err := io.ReadAll(io.LimitReader(reader, iscpnodeissuer.MaxCredentialBytes+1))
		_ = reader.Close()
		if err != nil || len(raw) > iscpnodeissuer.MaxCredentialBytes {
			return "", errors.New("invalid compact connection credential")
		}
		text = CredentialPrefix + base64.RawURLEncoding.EncodeToString(raw)
	}
	if !strings.HasPrefix(text, CredentialPrefix) {
		return "", errors.New("not a SparkClaw connection credential")
	}
	if _, err := iscpnodeissuer.ParseCredential(text, time.Now().UTC(), true); err != nil {
		return "", errors.New("invalid SparkClaw connection credential")
	}
	return text, nil
}

// CompactCredential converts a canonical credential to the QR form.
func CompactCredential(credential string) (string, error) {
	credential, err := NormalizeCredential(credential)
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(credential, CredentialPrefix))
	if err != nil {
		return "", errors.New("invalid SparkClaw connection credential")
	}
	var out bytes.Buffer
	writer, _ := flate.NewWriter(&out, flate.BestCompression)
	_, _ = writer.Write(raw)
	if err := writer.Close(); err != nil {
		return "", err
	}
	return CompactCredentialPrefix + base64.RawURLEncoding.EncodeToString(out.Bytes()), nil
}

// CredentialExpiresAtUnixMillis lets the app tell an expired invitation apart
// before contacting the network.
func CredentialExpiresAtUnixMillis(credential string) (int64, error) {
	credential, err := NormalizeCredential(credential)
	if err != nil {
		return 0, err
	}
	parsed, err := iscpnodeissuer.ParseCredential(credential, time.Now().UTC(), true)
	if err != nil {
		return 0, errors.New("invalid SparkClaw connection credential")
	}
	return parsed.ExpiresAt.UnixMilli(), nil
}
