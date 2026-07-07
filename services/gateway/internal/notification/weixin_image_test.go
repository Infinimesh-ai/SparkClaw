package notification

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestSendWeixinImageUploadsCDNAndSendsImageItem(t *testing.T) {
	var sawUpload bool
	var sentContext string
	var sentCaption string
	var sentImageParam string
	var sentAESKey string
	var sentMidSize float64
	var sendMessageCalls int

	var serverURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getuploadurl":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["media_type"].(float64) != 1 {
				t.Fatalf("expected image media_type, got %#v", payload["media_type"])
			}
			if payload["no_need_thumb"].(bool) != true {
				t.Fatalf("expected no_need_thumb true, got %#v", payload["no_need_thumb"])
			}
			_, _ = w.Write([]byte(`{"upload_full_url":"` + serverURL + `/upload"}`))
		case "/upload":
			sawUpload = true
			if r.Method != http.MethodPost {
				t.Fatalf("expected CDN upload method POST, got %s", r.Method)
			}
			if r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Fatalf("unexpected upload content type: %q", r.Header.Get("Content-Type"))
			}
			w.Header().Set("x-encrypted-param", "download-param-1")
			_, _ = w.Write([]byte(`ok`))
		case "/ilink/bot/sendmessage":
			sendMessageCalls++
			var payload struct {
				Msg struct {
					ContextToken string `json:"context_token"`
					ItemList     []struct {
						Type     int `json:"type"`
						TextItem struct {
							Text string `json:"text"`
						} `json:"text_item"`
						ImageItem struct {
							Media struct {
								EncryptQueryParam string `json:"encrypt_query_param"`
								AESKey            string `json:"aes_key"`
							} `json:"media"`
							MidSize float64 `json:"mid_size"`
						} `json:"image_item"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sentContext = payload.Msg.ContextToken
			if len(payload.Msg.ItemList) != 1 {
				t.Fatalf("expected exactly one message item per sendmessage, got %#v", payload.Msg.ItemList)
			}
			switch payload.Msg.ItemList[0].Type {
			case 1:
				sentCaption = payload.Msg.ItemList[0].TextItem.Text
			case 2:
				sentImageParam = payload.Msg.ItemList[0].ImageItem.Media.EncryptQueryParam
				sentAESKey = payload.Msg.ItemList[0].ImageItem.Media.AESKey
				sentMidSize = payload.Msg.ItemList[0].ImageItem.MidSize
			default:
				t.Fatalf("unexpected item type: %#v", payload.Msg.ItemList[0])
			}
			_, _ = w.Write([]byte(`{"ret":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	serverURL = ts.URL

	imagePath := filepath.Join(t.TempDir(), "tiny.png")
	if err := os.WriteFile(imagePath, tinyNotificationPNG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.NotificationChannelConfig{
		Enabled:    true,
		Provider:   "openclaw-weixin-qr",
		BaseURL:    ts.URL,
		CDNBaseURL: ts.URL,
		Token:      "bot-token",
	}
	result, err := SendWeixinImage(t.Context(), store.NewMemoryStore(), cfg, "wx-user-1", "ctx-1", "", ts.URL, imagePath, "这是一张图片", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "sent" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !sawUpload {
		t.Fatal("expected CDN upload")
	}
	if sendMessageCalls != 2 {
		t.Fatalf("expected caption and image to be sent as two sendmessage calls, got %d", sendMessageCalls)
	}
	if sentContext != "ctx-1" || sentCaption != "这是一张图片" {
		t.Fatalf("unexpected sendmessage context/caption: %q %q", sentContext, sentCaption)
	}
	if sentImageParam != "download-param-1" {
		t.Fatalf("unexpected image download param: %q", sentImageParam)
	}
	decodedAESKey, err := base64.StdEncoding.DecodeString(sentAESKey)
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{32}$`).Match(decodedAESKey) {
		t.Fatalf("expected base64-encoded 32-char hex aes key, got %q decoded=%q err=%v", sentAESKey, decodedAESKey, err)
	}
	if sentMidSize <= 0 {
		t.Fatalf("expected mid_size, got %v", sentMidSize)
	}
}

func TestSendWeixinFileUploadsCDNAndSendsFileItem(t *testing.T) {
	var sawUpload bool
	var sentContext string
	var sentCaption string
	var sentFileParam string
	var sentAESKey string
	var sentFileName string
	var sentLen string
	var sendMessageCalls int

	var serverURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getuploadurl":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["media_type"].(float64) != 3 {
				t.Fatalf("expected file media_type, got %#v", payload["media_type"])
			}
			_, _ = w.Write([]byte(`{"upload_full_url":"` + serverURL + `/upload"}`))
		case "/upload":
			sawUpload = true
			w.Header().Set("x-encrypted-param", "file-download-param-1")
			_, _ = w.Write([]byte(`ok`))
		case "/ilink/bot/sendmessage":
			sendMessageCalls++
			var payload struct {
				Msg struct {
					ContextToken string `json:"context_token"`
					ItemList     []struct {
						Type     int `json:"type"`
						TextItem struct {
							Text string `json:"text"`
						} `json:"text_item"`
						FileItem struct {
							Media struct {
								EncryptQueryParam string `json:"encrypt_query_param"`
								AESKey            string `json:"aes_key"`
							} `json:"media"`
							FileName string `json:"file_name"`
							Len      string `json:"len"`
						} `json:"file_item"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sentContext = payload.Msg.ContextToken
			if len(payload.Msg.ItemList) != 1 {
				t.Fatalf("expected exactly one message item per sendmessage, got %#v", payload.Msg.ItemList)
			}
			switch payload.Msg.ItemList[0].Type {
			case 1:
				sentCaption = payload.Msg.ItemList[0].TextItem.Text
			case 4:
				sentFileParam = payload.Msg.ItemList[0].FileItem.Media.EncryptQueryParam
				sentAESKey = payload.Msg.ItemList[0].FileItem.Media.AESKey
				sentFileName = payload.Msg.ItemList[0].FileItem.FileName
				sentLen = payload.Msg.ItemList[0].FileItem.Len
			default:
				t.Fatalf("unexpected item type: %#v", payload.Msg.ItemList[0])
			}
			_, _ = w.Write([]byte(`{"ret":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	serverURL = ts.URL

	filePath := filepath.Join(t.TempDir(), "report.docx")
	if err := os.WriteFile(filePath, []byte("fake docx bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.NotificationChannelConfig{
		Enabled:    true,
		Provider:   "openclaw-weixin-qr",
		BaseURL:    ts.URL,
		CDNBaseURL: ts.URL,
		Token:      "bot-token",
	}
	result, err := SendWeixinFile(t.Context(), store.NewMemoryStore(), cfg, "wx-user-1", "ctx-1", "", ts.URL, filePath, "报告.docx", "处理好了", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "sent" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !sawUpload {
		t.Fatal("expected CDN upload")
	}
	if sendMessageCalls != 2 {
		t.Fatalf("expected caption and file to be sent as two sendmessage calls, got %d", sendMessageCalls)
	}
	if sentContext != "ctx-1" || sentCaption != "处理好了" {
		t.Fatalf("unexpected sendmessage context/caption: %q %q", sentContext, sentCaption)
	}
	if sentFileParam != "file-download-param-1" || sentFileName != "报告.docx" || sentLen != "15" {
		t.Fatalf("unexpected file item: param=%q name=%q len=%q", sentFileParam, sentFileName, sentLen)
	}
	decodedAESKey, err := base64.StdEncoding.DecodeString(sentAESKey)
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{32}$`).Match(decodedAESKey) {
		t.Fatalf("expected base64-encoded 32-char hex aes key, got %q decoded=%q err=%v", sentAESKey, decodedAESKey, err)
	}
}

func tinyNotificationPNG(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
