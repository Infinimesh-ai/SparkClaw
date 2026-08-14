// Package weixinproto holds the protocol constants, request headers, and
// payload crypto shared by every package that talks to the iLink bot API
// (weixin inbound sync, notification outbound, binding handshake). The values
// mirror the provider's reverse-engineered wire protocol; when the provider
// bumps a version, change it here once.
package weixinproto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	// DefaultBaseURL is the iLink bot API host used when a binding or channel
	// does not configure its own.
	DefaultBaseURL = "https://ilinkai.weixin.qq.com"
	// DefaultCDNBaseURL is the media upload/download host.
	DefaultCDNBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"
	// ClientVersion is the iLink-App-ClientVersion header the provider expects.
	ClientVersion = "132102"
	// ChannelVersion is the channel_version field sent in request payloads.
	ChannelVersion = "2.4.6"
	// AppID is the iLink-App-Id header value.
	AppID = "bot"
	// AuthorizationType is the AuthorizationType header value.
	AuthorizationType = "ilink_bot_token"
	// DefaultProvider names bindings/channels that do not set a provider.
	DefaultProvider = "openclaw-weixin-compatible"
	QRProvider      = "openclaw-weixin-qr"
	QRLoginProvider = "openclaw-weixin-login-qr"
)

func IsQRLoginProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case QRProvider, QRLoginProvider:
		return true
	default:
		return false
	}
}

func IsQRLoginURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "liteapp.weixin.qq.com")
}

// SetHeaders applies the standard iLink bot API headers to a JSON request.
func SetHeaders(req *http.Request, token string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", AuthorizationType)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("iLink-App-Id", AppID)
	req.Header.Set("iLink-App-ClientVersion", ClientVersion)
	req.Header.Set("X-WECHAT-UIN", RandomUIN())
}

// RandomUIN produces the base64-encoded pseudo-UIN the provider expects in
// the X-WECHAT-UIN header.
func RandomUIN() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return base64.StdEncoding.EncodeToString([]byte("0"))
	}
	value := int(raw[0])<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
	if value < 0 {
		value = -value
	}
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", value)))
}

// ProviderName normalizes a configured provider string.
func ProviderName(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return DefaultProvider
	}
	return provider
}
