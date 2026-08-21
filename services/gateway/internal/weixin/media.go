package weixin

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

const (
	defaultWeixinCDNBaseURL = weixinproto.DefaultCDNBaseURL
	maxWeixinImageBytes     = 12 << 20
	maxWeixinFileBytes      = 50 << 20
)

type cdnMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param"`
	AESKey            string `json:"aes_key"`
	EncryptType       int    `json:"encrypt_type"`
	FullURL           string `json:"full_url"`
}

type imageItem struct {
	Media       cdnMedia `json:"media"`
	ThumbMedia  cdnMedia `json:"thumb_media"`
	AESKeyHex   string   `json:"aeskey"`
	URL         string   `json:"url"`
	MidSize     int      `json:"mid_size"`
	ThumbSize   int      `json:"thumb_size"`
	ThumbHeight int      `json:"thumb_height"`
	ThumbWidth  int      `json:"thumb_width"`
	HDSize      int      `json:"hd_size"`
}

type fileItem struct {
	Media    cdnMedia `json:"media"`
	FileName string   `json:"file_name"`
	MD5      string   `json:"md5"`
	Len      string   `json:"len"`
}

type MediaAdapter struct {
	cfg    config.Config
	store  MediaRepository
	client *http.Client
}

type MediaRepository interface {
	store.SessionRepository
	store.ArtifactMetadataRepository
	store.AuditRepository
}

func NewMediaAdapter(cfg config.Config, st MediaRepository) *MediaAdapter {
	return &MediaAdapter{
		cfg:    cfg,
		store:  st,
		client: &http.Client{Timeout: 45 * time.Second},
	}
}

func (a *MediaAdapter) DownloadInboundImage(ctx context.Context, binding app.NotificationBinding, img imageItem, sessionID, nameSeed string) (app.MessageAttachment, error) {
	if strings.TrimSpace(img.Media.EncryptQueryParam) == "" && strings.TrimSpace(img.Media.FullURL) == "" {
		return app.MessageAttachment{}, errors.New("weixin image item missing CDN reference")
	}
	raw, err := a.downloadCDNBytes(ctx, binding, img.Media)
	if err != nil {
		return app.MessageAttachment{}, err
	}
	if len(raw) > maxWeixinImageBytes+aes.BlockSize {
		return app.MessageAttachment{}, fmt.Errorf("weixin image encrypted payload exceeds limit: %d bytes", len(raw))
	}
	content := raw
	if hasImageAESKey(img) {
		key, err := imageAESKey(img)
		if err != nil {
			return app.MessageAttachment{}, err
		}
		content, err = weixinproto.DecryptAESECBPKCS7(raw, key)
		if err != nil {
			return app.MessageAttachment{}, err
		}
	}
	if len(content) == 0 {
		return app.MessageAttachment{}, errors.New("weixin image decrypted to empty content")
	}
	if len(content) > maxWeixinImageBytes {
		return app.MessageAttachment{}, fmt.Errorf("weixin image exceeds current inspect limit: %d bytes", len(content))
	}
	contentType := http.DetectContentType(content)
	if !supportedWeixinImageType(contentType) {
		return app.MessageAttachment{}, fmt.Errorf("unsupported weixin image content type %q", contentType)
	}
	relPath, absPath, err := a.writeUpload(ctx, content, contentType, sessionID, nameSeed)
	if err != nil {
		return app.MessageAttachment{}, err
	}
	now := time.Now().UTC()
	object := app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        "weixin_image_upload",
		SessionID:   sessionID,
		Backend:     "workspace",
		Key:         relPath,
		URI:         "workspace://" + filepath.ToSlash(relPath),
		Path:        absPath,
		ContentType: contentType,
		Bytes:       len(content),
		CreatedAt:   now,
	}
	if a.store != nil {
		stored, err := a.store.SaveArtifactObject(ctx, object)
		if err != nil {
			_ = os.Remove(absPath)
			return app.MessageAttachment{}, fmt.Errorf("save weixin image metadata: %w", err)
		}
		object = stored
		recordAudit(ctx, a.store, app.AuditEvent{
			SessionID: sessionID,
			Actor:     "gateway",
			Type:      "weixin.image.downloaded",
			Summary:   relPath,
			Fields: map[string]any{
				"binding_id":   binding.ID,
				"artifact_id":  object.ID,
				"rel_path":     relPath,
				"content_type": contentType,
				"bytes":        len(content),
			},
		})
	}
	sum := sha256.Sum256(content)
	return app.MessageAttachment{
		ArtifactID:  object.ID,
		Name:        filepath.Base(relPath),
		RelPath:     filepath.ToSlash(relPath),
		URI:         object.URI,
		ContentType: contentType,
		Bytes:       len(content),
		SHA256:      hex.EncodeToString(sum[:]),
		Source:      "weixin_inbound",
	}, nil
}

