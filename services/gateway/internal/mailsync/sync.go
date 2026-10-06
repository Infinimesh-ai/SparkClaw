// Package mailsync maintains a bounded durable journal derived only from the
// authoritative typed mail repository. It does not collect or send mail.
package mailsync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

var ErrReset = errors.New("mail cursor requires a fresh snapshot")
var ErrLimit = errors.New("mail synchronization capacity exceeded")
var ErrInvalid = errors.New("invalid mail synchronization request")
var ErrNotFound = errors.New("mailbox not found")

const MaxPageBytes = 1024 * 1024
const MaxMailBytes = 256 * 1024
const MaxStateBytes = 32 * 1024 * 1024
const MaxRecords = 20000
const MaxEvents = 4096

type Repository struct {
	OwnerStatus func(context.Context, string) (app.EmailOwnerStatus, error)
	Mailbox     func(context.Context, string, string) (app.EmailMailbox, bool, error)
	Mailboxes   func(context.Context, string) ([]app.EmailMailbox, error)
}
type Projection interface {
	ClientSyncMessages(context.Context, store.EmailQuery) (emailmanagement.MessagesView, error)
}
type Service struct {
	mu         sync.Mutex
	repository Repository
	projection Projection
	root       string
}

func New(root string, repository Repository, projection Projection) (*Service, error) {
	if !filepath.IsAbs(root) || repository.OwnerStatus == nil || repository.Mailbox == nil || repository.Mailboxes == nil || projection == nil {
		return nil, ErrInvalid
	}
	// Deliberately lazy: constructing the server never touches configured data.
	return &Service{root: root, repository: repository, projection: projection}, nil
}

type Message struct {
	ID                string                           `json:"id"`
	MailboxID         string                           `json:"mailbox_id"`
	Version           int64                            `json:"version"`
	ConversationID    string                           `json:"conversation_id,omitempty"`
	Subject           string                           `json:"subject"`
	From              string                           `json:"from"`
	To                []string                         `json:"to"`
	CC                []string                         `json:"cc"`
	ReceivingAddress  string                           `json:"receiving_address"`
	Direction         string                           `json:"direction"`
	SentAt            string                           `json:"sent_at"`
	ArrivedAt         string                           `json:"arrived_at"`
	Summary           string                           `json:"summary"`
	BodyText          string                           `json:"body_text"`
	BodyTruncated     bool                             `json:"body_truncated"`
	Viewed            bool                             `json:"viewed"`
	ProcessingState   string                           `json:"processing_state"`
	OriginalAvailable bool                             `json:"original_available"`
	Attachments       []emailmanagement.AttachmentView `json:"attachments"`
}
type Event struct {
	Sequence int64    `json:"sequence"`
	ID       string   `json:"id"`
	Deleted  bool     `json:"deleted"`
	Mail     *Message `json:"mail,omitempty"`
}
type State struct {
	Schema   int                `json:"schema_version"`
	Owner    string             `json:"owner_id"`
	Mailbox  string             `json:"mailbox_id"`
	Epoch    string             `json:"epoch"`
	Sequence int64              `json:"sequence"`
	Floor    int64              `json:"floor"`
	Records  map[string]Message `json:"records"`
	Events   []Event            `json:"events"`
}
type cursor struct {
	Epoch    string `json:"epoch"`
	Sequence int64  `json:"sequence"`
	Mode     string `json:"mode"`
	Offset   int    `json:"offset,omitempty"`
	Scope    string `json:"scope"`
}
type Response struct {
	Schema       int     `json:"schema_version"`
	Mailbox      string  `json:"mailbox_id"`
	Epoch        string  `json:"epoch"`
	Mode         string  `json:"mode"`
	BaseSequence int64   `json:"base_sequence"`
	Sequence     int64   `json:"sequence"`
	Events       []Event `json:"events"`
	Cursor       string  `json:"cursor"`
	More         bool    `json:"more"`
}

