package artifact

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type Object struct {
	Backend     string `json:"backend"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	URI         string `json:"uri"`
	Path        string `json:"path,omitempty"`
	ContentType string `json:"content_type"`
	Bytes       int    `json:"bytes"`
}

type Store interface {
	Put(ctx context.Context, key string, contentType string, raw []byte) (Object, error)
	Get(ctx context.Context, key string) ([]byte, error)
}

const maxArtifactReadBytes = 64 << 20

func NewStore(cfg config.StorageConfig) Store {
	switch strings.ToLower(strings.TrimSpace(cfg.ArtifactBackend)) {
	case "", "filesystem", "local":
		return FileStore{Root: cfg.ArtifactDir, Bucket: cfg.ArtifactBucket}
	case "s3", "minio":
		return S3Store{
			Endpoint:  cfg.S3Endpoint,
			Region:    cfg.S3Region,
			Bucket:    cfg.ArtifactBucket,
			AccessKey: cfg.S3AccessKey,
			SecretKey: cfg.S3SecretKey,
			Client:    &http.Client{Timeout: 30 * time.Second},
		}
	default:
		return NotImplementedStore{Backend: cfg.ArtifactBackend}
	}
}

type FileStore struct {
	Root   string
	Bucket string
}

func (s FileStore) Put(ctx context.Context, key string, contentType string, raw []byte) (Object, error) {
	if strings.TrimSpace(s.Root) == "" {
		return Object{}, errors.New("artifact filesystem root is empty")
	}
	if strings.TrimSpace(s.Bucket) == "" {
		s.Bucket = "sparkclaw"
	}
	key = cleanKey(key)
	select {
	case <-ctx.Done():
		return Object{}, ctx.Err()
	default:
	}
	path := filepath.Join(s.Root, s.Bucket, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Object{}, err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return Object{}, err
	}
	return Object{
		Backend:     "filesystem",
		Bucket:      s.Bucket,
		Key:         key,
		URI:         "artifact://" + s.Bucket + "/" + key,
		Path:        path,
		ContentType: contentType,
		Bytes:       len(raw),
	}, nil
}

func (s FileStore) Get(ctx context.Context, key string) ([]byte, error) {
	if strings.TrimSpace(s.Root) == "" {
		return nil, errors.New("artifact filesystem root is empty")
	}
	if strings.TrimSpace(s.Bucket) == "" {
		s.Bucket = "sparkclaw"
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	file, err := os.Open(filepath.Join(s.Root, s.Bucket, cleanKey(key)))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readBounded(file)
}

type NotImplementedStore struct {
	Backend string
}

func (s NotImplementedStore) Put(context.Context, string, string, []byte) (Object, error) {
	if s.Backend == "" {
		s.Backend = "unknown"
	}
	return Object{}, errors.New("artifact backend is not implemented: " + s.Backend)
}

func (s NotImplementedStore) Get(context.Context, string) ([]byte, error) {
	if s.Backend == "" {
		s.Backend = "unknown"
	}
	return nil, errors.New("artifact backend is not implemented: " + s.Backend)
}

func cleanKey(key string) string {
	key = filepath.ToSlash(filepath.Clean(key))
	key = strings.TrimPrefix(key, "/")
	for strings.HasPrefix(key, "../") {
		key = strings.TrimPrefix(key, "../")
	}
	if key == "." || key == "" {
		return "artifact"
	}
	return key
}

type S3Store struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	Client    *http.Client
	Now       func() time.Time
}

func (s S3Store) Put(ctx context.Context, key string, contentType string, raw []byte) (Object, error) {
	if strings.TrimSpace(s.Endpoint) == "" {
		return Object{}, errors.New("s3 artifact endpoint is empty")
	}
	if strings.TrimSpace(s.Bucket) == "" {
		return Object{}, errors.New("s3 artifact bucket is empty")
	}
	if strings.TrimSpace(s.AccessKey) == "" || strings.TrimSpace(s.SecretKey) == "" {
		return Object{}, errors.New("s3 artifact credentials are empty")
	}
	if s.Region == "" {
		s.Region = "us-east-1"
	}
	if s.Client == nil {
		s.Client = &http.Client{Timeout: 30 * time.Second}
	}
	key = cleanKey(key)
	endpoint := strings.TrimRight(s.Endpoint, "/")
	url := endpoint + "/" + pathEscape(s.Bucket) + "/" + pathEscapeKey(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(raw))
	if err != nil {
		return Object{}, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Length", fmt.Sprint(len(raw)))
	payloadHash := sha256Hex(raw)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	signV4(req, s.Region, s.AccessKey, s.SecretKey, payloadHash, now)
	resp, err := s.Client.Do(req)
	if err != nil {
		return Object{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Object{}, fmt.Errorf("s3 put object returned HTTP %d", resp.StatusCode)
	}
	return Object{
		Backend:     "s3",
		Bucket:      s.Bucket,
		Key:         key,
		URI:         "s3://" + s.Bucket + "/" + key,
		ContentType: contentType,
		Bytes:       len(raw),
	}, nil
}

func (s S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	if strings.TrimSpace(s.Endpoint) == "" {
		return nil, errors.New("s3 artifact endpoint is empty")
	}
	if strings.TrimSpace(s.Bucket) == "" {
		return nil, errors.New("s3 artifact bucket is empty")
	}
	if strings.TrimSpace(s.AccessKey) == "" || strings.TrimSpace(s.SecretKey) == "" {
		return nil, errors.New("s3 artifact credentials are empty")
	}
	if s.Region == "" {
		s.Region = "us-east-1"
	}
	if s.Client == nil {
		s.Client = &http.Client{Timeout: 30 * time.Second}
	}
	key = cleanKey(key)
	endpoint := strings.TrimRight(s.Endpoint, "/")
	objectURL := endpoint + "/" + pathEscape(s.Bucket) + "/" + pathEscapeKey(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, objectURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	payloadHash := sha256Hex(nil)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	signV4(req, s.Region, s.AccessKey, s.SecretKey, payloadHash, now)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("s3 get object returned HTTP %d", resp.StatusCode)
	}
	return readBounded(resp.Body)
}

func readBounded(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxArtifactReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxArtifactReadBytes {
		return nil, fmt.Errorf("artifact exceeds read limit of %d bytes", maxArtifactReadBytes)
	}
	return raw, nil
}

func signV4(req *http.Request, region, accessKey, secretKey, payloadHash string, now time.Time) {
	date := now.Format("20060102")
	scope := date + "/" + region + "/s3/aws4_request"
	canonicalHeaders, signedHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format("20060102T150405Z"),
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte("AWS4"+secretKey), date), region), "s3"), "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func canonicalHeaders(req *http.Request) (string, string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string]string{
		"host":                 host,
		"content-type":         req.Header.Get("Content-Type"),
		"x-amz-content-sha256": req.Header.Get("X-Amz-Content-Sha256"),
		"x-amz-date":           req.Header.Get("X-Amz-Date"),
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := strings.Builder{}
	for _, key := range keys {
		lines.WriteString(key)
		lines.WriteByte(':')
		lines.WriteString(strings.TrimSpace(headers[key]))
		lines.WriteByte('\n')
	}
	return lines.String(), strings.Join(keys, ";")
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func pathEscape(value string) string {
	return url.PathEscape(value)
}

func pathEscapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = pathEscape(part)
	}
	return strings.Join(parts, "/")
}