func (a *MediaAdapter) DownloadInboundFile(ctx context.Context, binding app.NotificationBinding, file fileItem, sessionID, nameSeed string) (app.MessageAttachment, error) {
	if strings.TrimSpace(file.Media.EncryptQueryParam) == "" && strings.TrimSpace(file.Media.FullURL) == "" {
		return app.MessageAttachment{}, errors.New("weixin file item missing CDN reference")
	}
	raw, err := a.downloadCDNBytesWithLimit(ctx, binding, file.Media, maxWeixinFileBytes+aes.BlockSize+1)
	if err != nil {
		return app.MessageAttachment{}, err
	}
	if len(raw) > maxWeixinFileBytes+aes.BlockSize {
		return app.MessageAttachment{}, fmt.Errorf("weixin file encrypted payload exceeds limit: %d bytes", len(raw))
	}
	content := raw
	if strings.TrimSpace(file.Media.AESKey) != "" {
		key, err := parseWeixinAESKey(file.Media.AESKey)
		if err != nil {
			return app.MessageAttachment{}, err
		}
		content, err = weixinproto.DecryptAESECBPKCS7(raw, key)
		if err != nil {
			return app.MessageAttachment{}, err
		}
	}
	if len(content) == 0 {
		return app.MessageAttachment{}, errors.New("weixin file decrypted to empty content")
	}
	if len(content) > maxWeixinFileBytes {
		return app.MessageAttachment{}, fmt.Errorf("weixin file exceeds current upload limit: %d bytes", len(content))
	}
	contentType := fileContentType(file.FileName, content)
	relPath, absPath, err := a.writeWorkspaceUpload(ctx, content, contentType, sessionID, nameSeed, file.FileName)
	if err != nil {
		return app.MessageAttachment{}, err
	}
	now := time.Now().UTC()
	object := app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        "weixin_file_upload",
		SessionID:   sessionID,
		Backend:     "workspace",
		Key:         relPath,
		URI:         "workspace://" + filepath.ToSlash(relPath),
		Path:        absPath,
		ContentType: contentType,
		Bytes:       len(content),
		CreatedAt:   now,
	}
	if a.store != nil {
		stored, err := a.store.SaveArtifactObject(ctx, object)
		if err != nil {
			_ = os.Remove(absPath)
			return app.MessageAttachment{}, fmt.Errorf("save weixin file metadata: %w", err)
		}
		object = stored
		recordAudit(ctx, a.store, app.AuditEvent{
			SessionID: sessionID,
			Actor:     "gateway",
			Type:      "weixin.file.downloaded",
			Summary:   relPath,
			Fields: map[string]any{
				"binding_id":   binding.ID,
				"artifact_id":  object.ID,
				"rel_path":     relPath,
				"content_type": contentType,
				"bytes":        len(content),
				"file_name":    file.FileName,
				"md5":          file.MD5,
			},
		})
	}
	sum := sha256.Sum256(content)
	return app.MessageAttachment{
		ArtifactID:  object.ID,
		Name:        filepath.Base(relPath),
		RelPath:     filepath.ToSlash(relPath),
		URI:         object.URI,
		ContentType: contentType,
		Bytes:       len(content),
		SHA256:      hex.EncodeToString(sum[:]),
		Source:      "weixin_inbound",
	}, nil
}

func (a *MediaAdapter) downloadCDNBytes(ctx context.Context, binding app.NotificationBinding, media cdnMedia) ([]byte, error) {
	return a.downloadCDNBytesWithLimit(ctx, binding, media, maxWeixinImageBytes+aes.BlockSize+1)
}

