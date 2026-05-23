package personaldata

import "context"

type EmailThread struct {
	ID       string         `json:"id"`
	Subject  string         `json:"subject"`
	From     string         `json:"from"`
	To       []string       `json:"to"`
	Date     string         `json:"date"`
	Labels   []string       `json:"labels"`
	Messages []EmailMessage `json:"messages"`
}

type EmailMessage struct {
	From string `json:"from"`
	Date string `json:"date"`
	Body string `json:"body"`
}

type EmailSendRequest struct {
	ThreadID string   `json:"thread_id,omitempty"`
	To       []string `json:"to"`
	Subject  string   `json:"subject"`
	Body     string   `json:"body"`
}

type EmailSendResult struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Adapter   string `json:"adapter"`
	CreatedAt string `json:"created_at"`
}

type CalendarEvent struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	Location  string   `json:"location,omitempty"`
	Attendees []string `json:"attendees,omitempty"`
	Notes     string   `json:"notes,omitempty"`
}

type EmailAdapter interface {
	SearchThreads(ctx context.Context, query string, maxResults int) ([]EmailThread, error)
	ReadThread(ctx context.Context, threadID string) (EmailThread, error)
	SendEmail(ctx context.Context, request EmailSendRequest) (EmailSendResult, error)
}

type CalendarAdapter interface {
	ReadEvents(ctx context.Context, from, to string) ([]CalendarEvent, error)
	CreateEvent(ctx context.Context, event CalendarEvent) (CalendarEvent, error)
}
