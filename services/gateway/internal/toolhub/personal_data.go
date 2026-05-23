package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/personaldata"
)

type EmailThread = personaldata.EmailThread
type EmailMessage = personaldata.EmailMessage
type EmailSendRequest = personaldata.EmailSendRequest
type EmailSendResult = personaldata.EmailSendResult
type CalendarEvent = personaldata.CalendarEvent

func (h *ToolHub) emailSearch(ctx context.Context, args map[string]any) (Result, error) {
	query := strings.ToLower(stringArg(args, "query", ""))
	maxResults := intArg(args, "max_results", 10)
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 10
	}
	threads, err := h.email.SearchThreads(ctx, query, maxResults)
	if err != nil {
		return Result{}, err
	}
	type result struct {
		ID      string   `json:"id"`
		Subject string   `json:"subject"`
		From    string   `json:"from"`
		Date    string   `json:"date"`
		Labels  []string `json:"labels"`
		Preview string   `json:"preview"`
	}
	results := []result{}
	for _, thread := range threads {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		default:
		}
		results = append(results, result{
			ID:      thread.ID,
			Subject: thread.Subject,
			From:    thread.From,
			Date:    thread.Date,
			Labels:  thread.Labels,
			Preview: previewText(personaldata.ThreadBody(thread), 180),
		})
		if len(results) >= maxResults {
			break
		}
	}
	return Result{Output: map[string]any{"query": query, "count": len(results), "results": results, "adapter": h.cfg.Adapters.Email.Backend}}, nil
}

func (h *ToolHub) emailReadThread(ctx context.Context, args map[string]any) (Result, error) {
	threadID := stringArg(args, "thread_id", "")
	if threadID == "" {
		return Result{}, errors.New("thread_id cannot be empty")
	}
	thread, err := h.email.ReadThread(ctx, threadID)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{"thread": thread, "adapter": h.cfg.Adapters.Email.Backend, "untrusted_external_content": true}}, nil
}

func (h *ToolHub) emailDraftReply(ctx context.Context, args map[string]any) (Result, error) {
	threadID := stringArg(args, "thread_id", "")
	body := stringArg(args, "body", "")
	if body == "" {
		return Result{}, errors.New("body cannot be empty")
	}
	path := filepath.Join(".sparkclaw", "drafts", "email-"+safeName(threadID)+"-"+time.Now().UTC().Format("20060102T150405Z")+".md")
	content := "# Email Reply Draft\n\n"
	if threadID != "" {
		content += "Thread: " + threadID + "\n\n"
	}
	content += body + "\n"
	result, err := h.filesWriteDraft(ctx, map[string]any{"path": path, "content": content})
	if err != nil {
		return Result{}, err
	}
	output, _ := result.Output.(map[string]any)
	if output == nil {
		output = map[string]any{}
	}
	output["status"] = "email_reply_draft_written"
	output["thread_id"] = threadID
	return Result{Output: output}, nil
}

func (h *ToolHub) emailSend(ctx context.Context, args map[string]any) (Result, error) {
	request := EmailSendRequest{
		ThreadID: stringArg(args, "thread_id", ""),
		To:       stringSliceArg(args, "to"),
		Subject:  stringArg(args, "subject", ""),
		Body:     stringArg(args, "body", ""),
	}
	if len(request.To) == 0 || strings.TrimSpace(request.Subject) == "" || strings.TrimSpace(request.Body) == "" {
		return Result{}, errors.New("to, subject and body are required")
	}
	result, err := h.email.SendEmail(ctx, request)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"status":    result.Status,
		"send":      result,
		"thread_id": request.ThreadID,
		"adapter":   result.Adapter,
	}}, nil
}

func (h *ToolHub) calendarRead(ctx context.Context, args map[string]any) (Result, error) {
	from := stringArg(args, "from", "")
	to := stringArg(args, "to", "")
	matches, err := h.cal.ReadEvents(ctx, from, to)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{"count": len(matches), "events": matches, "adapter": h.cfg.Adapters.Calendar.Backend}}, nil
}

func (h *ToolHub) calendarProposeEvent(ctx context.Context, args map[string]any) (Result, error) {
	title := stringArg(args, "title", "")
	start := stringArg(args, "start", "")
	end := stringArg(args, "end", "")
	if title == "" || start == "" || end == "" {
		return Result{}, errors.New("title, start and end are required")
	}
	proposal := CalendarEvent{
		ID:        "draft_" + safeName(title) + "_" + time.Now().UTC().Format("20060102T150405Z"),
		Title:     title,
		Start:     start,
		End:       end,
		Location:  stringArg(args, "location", ""),
		Attendees: stringSliceArg(args, "attendees"),
		Notes:     stringArg(args, "notes", ""),
	}
	raw, err := json.MarshalIndent(proposal, "", "  ")
	if err != nil {
		return Result{}, err
	}
	path := filepath.Join(".sparkclaw", "drafts", "calendar-"+proposal.ID+".json")
	result, err := h.filesWriteDraft(ctx, map[string]any{"path": path, "content": string(raw) + "\n"})
	if err != nil {
		return Result{}, err
	}
	output, _ := result.Output.(map[string]any)
	if output == nil {
		output = map[string]any{}
	}
	output["status"] = "calendar_event_proposal_written"
	output["proposal"] = proposal
	return Result{Output: output}, nil
}

func (h *ToolHub) calendarCreate(ctx context.Context, args map[string]any) (Result, error) {
	event := CalendarEvent{
		Title:     stringArg(args, "title", ""),
		Start:     stringArg(args, "start", ""),
		End:       stringArg(args, "end", ""),
		Location:  stringArg(args, "location", ""),
		Attendees: stringSliceArg(args, "attendees"),
		Notes:     stringArg(args, "notes", ""),
	}
	if strings.TrimSpace(event.Title) == "" || strings.TrimSpace(event.Start) == "" || strings.TrimSpace(event.End) == "" {
		return Result{}, errors.New("title, start and end are required")
	}
	created, err := h.cal.CreateEvent(ctx, event)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"status":  "calendar_event_created",
		"event":   created,
		"adapter": h.cfg.Adapters.Calendar.Backend,
	}}, nil
}

func previewText(value string, max int) string {
	value = compactWhitespace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func safeName(value string) string {
	value = strings.ToLower(value)
	builder := strings.Builder{}
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' {
			builder.WriteRune(ch)
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('-')
		}
	}
	out := strings.Trim(builder.String(), "-")
	if out == "" {
		return "draft"
	}
	if len(out) > 80 {
		return out[:80]
	}
	return out
}

func stringSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := []string{}
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
