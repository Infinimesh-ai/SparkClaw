package medicine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
)

type SyncPage struct {
	SchemaVersion int     `json:"schema_version"`
	Epoch         string  `json:"epoch"`
	Mode          string  `json:"mode"`
	BaseSequence  int64   `json:"base_sequence"`
	Sequence      int64   `json:"sequence"`
	Events        []Event `json:"events"`
	Cursor        string  `json:"cursor"`
	More          bool    `json:"more"`
}
type cursor struct {
	Owner    string `json:"owner"`
	Epoch    string `json:"epoch"`
	Mode     string `json:"mode"`
	Sequence int64  `json:"sequence"`
	Offset   int    `json:"offset"`
}

func encodeCursor(value cursor) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (s *Service) Sync(ctx context.Context, owner, token string, limit int) (SyncPage, error) {
	if ValidateOwner(owner) != nil || limit < 1 || limit > MaxPage || len(token) > 1024 {
		return SyncPage{}, ErrInvalid
	}
	box, err := s.Read(ctx, owner)
	if err != nil {
		return SyncPage{}, err
	}
	c := cursor{Owner: Digest(owner), Epoch: box.Epoch, Mode: "snapshot", Sequence: box.Sequence}
	if token != "" {
		raw, e := base64.RawURLEncoding.DecodeString(token)
		if e != nil {
			return SyncPage{}, ErrInvalid
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if dec.Decode(&c) != nil || c.Owner != Digest(owner) || c.Sequence < 0 || c.Offset < 0 {
			return SyncPage{}, ErrInvalid
		}
		if c.Epoch != box.Epoch || c.Sequence > box.Sequence {
			return SyncPage{}, ErrReset
		}
	}
	page := SyncPage{SchemaVersion: 1, Epoch: box.Epoch, Mode: c.Mode, BaseSequence: c.Sequence, Sequence: box.Sequence, Events: []Event{}}
	rows := []Event{}
	switch c.Mode {
	case "snapshot":
		if c.Sequence != box.Sequence || c.Offset > len(box.Items) {
			return SyncPage{}, ErrReset
		}
		for _, item := range box.Items[c.Offset:] {
			rows = append(rows, Event{Sequence: box.Sequence, ID: item.ID, Item: item})
		}
	case "delta":
		if c.Offset != 0 {
			return SyncPage{}, ErrInvalid
		}
		if c.Sequence < box.Floor {
			return SyncPage{}, ErrReset
		}
		for _, event := range box.Events {
			if event.Sequence > c.Sequence {
				rows = append(rows, event)
			}
		}
	default:
		return SyncPage{}, ErrInvalid
	}
	// Reserve envelope/cursor space under the 63,488-byte forward response bound.
	size := 2048
	for _, event := range rows {
		raw, _ := json.Marshal(event)
		if len(page.Events) >= limit || size+len(raw)+1 > 60<<10 {
			break
		}
		size += len(raw) + 1
		page.Events = append(page.Events, event)
	}
	page.More = len(page.Events) < len(rows)
	if page.Mode == "snapshot" && page.More {
		c.Offset += len(page.Events)
	} else if page.Mode == "delta" && page.More {
		c.Sequence = page.Events[len(page.Events)-1].Sequence
		page.Sequence = c.Sequence
	} else {
		c.Mode = "delta"
		c.Sequence = box.Sequence
		c.Offset = 0
	}
	page.Cursor = encodeCursor(c)
	return page, nil
}
