package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxAPIResponseBytes = 4 << 20
	maxPhotoUploadBytes = 10 << 20
	maxFileUploadBytes  = 50 << 20
)

type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	if e == nil {
		return "Telegram API error"
	}
	if e.Code > 0 {
		return fmt.Sprintf("Telegram API error %d: %s", e.Code, e.Description)
	}
	return "Telegram API error: " + e.Description
}

type BotAPI interface {
	GetMe(context.Context) (User, error)
	GetUpdates(context.Context, int64, int) ([]Update, error)
	GetFile(context.Context, string) (File, error)
	DownloadFile(context.Context, string, string, int64) (int64, error)
	SendMessage(context.Context, int64, int64, string, *InlineKeyboardMarkup) (Message, error)
	SendChatAction(context.Context, int64, int64, string) error
	SendPhoto(context.Context, int64, int64, string, string) (Message, error)
	SendDocument(context.Context, int64, int64, string, string, string) (Message, error)
	SendVoice(context.Context, int64, int64, string, string) (Message, error)
	AnswerCallbackQuery(context.Context, string, string) error
	SetMyCommands(context.Context, []BotCommand) error
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 65 * time.Second}
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		http:    httpClient,
	}
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var result User
	err := c.callJSON(ctx, "getMe", nil, &result)
	return result, err
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	payload := map[string]any{
		"offset":          offset,
		"timeout":         timeoutSeconds,
		"allowed_updates": []string{"message", "callback_query"},
	}
	var result []Update
	err := c.callJSON(ctx, "getUpdates", payload, &result)
	return result, err
}

func (c *Client) GetFile(ctx context.Context, fileID string) (File, error) {
	var result File
	err := c.callJSON(ctx, "getFile", map[string]any{"file_id": fileID}, &result)
	return result, err
}

func (c *Client) DownloadFile(ctx context.Context, filePath, destination string, maxBytes int64) (int64, error) {
	if strings.TrimSpace(filePath) == "" {
		return 0, errors.New("Telegram file path is empty")
	}
	if maxBytes <= 0 {
		return 0, errors.New("Telegram download limit must be positive")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.fileEndpoint(filePath), nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, c.transportError("download Telegram file", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("download Telegram file: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return 0, fmt.Errorf("Telegram file exceeds %d-byte limit", maxBytes)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".telegram-download-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	written, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, maxBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("write Telegram file: %w", copyErr)
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if written > maxBytes {
		return 0, fmt.Errorf("Telegram file exceeds %d-byte limit", maxBytes)
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return 0, err
	}
	return written, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID, threadID int64, message string, keyboard *InlineKeyboardMarkup) (Message, error) {
	payload := map[string]any{"chat_id": chatID, "text": message}
	if threadID != 0 {
		payload["message_thread_id"] = threadID
	}
	if keyboard != nil {
		payload["reply_markup"] = keyboard
	}
	var result Message
	err := c.callJSON(ctx, "sendMessage", payload, &result)
	return result, err
}

func (c *Client) SendChatAction(ctx context.Context, chatID, threadID int64, action string) error {
	payload := map[string]any{"chat_id": chatID, "action": action}
	if threadID != 0 {
		payload["message_thread_id"] = threadID
	}
	var result bool
	return c.callJSON(ctx, "sendChatAction", payload, &result)
}

func (c *Client) SendPhoto(ctx context.Context, chatID, threadID int64, path, caption string) (Message, error) {
	return c.sendMultipart(ctx, "sendPhoto", "photo", chatID, threadID, path, filepath.Base(path), caption, maxPhotoUploadBytes)
}

func (c *Client) SendDocument(ctx context.Context, chatID, threadID int64, path, name, caption string) (Message, error) {
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(path)
	}
	return c.sendMultipart(ctx, "sendDocument", "document", chatID, threadID, path, name, caption, maxFileUploadBytes)
}

func (c *Client) SendVoice(ctx context.Context, chatID, threadID int64, path, caption string) (Message, error) {
	return c.sendMultipart(ctx, "sendVoice", "voice", chatID, threadID, path, filepath.Base(path), caption, maxFileUploadBytes)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, message string) error {
	payload := map[string]any{"callback_query_id": callbackID}
	if strings.TrimSpace(message) != "" {
		payload["text"] = message
	}
	var result bool
	return c.callJSON(ctx, "answerCallbackQuery", payload, &result)
}

func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	var result bool
	return c.callJSON(ctx, "setMyCommands", map[string]any{"commands": commands}, &result)
}

func (c *Client) callJSON(ctx context.Context, method string, payload any, result any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodEndpoint(method), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.execute(req, result)
}

func (c *Client) sendMultipart(ctx context.Context, method, field string, chatID, threadID int64, path, name, caption string, maxBytes int64) (Message, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Message{}, err
	}
	if info.IsDir() || info.Size() > maxBytes {
		return Message{}, fmt.Errorf("Telegram upload exceeds %d-byte limit", maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return Message{}, err
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if threadID != 0 {
		_ = writer.WriteField("message_thread_id", strconv.FormatInt(threadID, 10))
	}
	if strings.TrimSpace(caption) != "" {
		_ = writer.WriteField("caption", caption)
	}
	header := make(textproto.MIMEHeader)
	name = strings.NewReplacer("\r", "_", "\n", "_", `"`, "_").Replace(filepath.Base(name))
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, name))
	header.Set("Content-Type", "application/octet-stream")
	part, err := writer.CreatePart(header)
	if err != nil {
		return Message{}, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return Message{}, err
	}
	if err := writer.Close(); err != nil {
		return Message{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodEndpoint(method), &body)
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	var result Message
	err = c.execute(req, &result)
	return result, err
}

func (c *Client) execute(req *http.Request, result any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return c.transportError("call Telegram API", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Telegram API response: %w", err)
	}
	if len(raw) > maxAPIResponseBytes {
		return errors.New("Telegram API response exceeds size limit")
	}
	var decoded apiResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("decode Telegram API response: HTTP %d", resp.StatusCode)
	}
	if !decoded.OK || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{Code: decoded.ErrorCode, Description: strings.TrimSpace(c.sanitize(decoded.Description))}
		if apiErr.Code == 0 {
			apiErr.Code = resp.StatusCode
		}
		if apiErr.Description == "" {
			apiErr.Description = http.StatusText(resp.StatusCode)
		}
		if decoded.Parameters.RetryAfter > 0 {
			apiErr.RetryAfter = time.Duration(decoded.Parameters.RetryAfter) * time.Second
		}
		return apiErr
	}
	if result == nil || len(decoded.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(decoded.Result, result); err != nil {
		return fmt.Errorf("decode Telegram API result: %w", err)
	}
	return nil
}

func (c *Client) methodEndpoint(method string) string {
	return c.baseURL + "/bot" + c.token + "/" + method
}

func (c *Client) fileEndpoint(filePath string) string {
	return c.baseURL + "/file/bot" + c.token + "/" + strings.TrimLeft(filePath, "/")
}

func (c *Client) transportError(operation string, err error) error {
	message := c.sanitize(err.Error())
	return fmt.Errorf("%s: %s", operation, message)
}

func (c *Client) sanitize(message string) string {
	if c == nil || c.token == "" {
		return message
	}
	replacer := strings.NewReplacer(
		c.token, "[REDACTED]",
		"bot"+c.token, "bot[REDACTED]",
	)
	return replacer.Replace(message)
}
