package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Each record is independently indexed and replaced. PostgreSQL queries only
// the bounded rows needed by a command, never an owner snapshot.
type EmailRecord struct {
	Owner   string          `json:"owner"`
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Parent  string          `json:"parent,omitempty"`
	Related string          `json:"related,omitempty"`
	State   string          `json:"state,omitempty"`
	Search  string          `json:"search,omitempty"`
	Sort    string          `json:"sort,omitempty"`
	Data    json.RawMessage `json:"data"`
}
type emailRowsQuery struct {
	EventSearch                                 bool
	EventEntry                                  string
	NonemptyEvents                              bool
	Direction                                   string
	RequireNativeCapture                        bool
	Validity                                    string
	AsOf                                        time.Time
	Entry                                       string
	NotificationSubtype                         string
	PendingOnly                                 bool
	UnassignedOnly                              bool
	UncapturedOnly                              bool
	InteractionConversation                     bool
	ExcludePageBatchSuperseded                  bool
	MailMessageID                               string
	MailThreadID                                string
	HistoryOnly                                 bool
	ExcludeHistory                              bool
	Asc                                         bool
	ConversationMailbox                         string
	ConversationSearch                          bool
	Kind, Parent, Related, State, Search, After string
	Limit                                       int
	States                                      []string
	Due                                         string
	CapturedOnly                                bool
	IncludeSuperseded                           bool
}
type emailRecords interface {
	get(string, string) (EmailRecord, bool, error)
	list(emailRowsQuery) ([]EmailRecord, error)
	count(emailRowsQuery) (EmailScopeCounts, error)
	exists(emailRowsQuery) (bool, error)
	put(EmailRecord) error
	delete(string, string) error
	changed() bool
}
type emailEngine struct {
	db    emailRecords
	owner string
	now   time.Time
	err   error
}

func emailID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
func emailRecordKey(owner, kind, id string) string { return owner + "\x00" + kind + "\x00" + id }
func emailJSON(v any) []byte                       { b, _ := json.Marshal(v); return b }
func emailGet[T any](e *emailEngine, kind, id string) (out T, ok bool) {
	if e.err != nil {
		return
	}
	var r EmailRecord
	r, ok, e.err = e.db.get(kind, id)
	if !ok || e.err != nil {
		return
	}
	if e.err = validateEmailRecord(r); e.err != nil {
		return out, false
	}
	if err := json.Unmarshal(r.Data, &out); err != nil {
		e.err = errors.Join(errEmailCorrupt, err)
	}
	return
}
func emailPut(e *emailEngine, kind, id, parent, related, state, search, order string, v any) {
	if e.err != nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		e.err = err
		return
	}
	r := EmailRecord{Owner: e.owner, Kind: kind, ID: id, Parent: parent, Related: related, State: state, Search: strings.ToLower(search), Sort: order, Data: b}
	emailUpdateStatus(e, r)
	if e.err == nil {
		e.err = e.db.put(r)
	}
}
func emailDelete(e *emailEngine, kind, id string) {
	if e.err != nil {
		return
	}
	r, ok, err := e.db.get(kind, id)
	if err != nil {
		e.err = err
		return
	}
	if !ok {
		return
	}
	emailDeleteStatus(e, r)
	if e.err == nil {
		e.err = e.db.delete(kind, id)
	}
}
func emailList[T any](e *emailEngine, q emailRowsQuery) []T {
	if e.err != nil {
		return nil
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	var rows []EmailRecord
	rows, e.err = e.db.list(q)
	out := make([]T, 0, len(rows))
	if e.err != nil {
		return out
	}
	for _, r := range rows {
		if e.err = validateEmailRecord(r); e.err != nil {
			return nil
		}
		var v T
		if err := json.Unmarshal(r.Data, &v); err != nil {
			e.err = errors.Join(errEmailCorrupt, err)
			return nil
		}
		out = append(out, v)
	}
	return out
}
func emailOrder(at time.Time, id string) string {
	return at.UTC().Format("2006-01-02T15:04:05.000000Z") + "/" + id
}
func emailLimit(n int) int {
	if n < 1 {
		return 50
	}
	if n > 100 {
		return 100
	}
	return n
}
func emailAddress(address string) (string, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(address))
	if err != nil {
		return "", errors.New("verified mailbox address is required")
	}
	at := strings.LastIndexByte(a.Address, '@')
	if at < 1 {
		return "", errors.New("mailbox address is invalid")
	}
	return a.Address[:at+1] + strings.ToLower(a.Address[at+1:]), nil
}
func emailSafePath(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\x00\\")
}
func emailHashValid(h string) bool {
	b, err := hex.DecodeString(strings.TrimPrefix(h, "sha256:"))
	return err == nil && len(b) == sha256.Size
}
func emailUnique(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range items {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func emailRequire(ok bool, message string) error {
	if !ok {
		return fmt.Errorf("%w: %s", errEmailInvalid, message)
	}
	return nil
}

var errEmailInvalid = errors.New("invalid email command")
var errEmailConflict = errors.New("email state changed")
var errEmailNotFound = errors.New("email record not found")
var ErrEmailBacklogFull = fmt.Errorf("%w: email backlog full", errEmailConflict)

func emailClassify(ctx context.Context, op StoreOperation, err error) error {
	if err == nil {
		return nil
	}
	var se *StoreError
	if errors.As(err, &se) {
		return err
	}
	code := StoreErrorInternal
	switch {
	case errors.Is(err, errEmailCorrupt):
		code = StoreErrorCorrupt
	case errors.Is(err, errEmailInvalid):
		code = StoreErrorInvalid
	case errors.Is(err, errEmailConflict):
		code = StoreErrorConflict
	case errors.Is(err, errEmailNotFound):
		code = StoreErrorNotFound
	case errors.Is(err, context.Canceled):
		code = StoreErrorCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = StoreErrorTimeout
	}
	return storeError(ctx, op, code, err)
}
func emailRun[T any](e *emailEngine, op StoreOperation, key string, input any, fn func(*emailEngine) (T, error)) (out T, err error) {
	if strings.ContainsAny(e.owner, "\x00\r\n") {
		return out, errEmailInvalid
	}
	if key != "" {
		if len(key) > 512 || strings.ContainsAny(key, "\x00\r\n") {
			return out, errEmailInvalid
		}
		receipt, ok := emailGet[EmailCommandReceipt](e, "command", key)
		if e.err != nil {
			return out, e.err
		}
		inputBytes, encodeErr := json.Marshal(input)
		if encodeErr != nil {
			return out, errors.Join(errEmailInvalid, encodeErr)
		}
		hash := emailID(string(inputBytes))
		if ok {
			if receipt.Operation != string(op) || receipt.ContentHash != hash {
				return out, errEmailConflict
			}
			err = json.Unmarshal(receipt.Result, &out)
			return
		}
		out, err = fn(e)
		if e.err != nil {
			return out, e.err
		}
		if err != nil {
			return out, err
		}
		if !e.db.changed() {
			return out, nil
		}
		emailPut(e, "command", key, "", "", "", "", key, EmailCommandReceipt{Key: key, Operation: string(op), ContentHash: hash, Result: emailJSON(out), CommittedAt: e.now})
		return out, e.err
	}
	out, err = fn(e)
	if e.err != nil {
		err = e.err
	}
	return
}
