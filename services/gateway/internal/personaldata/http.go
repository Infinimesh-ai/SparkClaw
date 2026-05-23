package personaldata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type HTTPEmailAdapter struct {
	baseURL string
	token   string
	client  *http.Client
}

type HTTPCalendarAdapter struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewEmailAdapter(cfg config.ServiceAdapterConfig, workspaceRoot string) EmailAdapter {
	if cfg.IsHTTP() {
		return HTTPEmailAdapter{baseURL: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.Token, client: &http.Client{Timeout: 10 * time.Second}}
	}
	return FileAdapter{Root: workspaceRoot}
}

func NewCalendarAdapter(cfg config.ServiceAdapterConfig, workspaceRoot string) CalendarAdapter {
	if cfg.IsHTTP() {
		return HTTPCalendarAdapter{baseURL: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.Token, client: &http.Client{Timeout: 10 * time.Second}}
	}
	return FileAdapter{Root: workspaceRoot}
}

func (a HTTPEmailAdapter) SearchThreads(ctx context.Context, query string, maxResults int) ([]EmailThread, error) {
	var decoded struct {
		Threads []EmailThread `json:"threads"`
	}
	params := url.Values{}
	params.Set("query", query)
	if maxResults > 0 {
		params.Set("max_results", intString(maxResults))
	}
	if err := a.get(ctx, "/email/search?"+params.Encode(), &decoded); err != nil {
		return nil, err
	}
	return decoded.Threads, nil
}

func (a HTTPEmailAdapter) ReadThread(ctx context.Context, threadID string) (EmailThread, error) {
	var decoded struct {
		Thread EmailThread `json:"thread"`
	}
	if err := a.get(ctx, "/email/threads/"+url.PathEscape(threadID), &decoded); err != nil {
		return EmailThread{}, err
	}
	return decoded.Thread, nil
}

func (a HTTPEmailAdapter) SendEmail(ctx context.Context, request EmailSendRequest) (EmailSendResult, error) {
	raw, err := adapterPOST(ctx, a.client, a.baseURL, a.token, "/email/send", request)
	if err != nil {
		return EmailSendResult{}, err
	}
	var wrapped struct {
		Result EmailSendResult `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && !emptyEmailResult(wrapped.Result) {
		return wrapped.Result, nil
	}
	var direct EmailSendResult
	if err := json.Unmarshal(raw, &direct); err != nil {
		return EmailSendResult{}, err
	}
	return direct, nil
}

func (a HTTPEmailAdapter) get(ctx context.Context, path string, out any) error {
	return adapterGET(ctx, a.client, a.baseURL, a.token, path, out)
}

func (a HTTPCalendarAdapter) ReadEvents(ctx context.Context, from, to string) ([]CalendarEvent, error) {
	var decoded struct {
		Events []CalendarEvent `json:"events"`
	}
	params := url.Values{}
	if from != "" {
		params.Set("from", from)
	}
	if to != "" {
		params.Set("to", to)
	}
	path := "/calendar/events"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	if err := adapterGET(ctx, a.client, a.baseURL, a.token, path, &decoded); err != nil {
		return nil, err
	}
	return decoded.Events, nil
}

func (a HTTPCalendarAdapter) CreateEvent(ctx context.Context, event CalendarEvent) (CalendarEvent, error) {
	raw, err := adapterPOST(ctx, a.client, a.baseURL, a.token, "/calendar/events", event)
	if err != nil {
		return CalendarEvent{}, err
	}
	var wrapped struct {
		Event CalendarEvent `json:"event"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Event.ID != "" {
		return wrapped.Event, nil
	}
	var direct CalendarEvent
	if err := json.Unmarshal(raw, &direct); err != nil {
		return CalendarEvent{}, err
	}
	return direct, nil
}

func adapterGET(ctx context.Context, client *http.Client, baseURL, token, path string, out any) error {
	if strings.TrimSpace(baseURL) == "" {
		return errors.New("personal data adapter base URL is empty")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("personal data adapter returned HTTP " + intString(resp.StatusCode))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func adapterPOST(ctx context.Context, client *http.Client, baseURL, token, path string, in any) ([]byte, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("personal data adapter base URL is empty")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("personal data adapter returned HTTP " + intString(resp.StatusCode))
	}
	if readErr != nil {
		return nil, readErr
	}
	return body, nil
}

func emptyEmailResult(result EmailSendResult) bool {
	return result.ID == "" && result.Status == "" && result.Adapter == "" && result.CreatedAt == ""
}

func intString(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	n := value
	if n < 0 {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if value < 0 {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