func (a *MediaAdapter) downloadCDNBytesWithLimit(ctx context.Context, binding app.NotificationBinding, media cdnMedia, limit int64) ([]byte, error) {
	downloadURL := strings.TrimSpace(media.FullURL)
	if downloadURL == "" {
		param := strings.TrimSpace(media.EncryptQueryParam)
		if param == "" {
			return nil, errors.New("weixin CDN media reference is empty")
		}
		base := strings.TrimRight(strings.TrimSpace(a.cfg.Tools.Notifications.Channels["weixin"].CDNBaseURL), "/")
		if base == "" {
			base = strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
		}
		if base == "" || strings.Contains(base, "ilinkai.weixin.qq.com") {
			base = defaultWeixinCDNBaseURL
		}
		downloadURL = base + "/download?encrypted_query_param=" + url.QueryEscape(param)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("weixin CDN download returned HTTP %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	if limit <= 0 {
		limit = maxWeixinImageBytes + aes.BlockSize + 1
	}
	if _, err := io.Copy(&buf, io.LimitReader(resp.Body, limit)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (a *MediaAdapter) writeUpload(ctx context.Context, content []byte, contentType, sessionID, nameSeed string) (string, string, error) {
	root, err := a.workspaceRootForSession(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if root == "" {
		return "", "", errors.New("workspace root is not configured")
	}
	sum := sha256.Sum256(content)
	seed := cleanMediaName(nameSeed)
	if seed == "" {
		seed = hex.EncodeToString(sum[:])[:12]
	}
	ext := imageExtension(contentType)
	relPath := filepath.Join("media", time.Now().UTC().Format("20060102"), app.NewID("wximg")+"-"+seed+ext)
	absPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(absPath, content, 0o644); err != nil {
		return "", "", err
	}
	return filepath.ToSlash(relPath), absPath, nil
}

func (a *MediaAdapter) writeWorkspaceUpload(ctx context.Context, content []byte, contentType, sessionID, nameSeed, originalName string) (string, string, error) {
	root, err := a.workspaceRootForSession(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if root == "" {
		return "", "", errors.New("workspace root is not configured")
	}
	sum := sha256.Sum256(content)
	safeName := cleanUploadFileName(originalName)
	if safeName == "" {
		seed := cleanMediaName(nameSeed)
		if seed == "" {
			seed = hex.EncodeToString(sum[:])[:12]
		}
		safeName = seed + extensionForUpload(contentType, originalName)
	}
	relPath := filepath.Join("uploads", time.Now().UTC().Format("20060102"), app.NewID("wxfile")+"-"+safeName)
	absPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(absPath, content, 0o644); err != nil {
		return "", "", err
	}
	return filepath.ToSlash(relPath), absPath, nil
}

func (a *MediaAdapter) workspaceRootForSession(ctx context.Context, sessionID string) (string, error) {
	if a.store != nil && strings.TrimSpace(sessionID) != "" {
		session, ok, err := a.store.GetSession(ctx, sessionID)
		if err != nil {
			return "", fmt.Errorf("resolve Weixin media session: %w", err)
		}
		if ok && strings.TrimSpace(session.WorkspaceRoot) != "" {
			return strings.TrimSpace(session.WorkspaceRoot), nil
		}
	}
	return strings.TrimSpace(a.cfg.Workspaces.DefaultRoot), nil
}

func fileContentType(name string, content []byte) string {
	if ext := strings.ToLower(filepath.Ext(name)); ext != "" {
		if value := mime.TypeByExtension(ext); value != "" {
			return value
		}
	}
	if len(content) > 0 {
		return http.DetectContentType(content)
	}
	return "application/octet-stream"
}

func extensionForUpload(contentType, originalName string) string {
	if ext := strings.ToLower(filepath.Ext(originalName)); ext != "" {
		return ext
	}
	extensions, err := mime.ExtensionsByType(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if err == nil && len(extensions) > 0 {
		return extensions[0]
	}
	return ".bin"
}

func imageAESKey(img imageItem) ([]byte, error) {
	if hexKey := strings.TrimSpace(img.AESKeyHex); hexKey != "" {
		key, err := hex.DecodeString(hexKey)
		if err != nil {
			return nil, fmt.Errorf("invalid image_item aeskey: %w", err)
		}
		if len(key) != aes.BlockSize {
			return nil, fmt.Errorf("image_item aeskey must be 16 bytes, got %d", len(key))
		}
		return key, nil
	}
	return parseWeixinAESKey(img.Media.AESKey)
}

func hasImageAESKey(img imageItem) bool {
	return strings.TrimSpace(img.AESKeyHex) != "" || strings.TrimSpace(img.Media.AESKey) != ""
}

func parseWeixinAESKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("weixin media aes_key is missing")
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid weixin media aes_key: %w", err)
	}
	if len(decoded) == aes.BlockSize {
		return decoded, nil
	}
	if len(decoded) == 32 {
		text := string(decoded)
		if isHex32(text) {
			key, err := hex.DecodeString(text)
			if err != nil {
				return nil, err
			}
			return key, nil
		}
	}
	return nil, fmt.Errorf("weixin media aes_key decoded to %d bytes, expected 16 raw bytes or 32 hex chars", len(decoded))
}

func randomAESKey() ([]byte, error) {
	key := make([]byte, aes.BlockSize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

func aesEcbPaddedSize(size int) int {
	return ((size / aes.BlockSize) + 1) * aes.BlockSize
}

func supportedWeixinImageType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func imageExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func cleanMediaName(value string) string {
	value = strings.TrimSpace(value)
	value = filepath.Base(value)
	value = strings.TrimSuffix(value, filepath.Ext(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		}
		if b.Len() >= 48 {
			break
		}
	}
	return b.String()
}

func cleanUploadFileName(value string) string {
	value = strings.TrimSpace(value)
	value = filepath.Base(value)
	if value == "." || value == string(filepath.Separator) {
		return ""
	}
	ext := filepath.Ext(value)
	base := strings.TrimSuffix(value, ext)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r > 127:
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	name := strings.Trim(b.String(), ".-_ ")
	if name == "" {
		return ""
	}
	ext = strings.ToLower(ext)
	if len(ext) > 16 {
		ext = ""
	}
	return name + ext
}

func isHex32(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