func validID(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}
func scope(owner, mailbox string) string {
	s := sha256.Sum256([]byte(owner + "\x00" + mailbox))
	return hex.EncodeToString(s[:])
}
func encode(c cursor) string { b, _ := json.Marshal(c); return base64.RawURLEncoding.EncodeToString(b) }
func decode(raw string) (cursor, error) {
	var c cursor
	if len(raw) > 1024 {
		return c, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return c, ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Sequence < 0 || c.Offset < 0 || (c.Mode != "snapshot" && c.Mode != "delta") {
		return c, ErrInvalid
	}
	return c, nil
}
func project(v emailmanagement.MessageView) Message {
	body := v.BodyText
	truncated := false
	if len(body) > 64*1024 {
		body = ""
		truncated = true
	}
	return Message{ID: v.ID, MailboxID: v.MailboxID, Version: v.Version, ConversationID: v.ConversationID, Subject: v.Subject, From: v.From, To: v.To, CC: v.CC, ReceivingAddress: v.ReceivingAddress, Direction: v.Direction, SentAt: v.SentAt, ArrivedAt: v.ArrivedAt, Summary: v.Summary, BodyText: body, BodyTruncated: truncated, Viewed: v.Viewed, ProcessingState: v.ProcessingState, OriginalAvailable: v.OriginalAvailable, Attachments: v.Attachments}
}
func (s *Service) Mailboxes(ctx context.Context, owner string) ([]emailmanagement.MailboxView, error) {
	if !validID(owner) {
		return nil, ErrInvalid
	}
	boxes, e := s.repository.Mailboxes(ctx, owner)
	if e != nil {
		return nil, e
	}
	if len(boxes) > 100 {
		return nil, ErrLimit
	}
	out := []emailmanagement.MailboxView{}
	for _, b := range boxes {
		out = append(out, emailmanagement.ProjectMailbox(b))
	}
	return out, nil
}
func (s *Service) read(owner, mailbox string) (State, error) {
	out := State{Schema: 1, Owner: owner, Mailbox: mailbox, Records: map[string]Message{}, Events: []Event{}}
	p := filepath.Join(s.root, scope(owner, mailbox)+".json")
	info, e := os.Lstat(p)
	if errors.Is(e, os.ErrNotExist) {
		b := make([]byte, 16)
		if _, e = rand.Read(b); e != nil {
			return out, e
		}
		out.Epoch = hex.EncodeToString(b)
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if !info.Mode().IsRegular() || info.Size() > MaxStateBytes {
		return out, ErrLimit
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(b, &out); e != nil {
		return out, e
	}
	if out.Schema != 1 || out.Owner != owner || out.Mailbox != mailbox || out.Epoch == "" || out.Sequence < out.Floor || out.Floor < 0 || len(out.Records) > MaxRecords || len(out.Events) > MaxEvents {
		return out, ErrInvalid
	}
	return out, nil
}
func (s *Service) persist(state State) error {
	b, e := json.Marshal(state)
	if e != nil {
		return e
	}
	if len(b) > MaxStateBytes {
		return ErrLimit
	}
	if e = os.MkdirAll(s.root, 0700); e != nil {
		return e
	}
	info, e := os.Lstat(s.root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalid
	}
	if e = os.Chmod(s.root, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(s.root, ".pending-mail-")
	if e != nil {
		return e
	}
	p := f.Name()
	defer os.Remove(p)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(p, filepath.Join(s.root, scope(state.Owner, state.Mailbox)+".json")); e != nil {
		return e
	}
	d, e := os.Open(s.root)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *Service) refresh(ctx context.Context, owner, mailbox string) (State, error) {
	state, e := s.read(owner, mailbox)
	if e != nil {
		return state, e
	}
	if _, found, e := s.repository.Mailbox(ctx, owner, mailbox); e != nil {
		return state, e
	} else if !found {
		return state, ErrNotFound
	}
	var records map[string]Message
	for attempt := 0; attempt < 3; attempt++ {
		before, e := s.repository.OwnerStatus(ctx, owner)
		if e != nil {
			return state, e
		}
		records = map[string]Message{}
		next := ""
		bytes := 0
		for {
			if e = ctx.Err(); e != nil {
				return state, e
			}
			page, e := s.projection.ClientSyncMessages(ctx, store.EmailQuery{OwnerID: owner, MailboxID: mailbox, After: next, Limit: 100})
			if e != nil {
				return state, e
			}
			for _, v := range page.Messages {
				m := project(v)
				if !validID(m.ID) || m.MailboxID != mailbox {
					return state, ErrInvalid
				}
				b, e := json.Marshal(m)
				if e != nil {
					return state, e
				}
				bytes += len(b)
				if len(b) > MaxMailBytes || bytes > MaxStateBytes/2 || len(records) >= MaxRecords {
					return state, ErrLimit
				}
				if _, exists := records[m.ID]; exists {
					return state, emailmanagement.ErrProjectionBusy
				}
				records[m.ID] = m
			}
			if page.NextCursor == "" {
				break
			}
			if next == page.NextCursor {
				return state, ErrInvalid
			}
			next = page.NextCursor
		}
		after, e := s.repository.OwnerStatus(ctx, owner)
		if e != nil {
			return state, e
		}
		if before.Revision == after.Revision {
			break
		}
		if attempt == 2 {
			return state, emailmanagement.ErrProjectionBusy
		}
	}
	ids := make([]string, 0, len(records)+len(state.Records))
	seen := map[string]bool{}
	for id := range records {
		ids = append(ids, id)
		seen[id] = true
	}
	for id := range state.Records {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	changed := state.Sequence == 0
	for _, id := range ids {
		m, exists := records[id]
		old, prior := state.Records[id]
		b, _ := json.Marshal(m)
		a, _ := json.Marshal(old)
		if exists && prior && string(a) == string(b) {
			continue
		}
		state.Sequence++
		event := Event{Sequence: state.Sequence, ID: id, Deleted: !exists}
		if exists {
			copy := m
			event.Mail = &copy
		}
		state.Events = append(state.Events, event)
		changed = true
	}
	state.Records = records
	if len(state.Events) > MaxEvents {
		n := len(state.Events) - MaxEvents
		state.Floor = state.Events[n-1].Sequence
		state.Events = state.Events[n:]
	}
	if changed {
		if e = s.persist(state); e != nil {
			return state, e
		}
	}
	return state, nil
}

// Sync returns bounded snapshot pages or ordered deltas. Snapshot pages are
// fenced to one durable revision; any intervening write forces a fresh snapshot.
func (s *Service) Sync(ctx context.Context, owner, mailbox, raw string, limit int) (Response, error) {
	out := Response{Schema: 1, Mailbox: mailbox, Events: []Event{}}
	if !validID(owner) || !validID(mailbox) || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, e := s.refresh(ctx, owner, mailbox)
	if e != nil {
		return out, e
	}
	out.Epoch = state.Epoch
	out.Sequence = state.Sequence
	c := cursor{Epoch: state.Epoch, Sequence: state.Sequence, Mode: "snapshot", Scope: scope(owner, mailbox)}
	if raw != "" {
		c, e = decode(raw)
		if e != nil {
			return out, e
		}
		if c.Scope != scope(owner, mailbox) || c.Epoch != state.Epoch || c.Sequence > state.Sequence || c.Sequence < state.Floor || (c.Mode == "snapshot" && c.Sequence != state.Sequence) {
			return out, ErrReset
		}
	}
	out.Mode = c.Mode
	out.BaseSequence = c.Sequence
	bytes := 0
	if c.Mode == "snapshot" {
		ids := make([]string, 0, len(state.Records))
		for id := range state.Records {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if c.Offset > len(ids) {
			return out, ErrReset
		}
		index := c.Offset
		for index < len(ids) && len(out.Events) < limit {
			m := state.Records[ids[index]]
			event := Event{Sequence: state.Sequence, ID: m.ID, Mail: &m}
			b, _ := json.Marshal(event)
			if bytes+len(b) > MaxPageBytes-8192 {
				break
			}
			out.Events = append(out.Events, event)
			bytes += len(b)
			index++
		}
		out.More = index < len(ids)
		c.Offset = index
		if !out.More {
			c.Mode = "delta"
			c.Offset = 0
		}
	} else {
		latest := c.Sequence
		for _, event := range state.Events {
			if event.Sequence <= c.Sequence {
				continue
			}
			b, _ := json.Marshal(event)
			if len(out.Events) >= limit || bytes+len(b) > MaxPageBytes-8192 {
				out.More = true
				break
			}
			out.Events = append(out.Events, event)
			bytes += len(b)
			latest = event.Sequence
		}
		c.Sequence = latest
	}
	out.Cursor = encode(c)
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if len(encoded) > MaxPageBytes {
		return Response{}, ErrLimit
	}
	return out, nil
}
func (s *Service) String() string { return fmt.Sprintf("bounded mail sync (%d events)", MaxEvents) }
