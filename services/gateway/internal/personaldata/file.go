package personaldata

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type FileAdapter struct {
	Root string
}

func (a FileAdapter) SearchThreads(ctx context.Context, query string, maxResults int) ([]EmailThread, error) {
	threads, err := a.loadEmailThreads()
	if err != nil {
		return nil, err
	}
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 10
	}
	query = strings.ToLower(query)
	out := []EmailThread{}
	for _, thread := range threads {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		haystack := strings.ToLower(thread.Subject + " " + thread.From + " " + strings.Join(thread.Labels, " ") + " " + ThreadBody(thread))
		if query == "" || strings.Contains(haystack, query) {
			out = append(out, thread)
			if len(out) >= maxResults {
				break
			}
		}
	}
	return out, nil
}

func (a FileAdapter) ReadThread(ctx context.Context, threadID string) (EmailThread, error) {
	threads, err := a.loadEmailThreads()
	if err != nil {
		return EmailThread{}, err
	}
	for _, thread := range threads {
		select {
		case <-ctx.Done():
			return EmailThread{}, ctx.Err()
		default:
		}
		if thread.ID == threadID {
			return thread, nil
		}
	}
	return EmailThread{}, fmt.Errorf("email thread %q not found", threadID)
}

func (a FileAdapter) SendEmail(ctx context.Context, request EmailSendRequest) (EmailSendResult, error) {
	request.To = cleanStrings(request.To)
	request.Subject = strings.TrimSpace(request.Subject)
	request.Body = strings.TrimSpace(request.Body)
	if len(request.To) == 0 || request.Subject == "" || request.Body == "" {
		return EmailSendResult{}, fmt.Errorf("email send requires to, subject and body")
	}
	select {
	case <-ctx.Done():
		return EmailSendResult{}, ctx.Err()
	default:
	}
	now := time.Now().UTC()
	result := EmailSendResult{
		ID:        "email_" + now.Format("20060102T150405.000000000Z"),
		Status:    "sent_mock",
		Adapter:   "file",
		CreatedAt: now.Format(time.RFC3339),
	}
	record := struct {
		Result  EmailSendResult  `json:"result"`
		Request EmailSendRequest `json:"request"`
	}{
		Result:  result,
		Request: request,
	}
	if err := a.appendMockJSONL("email_outbox.jsonl", record); err != nil {
		return EmailSendResult{}, err
	}
	return result, nil
}

func (a FileAdapter) ReadEvents(ctx context.Context, from, to string) ([]CalendarEvent, error) {
	events, err := a.loadCalendarEvents()
	if err != nil {
		return nil, err
	}
	out := []CalendarEvent{}
	for _, event := range events {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if EventInRange(event, from, to) {
			out = append(out, event)
		}
	}
	slices.SortFunc(out, func(a, b CalendarEvent) int {
		return strings.Compare(a.Start, b.Start)
	})
	return out, nil
}

func (a FileAdapter) CreateEvent(ctx context.Context, event CalendarEvent) (CalendarEvent, error) {
	event.Title = strings.TrimSpace(event.Title)
	event.Start = strings.TrimSpace(event.Start)
	event.End = strings.TrimSpace(event.End)
	event.Location = strings.TrimSpace(event.Location)
	event.Notes = strings.TrimSpace(event.Notes)
	event.Attendees = cleanStrings(event.Attendees)
	if event.Title == "" || event.Start == "" || event.End == "" {
		return CalendarEvent{}, fmt.Errorf("calendar create requires title, start and end")
	}
	select {
	case <-ctx.Done():
		return CalendarEvent{}, ctx.Err()
	default:
	}
	if event.ID == "" {
		event.ID = "event_" + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	if err := a.appendMockJSONL("calendar_created_events.jsonl", event); err != nil {
		return CalendarEvent{}, err
	}
	return event, nil
}

func (a FileAdapter) loadEmailThreads() ([]EmailThread, error) {
	path := filepath.Join(a.Root, ".sparkclaw", "mock", "email_threads.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("email fixture not found at %s: %w", path, err)
	}
	var threads []EmailThread
	if err := json.Unmarshal(raw, &threads); err != nil {
		return nil, err
	}
	return threads, nil
}

func (a FileAdapter) loadCalendarEvents() ([]CalendarEvent, error) {
	path := filepath.Join(a.Root, ".sparkclaw", "mock", "calendar_events.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("calendar fixture not found at %s: %w", path, err)
	}
	var events []CalendarEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func (a FileAdapter) appendMockJSONL(name string, value any) error {
	path := filepath.Join(a.Root, ".sparkclaw", "mock", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func cleanStrings(values []string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func ThreadBody(thread EmailThread) string {
	parts := []string{}
	for _, message := range thread.Messages {
		parts = append(parts, message.Body)
	}
	return strings.Join(parts, "\n")
}

func EventInRange(event CalendarEvent, from, to string) bool {
	if from != "" && event.End < from {
		return false
	}
	if to != "" && event.Start > to {
		return false
	}
	return true
}
